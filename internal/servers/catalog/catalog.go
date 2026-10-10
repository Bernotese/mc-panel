// Package catalog fetches the Minecraft version lists for the create form (spec §6). Only
// fixed upstream URLs are queried; the instance type selects the source, never the client.
package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/clock"
)

const (
	// Timeout per upstream request.
	Timeout = 10 * time.Second
	// Deadline bounds one Get across all its upstream requests (below the 60 s write timeout).
	Deadline = 45 * time.Second
	// MaxBytes limits every upstream response.
	MaxBytes = 5 << 20
	// CacheTTL is how long a fetched catalog is served from memory.
	CacheTTL = time.Hour
	// paperStableChecks: only the newest Paper versions are asked for a STABLE build; the
	// older ones all have one (verified 2026-10-10).
	paperStableChecks = 10
)

// Upstreams are the base URLs (scheme and host, no trailing slash) of the catalog sources.
type Upstreams struct {
	Mojang, Paper, Purpur, Fabric, NeoForge string
}

// DefaultUpstreams are the production sources, checked against the live services 2026-10-10.
var DefaultUpstreams = Upstreams{
	Mojang:   "https://piston-meta.mojang.com",
	Paper:    "https://fill.papermc.io",
	Purpur:   "https://api.purpurmc.org",
	Fabric:   "https://meta.fabricmc.net",
	NeoForge: "https://maven.neoforged.net",
}

var (
	// ErrUnsupported: the type has no version catalog (modrinth, curseforge: the pack decides).
	ErrUnsupported = errors.New("catalog: no version catalog for this type")
	// ErrUnavailable: the upstream failed (network, status, size, JSON, no usable versions).
	ErrUnavailable = errors.New("catalog: upstream unavailable")
)

// Version is the OpenAPI schema CatalogVersion: a Minecraft version and its derived image.
type Version struct {
	ID   string            `json:"id"`
	Java instancespec.Java `json:"java"`
}

// Catalog is the OpenAPI schema Catalog; versions newest first.
type Catalog struct {
	Versions []Version `json:"versions"`
	Loaders  []string  `json:"loaders"`
}

type Options struct {
	Clock      clock.Clock
	Version    string        // panel version for the User-Agent; "" = "dev"
	Upstreams  Upstreams     // zero value = DefaultUpstreams
	HTTPClient *http.Client  // nil = a client with Timeout; copied, redirects limited to same-host https
	Timeout    time.Duration // per request; 0 = Timeout
	Deadline   time.Duration // per Get; 0 = Deadline
}

type entry struct {
	at  time.Time
	cat Catalog
}

// call is one upstream fetch that concurrent Gets of the same type share.
type call struct {
	done chan struct{}
	cat  Catalog
	err  error
}

// Client fetches and caches catalogs. It is safe for concurrent use.
type Client struct {
	clock    clock.Clock
	hc       *http.Client
	ua       string
	up       Upstreams
	timeout  time.Duration
	deadline time.Duration

	mu       sync.Mutex
	cache    map[instancespec.Type]entry
	inflight map[instancespec.Type]*call
}

func New(o Options) *Client {
	c := &Client{clock: o.Clock, ua: UserAgent(o.Version), up: o.Upstreams, timeout: o.Timeout, deadline: o.Deadline,
		cache: map[instancespec.Type]entry{}, inflight: map[instancespec.Type]*call{}}
	if c.up == (Upstreams{}) {
		c.up = DefaultUpstreams
	}
	if c.timeout <= 0 {
		c.timeout = Timeout
	}
	if c.deadline <= 0 {
		c.deadline = Deadline
	}
	c.hc = &http.Client{Timeout: c.timeout}
	if o.HTTPClient != nil {
		hc := *o.HTTPClient
		c.hc = &hc
	}
	c.hc.CheckRedirect = sameHostHTTPS
	return c
}

// sameHostHTTPS follows a redirect only to https on the host of the original request, so an
// upstream cannot send the panel to another host or to plain http.
func sameHostHTTPS(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if req.URL.Scheme != "https" || req.URL.Host != via[0].URL.Host {
		return fmt.Errorf("redirect to %s refused: only https on the same host", req.URL.Redacted())
	}
	return nil
}

// UserAgent is sent on every catalog request; the PaperMC Fill API requires a contact.
func UserAgent(version string) string {
	if version == "" {
		version = "dev"
	}
	return "mc-panel/" + version + " (https://github.com/Bernotese/mc-panel)"
}

// Get returns the catalog of t, from the cache if it is younger than CacheTTL. Failures are
// not cached. Concurrent Gets of the same type share one fetch, which has an overall
// Deadline and is not cancelled when one of the waiting callers goes away.
func (c *Client) Get(ctx context.Context, t instancespec.Type) (Catalog, error) {
	fetch := map[instancespec.Type]func(context.Context) (Catalog, error){
		instancespec.TypeVanilla:  c.vanilla,
		instancespec.TypePaper:    c.paper,
		instancespec.TypePurpur:   c.purpur,
		instancespec.TypeFabric:   c.fabric,
		instancespec.TypeNeoForge: c.neoforge,
	}[t]
	if fetch == nil {
		return Catalog{}, ErrUnsupported
	}
	c.mu.Lock()
	if e, ok := c.cache[t]; ok && c.clock.Now().Sub(e.at) < CacheTTL {
		c.mu.Unlock()
		return e.cat, nil
	}
	cl, ok := c.inflight[t]
	if !ok {
		cl = &call{done: make(chan struct{})}
		c.inflight[t] = cl
		go c.run(context.WithoutCancel(ctx), t, fetch, cl)
	}
	c.mu.Unlock()
	select {
	case <-cl.done:
		return cl.cat, cl.err
	case <-ctx.Done():
		return Catalog{}, fmt.Errorf("%w: %s: %w", ErrUnavailable, t, ctx.Err())
	}
}

// run performs the shared fetch of cl within the overall deadline and caches a success.
func (c *Client) run(ctx context.Context, t instancespec.Type, fetch func(context.Context) (Catalog, error), cl *call) {
	ctx, cancel := context.WithTimeout(ctx, c.deadline)
	defer cancel()
	cat, err := load(ctx, t, fetch)
	c.mu.Lock()
	if err == nil {
		c.cache[t] = entry{at: c.clock.Now(), cat: cat}
	}
	delete(c.inflight, t)
	c.mu.Unlock()
	cl.cat, cl.err = cat, err
	close(cl.done)
}

func load(ctx context.Context, t instancespec.Type, fetch func(context.Context) (Catalog, error)) (Catalog, error) {
	cat, err := fetch(ctx)
	if err != nil {
		return Catalog{}, fmt.Errorf("%w: %s: %w", ErrUnavailable, t, err)
	}
	if len(cat.Versions) == 0 {
		return Catalog{}, fmt.Errorf("%w: %s: no usable versions", ErrUnavailable, t)
	}
	if cat.Loaders == nil {
		cat.Loaders = []string{}
	}
	return cat, nil
}

// getJSON fetches url (one of the fixed upstreams) and decodes at most MaxBytes of JSON.
func (c *Client) getJSON(ctx context.Context, url string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBytes+1))
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	if len(body) > MaxBytes {
		return fmt.Errorf("GET %s: response larger than %d bytes", url, MaxBytes)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	return nil
}
