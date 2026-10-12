package files

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// One listing hashes at most jarHashBudget bytes: jars beyond it come without checksums and
// are not cached, so the next listing computes them (cached jars cost nothing).
func TestJarsHashBudgetPerRequest(t *testing.T) {
	s, data := newService(t)
	if s.jarHashBudget != maxHashBytesPerRequest || maxHashBytesPerRequest != 2<<30 {
		t.Fatalf("hash budget %d (const %d), want 2 GiB", s.jarHashBudget, maxHashBytesPerRequest)
	}
	s.jarHashBudget = 10
	var h countingHook
	s.jarHook = h.hook
	put(t, data, "plugins/a.jar", "aaaaaa") // 6: hashed, 4 left
	put(t, data, "plugins/b.jar", "bbbbbb") // 6 > 4: no checksums, not cached
	put(t, data, "plugins/c.jar", "ccc")    // 3 <= 4: hashed
	first := listJars(t, s, filesapi.DirPlugins)
	a, b, c := jarByName(t, first, "a.jar"), jarByName(t, first, "b.jar"), jarByName(t, first, "c.jar")
	if a.SHA256 == "" || c.SHA512 == "" {
		t.Fatalf("jars within the budget: %+v %+v", a, c)
	}
	if b.SHA256 != "" || b.SHA512 != "" || b.Size != 6 {
		t.Fatalf("jar beyond the budget: %+v, want no checksums", b)
	}
	second := listJars(t, s, filesapi.DirPlugins)
	if h256, _ := sums("bbbbbb"); jarByName(t, second, "b.jar").SHA256 != h256 {
		t.Fatalf("next listing: %+v, want b.jar hashed", jarByName(t, second, "b.jar"))
	}
	if h.count("a.jar") != 1 || h.count("c.jar") != 1 || h.count("b.jar") != 2 {
		t.Fatalf("computed %v, want a and c once, b twice", h.n)
	}
}

// Two listings of the same server never hash at the same time (one pass per instance), while
// different servers still share both service-wide slots.
func TestJarsOneHashPassPerInstance(t *testing.T) {
	s, data := newService(t)
	for _, n := range []string{"a", "b", "c", "d"} {
		put(t, data, "plugins/"+n+".jar", n)
	}
	var active, peak atomic.Int32
	s.jarHook = func(string) {
		n := active.Add(1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		time.Sleep(30 * time.Millisecond)
		active.Add(-1)
	}
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			if _, err := s.Jars(ctx, 1, filesapi.DirPlugins); err != nil {
				t.Errorf("jars: %v", err)
			}
		})
	}
	wg.Wait()
	if p := peak.Load(); p != 1 {
		t.Fatalf("%d passes ran at once for one server, want 1", p)
	}
	s.instMu.Lock()
	left := len(s.instHash)
	s.instMu.Unlock()
	if left != 0 {
		t.Fatalf("%d per-instance slots left behind", left)
	}
}
