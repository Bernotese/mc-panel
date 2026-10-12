package upstream_test

import (
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

// Hosts with non-ASCII letters that case folding turns into ASCII: "\u0130" (dotted capital I)
// lowercases to "i", so cdn.modr\u0130nth.com passed strings.ToLower while net/http contacts
// its punycode form cdn.xn--modrinth-s0e.com. The Kelvin sign (U+212A) folds to "k" under
// strings.EqualFold; any non-ASCII host is refused outright.
var foldingHosts = []string{
	"https://cdn.modr%C4%B0nth.com/x.png",
	"https://cdn.modr\u0130nth.com/x.png",
	"https://hangarcdn.p%C4%B0permc.io/x.png",
	"https://\u212Acdn.modrinth.com/x.png",
	"https://cdn.modrinth.com\u212A/x.png",
}

func TestDefaultHostAllowedRejectsUnicodeFolding(t *testing.T) {
	for _, raw := range foldingHosts {
		u, err := url.Parse(raw)
		if err != nil {
			continue // refused by the parser is fine too
		}
		if upstream.DefaultHostAllowed(u) {
			t.Errorf("DefaultHostAllowed(%s) = true (host %q)", raw, u.Hostname())
		}
	}
	u, _ := url.Parse("https://CDN.MODRINTH.COM/x.png")
	if !upstream.DefaultHostAllowed(u) {
		t.Error("ASCII upper case must still be accepted")
	}
}

func TestIconAndDownloadRejectUnicodeFolding(t *testing.T) {
	c := upstream.New(upstream.Options{Clock: clock.NewFake(t0)})
	for _, raw := range foldingHosts {
		if c.IconURLAllowed(raw) {
			t.Errorf("IconURLAllowed(%s) = true", raw)
		}
		if _, err := c.Icon(bg, raw); !errors.Is(err, upstream.ErrHostNotAllowed) {
			t.Errorf("Icon(%s): %v", raw, err)
		}
		if _, err := c.Download(bg, upstream.File{URL: raw, Size: 1}); !errors.Is(err, upstream.ErrHostNotAllowed) {
			t.Errorf("Download(%s): %v", raw, err)
		}
	}
}

// A redirect of an allowed CDN onto a folding host is refused before it is followed.
func TestDownloadRedirectRejectsUnicodeFolding(t *testing.T) {
	cdn, _ := cdnServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://cdn.modr%C4%B0nth.com/a.jar", http.StatusFound)
	})
	host := cdn.Listener.Addr().String()
	c := newClient(t, nil, cdn, clock.NewFake(t0), func(o *upstream.Options) {
		o.AllowHost = func(u *url.URL) bool {
			return (u.Scheme == "https" && u.Host == host && u.User == nil) || upstream.DefaultHostAllowed(u)
		}
	})
	if _, err := readAll(t, c, upstream.File{URL: cdn.URL + "/a.jar", Size: 4}); !errors.Is(err, upstream.ErrHostNotAllowed) {
		t.Fatalf("redirect: %v", err)
	}
}
