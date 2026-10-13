package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	resticStderrMax   = 4 << 10          // tail of stderr kept for logs and messages
	resticStdoutMax   = 16 << 20         // stdout of small calls (snapshots, tag, version)
	resticGrace       = 10 * time.Second // SIGINT, then SIGKILL (spec 4 §4.5)
	resticVersionWait = 10 * time.Second // spec 4 §3.2
	resticMessageMax  = 300              // bytes of restic's last line in an answer

	// restic exit codes (restic.readthedocs.io, "Scripting", since 0.17).
	exitNoRepo        = 10
	exitLocked        = 11
	exitWrongPassword = 12
)

// resticRunner starts restic processes: execRunner in production, fakeRestic in unit tests.
type resticRunner interface {
	Start(ctx context.Context, c resticCmd) (resticProc, error)
}

// resticCmd is one restic invocation.
type resticCmd struct {
	Args   []string  // without the binary; argv, never a shell
	Env    []string  // the complete environment (nothing is inherited)
	Stdin  bool      // Stdin() of the process is a pipe
	Stdout io.Writer // nil = discarded
}

// resticProc is a started restic process.
type resticProc interface {
	Stdin() io.WriteCloser // nil unless resticCmd.Stdin
	// Kill ends the process at once (SIGKILL). A stdin backup that is killed before its stdin
	// ends never creates a snapshot.
	Kill()
	// Wait waits for the exit; a non-zero exit is a *resticExit.
	Wait() error
}

// resticExit is a failed restic run. Code is -1 when a signal ended the process.
type resticExit struct {
	Code   int
	Stderr string // the last resticStderrMax bytes
}

func (e *resticExit) Error() string {
	return fmt.Sprintf("restic exited with code %d: %s", e.Code, lastLine(e.Stderr))
}

// execRunner runs the restic binary at bin.
type execRunner struct{ bin string }

func (r execRunner) Start(ctx context.Context, c resticCmd) (resticProc, error) {
	cmd := exec.CommandContext(ctx, r.bin, c.Args...) //nolint:gosec // G204: the binary comes from files.toml; argv is built by the service from validated ids and fixed flags, no shell
	cmd.Env = c.Env
	if cmd.Env == nil {
		cmd.Env = []string{} // never inherit the service's environment
	}
	cmd.Dir = "/"
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = resticGrace
	stderr := &tailBuffer{max: resticStderrMax}
	cmd.Stderr = stderr
	cmd.Stdout = c.Stdout
	p := &execProc{cmd: cmd, stderr: stderr, done: make(chan struct{})}
	if c.Stdin {
		w, err := cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
		p.stdin = w
	}
	if err := startProcess(cmd, p.done); err != nil {
		close(p.done)
		return nil, err
	}
	return p, nil
}

type execProc struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stderr   *tailBuffer
	done     chan struct{} // closed once the process has been reaped (startProcess)
	doneOnce sync.Once
}

func (p *execProc) Stdin() io.WriteCloser { return p.stdin }

func (p *execProc) Kill() { _ = p.cmd.Process.Kill() }

func (p *execProc) Wait() error {
	err := p.cmd.Wait()
	p.doneOnce.Do(func() { close(p.done) })
	var ee *exec.ExitError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &ee):
		return &resticExit{Code: ee.ExitCode(), Stderr: p.stderr.String()}
	case errors.Is(err, exec.ErrWaitDelay):
		return &resticExit{Code: -1, Stderr: p.stderr.String()}
	}
	return err
}

// tailBuffer keeps the last max bytes written to it. Safe for concurrent use.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = append(t.b[:0], t.b[len(t.b)-t.max:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}

// limitedBuffer collects at most max bytes; more is recorded as overflow.
type limitedBuffer struct {
	mu       sync.Mutex
	max      int
	b        []byte
	overflow bool
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.b)+len(p) > l.max {
		l.overflow = true
		p = p[:max(l.max-len(l.b), 0)]
	}
	l.b = append(l.b, p...)
	return len(p), nil
}

func (l *limitedBuffer) Bytes() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b
}

var versionRe = regexp.MustCompile(`^restic (\d+)\.(\d+)\.(\d+)`)

// parseResticVersion reads "restic X.Y.Z …" (the first line of `restic version`).
func parseResticVersion(out string) ([3]int, bool) {
	m := versionRe.FindStringSubmatch(strings.TrimSpace(out))
	if m == nil {
		return [3]int{}, false
	}
	var v [3]int
	for i := range v {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return [3]int{}, false
		}
		v[i] = n
	}
	return v, true
}

// versionAtLeast compares v with a "X.Y.Z" minimum.
func versionAtLeast(v [3]int, minimum string) bool {
	m, ok := parseResticVersion("restic " + minimum)
	if !ok {
		return false
	}
	for i := range v {
		if v[i] != m[i] {
			return v[i] > m[i]
		}
	}
	return true
}

// lastLine returns the last non-empty line of restic's stderr, shortened to resticMessageMax
// bytes; for a JSON error line its "message".
func lastLine(stderr string) string {
	lines := strings.Split(strings.TrimRight(stderr, "\n"), "\n")
	line := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			line = l
			break
		}
	}
	var msg struct {
		Message string `json:"message"`
	}
	if strings.HasPrefix(line, "{") && json.Unmarshal([]byte(line), &msg) == nil && msg.Message != "" {
		line = msg.Message
	}
	if len(line) > resticMessageMax {
		line = line[:resticMessageMax]
	}
	return line
}
