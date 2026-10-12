package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

func listJars(t *testing.T, s *Service, dir string) filesapi.JarsResponse {
	t.Helper()
	res, err := s.Jars(ctx, 1, dir)
	if err != nil {
		t.Fatalf("jars %s: %v", dir, err)
	}
	if res.Jars == nil {
		t.Fatal("jars is nil, want a list")
	}
	return res
}

func jarNames(res filesapi.JarsResponse) []string {
	out := make([]string, 0, len(res.Jars))
	for _, j := range res.Jars {
		out = append(out, j.Name)
	}
	return out
}

func jarByName(t *testing.T, res filesapi.JarsResponse, name string) filesapi.Jar {
	t.Helper()
	for _, j := range res.Jars {
		if j.Name == name {
			return j
		}
	}
	t.Fatalf("%s not listed: %v", name, jarNames(res))
	return filesapi.Jar{}
}

// countingHook counts the computed (not cached) jars by name.
type countingHook struct {
	mu sync.Mutex
	n  map[string]int
}

func (h *countingHook) hook(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.n == nil {
		h.n = map[string]int{}
	}
	h.n[name]++
}

func (h *countingHook) count(name string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.n[name]
}

func TestJarsListsJarFiles(t *testing.T) {
	s, data := newService(t)
	put(t, data, "plugins/LuckPerms.jar", "lp")
	put(t, data, "plugins/x.jar", "x-on")
	put(t, data, "plugins/x.jar.disabled", "x-off")
	put(t, data, "plugins/OLD.JAR.DISABLED", "old")
	put(t, data, "plugins/readme.txt", "no")
	put(t, data, "plugins/notes.jar.bak", "no")
	put(t, data, "plugins/.jar", "no name")
	put(t, data, "plugins/.mcpanel-tmp-abc.jar", "hidden temp file")
	put(t, data, "plugins/LuckPerms/config.yml", "folder content")
	put(t, data, "plugins/sub/deep.jar", "not on the top level")
	if err := os.Mkdir(filepath.Join(data, "plugins", "folder.jar"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := listJars(t, s, filesapi.DirPlugins)
	if want := []string{"LuckPerms.jar", "OLD.JAR.DISABLED", "x.jar", "x.jar.disabled"}; !reflect.DeepEqual(jarNames(res), want) {
		t.Fatalf("jars %v, want %v", jarNames(res), want)
	}
	if res.Truncated {
		t.Fatal("truncated without reason")
	}
	for name, c := range map[string]struct {
		content string
		enabled bool
	}{
		"LuckPerms.jar": {"lp", true}, "OLD.JAR.DISABLED": {"old", false},
		"x.jar": {"x-on", true}, "x.jar.disabled": {"x-off", false},
	} {
		j := jarByName(t, res, name)
		h256, h512 := sums(c.content)
		fi, err := os.Stat(filepath.Join(data, "plugins", name))
		if err != nil {
			t.Fatal(err)
		}
		if j.Enabled != c.enabled || j.Size != int64(len(c.content)) || j.MTime != fi.ModTime().UnixMilli() ||
			j.SHA256 != h256 || j.SHA512 != h512 || j.Meta != nil {
			t.Errorf("%s: %+v", name, j)
		}
	}
	// Review focus 2: x.jar and x.jar.disabled are two jars with their own content.
	if jarByName(t, res, "x.jar").SHA512 == jarByName(t, res, "x.jar.disabled").SHA512 {
		t.Fatal("x.jar and x.jar.disabled share a checksum")
	}
}

// Review focus 2: names that differ only in case are separate jars (Linux is case-sensitive).
func TestJarsKeepsCaseVariantsApart(t *testing.T) {
	s, data := newService(t)
	put(t, data, "plugins/Foo.jar", "upper")
	if _, err := os.Stat(filepath.Join(data, "plugins", "foo.JAR")); err == nil {
		t.Skip("case-insensitive file system")
	}
	put(t, data, "plugins/foo.JAR", "lower")
	res := listJars(t, s, filesapi.DirPlugins)
	if want := []string{"Foo.jar", "foo.JAR"}; !reflect.DeepEqual(jarNames(res), want) {
		t.Fatalf("jars %v, want %v", jarNames(res), want)
	}
	up256, _ := sums("upper")
	low256, _ := sums("lower")
	if jarByName(t, res, "Foo.jar").SHA256 != up256 || jarByName(t, res, "foo.JAR").SHA256 != low256 {
		t.Fatalf("checksums mixed up: %+v", res.Jars)
	}
	if !jarByName(t, res, "foo.JAR").Enabled {
		t.Fatal(".JAR is an enabled jar")
	}
}

func TestJarsMissingOrOddFolder(t *testing.T) {
	s, data := newService(t)
	if res := listJars(t, s, filesapi.DirPlugins); len(res.Jars) != 0 || res.Truncated {
		t.Fatalf("missing folder: %+v", res)
	}
	put(t, data, "mods", "a file, not a folder")
	if res := listJars(t, s, filesapi.DirMods); len(res.Jars) != 0 {
		t.Fatalf("file instead of a folder: %+v", res)
	}
	// A server that never ran has no data directory: no jars, no error.
	res, err := s.Jars(ctx, 2, filesapi.DirPlugins)
	if err != nil || res.Jars == nil || len(res.Jars) != 0 {
		t.Fatalf("server without data dir: %+v %v", res, err)
	}
	for _, dir := range []string{"", "world", "Plugins", "plugins/", "../plugins", "plugins/sub"} {
		if _, err := s.Jars(ctx, 1, dir); !errors.Is(err, filesapi.ErrInvalid) {
			t.Errorf("dir %q: %v, want ErrInvalid", dir, err)
		}
	}
	if _, err := s.Jars(ctx, 0, filesapi.DirPlugins); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("id 0: %v, want ErrInvalid", err)
	}
}

// Review focus 5: more than filesapi.MaxJars jars.
func TestJarsTruncates(t *testing.T) {
	s, data := newService(t)
	for i := range filesapi.MaxJars {
		put(t, data, fmt.Sprintf("mods/m%03d.jar", i), "m"+strconv.Itoa(i))
	}
	for i := range 5 {
		put(t, data, fmt.Sprintf("mods/readme%d.txt", i), "not a jar") // never counted
	}
	res := listJars(t, s, filesapi.DirMods)
	if len(res.Jars) != filesapi.MaxJars || res.Truncated {
		t.Fatalf("exactly %d jars: %d listed, truncated=%v", filesapi.MaxJars, len(res.Jars), res.Truncated)
	}
	put(t, data, "mods/zzz.jar", "one more")
	res = listJars(t, s, filesapi.DirMods)
	if len(res.Jars) != filesapi.MaxJars || !res.Truncated {
		t.Fatalf("%d jars: %d listed, truncated=%v", filesapi.MaxJars+1, len(res.Jars), res.Truncated)
	}
	if !sort.StringsAreSorted(jarNames(res)) {
		t.Fatal("not sorted by name")
	}
}

// Review focus 5: jars above the checksum limit (256 MiB, here lowered) come without checksums.
func TestJarsChecksumLimit(t *testing.T) {
	s, data := newService(t)
	if s.jarHashMax != filesapi.MaxJarHashBytes {
		t.Fatalf("default checksum limit %d, want filesapi.MaxJarHashBytes", s.jarHashMax)
	}
	s.jarHashMax = 8
	put(t, data, "plugins/small.jar", "12345678")
	put(t, data, "plugins/big.jar", "123456789")
	res := listJars(t, s, filesapi.DirPlugins)
	small, big := jarByName(t, res, "small.jar"), jarByName(t, res, "big.jar")
	if h256, h512 := sums("12345678"); small.SHA256 != h256 || small.SHA512 != h512 {
		t.Fatalf("jar at the limit: %+v", small)
	}
	if big.SHA256 != "" || big.SHA512 != "" || big.Size != 9 {
		t.Fatalf("jar above the limit: %+v, want no checksums", big)
	}
}

func TestJarsCachesByFileIdentity(t *testing.T) {
	s, data := newService(t)
	var h countingHook
	s.jarHook = h.hook
	put(t, data, "plugins/a.jar", "aaaa")
	put(t, data, "plugins/b.jar", "bbbb")
	first := listJars(t, s, filesapi.DirPlugins)
	if h.count("a.jar") != 1 || h.count("b.jar") != 1 {
		t.Fatalf("first listing computed %v", h.n)
	}
	if second := listJars(t, s, filesapi.DirPlugins); !reflect.DeepEqual(first, second) {
		t.Fatalf("cached listing differs: %+v vs %+v", first, second)
	}
	if h.count("a.jar") != 1 || h.count("b.jar") != 1 {
		t.Fatalf("second listing computed again: %v", h.n)
	}

	// Same size, new content and a new mtime: computed again.
	put(t, data, "plugins/a.jar", "AAAA")
	later := testStart.Add(time.Hour)
	if err := os.Chtimes(filepath.Join(data, "plugins", "a.jar"), later, later); err != nil {
		t.Fatal(err)
	}
	third := listJars(t, s, filesapi.DirPlugins)
	if h256, _ := sums("AAAA"); jarByName(t, third, "a.jar").SHA256 != h256 || h.count("a.jar") != 2 {
		t.Fatalf("changed jar: %+v, computed %d times", jarByName(t, third, "a.jar"), h.count("a.jar"))
	}

	// Toggling renames the jar: same inode, size and mtime, so the cache still applies.
	if err := os.Rename(filepath.Join(data, "plugins", "b.jar"), filepath.Join(data, "plugins", "b.jar.disabled")); err != nil {
		t.Fatal(err)
	}
	fourth := listJars(t, s, filesapi.DirPlugins)
	off := jarByName(t, fourth, "b.jar.disabled")
	if b256, _ := sums("bbbb"); off.SHA256 != b256 || off.Enabled || h.count("b.jar.disabled") != 0 {
		t.Fatalf("renamed jar: %+v, computed %d times", off, h.count("b.jar.disabled"))
	}
	if n := s.jarCache.size(); n > hashCacheSize {
		t.Fatalf("cache holds %d entries", n)
	}
}

func TestJarsWaitForAHashSlot(t *testing.T) {
	s, data := newService(t)
	put(t, data, "plugins/a.jar", "a")
	listJars(t, s, filesapi.DirPlugins) // a.jar is cached now

	// Both slots taken: a cached jar needs none ...
	s.hashSlots <- struct{}{}
	s.hashSlots <- struct{}{}
	if res := listJars(t, s, filesapi.DirPlugins); len(res.Jars) != 1 || res.Jars[0].SHA256 == "" {
		t.Fatalf("cached jar while the slots are taken: %+v", res)
	}
	// ... a new one waits and gives up with its context.
	put(t, data, "plugins/b.jar", "b")
	wait, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := s.Jars(wait, 1, filesapi.DirPlugins); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("no slot: %v, want the context's error", err)
	}
	<-s.hashSlots
	<-s.hashSlots
	if res := listJars(t, s, filesapi.DirPlugins); len(res.Jars) != 2 {
		t.Fatalf("after the slots are free: %+v", res)
	}

	// Never more than maxHashRuns passes at once, however many requests run.
	var active, peak atomic.Int32
	s.jarHook = func(string) {
		n := active.Add(1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		time.Sleep(20 * time.Millisecond)
		active.Add(-1)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for id := int64(2); id <= 7; id++ {
		dir := filepath.Join(s.cfg.InstancesDir, "instances", strconv.FormatInt(id, 10))
		for i := range 3 {
			put(t, dir, fmt.Sprintf("plugins/p%d.jar", i), fmt.Sprintf("server %d jar %d", id, i))
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Jars(ctx, id, filesapi.DirPlugins)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent listing: %v", err)
		}
	}
	if p := peak.Load(); p > maxHashRuns || p == 0 {
		t.Fatalf("%d passes ran at once, want 1..%d", p, maxHashRuns)
	}
}

func TestServerJars(t *testing.T) {
	s, data := newService(t)
	put(t, data, "mods/lithium.jar", "lith")
	ts := startHTTP(t, NewServer(s, "test", discardLog()))

	status, body := postJSON(t, ts, filesapi.PathJars, filesapi.JarsRequest{ID: 1, Dir: filesapi.DirMods})
	if status != http.StatusOK {
		t.Fatalf("jars: %d %s", status, body)
	}
	var res filesapi.JarsResponse
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatal(err)
	}
	if _, h512 := sums("lith"); len(res.Jars) != 1 || res.Jars[0].Name != "lithium.jar" || res.Jars[0].SHA512 != h512 {
		t.Fatalf("jars answer: %s", body)
	}
	status, body = postJSON(t, ts, filesapi.PathJars, filesapi.JarsRequest{ID: 1, Dir: filesapi.DirPlugins})
	if status != http.StatusOK || !strings.Contains(string(body), `"jars":[]`) {
		t.Fatalf("empty folder: %d %s, want \"jars\":[]", status, body)
	}
	status, body = postJSON(t, ts, filesapi.PathJars, filesapi.JarsRequest{ID: 1, Dir: "world"})
	if status != http.StatusBadRequest || errCode(t, body) != filesapi.CodeInvalid {
		t.Fatalf("bad dir: %d %s", status, body)
	}
	status, body = postJSON(t, ts, filesapi.PathJars, map[string]any{"id": 1, "dir": "mods", "path": "x"})
	if status != http.StatusBadRequest || errCode(t, body) != filesapi.CodeInvalid {
		t.Fatalf("unknown field: %d %s", status, body)
	}
	resp, err := http.Get(ts.URL + filesapi.PathJars)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d, want 405", resp.StatusCode)
	}
}

// Hashing many jars may take longer than the server's WriteTimeout; the answer still arrives.
func TestJarsOutliveWriteTimeout(t *testing.T) {
	s, data := newService(t)
	put(t, data, "plugins/a.jar", "a")
	srv := NewServer(s, "test", discardLog())
	srv.testHook = func(op string) {
		if op == "jars" {
			time.Sleep(300 * time.Millisecond)
		}
	}
	ts := httptest.NewUnstartedServer(srv)
	ts.Config.WriteTimeout = 100 * time.Millisecond
	ts.Start()
	t.Cleanup(ts.Close)
	if status, body := postJSON(t, ts, filesapi.PathJars, filesapi.JarsRequest{ID: 1, Dir: filesapi.DirPlugins}); status != http.StatusOK {
		t.Fatalf("slow jars: %d %s", status, body)
	}
}

// The server's ReadTimeout does not cancel a long listing either: net/http clears the read
// deadline when it starts its background read after the request body, so the request context
// stays alive while a slow cache miss is computed.
func TestJarsOutliveReadTimeout(t *testing.T) {
	s, data := newService(t)
	put(t, data, "plugins/a.jar", "a")
	s.jarHook = func(string) { time.Sleep(300 * time.Millisecond) } // inside the ctx-checked pass
	ts := httptest.NewUnstartedServer(NewServer(s, "test", discardLog()))
	ts.Config.ReadTimeout = 100 * time.Millisecond
	ts.Start()
	t.Cleanup(ts.Close)
	status, body := postJSON(t, ts, filesapi.PathJars, filesapi.JarsRequest{ID: 1, Dir: filesapi.DirPlugins})
	var res filesapi.JarsResponse
	if err := json.Unmarshal(body, &res); status != http.StatusOK || err != nil || len(res.Jars) != 1 || res.Jars[0].SHA256 == "" {
		t.Fatalf("slow jars past ReadTimeout: %d %q", status, body)
	}
}
