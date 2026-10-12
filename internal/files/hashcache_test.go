package files

import "testing"

func TestHashCacheEvictsLeastRecentlyUsed(t *testing.T) {
	if hashCacheSize != 4096 || maxHashRuns != 2 {
		t.Fatalf("limits changed: cache %d, runs %d (spec 3c §5.1: 4096, 2)", hashCacheSize, maxHashRuns)
	}
	c := newHashCache(2)
	key := func(ino uint64) jarKey { return jarKey{ino: ino, size: 1, mtime: 1} }
	c.put(key(1), jarInfo{sha256: "1"})
	c.put(key(2), jarInfo{sha256: "2"})
	if _, ok := c.get(key(1)); !ok { // 1 is now the most recently used
		t.Fatal("entry 1 missing")
	}
	c.put(key(3), jarInfo{sha256: "3"})
	if _, ok := c.get(key(2)); ok {
		t.Fatal("the least recently used entry was not evicted")
	}
	for _, k := range []uint64{1, 3} {
		if info, ok := c.get(key(k)); !ok || info.sha256 == "" {
			t.Fatalf("entry %d missing", k)
		}
	}
	c.put(key(3), jarInfo{sha256: "3b"}) // update in place
	if info, _ := c.get(key(3)); info.sha256 != "3b" || c.size() != 2 {
		t.Fatalf("update: %+v, size %d", info, c.size())
	}
	// Any part of the key distinguishes entries.
	if _, ok := c.get(jarKey{ino: 3, size: 1, mtime: 2}); ok {
		t.Fatal("another mtime hit the cache")
	}
	if _, ok := c.get(jarKey{ino: 3, size: 2, mtime: 1}); ok {
		t.Fatal("another size hit the cache")
	}
}
