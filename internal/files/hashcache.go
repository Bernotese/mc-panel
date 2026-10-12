package files

import (
	"container/list"
	"sync"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// jarKey identifies the content of a jar without reading it (spec 3c §5.1): device and inode
// (fileKeyOf, per platform), size, mtime and ctime in nanoseconds. A replaced jar (new
// upload, rename over it) or one changed in place (new size, mtime or ctime; the ctime also
// moves when the mtime is set back) gets a new key. Renaming (toggle) moves the ctime as well
// on Linux, so a toggled jar is computed once more.
type jarKey struct {
	dev   devID
	ino   uint64
	size  int64
	mtime int64
	ctime int64
}

// jarInfo is what the service computes per jar.
type jarInfo struct {
	sha256, sha512 string            // lowercase hex; "" above jarHashMax
	meta           *filesapi.JarMeta // nil = not readable; shared between answers, never modified
}

type cacheEntry struct {
	key  jarKey
	info jarInfo
}

// hashCache keeps the jarInfo of at most limit jars in memory; when it is full, the least
// recently used entry goes. Safe for concurrent use.
type hashCache struct {
	mu    sync.Mutex
	limit int
	order *list.List // of *cacheEntry, front = most recently used
	items map[jarKey]*list.Element
}

func newHashCache(limit int) *hashCache {
	return &hashCache{limit: limit, order: list.New(), items: make(map[jarKey]*list.Element)}
}

func (c *hashCache) get(k jarKey) (jarInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[k]
	if !ok {
		return jarInfo{}, false
	}
	c.order.MoveToFront(el)
	return el.Value.(*cacheEntry).info, true
}

func (c *hashCache) put(k jarKey, info jarInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[k]; ok {
		el.Value.(*cacheEntry).info = info
		c.order.MoveToFront(el)
		return
	}
	c.items[k] = c.order.PushFront(&cacheEntry{key: k, info: info})
	for c.order.Len() > c.limit {
		old := c.order.Back()
		c.order.Remove(old)
		delete(c.items, old.Value.(*cacheEntry).key)
	}
}

// size returns the number of cached jars.
func (c *hashCache) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}
