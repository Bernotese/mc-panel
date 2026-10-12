package files

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRestic is an in-memory restic for unit tests: one repository per RESTIC_REPOSITORY. It
// understands exactly the commands the service sends (spec 4 §3.3) and records every call.
type fakeRestic struct {
	mu      sync.Mutex
	version string                 // first line of "restic version"
	repos   map[string]*fakeRepo   // by RESTIC_REPOSITORY
	calls   []fakeCall             // every Start
	fail    map[string]*resticExit // command → exit instead of running it
	next    time.Time              // time of the next snapshot (+1 min per backup)
	// stdinRead, if set, receives the number of bytes a backup read before it ended (EOF or kill).
	stdinRead chan int
	// sawEOF records per backup whether stdin ended (true) or the process was killed first.
	sawEOF []bool
	// gate, if set, is waited on by every backup before it reads stdin and by every dump
	// before it writes (tests: a job that "runs").
	gate chan struct{}
}

type fakeCall struct{ Args, Env []string }

type fakeRepo struct{ snaps []*fakeSnap }

type fakeSnap struct {
	ID   string
	Time time.Time
	Tags []string
	Data []byte
}

func newFakeRestic() *fakeRestic {
	return &fakeRestic{
		version: "restic 0.19.1 compiled with go1.25.1 on linux/amd64",
		repos:   map[string]*fakeRepo{},
		fail:    map[string]*resticExit{},
		next:    time.Date(2026, 10, 12, 4, 0, 0, 0, time.UTC),
	}
}

// newBackupService is newService with the fake restic, the backups directory of production
// (0700, normally created by the agent) and InitBackups already run.
func newBackupService(t *testing.T, mod ...func(*Config)) (*Service, string, *fakeRestic) {
	t.Helper()
	f := newFakeRestic()
	s, data := newService(t, append([]func(*Config){func(c *Config) { c.runner = f }}, mod...)...)
	if err := os.Mkdir(s.backupsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if st := s.InitBackups(context.Background()); !st.Available {
		t.Fatalf("InitBackups: %+v", st)
	}
	return s, data, f
}

func randomID() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// command returns the restic command word (after the global flags).
func command(args []string) (string, []string) {
	for i, a := range args {
		if !strings.HasPrefix(a, "--") {
			return a, args[i+1:]
		}
	}
	return "", nil
}

func envOf(env []string, key string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}

// callsOf returns the recorded calls of one command.
func (f *fakeRestic) callsOf(cmd string) []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeCall
	for _, c := range f.calls {
		if w, _ := command(c.Args); w == cmd {
			out = append(out, c)
		}
	}
	return out
}

// snaps returns a copy of the snapshots of the repository at path.
func (f *fakeRestic) snaps(repo string) []fakeSnap {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeSnap
	if r := f.repos[repo]; r != nil {
		for _, s := range r.snaps {
			out = append(out, *s)
		}
	}
	return out
}

// addSnap puts a snapshot into the repository at repo (creating the repo on disk if needed).
func (f *fakeRestic) addSnap(t *testing.T, repo string, at time.Time, data []byte, tags ...string) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.repos[repo]
	if r == nil {
		if err := os.MkdirAll(repo, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, "config"), []byte("fake"), 0o600); err != nil {
			t.Fatal(err)
		}
		r = &fakeRepo{}
		f.repos[repo] = r
	}
	id := randomID()
	r.snaps = append(r.snaps, &fakeSnap{ID: id, Time: at, Tags: tags, Data: data})
	return id
}

type fakeProc struct {
	stdin    *io.PipeWriter
	done     chan struct{}
	killed   chan struct{}
	killOnce sync.Once
	err      error
}

func (p *fakeProc) Stdin() io.WriteCloser {
	if p.stdin == nil {
		return nil
	}
	return p.stdin
}

func (p *fakeProc) Kill() { p.killOnce.Do(func() { close(p.killed) }) }

func (p *fakeProc) Wait() error { <-p.done; return p.err }

var errFakeKilled = errors.New("fake restic: killed")

func (f *fakeRestic) Start(ctx context.Context, c resticCmd) (resticProc, error) {
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{Args: slices.Clone(c.Args), Env: slices.Clone(c.Env)})
	f.mu.Unlock()
	p := &fakeProc{done: make(chan struct{}), killed: make(chan struct{})}
	var stdin *io.PipeReader
	if c.Stdin {
		stdin, p.stdin = io.Pipe()
	}
	go func() {
		defer close(p.done)
		p.err = f.run(ctx, p, c, stdin)
		if stdin != nil {
			_ = stdin.CloseWithError(errFakeKilled) // a writer after the end fails like a broken pipe
		}
	}()
	return p, nil
}

func (f *fakeRestic) exit(code int, msg string) error {
	return &resticExit{Code: code, Stderr: msg + "\n"}
}

func (f *fakeRestic) run(ctx context.Context, p *fakeProc, c resticCmd, stdin *io.PipeReader) error {
	cmd, rest := command(c.Args)
	f.mu.Lock()
	failure := f.fail[cmd]
	f.mu.Unlock()
	if failure != nil {
		return failure
	}
	out := c.Stdout
	if out == nil {
		out = io.Discard
	}
	repoPath := envOf(c.Env, "RESTIC_REPOSITORY")
	switch cmd {
	case "version":
		_, _ = fmt.Fprintln(out, f.version)
		return nil
	case "init":
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.repos[repoPath] != nil {
			return f.exit(1, "Fatal: create repository at "+repoPath+" failed: config file already exists")
		}
		if err := os.MkdirAll(repoPath, 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(repoPath, "config"), []byte("fake"), 0o600); err != nil {
			return err
		}
		f.repos[repoPath] = &fakeRepo{}
		return nil
	case "unlock":
		return nil
	}
	f.mu.Lock()
	repo := f.repos[repoPath]
	f.mu.Unlock()
	if repo == nil {
		return f.exit(exitNoRepo, "Fatal: repository does not exist: unable to open config file")
	}
	switch cmd {
	case "backup":
		return f.backup(ctx, p, c, rest, repo, stdin, out)
	case "snapshots":
		return f.snapshots(repo, out)
	case "dump":
		return f.dump(ctx, p, rest, repo, out)
	case "tag":
		return f.tag(rest, repo, out)
	case "forget":
		return f.forget(rest, repo)
	}
	return f.exit(1, "fake restic: unknown command "+cmd)
}

func (f *fakeRestic) backup(ctx context.Context, p *fakeProc, c resticCmd, rest []string, repo *fakeRepo, stdin *io.PipeReader, out io.Writer) error {
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-p.killed:
			return &resticExit{Code: -1}
		case <-ctx.Done(): // like SIGINT
			return &resticExit{Code: 130, Stderr: "signal interrupt received, cleaning up\n"}
		}
	}
	var tags []string
	for i := 0; i < len(rest)-1; i++ {
		if rest[i] == "--tag" {
			tags = append(tags, rest[i+1])
		}
	}
	var buf bytes.Buffer
	readDone := make(chan error, 1)
	go func() { _, err := io.Copy(&buf, stdin); readDone <- err }()
	select {
	case err := <-readDone:
		f.mu.Lock()
		f.sawEOF = append(f.sawEOF, err == nil)
		f.mu.Unlock()
		if f.stdinRead != nil {
			f.stdinRead <- buf.Len()
		}
		if err != nil {
			return f.exit(1, "Fatal: reading stdin: "+err.Error())
		}
	case <-p.killed:
		_ = stdin.CloseWithError(errFakeKilled)
		<-readDone
		f.mu.Lock()
		f.sawEOF = append(f.sawEOF, false)
		f.mu.Unlock()
		if f.stdinRead != nil {
			f.stdinRead <- buf.Len()
		}
		return &resticExit{Code: -1}
	case <-ctx.Done():
		_ = stdin.CloseWithError(errFakeKilled)
		<-readDone
		return &resticExit{Code: 130, Stderr: "signal interrupt received, cleaning up\n"}
	}
	f.mu.Lock()
	at := f.next
	f.next = f.next.Add(time.Minute)
	id := randomID()
	repo.snaps = append(repo.snaps, &fakeSnap{ID: id, Time: at, Tags: tags, Data: buf.Bytes()})
	f.mu.Unlock()
	_, _ = fmt.Fprintf(out, `{"message_type":"status","percent_done":1}`+"\n")
	_, _ = fmt.Fprintf(out, `{"message_type":"summary","total_bytes_processed":%d,"snapshot_id":%q}`+"\n", buf.Len(), id)
	return nil
}

func (f *fakeRestic) snapshots(repo *fakeRepo, out io.Writer) error {
	type summary struct {
		TotalBytesProcessed int64 `json:"total_bytes_processed"`
	}
	type snap struct {
		Time     string   `json:"time"`
		ID       string   `json:"id"`
		ShortID  string   `json:"short_id"`
		Hostname string   `json:"hostname"`
		Paths    []string `json:"paths"`
		Tags     []string `json:"tags"`
		Summary  summary  `json:"summary"`
	}
	f.mu.Lock()
	list := []snap{}
	for _, s := range repo.snaps {
		if !slices.Contains(s.Tags, "mcpanel") {
			continue
		}
		list = append(list, snap{
			Time: s.Time.Format(time.RFC3339Nano), ID: s.ID, ShortID: s.ID[:8], Hostname: "mcpanel",
			Paths: []string{"/data.tar"}, Tags: s.Tags, Summary: summary{TotalBytesProcessed: int64(len(s.Data))},
		})
	}
	f.mu.Unlock()
	return json.NewEncoder(out).Encode(list)
}

func (f *fakeRestic) find(repo *fakeRepo, id string) *fakeSnap {
	for _, s := range repo.snaps {
		if s.ID == id {
			return s
		}
	}
	return nil
}

func (f *fakeRestic) dump(ctx context.Context, p *fakeProc, rest []string, repo *fakeRepo, out io.Writer) error {
	if len(rest) != 2 || rest[1] != "/data.tar" {
		return f.exit(1, "Fatal: no file and no snapshot ID specified")
	}
	f.mu.Lock()
	s := f.find(repo, rest[0])
	f.mu.Unlock()
	if s == nil {
		return f.exit(1, "Fatal: failed to find snapshot: no matching ID found for prefix "+rest[0])
	}
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-p.killed:
			return &resticExit{Code: -1}
		case <-ctx.Done():
			return &resticExit{Code: 130}
		}
	}
	if _, err := out.Write(s.Data); err != nil {
		return &resticExit{Code: 1, Stderr: "Fatal: write: " + err.Error()}
	}
	return nil
}

func (f *fakeRestic) tag(rest []string, repo *fakeRepo, out io.Writer) error {
	if len(rest) != 3 || rest[1] != "pinned" || (rest[0] != "--add" && rest[0] != "--remove") {
		return f.exit(1, "fake restic: unexpected tag arguments "+strings.Join(rest, " "))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.find(repo, rest[2])
	if s == nil {
		return f.exit(1, "Fatal: no matching ID found")
	}
	has := slices.Contains(s.Tags, "pinned")
	if (rest[0] == "--add") == has {
		_, _ = fmt.Fprintln(out, `{"message_type":"summary","changed_snapshots":0}`)
		return nil
	}
	old := s.ID
	if rest[0] == "--add" {
		s.Tags = append(s.Tags, "pinned")
	} else {
		s.Tags = slices.DeleteFunc(s.Tags, func(t string) bool { return t == "pinned" })
	}
	s.ID = randomID()
	_, _ = fmt.Fprintf(out, `{"message_type":"changed","old_snapshot_id":%q,"new_snapshot_id":%q}`+"\n", old, s.ID)
	_, _ = fmt.Fprintln(out, `{"message_type":"summary","changed_snapshots":1}`)
	return nil
}

// forget removes explicit IDs; with a policy it applies only --keep-last to unpinned
// kind=scheduled snapshots (enough for unit tests; the real policy is covered in Aufgabe 8).
func (f *fakeRestic) forget(rest []string, repo *fakeRepo) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	keepLast := -1
	var ids []string
	for i := 0; i < len(rest); i++ {
		switch a := rest[i]; {
		case a == "--keep-last":
			keepLast, _ = strconv.Atoi(rest[i+1])
			i++
		case strings.HasPrefix(a, "--") && a != "--prune":
			i++ // flag with a value
		case a == "--prune":
		default:
			ids = append(ids, a)
		}
	}
	if len(ids) > 0 {
		for _, id := range ids {
			if f.find(repo, id) == nil {
				return f.exit(1, "Fatal: no matching ID found for prefix "+id)
			}
		}
		repo.snaps = slices.DeleteFunc(repo.snaps, func(s *fakeSnap) bool { return slices.Contains(ids, s.ID) })
		return nil
	}
	if keepLast < 0 {
		return nil
	}
	var sched []*fakeSnap
	for _, s := range repo.snaps {
		if slices.Contains(s.Tags, "kind=scheduled") && !slices.Contains(s.Tags, "pinned") {
			sched = append(sched, s)
		}
	}
	slices.SortFunc(sched, func(a, b *fakeSnap) int { return b.Time.Compare(a.Time) })
	if len(sched) > keepLast {
		drop := sched[keepLast:]
		repo.snaps = slices.DeleteFunc(repo.snaps, func(s *fakeSnap) bool { return slices.Contains(drop, s) })
	}
	return nil
}
