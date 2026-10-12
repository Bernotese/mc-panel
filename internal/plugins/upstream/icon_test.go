package upstream_test

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

var pngMagic = []byte("\x89PNG\r\n\x1a\n")

func TestDetectImage(t *testing.T) {
	for in, want := range map[string]string{
		string(pngMagic) + "rest":                   "image/png",
		"\xff\xd8\xff\xe0rest":                      "image/jpeg",
		"GIF89a....":                                "image/gif",
		"GIF87a....":                                "image/gif",
		"RIFF\x00\x00\x00\x00WEBPVP8":               "image/webp",
		"RIFF\x00\x00\x00\x00WAVE":                  "",
		`<svg xmlns="http://www.w3.org/2000/svg"/>`: "",
		"": "",
	} {
		if got := upstream.DetectImage([]byte(in)); got != want {
			t.Errorf("DetectImage(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIcon(t *testing.T) {
	cdn, hits := cdnServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/i.png":
			_, _ = w.Write(append(bytes.Clone(pngMagic), "data"...))
		case "/i.svg":
			w.Header().Set("Content-Type", "image/png") // lies: the magic bytes decide
			_, _ = w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
		case "/big.png":
			_, _ = w.Write(append(bytes.Clone(pngMagic), make([]byte, upstream.MaxIconBytes)...))
		}
	})
	c := newClient(t, nil, cdn, clock.NewFake(t0))
	ic, err := c.Icon(bg, cdn.URL+"/i.png")
	if err != nil || ic.ContentType != "image/png" || !bytes.HasSuffix(ic.Data, []byte("data")) {
		t.Fatalf("png = %+v, %v", ic, err)
	}
	if _, err := c.Icon(bg, cdn.URL+"/i.svg"); !errors.Is(err, upstream.ErrNotImage) {
		t.Errorf("svg: %v", err)
	}
	if _, err := c.Icon(bg, cdn.URL+"/big.png"); !errors.Is(err, upstream.ErrDownload) {
		t.Errorf("too large: %v", err)
	}
	n := hits.Load()
	if _, err := c.Icon(bg, "https://evil.example/i.png"); !errors.Is(err, upstream.ErrHostNotAllowed) {
		t.Errorf("foreign host: %v", err)
	}
	if hits.Load() != n {
		t.Fatal("a foreign icon URL reached a server")
	}
}

func TestIconURLAllowed(t *testing.T) {
	c := upstream.New(upstream.Options{Clock: clock.NewFake(t0)})
	long := "https://cdn.modrinth.com/" + strings.Repeat("a", upstream.MaxIconURLLen)
	for raw, want := range map[string]bool{
		"https://cdn.modrinth.com/data/P/icon.png":                 true,
		"https://hangarcdn.papermc.io/avatars/project/12.webp?v=1": true,
		"https://hangar.papermc.io/avatars/project/12.webp":        false,
		"http://cdn.modrinth.com/data/P/icon.png":                  false,
		"":   false,
		long: false,
	} {
		if got := c.IconURLAllowed(raw); got != want {
			t.Errorf("IconURLAllowed(%.60s) = %v, want %v", raw, got, want)
		}
	}
}
