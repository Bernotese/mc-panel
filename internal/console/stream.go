package console

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
)

// Stream parameters (spec §6.2).
const (
	defaultTail       = 500
	heartbeatInterval = 15 * time.Second
	recheckInterval   = 30 * time.Second
	reconnectDelay    = 3 * time.Second  // first delay before the agent stream is re-opened
	maxReconnectDelay = 10 * time.Second // the delay doubles up to this while nothing changes
	writeTimeout      = 30 * time.Second
	maxStreamsPerUser = 4
	lineBuffer        = 64
)

var (
	errTooManyStreams = errors.New("console: too many open streams")
	errShuttingDown   = errors.New("console: shutting down")
	errStop           = errors.New("console: stop stream")
)

// lineEvent and stateEvent are the data of the SSE events "line" and "state" (V3).
type lineEvent struct {
	TS     int64  `json:"ts"` // Unix ms
	Stream string `json:"stream"`
	Line   string `json:"line"`
}

type stateEvent struct {
	Running bool `json:"running"`
}

// streamSet counts open streams per user and cancels all of them on Shutdown.
type streamSet struct {
	mu      sync.Mutex
	closed  bool
	next    int
	perUser map[int64]int
	cancels map[int]context.CancelFunc
}

func newStreamSet() *streamSet {
	return &streamSet{perUser: map[int64]int{}, cancels: map[int]context.CancelFunc{}}
}

// acquire registers a stream of userID; release must be called when it ends.
func (s *streamSet) acquire(userID int64, cancel context.CancelFunc) (release func(), err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errShuttingDown
	}
	if s.perUser[userID] >= maxStreamsPerUser {
		return nil, errTooManyStreams
	}
	s.perUser[userID]++
	s.next++
	id := s.next
	s.cancels[id] = cancel
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.cancels, id)
		s.perUser[userID]--
		if s.perUser[userID] <= 0 {
			delete(s.perUser, userID)
		}
	}, nil
}

func (s *streamSet) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for _, cancel := range s.cancels {
		cancel()
	}
}

// handleStream serves the live log as Server-Sent Events (spec §6.2).
func (h *Handler) handleStream(w http.ResponseWriter, r *http.Request) {
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	req, problem := logsRequest(s.ID, r)
	if problem != "" {
		writeValidation(w, r, problem)
		return
	}
	p, _ := principal.From(r.Context()) // guaranteed by the rbac rule
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	release, err := h.streams.acquire(p.UserID, cancel)
	switch {
	case errors.Is(err, errTooManyStreams):
		httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "too many open consoles, close one first")
		return
	case err != nil:
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeAgentUnavailable, "the panel is shutting down")
		return
	}
	defer release()

	rc := http.NewResponseController(w)
	// The server's ReadTimeout would cancel the request context of an idle connection after
	// 30 s; a stream is bounded by the recheck instead.
	if err := rc.SetReadDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		h.log.DebugContext(ctx, "clear read deadline", "err", err)
	}
	hdr := w.Header()
	hdr.Set("Content-Type", "text/event-stream")
	hdr.Set("Cache-Control", "no-store")
	hdr.Set("X-Accel-Buffering", "no")
	sw := &sseWriter{w: w, rc: rc, clock: h.ioClock}
	if err := sw.deadline(); err != nil {
		return
	}
	w.WriteHeader(http.StatusOK)
	if err := sw.flush(); err != nil {
		return
	}
	ls := &logSession{h: h, ctx: ctx, sw: sw, p: p, serverID: s.ID, req: req}
	ls.run()
}

// logsRequest reads ?tail and Last-Event-ID; problem is non-empty for an invalid tail.
func logsRequest(id int64, r *http.Request) (agentapi.LogsRequest, string) {
	req := agentapi.LogsRequest{ID: id, Tail: defaultTail}
	if v := r.URL.Query().Get("tail"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > agentapi.MaxLogTail {
			return req, "tail must be an integer from 0 to " + strconv.Itoa(agentapi.MaxLogTail)
		}
		req.Tail = n
	}
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		if ts, err := strconv.ParseInt(v, 10, 64); err == nil && ts > 0 && ts < math.MaxInt64 {
			req.Since = ts + 1
		}
	}
	return req, ""
}

// sseWriter writes events with a fresh write deadline each time. Deadlines are wall-clock time
// (clock.Real), whatever clock the handler uses for its logic.
type sseWriter struct {
	w     io.Writer
	rc    *http.ResponseController
	clock clock.Clock
}

func (s *sseWriter) deadline() error {
	err := s.rc.SetWriteDeadline(s.clock.Now().Add(writeTimeout))
	if errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	return err
}

func (s *sseWriter) flush() error {
	if err := s.deadline(); err != nil {
		return err
	}
	err := s.rc.Flush()
	if errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	return err
}

// event writes one event (without flushing); id may be empty. JSON never contains a newline,
// so data fits on one line.
func (s *sseWriter) event(name, id string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	buf.WriteString("event: " + name + "\n")
	if id != "" {
		buf.WriteString("id: " + id + "\n")
	}
	buf.WriteString("data: ")
	buf.Write(b)
	buf.WriteString("\n\n")
	if err := s.deadline(); err != nil {
		return err
	}
	_, err = s.w.Write(buf.Bytes())
	return err
}

// ping writes and flushes the heartbeat comment.
func (s *sseWriter) ping() error {
	if err := s.deadline(); err != nil {
		return err
	}
	if _, err := io.WriteString(s.w, ": ping\n\n"); err != nil {
		return err
	}
	return s.flush()
}

// logSession is one open stream: it (re)opens the agent stream and forwards its lines.
type logSession struct {
	h        *Handler
	ctx      context.Context
	sw       *sseWriter
	p        principal.Principal
	serverID int64
	req      agentapi.LogsRequest // next request to the agent; Since = newest timestamp sent + 1
	// skipBefore is the Since of the open agent stream: older lines repeat lines sent before
	// the reconnect. Lines of one agent stream are never compared with each other, so several
	// lines with the same timestamp all get through.
	skipBefore int64
	running    *bool // last state sent; nil = none yet
	// progress: a line or a state event was sent since the agent stream was last (re)opened.
	progress bool
}

type lineOrErr struct {
	line agentapi.LogLine
	err  error
}

func (s *logSession) run() {
	heartbeat, stopHeartbeat := s.h.timers.Every(heartbeatInterval)
	defer stopHeartbeat()
	recheck, stopRecheck := s.h.timers.Every(recheckInterval)
	defer stopRecheck()
	delay := reconnectDelay
	for {
		s.skipBefore = s.req.Since
		s.progress = false
		stream, err := s.h.agent.Logs(s.ctx, s.req)
		switch {
		case err == nil:
			// Docker also streams the history of a stopped container (and ends at once), so the
			// state comes from the agent (X-Container-Running), not from "the stream opened".
			if s.setRunning(stream.Running()) != nil {
				_ = stream.Close()
				return
			}
			err = s.follow(stream, heartbeat, recheck)
			_ = stream.Close()
			if errors.Is(err, errStop) {
				return
			}
		case s.ctx.Err() != nil:
			return
		case errors.Is(err, agentapi.ErrBusy):
			// Operators should see that the agent's log streams are used up.
			s.h.log.InfoContext(s.ctx, "console log stream refused: agent log stream limit reached", "server", s.serverID, "err", err)
		default:
			s.h.log.DebugContext(s.ctx, "console log stream unavailable", "server", s.serverID, "err", err)
		}
		if s.setRunning(false) != nil {
			return
		}
		// A stopped server's log is re-read from Docker on every reconnect: back off while
		// nothing new arrives (spec §6.2), start over after a line or a state change.
		if s.progress {
			delay = reconnectDelay
		} else {
			delay = min(2*delay, maxReconnectDelay)
		}
		if !s.wait(delay, heartbeat, recheck) {
			return
		}
	}
}

// follow forwards the lines of one agent stream. It returns nil when the agent ended the stream
// (reconnect) and errStop when the whole SSE stream must end.
func (s *logSession) follow(stream agentapi.LogStream, heartbeat, recheck <-chan time.Time) error {
	lines := make(chan lineOrErr, lineBuffer)
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			l, err := stream.Next()
			select {
			case lines <- lineOrErr{line: l, err: err}:
			case <-done:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		select {
		case <-s.ctx.Done():
			return errStop
		case <-heartbeat:
			if s.sw.ping() != nil {
				return errStop
			}
		case <-recheck:
			if !s.allowed() {
				return errStop
			}
		case le := <-lines:
			if le.err != nil {
				if !errors.Is(le.err, io.EOF) {
					s.h.log.DebugContext(s.ctx, "console log stream broke", "server", s.serverID, "err", le.err)
				}
				return nil
			}
			if s.sendLine(le.line) != nil {
				return errStop
			}
			if len(lines) == 0 && s.sw.flush() != nil {
				return errStop
			}
		}
	}
}

// sendLine forwards one line unless it repeats one sent before the reconnect (spec §6.2:
// since = ts + 1).
func (s *logSession) sendLine(l agentapi.LogLine) error {
	if l.TS < s.skipBefore {
		return nil
	}
	// The event id is the newest timestamp sent so far (not the line's own, which can be older
	// than one already sent), so Last-Event-ID + 1 never repeats lines on a browser reconnect.
	id := ""
	if l.TS > 0 {
		s.req.Since = max(s.req.Since, l.TS+1)
		id = strconv.FormatInt(s.req.Since-1, 10)
	}
	s.progress = true
	return s.sw.event("line", id, lineEvent{TS: l.TS / int64(time.Millisecond), Stream: l.Stream, Line: l.Line})
}

// setRunning sends a state event if v differs from the last state sent (no flapping while a
// stopped server is retried).
func (s *logSession) setRunning(v bool) error {
	if s.running != nil && *s.running == v {
		return nil
	}
	s.running = &v
	s.progress = true
	if err := s.sw.event("state", "", stateEvent{Running: v}); err != nil {
		return err
	}
	return s.sw.flush()
}

// wait sleeps delay until the next reconnect attempt, still serving heartbeat and recheck.
func (s *logSession) wait(delay time.Duration, heartbeat, recheck <-chan time.Time) bool {
	retry, stop := s.h.timers.After(delay)
	defer stop()
	for {
		select {
		case <-s.ctx.Done():
			return false
		case <-retry:
			return true
		case <-heartbeat:
			if s.sw.ping() != nil {
				return false
			}
		case <-recheck:
			if !s.allowed() {
				return false
			}
		}
	}
}

// allowed re-checks session, permission and the server itself (spec §6.2).
func (s *logSession) allowed() bool {
	if err := s.h.access.Recheck(s.ctx, s.p, s.serverID, rbac.PermServerConsole); err != nil {
		s.h.log.InfoContext(s.ctx, "console stream closed", "server", s.serverID, "user", s.p.UserID, "reason", err)
		return false
	}
	if _, err := s.h.q.ServersGetInstance(s.ctx, s.serverID); err != nil {
		s.h.log.InfoContext(s.ctx, "console stream closed", "server", s.serverID, "reason", "server gone", "err", err)
		return false
	}
	return true
}
