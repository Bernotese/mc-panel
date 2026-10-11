package instances

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/docker"
)

const (
	// DefaultRconCommand runs every console command; itzg/minecraft-server ships it and
	// keeps the RCON password it generates for it in /data (spec §2).
	DefaultRconCommand = "rcon-cli"
	// RconTimeout bounds one console command: inspect, exec, output and exit code.
	RconTimeout = 10 * time.Second
	// MaxParallelRcon bounds the console commands running at once per instance.
	MaxParallelRcon = 4

	maxRconErrorRunes = 500
	// maxRawLogLine bounds the bytes kept of one log line (timestamp + text); the rest
	// of a longer line up to its newline is dropped.
	maxRawLogLine    = agentapi.MaxLogLineBytes + 64
	truncatedMark    = "\n[… gekürzt]"
	exitPollInterval = 20 * time.Millisecond
	// truncatedExitWait bounds the wait for the exit code after the output was cut: the
	// process may block writing the rest we no longer read.
	truncatedExitWait = 2 * time.Second
)

// consoleState counts the running console commands per instance; the zero value is ready.
type consoleState struct {
	mu   sync.Mutex
	rcon map[int64]int
}

func (s *consoleState) acquire(id int64) (release func(), ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rcon == nil {
		s.rcon = map[int64]int{}
	}
	if s.rcon[id] >= MaxParallelRcon {
		return nil, false
	}
	s.rcon[id]++
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.rcon[id]--
		if s.rcon[id] == 0 {
			delete(s.rcon, id)
		}
	}, true
}

// dockerErr marks errors of an unreachable daemon as agentapi.ErrDockerUnavailable.
func dockerErr(err error) error {
	if errors.Is(err, docker.ErrUnavailable) {
		return fmt.Errorf("%w: %w", agentapi.ErrDockerUnavailable, err)
	}
	return err
}

// consoleContainer inspects mcpanel-mc-<id> for a console call. A missing container, one
// mc-panel does not manage or one of another server is agentapi.ErrNotFound: console
// calls never reach a foreign container.
func (m *Manager) consoleContainer(ctx context.Context, id int64) (docker.ContainerInfo, error) {
	name := ContainerName(id)
	info, err := m.dc.Inspect(ctx, name)
	switch {
	case docker.IsNotFound(err):
		return info, fmt.Errorf("%w: container %s does not exist", agentapi.ErrNotFound, name)
	case err != nil:
		return info, dockerErr(err)
	}
	if err := checkManaged(info, id); err != nil {
		return info, fmt.Errorf("%w: %w", agentapi.ErrNotFound, err)
	}
	if err := checkOwner(info, id, m.expectedName(id)); err != nil {
		return info, fmt.Errorf("%w: %w", agentapi.ErrNotFound, err)
	}
	return info, nil
}

// Logs streams the log of mcpanel-mc-<id> (spec §5.2). Failures before Docker opened the
// stream are returned (ErrInvalid, ErrNotFound, ErrDockerUnavailable, …); then start is
// called once with the container state seen by the inspect, and emit for every line until
// Docker ends the stream (container stopped or removed, or a stopped container's history
// is done: nil, also when Docker cuts the stream inside a frame), ctx ends (ctx.Err()) or
// emit fails (its error). It never waits for a stopped container to come back; the panel
// reconnects.
func (m *Manager) Logs(ctx context.Context, req agentapi.LogsRequest, start func(running bool), emit func(agentapi.LogLine) error) error {
	if err := req.Validate(); err != nil {
		return err
	}
	if !m.dockerAvailable(ctx) {
		return agentapi.ErrDockerUnavailable
	}
	info, err := m.consoleContainer(ctx, req.ID)
	if err != nil {
		return err
	}
	opts := docker.LogsOptions{Follow: true, Tail: req.Tail, Since: req.Since}
	if req.Since > 0 {
		opts.Tail = -1 // since wins over tail
	}
	name := ContainerName(req.ID)
	body, err := m.dc.ContainerLogs(ctx, name, opts)
	switch {
	case docker.IsNotFound(err):
		return fmt.Errorf("%w: container %s does not exist", agentapi.ErrNotFound, name)
	case err != nil:
		return dockerErr(err)
	}
	defer func() { _ = body.Close() }()
	start(info.State.Running && !info.State.Restarting)
	err = copyLogLines(body, emit)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

// copyLogLines demultiplexes a log stream with timestamps and emits one LogLine per line.
// Frames need not align with lines: stdout and stderr are reassembled separately.
func copyLogLines(r io.Reader, emit func(agentapi.LogLine) error) error {
	dm := docker.NewDemuxer(r)
	var out, errs lineBuffer
	var lastTS int64
	flush := func(b *lineBuffer, stream string, final bool) error {
		for {
			raw, ok := b.next(final)
			if !ok {
				return nil
			}
			if err := emit(parseLogLine(raw, &lastTS, stream)); err != nil {
				return err
			}
		}
	}
	for {
		f, err := dm.Next()
		if errors.Is(err, io.EOF) {
			if err := flush(&out, agentapi.StreamOut, true); err != nil {
				return err
			}
			return flush(&errs, agentapi.StreamErr, true)
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			// Docker cut the stream inside a frame (container removed). End like a clean end,
			// but without the unterminated rest: the panel resumes with since and gets it whole.
			return nil
		}
		if err != nil {
			return err
		}
		switch f.Stream {
		case docker.Stdout:
			out.write(f.Data)
			err = flush(&out, agentapi.StreamOut, false)
		case docker.Stderr:
			errs.write(f.Data)
			err = flush(&errs, agentapi.StreamErr, false)
		}
		if err != nil {
			return err
		}
	}
}

// lineBuffer reassembles the lines of one stream. A line longer than maxRawLogLine is
// cut there and the rest up to its newline is dropped.
type lineBuffer struct {
	buf      []byte
	dropping bool // inside the dropped rest of an overlong line
}

func (b *lineBuffer) write(p []byte) {
	if b.dropping {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			return
		}
		b.dropping = false
		p = p[i+1:]
	}
	b.buf = append(b.buf, p...)
}

// next returns the next line without its "\n"; an unterminated line only once it is too
// long or when final is set (end of stream).
func (b *lineBuffer) next(final bool) ([]byte, bool) {
	i := bytes.IndexByte(b.buf, '\n')
	switch {
	case i >= 0 && i <= maxRawLogLine:
		line := b.buf[:i]
		b.buf = b.buf[i+1:]
		return line, true
	case i > maxRawLogLine:
		line := b.buf[:maxRawLogLine]
		b.buf = b.buf[i+1:]
		return line, true
	case len(b.buf) > maxRawLogLine:
		line := b.buf[:maxRawLogLine]
		b.buf, b.dropping = nil, true
		return line, true
	case final && len(b.buf) > 0:
		line := b.buf
		b.buf = nil
		return line, true
	}
	return nil, false
}

// parseLogLine splits "<RFC 3339 timestamp> <text>". A line without a valid timestamp
// keeps the previous one, so the panel's resume position never goes backwards. The text
// loses a trailing "\r", invalid UTF-8 becomes U+FFFD and it is cut to MaxLogLineBytes
// at a rune boundary.
func parseLogLine(raw []byte, lastTS *int64, stream string) agentapi.LogLine {
	text := raw
	if ts, rest, ok := bytes.Cut(raw, []byte{' '}); ok {
		if t, err := time.Parse(time.RFC3339Nano, string(ts)); err == nil {
			*lastTS = t.UnixNano()
			text = rest
		}
	}
	s := strings.ToValidUTF8(string(bytes.TrimRight(text, "\r")), "�")
	return agentapi.LogLine{TS: *lastTS, Stream: stream, Line: truncateUTF8(s, agentapi.MaxLogLineBytes)}
}

// truncateUTF8 cuts s to at most n bytes without splitting a rune.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func firstRunes(s string, n int) string {
	i := 0
	for j := range s {
		if i == n {
			return s[:j]
		}
		i++
	}
	return s
}

// Rcon runs one console command in mcpanel-mc-<id> as `<rcon-cli> <command>` with the
// container user's uid:gid in /data (spec §5.3) and returns its output.
func (m *Manager) Rcon(ctx context.Context, id int64, command string) (string, error) {
	if err := (agentapi.RconRequest{ID: id, Command: command}).Validate(); err != nil {
		return "", err
	}
	if !m.dockerAvailable(ctx) {
		return "", agentapi.ErrDockerUnavailable
	}
	release, ok := m.console.acquire(id)
	if !ok {
		return "", fmt.Errorf("%w: %d console commands of instance %d are running", agentapi.ErrBusy, MaxParallelRcon, id)
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, m.rconTimeout)
	defer cancel()
	// fail turns errors into rcon_unavailable once our own deadline has passed.
	fail := func(err error) error {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("%w: %s did not answer within %s", agentapi.ErrRconUnavailable, m.rconCmd, m.rconTimeout)
		}
		return dockerErr(err)
	}

	name := ContainerName(id)
	info, err := m.consoleContainer(ctx, id)
	if errors.Is(err, agentapi.ErrNotFound) {
		return "", err
	}
	if err != nil {
		return "", fail(err)
	}
	if !info.State.Running || info.State.Restarting {
		return "", fmt.Errorf("%w: container %s is %s", agentapi.ErrNotRunning, name, info.State.Status)
	}
	execID, err := m.dc.ExecCreate(ctx, name, docker.ExecConfig{
		Cmd:          []string{m.rconCmd, command},
		User:         strconv.Itoa(m.host.UID) + ":" + strconv.Itoa(m.host.GID),
		WorkingDir:   "/data",
		Env:          []string{"HOME=/data"},
		AttachStdout: true,
		AttachStderr: true,
	})
	switch {
	case docker.IsNotFound(err):
		return "", fmt.Errorf("%w: container %s does not exist", agentapi.ErrNotFound, name)
	case docker.IsConflict(err):
		return "", fmt.Errorf("%w: %w", agentapi.ErrNotRunning, err)
	case err != nil:
		return "", fail(err)
	}
	body, err := m.dc.ExecStart(ctx, execID)
	if err != nil {
		return "", fail(err)
	}
	out, truncated, err := readExecOutput(body, agentapi.MaxRconOutputBytes)
	_ = body.Close()
	if err != nil {
		return "", fail(err)
	}
	var code int
	if truncated {
		// We stopped reading; the process may be blocked writing the rest, so wait only
		// briefly for its exit code. Without one the cut output is the answer.
		wctx, wcancel := context.WithTimeout(ctx, truncatedExitWait)
		code, err = m.execExitCode(wctx, execID)
		wcancel()
		if err != nil {
			return out, nil
		}
	} else if code, err = m.execExitCode(ctx, execID); err != nil {
		return "", fail(err)
	}
	if code != 0 {
		msg := out
		if strings.TrimSpace(msg) == "" {
			msg = fmt.Sprintf("%s exited with code %d", m.rconCmd, code)
		}
		return "", fmt.Errorf("%w: %s", agentapi.ErrRconUnavailable, firstRunes(msg, maxRconErrorRunes))
	}
	return out, nil
}

// readExecOutput collects stdout and stderr in arrival order, at most limit bytes (then
// truncated is set and the rest is not read). The result is valid UTF-8 without trailing
// line breaks; a truncated output ends with truncatedMark.
func readExecOutput(r io.Reader, limit int) (out string, truncated bool, err error) {
	dm := docker.NewDemuxer(r)
	var buf []byte
	for {
		f, err := dm.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", false, err
		}
		if f.Stream == docker.Stdin {
			continue
		}
		buf = append(buf, f.Data...)
		if len(buf) > limit {
			truncated = true
			break
		}
	}
	s := truncateUTF8(strings.ToValidUTF8(string(buf), "�"), limit)
	s = strings.TrimRight(s, "\r\n")
	if truncated {
		s += truncatedMark
	}
	return s, truncated, nil
}

// execExitCode waits until Docker reports the exec as finished (the output stream may
// end slightly before the exit code is recorded) and returns the exit code.
func (m *Manager) execExitCode(ctx context.Context, execID string) (int, error) {
	for {
		info, err := m.dc.ExecInspect(ctx, execID)
		if err != nil {
			return 0, err
		}
		if !info.Running && info.ExitCode != nil {
			return *info.ExitCode, nil
		}
		t := time.NewTimer(exitPollInterval)
		select {
		case <-ctx.Done():
			t.Stop()
			return 0, ctx.Err()
		case <-t.C:
		}
	}
}
