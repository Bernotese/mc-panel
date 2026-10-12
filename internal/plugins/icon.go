package plugins

import (
	"container/list"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

const (
	iconCacheBytes = 32 << 20         // LRU size (spec §6.8)
	iconTTL        = 24 * time.Hour   // per icon
	maxIconFetches = 8                // parallel upstream fetches in the panel
	iconTimeout    = 30 * time.Second // one fetch including the wait for a slot
)

// iconCache is an LRU of icons by bytes with shared fetches and a panel-wide fetch limit.
type iconCache struct {
	clock    clock.Clock
	up       *upstream.Client
	sem      chan struct{}
	maxBytes int64

	mu       sync.Mutex
	ll       *list.List // front = most recently used
	items    map[string]*list.Element
	bytes    int64
	inflight map[string]*iconCall
}

type iconItem struct {
	url  string
	icon upstream.Icon
	at   time.Time
}

type iconCall struct {
	done chan struct{}
	icon upstream.Icon
	err  error
}

func newIconCache(clk clock.Clock, up *upstream.Client) *iconCache {
	return &iconCache{
		clock: clk, up: up, sem: make(chan struct{}, maxIconFetches), maxBytes: iconCacheBytes,
		ll: list.New(), items: map[string]*list.Element{}, inflight: map[string]*iconCall{},
	}
}

func (c *iconCache) get(ctx context.Context, raw string) (upstream.Icon, error) {
	c.mu.Lock()
	if el, ok := c.items[raw]; ok {
		it := el.Value.(*iconItem)
		if c.clock.Now().Sub(it.at) < iconTTL {
			c.ll.MoveToFront(el)
			c.mu.Unlock()
			return it.icon, nil
		}
		c.removeLocked(el)
	}
	cl, ok := c.inflight[raw]
	if !ok {
		cl = &iconCall{done: make(chan struct{})}
		c.inflight[raw] = cl
		go c.fetch(context.WithoutCancel(ctx), raw, cl)
	}
	c.mu.Unlock()
	select {
	case <-cl.done:
		return cl.icon, cl.err
	case <-ctx.Done():
		return upstream.Icon{}, ctx.Err()
	}
}

func (c *iconCache) fetch(ctx context.Context, raw string, cl *iconCall) {
	ctx, cancel := context.WithTimeout(ctx, iconTimeout)
	defer cancel()
	var (
		icon upstream.Icon
		err  error
	)
	select {
	case c.sem <- struct{}{}:
		icon, err = c.up.Icon(ctx, raw)
		<-c.sem
	case <-ctx.Done():
		err = fmt.Errorf("%w: no free icon slot: %w", upstream.ErrUnavailable, ctx.Err())
	}
	c.mu.Lock()
	if err == nil {
		c.addLocked(raw, icon)
	}
	delete(c.inflight, raw)
	c.mu.Unlock()
	cl.icon, cl.err = icon, err
	close(cl.done)
}

func (c *iconCache) addLocked(raw string, icon upstream.Icon) {
	c.items[raw] = c.ll.PushFront(&iconItem{url: raw, icon: icon, at: c.clock.Now()})
	c.bytes += int64(len(icon.Data))
	for c.bytes > c.maxBytes && c.ll.Len() > 0 {
		c.removeLocked(c.ll.Back())
	}
}

func (c *iconCache) removeLocked(el *list.Element) {
	it := el.Value.(*iconItem)
	c.ll.Remove(el)
	delete(c.items, it.url)
	c.bytes -= int64(len(it.icon.Data))
}

// handleIcon proxies a project icon (spec §6.8); the browser never contacts a third party.
func (h *Handler) handleIcon(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.loadServer(w, r, false); !ok {
		return
	}
	raw := r.URL.Query().Get("url")
	if !h.up.IconURLAllowed(raw) {
		writeValidation(w, r, "url must be an https URL on cdn.modrinth.com or hangarcdn.papermc.io (at most 512 characters)")
		return
	}
	icon, err := h.icons.get(r.Context(), raw)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		h.log.WarnContext(r.Context(), "plugins: icon unavailable", "err", err)
		httpx.WriteError(w, r, http.StatusBadGateway, httpx.CodePluginSourceUnavailable, "the icon could not be loaded")
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", icon.ContentType)
	hd.Set("Content-Length", strconv.Itoa(len(icon.Data)))
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Content-Security-Policy", "default-src 'none'")
	hd.Set("Cache-Control", "private, max-age=86400")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(icon.Data)
}
