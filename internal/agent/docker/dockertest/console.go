package dockertest

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/docker"
)

// logTimeFormat is Docker's fixed-width timestamp of log lines (timestamps=1).
const logTimeFormat = "2006-01-02T15:04:05.000000000Z07:00"

type logEntry struct {
	at       time.Time
	stream   docker.StreamType
	payload  []byte
	unframed bool // written as is, without a frame header
}

// ExecResult is what a fake exec process prints and returns.
type ExecResult struct {
	Stdout, Stderr string
	ExitCode       int
}

// ExecRecord is one exec create request.
type ExecRecord struct {
	Container string
	Config    docker.ExecConfig
}

type execState struct {
	container string
	cfg       docker.ExecConfig
	started   bool
	exitCode  *int
}

// AppendLog adds one line to the log of the container called name the way Docker's
// json-file driver returns it with timestamps=1: "<timestamp> <line>\n" in one frame.
// Every entry is 1 ms after the previous one; its timestamp is returned.
func (d *Daemon) AppendLog(name string, stream docker.StreamType, line string) time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()
	at := d.nextLogTime()
	d.logs[name] = append(d.logs[name], logEntry{at: at, stream: stream, payload: []byte(at.UTC().Format(logTimeFormat) + " " + line + "\n")})
	d.wakeLogs()
	return at
}

// AppendRawLog adds one frame with exactly payload (no timestamp or newline added), e.g.
// half a line. For since it counts as written 1 ms after the previous entry.
func (d *Daemon) AppendRawLog(name string, stream docker.StreamType, payload []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.logs[name] = append(d.logs[name], logEntry{at: d.nextLogTime(), stream: stream, payload: slices.Clone(payload)})
	d.wakeLogs()
}

// AppendUnframed adds data that is written as is, without a frame header, e.g. a frame
// header promising more bytes than follow (a stream cut inside a frame).
func (d *Daemon) AppendUnframed(name string, data []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.logs[name] = append(d.logs[name], logEntry{at: d.nextLogTime(), payload: slices.Clone(data), unframed: true})
	d.wakeLogs()
}

// LogQueries returns the raw query strings of all logs requests in arrival order.
func (d *Daemon) LogQueries() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.logQueries)
}

// SetExecHandler sets what exec processes do. The default prints the arguments after the
// program name plus "\n" (like echo) and exits 0. f runs with the daemon locked and must
// not call Daemon methods.
func (d *Daemon) SetExecHandler(f func(container string, cfg docker.ExecConfig) ExecResult) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.execHandler = f
}

// Execs returns all exec create requests in arrival order.
func (d *Daemon) Execs() []ExecRecord {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.execRecords)
}

func (d *Daemon) nextLogTime() time.Time {
	d.logSeq++
	return d.now.Add(time.Duration(d.logSeq) * time.Millisecond)
}

// wakeLogs wakes all followers (caller holds d.mu).
func (d *Daemon) wakeLogs() {
	close(d.logWake)
	d.logWake = make(chan struct{})
}

func parseSince(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	sec, frac, _ := strings.Cut(s, ".")
	secs, err := strconv.ParseInt(sec, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	var ns int64
	if frac != "" {
		if len(frac) != 9 {
			return time.Time{}, errors.New("want 9 fraction digits")
		}
		if ns, err = strconv.ParseInt(frac, 10, 64); err != nil {
			return time.Time{}, err
		}
	}
	return time.Unix(secs, ns), nil
}

// serveLogs streams the log like Docker: since (inclusive) and tail select the history,
// follow keeps the stream open while the container runs.
func (d *Daemon) serveLogs(w http.ResponseWriter, r *http.Request, ref string) {
	q := r.URL.Query()
	since, err := parseSince(q.Get("since"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid since: "+err.Error())
		return
	}
	tail := -1
	if t := q.Get("tail"); t != "" && t != "all" {
		n, err := strconv.Atoi(t)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid tail "+t)
			return
		}
		tail = n
	}
	follow := q.Get("follow") == "1" || q.Get("follow") == "true"

	d.mu.Lock()
	c := d.lookup(ref)
	if c == nil {
		d.mu.Unlock()
		writeError(w, http.StatusNotFound, "No such container: "+ref)
		return
	}
	name := c.Name
	d.logQueries = append(d.logQueries, r.URL.RawQuery)
	// snapshot (caller holds d.mu) reads entries, state and the wake channel together, so
	// no change between two snapshots is missed.
	snapshot := func() ([]logEntry, bool, chan struct{}) {
		cur := d.containers[name]
		return slices.Clone(d.logs[name]), cur != nil && cur.Status == "running", d.logWake
	}
	// The history (and tail) is fixed when the request arrives, like Docker: a line
	// appended after the headers went out is a followed line, never cut by tail.
	entries, running, wake := snapshot()
	d.mu.Unlock()

	w.Header().Set("Content-Type", "application/vnd.docker.multiplexed-stream")
	w.WriteHeader(http.StatusOK)
	flush := func() {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	flush()
	next, first := 0, true
	for {
		var out []logEntry
		for _, e := range entries[next:] {
			if since.IsZero() || !e.at.Before(since) {
				out = append(out, e)
			}
		}
		next = len(entries)
		if first && tail >= 0 && len(out) > tail {
			out = out[len(out)-tail:]
		}
		first = false
		for _, e := range out {
			var err error
			if e.unframed {
				_, err = w.Write(e.payload)
			} else {
				err = docker.WriteFrame(w, e.stream, e.payload)
			}
			if err != nil {
				return
			}
		}
		flush()
		if !follow || !running {
			return
		}
		select {
		case <-wake:
		case <-r.Context().Done():
			return
		}
		d.mu.Lock()
		entries, running, wake = snapshot()
		d.mu.Unlock()
	}
}

func echoExec(_ string, cfg docker.ExecConfig) ExecResult {
	return ExecResult{Stdout: strings.Join(cfg.Cmd[1:], " ") + "\n"}
}

// execCreate handles POST /containers/{id}/exec (caller holds d.mu).
func (d *Daemon) execCreate(w http.ResponseWriter, r *http.Request, c *Container) {
	var cfg docker.ExecConfig
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil || len(cfg.Cmd) == 0 {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("bad exec config: %v", err))
		return
	}
	if c.Status != "running" {
		writeError(w, http.StatusConflict, "container "+c.ID+" is not running")
		return
	}
	d.seq++
	id := fmt.Sprintf("exec-%d", d.seq)
	d.execs[id] = &execState{container: c.Name, cfg: cfg}
	d.execRecords = append(d.execRecords, ExecRecord{Container: c.Name, Config: cfg})
	writeJSON(w, http.StatusCreated, map[string]string{"Id": id})
}

// execStart handles POST /exec/{id}/start (caller holds d.mu): only attached, without TTY.
func (d *Daemon) execStart(w http.ResponseWriter, r *http.Request, id string) {
	e := d.execs[id]
	if e == nil {
		writeError(w, http.StatusNotFound, "No such exec instance: "+id)
		return
	}
	var req struct {
		Detach bool
		Tty    bool
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || req.Detach || req.Tty {
		writeError(w, http.StatusBadRequest, "fake supports only attached exec without TTY")
		return
	}
	if e.started {
		writeError(w, http.StatusConflict, "exec "+id+" already started")
		return
	}
	e.started = true
	h := d.execHandler
	if h == nil {
		h = echoExec
	}
	res := h(e.container, e.cfg)
	w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
	w.WriteHeader(http.StatusOK)
	if res.Stdout != "" {
		_ = docker.WriteFrame(w, docker.Stdout, []byte(res.Stdout))
	}
	if res.Stderr != "" {
		_ = docker.WriteFrame(w, docker.Stderr, []byte(res.Stderr))
	}
	code := res.ExitCode
	e.exitCode = &code
}

// execInspect handles GET /exec/{id}/json (caller holds d.mu).
func (d *Daemon) execInspect(w http.ResponseWriter, id string) {
	e := d.execs[id]
	if e == nil {
		writeError(w, http.StatusNotFound, "No such exec instance: "+id)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ID": id, "Running": false, "ExitCode": e.exitCode})
}
