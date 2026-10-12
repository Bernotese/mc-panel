// Package upstream talks to the plugin sources Modrinth and Hangar (sub-project 3c §4): fixed
// API base URLs, a contact User-Agent, and jars and icons only from AllowedHosts over https.
//
// Phase A of the plan only declares the contract (V4); phase B2 adds the clients, the download,
// the icon fetch and the caches.
package upstream

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/servers/catalog"
)

// Bases are the API base URLs (scheme and host, no trailing slash).
type Bases struct{ Modrinth, Hangar string }

// DefaultBases are the production APIs, checked against the live services 2026-10-12.
var DefaultBases = Bases{Modrinth: "https://api.modrinth.com", Hangar: "https://hangar.papermc.io"}

// AllowedHosts are the only hosts jars and icons are fetched from (https only).
var AllowedHosts = []string{"cdn.modrinth.com", "hangarcdn.papermc.io"}

// Options configure a Client.
type Options struct {
	Clock      clock.Clock
	Version    string       // panel version for the User-Agent; "" = "dev"
	Bases      Bases        // zero = DefaultBases
	HTTPClient *http.Client // nil = default; tests inject httptest TLS clients
	// AllowHost overrides the download/icon host check in tests (nil = DefaultHostAllowed).
	AllowHost func(u *url.URL) bool
}

// ErrUnavailable: network, status, size, JSON or a 429 of the upstream.
var ErrUnavailable = errors.New("upstream: unavailable")

// Client talks to Modrinth and Hangar. It is safe for concurrent use.
type Client struct {
	ua        string
	bases     Bases
	allowHost func(u *url.URL) bool
}

// New returns a client with the defaults of Options filled in.
func New(o Options) *Client {
	c := &Client{ua: catalog.UserAgent(o.Version), bases: o.Bases, allowHost: o.AllowHost}
	if c.bases == (Bases{}) {
		c.bases = DefaultBases
	}
	if c.allowHost == nil {
		c.allowHost = DefaultHostAllowed
	}
	return c
}

// UserAgent is sent on every upstream request (same contact format as the version catalogs).
func (c *Client) UserAgent() string { return c.ua }

// Bases returns the API base URLs in use.
func (c *Client) Bases() Bases { return c.bases }

// HostAllowed reports whether a jar or icon (or a redirect on the way) may be fetched from u.
func (c *Client) HostAllowed(u *url.URL) bool { return u != nil && c.allowHost(u) }

// DefaultHostAllowed accepts only https URLs without user info on one of AllowedHosts (host
// compared case-insensitively, port empty or 443).
func DefaultHostAllowed(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" {
		return false
	}
	if p := u.Port(); p != "" && p != "443" {
		return false
	}
	return slices.Contains(AllowedHosts, strings.ToLower(u.Hostname()))
}
