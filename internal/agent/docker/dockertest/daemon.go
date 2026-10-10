// Package dockertest is an in-memory fake of the Docker Engine API subset used by
// internal/agent/docker. It serves HTTP on a Unix socket, so tests exercise the real
// client. Not for production use.
package dockertest

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/docker"
)

// Op names used by Fail and Hold.
const (
	OpPing    = "ping"
	OpImage   = "image" // GET /images/{ref}/json
	OpPull    = "pull"
	OpNetwork = "network" // GET /networks/{name} and POST /networks/create
	OpCreate  = "create"
	OpStart   = "start"
	OpStop    = "stop"
	OpRestart = "restart"
	OpKill    = "kill"
	OpRemove  = "remove"
	OpInspect = "inspect"
	OpList    = "list"
	OpStats   = "stats"
)

// Container is a snapshot of a fake container.
type Container struct {
	ID        string
	Name      string
	Config    docker.ContainerConfig
	Status    string // created, running, exited
	ExitCode  int
	StartedAt time.Time
	Kills     []string // signals received
	StopTimes []int    // t= of every stop/restart
}

type failure struct {
	status int
	msg    string
}

// Daemon is safe for concurrent use.
type Daemon struct {
	// Socket is the path of the Unix socket the fake listens on.
	Socket string

	mu         sync.Mutex
	now        time.Time
	seq        int
	containers map[string]*Container // by name
	images     map[string]bool       // pinned refs "repo@sha256:…"
	networks   map[string]network
	pulls      []string
	calls      []string
	fail       map[string]failure
	pullError  string
	holds      map[string]*hold
	stats      map[string]docker.Stats
}

type network struct {
	driver string
	labels map[string]string
}

type hold struct {
	arrived chan string
	release chan struct{}
}

// New starts a fake daemon on a short socket path; it stops when the test ends.
func New(t testing.TB) *Daemon {
	t.Helper()
	dir, err := os.MkdirTemp("", "dkr")
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{
		Socket:     filepath.Join(dir, "d.sock"),
		now:        time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC),
		containers: map[string]*Container{},
		images:     map[string]bool{},
		networks:   map[string]network{},
		fail:       map[string]failure{},
		holds:      map[string]*hold{},
		stats:      map[string]docker.Stats{},
	}
	l, err := net.Listen("unix", d.Socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: d, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() {
		d.mu.Lock()
		for _, h := range d.holds {
			close(h.release)
		}
		d.holds = map[string]*hold{}
		d.mu.Unlock()
		_ = srv.Close()
		_ = os.RemoveAll(dir)
	})
	return d
}

// AddImage marks a pinned image ("repo@sha256:…") as present.
func (d *Daemon) AddImage(pinned string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.images[pinned] = true
}

// AddContainer seeds a container (e.g. a foreign one without mc-panel labels).
func (d *Daemon) AddContainer(name string, cfg docker.ContainerConfig, running bool) Container {
	d.mu.Lock()
	defer d.mu.Unlock()
	c := d.newContainer(name, cfg)
	if running {
		c.Status = "running"
		c.StartedAt = d.now
	}
	return *c
}

// Container returns a snapshot of the container called name.
func (d *Daemon) Container(name string) (Container, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, ok := d.containers[name]
	if !ok {
		return Container{}, false
	}
	cp := *c
	cp.Kills = slices.Clone(c.Kills)
	cp.StopTimes = slices.Clone(c.StopTimes)
	return cp, true
}

// SetState changes a container's state, e.g. to simulate a crash ("exited", 1).
func (d *Daemon) SetState(name, status string, exitCode int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if c, ok := d.containers[name]; ok {
		c.Status, c.ExitCode = status, exitCode
	}
}

// SetStats sets the answer of the stats endpoint for the container called name.
func (d *Daemon) SetStats(name string, s docker.Stats) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stats[name] = s
}

// HasNetwork reports whether the network exists.
func (d *Daemon) HasNetwork(name string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.networks[name]
	return ok
}

// AddNetwork seeds a network, e.g. a foreign one mc-panel must not adopt.
func (d *Daemon) AddNetwork(name, driver string, labels map[string]string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.networks[name] = network{driver: driver, labels: labels}
}

// SetImage changes the image a container reports on inspect, e.g. to the form
// Docker normalises a reference to ("docker.io/…").
func (d *Daemon) SetImage(name, image string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if c, ok := d.containers[name]; ok {
		c.Config.Image = image
	}
}

// HasImage reports whether the pinned image is present.
func (d *Daemon) HasImage(pinned string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.images[pinned]
}

// Pulls returns the pulled references as "fromImage@tag".
func (d *Daemon) Pulls() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.pulls)
}

// Calls returns every request as "<op> <container or ref>" in arrival order.
func (d *Daemon) Calls() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.calls)
}

// Fail makes every request of op answer status with message until Clear.
func (d *Daemon) Fail(op string, status int, message string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.fail[op] = failure{status: status, msg: message}
}

// FailPull makes pulls answer 200 with an error line in the progress stream.
func (d *Daemon) FailPull(message string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pullError = message
}

// Clear removes all failures set by Fail and FailPull.
func (d *Daemon) Clear() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.fail = map[string]failure{}
	d.pullError = ""
}

// Hold makes requests of op wait until release is called. Every held request
// sends its target (container name or image) on arrived first.
func (d *Daemon) Hold(op string) (arrived <-chan string, release func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	h := &hold{arrived: make(chan string, 16), release: make(chan struct{})}
	d.holds[op] = h
	var once sync.Once
	return h.arrived, func() {
		once.Do(func() {
			d.mu.Lock()
			if d.holds[op] == h {
				delete(d.holds, op)
			}
			d.mu.Unlock()
			close(h.release)
		})
	}
}

func (d *Daemon) newContainer(name string, cfg docker.ContainerConfig) *Container {
	d.seq++
	c := &Container{ID: fmt.Sprintf("%064x", d.seq), Name: name, Config: cfg, Status: "created"}
	d.containers[name] = c
	return c
}

// lookup finds a container by name or full ID (caller holds d.mu).
func (d *Daemon) lookup(ref string) *Container {
	if c, ok := d.containers[ref]; ok {
		return c
	}
	for _, c := range d.containers {
		if c.ID == ref {
			return c
		}
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"message": msg})
}

// route maps a request to (op, target, action).
func route(method, path string) (op, target string) {
	switch {
	case method == http.MethodGet && path == "/_ping":
		return OpPing, ""
	case method == http.MethodPost && path == "/images/create":
		return OpPull, ""
	case method == http.MethodGet && strings.HasPrefix(path, "/images/") && strings.HasSuffix(path, "/json"):
		return OpImage, strings.TrimSuffix(strings.TrimPrefix(path, "/images/"), "/json")
	case path == "/networks/create" || strings.HasPrefix(path, "/networks/"):
		return OpNetwork, strings.TrimPrefix(path, "/networks/")
	case method == http.MethodPost && path == "/containers/create":
		return OpCreate, ""
	case method == http.MethodGet && path == "/containers/json":
		return OpList, ""
	case strings.HasPrefix(path, "/containers/"):
		rest := strings.TrimPrefix(path, "/containers/")
		id, action, _ := strings.Cut(rest, "/")
		switch {
		case method == http.MethodDelete && action == "":
			return OpRemove, id
		case method == http.MethodGet && action == "json":
			return OpInspect, id
		case method == http.MethodGet && action == "stats":
			return OpStats, id
		case method == http.MethodPost && slices.Contains([]string{OpStart, OpStop, OpRestart, OpKill}, action):
			return action, id
		}
	}
	return "", ""
}

func (d *Daemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path, ok := strings.CutPrefix(r.URL.Path, docker.APIVersion)
	if !ok {
		writeError(w, http.StatusBadRequest, "missing API version prefix "+docker.APIVersion)
		return
	}
	op, target := route(r.Method, path)
	if op == "" {
		writeError(w, http.StatusNotFound, "page not found")
		return
	}
	if op == OpCreate {
		target = r.URL.Query().Get("name")
	}
	if op == OpPull {
		target = r.URL.Query().Get("fromImage") + "@" + r.URL.Query().Get("tag")
	}

	d.mu.Lock()
	d.calls = append(d.calls, op+" "+target)
	h := d.holds[op]
	f, failing := d.fail[op]
	d.mu.Unlock()

	if h != nil {
		h.arrived <- target
		select {
		case <-h.release:
		case <-r.Context().Done():
			return
		}
	}
	if failing {
		writeError(w, f.status, f.msg)
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	switch op {
	case OpPing:
		w.Header().Set("Api-Version", "1.52")
		_, _ = io.WriteString(w, "OK")
	case OpImage:
		if !d.images[target] {
			writeError(w, http.StatusNotFound, "No such image: "+target)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"Id": "sha256:" + strings.Repeat("ab", 32)})
	case OpPull:
		d.pull(w, target)
	case OpNetwork:
		d.network(w, r, target)
	case OpCreate:
		d.create(w, r, target)
	case OpList:
		d.list(w, r)
	default:
		d.container(w, r, op, target)
	}
}

func (d *Daemon) pull(w http.ResponseWriter, target string) {
	d.pulls = append(d.pulls, target)
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"status":"Pulling from library/x","id":"1"}`+"\n")
	if d.pullError != "" {
		b, _ := json.Marshal(map[string]any{"errorDetail": map[string]string{"message": d.pullError}, "error": d.pullError})
		_, _ = w.Write(append(b, '\n'))
		return
	}
	d.images[target] = true
	_, _ = io.WriteString(w, `{"status":"Digest: x"}`+"\n"+`{"status":"Status: Downloaded newer image"}`+"\n")
}

func (d *Daemon) network(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method == http.MethodGet {
		n, ok := d.networks[name]
		if !ok {
			writeError(w, http.StatusNotFound, "network "+name+" not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"Name": name, "Driver": n.driver, "Labels": n.labels})
		return
	}
	var req struct {
		Name   string
		Driver string
		Labels map[string]string
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" || req.Driver != "bridge" {
		writeError(w, http.StatusBadRequest, "bad network request")
		return
	}
	if _, ok := d.networks[req.Name]; ok {
		writeError(w, http.StatusConflict, "network with name "+req.Name+" already exists")
		return
	}
	d.networks[req.Name] = network{driver: req.Driver, labels: req.Labels}
	writeJSON(w, http.StatusCreated, map[string]string{"Id": "net-" + req.Name})
}

func (d *Daemon) create(w http.ResponseWriter, r *http.Request, name string) {
	var cfg docker.ContainerConfig
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		writeError(w, http.StatusBadRequest, "bad container config: "+err.Error())
		return
	}
	if name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	if _, ok := d.containers[name]; ok {
		writeError(w, http.StatusConflict, `Conflict. The container name "/`+name+`" is already in use`)
		return
	}
	if !d.images[cfg.Image] {
		writeError(w, http.StatusNotFound, "No such image: "+cfg.Image)
		return
	}
	if mode := cfg.HostConfig.NetworkMode; mode != "" && mode != "default" && mode != "bridge" {
		if _, ok := d.networks[mode]; !ok {
			writeError(w, http.StatusNotFound, "network "+mode+" not found")
			return
		}
	}
	c := d.newContainer(name, cfg)
	writeJSON(w, http.StatusCreated, map[string]any{"Id": c.ID, "Warnings": []string{}})
}

func (d *Daemon) list(w http.ResponseWriter, r *http.Request) {
	all := r.URL.Query().Get("all") == "1"
	var filters map[string][]string
	if f := r.URL.Query().Get("filters"); f != "" {
		if err := json.Unmarshal([]byte(f), &filters); err != nil {
			writeError(w, http.StatusBadRequest, "invalid filters")
			return
		}
	}
	out := []docker.ContainerSummary{}
	names := make([]string, 0, len(d.containers))
	for n := range d.containers {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		c := d.containers[n]
		if !all && c.Status != "running" {
			continue
		}
		if !matchLabels(c.Config.Labels, filters["label"]) {
			continue
		}
		out = append(out, docker.ContainerSummary{
			ID: c.ID, Names: []string{"/" + c.Name}, Image: c.Config.Image, Labels: c.Config.Labels, State: c.Status,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// matchLabels implements Docker's label filter ("key" or "key=value", all must match).
func matchLabels(labels map[string]string, filters []string) bool {
	for _, f := range filters {
		k, v, hasValue := strings.Cut(f, "=")
		got, ok := labels[k]
		if !ok || (hasValue && got != v) {
			return false
		}
	}
	return true
}

func (d *Daemon) container(w http.ResponseWriter, r *http.Request, op, ref string) {
	c := d.lookup(ref)
	if c == nil {
		writeError(w, http.StatusNotFound, "No such container: "+ref)
		return
	}
	q := r.URL.Query()
	switch op {
	case OpStart:
		if c.Status == "running" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		c.Status, c.ExitCode, c.StartedAt = "running", 0, d.now
		w.WriteHeader(http.StatusNoContent)
	case OpStop:
		if c.Status != "running" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		t, _ := strconv.Atoi(q.Get("t"))
		c.StopTimes = append(c.StopTimes, t)
		c.Status, c.ExitCode = "exited", 0
		w.WriteHeader(http.StatusNoContent)
	case OpRestart:
		t, _ := strconv.Atoi(q.Get("t"))
		c.StopTimes = append(c.StopTimes, t)
		c.Status, c.ExitCode, c.StartedAt = "running", 0, d.now
		w.WriteHeader(http.StatusNoContent)
	case OpKill:
		if c.Status != "running" {
			writeError(w, http.StatusConflict, "Container "+c.ID+" is not running")
			return
		}
		sig := q.Get("signal")
		if sig == "" {
			sig = "SIGKILL"
		}
		c.Kills = append(c.Kills, sig)
		if sig == "SIGKILL" || sig == "SIGTERM" {
			c.Status, c.ExitCode = "exited", 137
		}
		w.WriteHeader(http.StatusNoContent)
	case OpRemove:
		if c.Status == "running" && q.Get("force") != "true" {
			writeError(w, http.StatusConflict, "You cannot remove a running container "+c.ID+". Stop the container before attempting removal or force remove")
			return
		}
		delete(d.containers, c.Name)
		w.WriteHeader(http.StatusNoContent)
	case OpInspect:
		started := "0001-01-01T00:00:00Z"
		if !c.StartedAt.IsZero() {
			started = c.StartedAt.Format(time.RFC3339Nano)
		}
		writeJSON(w, http.StatusOK, docker.ContainerInfo{
			ID:   c.ID,
			Name: "/" + c.Name,
			State: docker.ContainerState{
				Status: c.Status, Running: c.Status == "running", Restarting: c.Status == "restarting",
				ExitCode: c.ExitCode, StartedAt: started,
			},
			Config: docker.InfoConfig{Image: c.Config.Image, Labels: c.Config.Labels},
		})
	case OpStats:
		writeJSON(w, http.StatusOK, d.stats[c.Name])
	}
}
