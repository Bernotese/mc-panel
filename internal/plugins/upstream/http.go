// Package upstream talks to Modrinth and Hangar (sub-project 3c, spec §4): search, projects,
// versions, hash lookups, jar downloads and icons. Only the fixed API bases are queried; jars
// and icons come only from AllowedHosts over https.
package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/servers/catalog"
)

const (
	// APITimeout bounds every API request (spec §4).
	APITimeout = 10 * time.Second
	// ReadTimeout bounds every single read of a jar or icon body; downloads have no overall
	// deadline.
	ReadTimeout = 60 * time.Second
	// MaxJSONBytes limits every API answer.
	MaxJSONBytes = 5 << 20
	// MaxJarBytes limits a jar (the size the API announces).
	MaxJarBytes = 256 << 20
	// MaxIconBytes limits an icon.
	MaxIconBytes = 256 << 10
	// MaxIconURLLen limits the icon proxy's url parameter.
	MaxIconURLLen = 512
	// MaxRedirects is the most redirects any request follows.
	MaxRedirects = 3
	// PageSize is the number of search hits per page.
	PageSize = 20
	// MaxVersions is the length of a version list.
	MaxVersions = 25
	// MaxHashBatch is the most hashes per Modrinth version_files request.
	MaxHashBatch = 500
	// MaxCacheEntries bounds every cache (oldest first out).
	MaxCacheEntries = 1000

	searchTTL  = 5 * time.Minute
	projectTTL = 15 * time.Minute
	hashTTL    = time.Hour
)

// Source is a plugin source.
type Source string

const (
	Modrinth Source = "modrinth"
	Hangar   Source = "hangar"
)

func (s Source) Valid() bool { return s == Modrinth || s == Hangar }

// Channel is the stability of a version.
type Channel string

const (
	Release Channel = "release"
	Beta    Channel = "beta"
	Alpha   Channel = "alpha"
)

// Bases are the API base URLs (scheme and host, no trailing slash).
type Bases struct{ Modrinth, Hangar string }

// DefaultBases are the production APIs, checked against the live services 2026-10-12.
var DefaultBases = Bases{Modrinth: "https://api.modrinth.com", Hangar: "https://hangar.papermc.io"}

// AllowedHosts are the only hosts jars and icons are fetched from (https only).
var AllowedHosts = []string{"cdn.modrinth.com", "hangarcdn.papermc.io"}

type Options struct {
	Clock   clock.Clock
	Version string // panel version for the User-Agent; "" = "dev"
	Bases   Bases  // zero = DefaultBases
	// HTTPClient: only its Transport is used (nil = http.DefaultTransport); tests inject the
	// transport of an httptest TLS server.
	HTTPClient *http.Client
	// AllowHost overrides the download/icon host check in tests (nil = DefaultHostAllowed).
	AllowHost   func(u *url.URL) bool
	APITimeout  time.Duration // 0 = APITimeout
	ReadTimeout time.Duration // 0 = ReadTimeout
}

var (
	// ErrUnavailable: network, status, size, JSON or rate limit (429) of an upstream.
	ErrUnavailable = errors.New("upstream: unavailable")
	// ErrNotFound: the upstream does not know the project or version (404), or the reference
	// is malformed.
	ErrNotFound = errors.New("upstream: not found")
	// ErrDownload: a jar or icon could not be fetched as announced (host, size, status, data).
	ErrDownload = errors.New("upstream: download failed")
	// ErrHostNotAllowed: a jar or icon URL (or a redirect) points outside AllowedHosts.
	ErrHostNotAllowed = errors.New("upstream: host not allowed")
	// ErrNotImage: an icon is not PNG, JPEG, WebP or GIF.
	ErrNotImage = errors.New("upstream: not a supported image")
)

// Client queries Modrinth and Hangar. It is safe for concurrent use.
type Client struct {
	clock       clock.Clock
	ua          string
	bases       Bases
	allowHost   func(*url.URL) bool
	api         *http.Client
	dl          *http.Client
	apiTimeout  time.Duration
	readTimeout time.Duration

	search    *Cache[SearchResult]
	projects  *Cache[Project]
	versions  *Cache[[]Version]
	version   *Cache[Version]
	hashes    *Cache[hashEntry]
	hashCalls *Cache[map[string]Version]
}

// hashEntry caches one hash lookup; known false means Modrinth does not know the file.
type hashEntry struct {
	v     Version
	known bool
}

func New(o Options) *Client {
	clk := o.Clock
	if clk == nil {
		clk = clock.Real{}
	}
	c := &Client{
		clock: clk, ua: catalog.UserAgent(o.Version), bases: o.Bases, allowHost: o.AllowHost,
		apiTimeout: o.APITimeout, readTimeout: o.ReadTimeout,
	}
	if c.bases == (Bases{}) {
		c.bases = DefaultBases
	}
	if c.allowHost == nil {
		c.allowHost = DefaultHostAllowed
	}
	if c.apiTimeout <= 0 {
		c.apiTimeout = APITimeout
	}
	if c.readTimeout <= 0 {
		c.readTimeout = ReadTimeout
	}
	tr := http.DefaultTransport
	if o.HTTPClient != nil && o.HTTPClient.Transport != nil {
		tr = o.HTTPClient.Transport
	}
	c.api = &http.Client{Transport: tr, Timeout: c.apiTimeout, CheckRedirect: sameHostHTTPS}
	c.dl = &http.Client{Transport: tr, CheckRedirect: c.allowedRedirect}
	c.search = NewCache[SearchResult](clk, searchTTL, MaxCacheEntries)
	c.projects = NewCache[Project](clk, projectTTL, MaxCacheEntries)
	c.versions = NewCache[[]Version](clk, projectTTL, MaxCacheEntries)
	c.version = NewCache[Version](clk, projectTTL, MaxCacheEntries)
	c.hashes = NewCache[hashEntry](clk, hashTTL, MaxCacheEntries)
	c.hashCalls = NewCache[map[string]Version](clk, 0, MaxCacheEntries)
	return c
}

// UserAgent is sent on every request (like the version catalogs).
func (c *Client) UserAgent() string { return c.ua }

func (c *Client) Bases() Bases { return c.bases }

// HostAllowed reports whether jars and icons may be fetched from u.
func (c *Client) HostAllowed(u *url.URL) bool { return u != nil && c.allowHost(u) }

// DefaultHostAllowed accepts https URLs on exactly one of AllowedHosts (default port, no user
// info).
func DefaultHostAllowed(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" {
		return false
	}
	if p := u.Port(); p != "" && p != "443" {
		return false
	}
	host, ok := asciiLower(u.Hostname())
	return ok && slices.Contains(AllowedHosts, host)
}

// asciiLower lowercases A-Z only and refuses any byte outside printable ASCII: Unicode case
// mapping would turn e.g. "\u0130" into "i" (strings.ToLower) or the Kelvin sign into "k"
// (strings.EqualFold), while net/http contacts the punycode form of the original host.
func asciiLower(s string) (string, bool) {
	b := []byte(s)
	for i, c := range b {
		switch {
		case c < 0x21 || c > 0x7e:
			return "", false
		case 'A' <= c && c <= 'Z':
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b), true
}

// sameHostHTTPS follows an API redirect only to https on the host of the original request, at
// most MaxRedirects times.
func sameHostHTTPS(req *http.Request, via []*http.Request) error {
	if len(via) > MaxRedirects {
		return fmt.Errorf("stopped after %d redirects", MaxRedirects)
	}
	if req.URL.Scheme != "https" || req.URL.Host != via[0].URL.Host {
		return fmt.Errorf("redirect to %s refused: only https on the same host", req.URL.Redacted())
	}
	return nil
}

// allowedRedirect follows a download redirect only to an allowed host, at most MaxRedirects
// times.
func (c *Client) allowedRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > MaxRedirects {
		return fmt.Errorf("%w: more than %d redirects", ErrDownload, MaxRedirects)
	}
	if !c.allowHost(req.URL) {
		return fmt.Errorf("%w: %w: redirect to %s", ErrDownload, ErrHostNotAllowed, req.URL.Redacted())
	}
	return nil
}

// doJSON sends a request to one of the fixed APIs and decodes at most MaxJSONBytes of JSON
// into out; in (if not nil) is sent as a JSON body.
func (c *Client) doJSON(ctx context.Context, method, rawURL string, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, c.apiTimeout)
	defer cancel()
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("%w: encode request: %w", ErrUnavailable, err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return fmt.Errorf("%w: build request: %w", ErrUnavailable, err)
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.api.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s %s: %w", ErrUnavailable, method, rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w: %s %s", ErrNotFound, method, rawURL)
	case resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("%w: %s %s: rate limited by the source (HTTP 429)", ErrUnavailable, method, rawURL)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("%w: %s %s: HTTP %d", ErrUnavailable, method, rawURL, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxJSONBytes+1))
	if err != nil {
		return fmt.Errorf("%w: %s %s: %w", ErrUnavailable, method, rawURL, err)
	}
	if len(b) > MaxJSONBytes {
		return fmt.Errorf("%w: %s %s: answer larger than %d bytes", ErrUnavailable, method, rawURL, MaxJSONBytes)
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("%w: %s %s: %w", ErrUnavailable, method, rawURL, err)
	}
	return nil
}
