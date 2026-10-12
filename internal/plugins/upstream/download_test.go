package upstream_test

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

// cdnServer is a TLS test server that counts its requests.
func cdnServer(t *testing.T, h http.HandlerFunc) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func readAll(t *testing.T, c *upstream.Client, f upstream.File) ([]byte, error) {
	t.Helper()
	body, err := c.Download(bg, f)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	return io.ReadAll(body)
}

func TestDownloadStreams(t *testing.T) {
	cdn, _ := cdnServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != testUA || r.URL.Path != "/data/a.jar" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte("jar-bytes"))
	})
	c := newClient(t, nil, cdn, clock.NewFake(t0))
	got, err := readAll(t, c, upstream.File{URL: cdn.URL + "/data/a.jar", Size: 9})
	if err != nil || string(got) != "jar-bytes" {
		t.Fatalf("download = %q, %v", got, err)
	}
}

// Review Focus #1: jar URLs on foreign hosts or plain http are refused before any request.
func TestDownloadRejectsForeignHost(t *testing.T) {
	cdn, hits := cdnServer(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("x")) })
	c := newClient(t, nil, cdn, clock.NewFake(t0))
	for _, raw := range []string{
		"https://evil.example/a.jar",
		"http://" + cdn.Listener.Addr().String() + "/a.jar",
		"https://user:pw@" + cdn.Listener.Addr().String() + "/a.jar",
		"::not a url",
	} {
		_, err := readAll(t, c, upstream.File{URL: raw, Size: 1})
		if !errors.Is(err, upstream.ErrDownload) || !errors.Is(err, upstream.ErrHostNotAllowed) {
			t.Errorf("%s: %v", raw, err)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("refused URLs reached the CDN %d times", hits.Load())
	}
}

// Review Focus #1: redirects only to allowed hosts over https, at most 3.
func TestDownloadRedirects(t *testing.T) {
	other, otherHits := cdnServer(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("evil")) })
	cdn, _ := cdnServer(t, func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		switch {
		case p == "a.jar":
			_, _ = w.Write([]byte("good"))
		case p == "same":
			http.Redirect(w, r, "/a.jar", http.StatusFound)
		case p == "foreign":
			http.Redirect(w, r, other.URL+"/a.jar", http.StatusFound)
		case p == "plain":
			http.Redirect(w, r, "http://"+r.Host+"/a.jar", http.StatusFound) //nolint:gosec // test server: the downgrade to http is the case under test
		case strings.HasPrefix(p, "hop"):
			n, _ := strconv.Atoi(strings.TrimPrefix(p, "hop"))
			if n >= 3 {
				http.Redirect(w, r, "/a.jar", http.StatusFound)
				return
			}
			http.Redirect(w, r, fmt.Sprintf("/hop%d", n+1), http.StatusFound)
		}
	})
	c := newClient(t, nil, cdn, clock.NewFake(t0))
	for _, p := range []string{"same", "hop1"} {
		if got, err := readAll(t, c, upstream.File{URL: cdn.URL + "/" + p, Size: 4}); err != nil || string(got) != "good" {
			t.Errorf("%s: %q, %v", p, got, err)
		}
	}
	for _, p := range []string{"foreign", "plain"} {
		if _, err := readAll(t, c, upstream.File{URL: cdn.URL + "/" + p, Size: 4}); !errors.Is(err, upstream.ErrHostNotAllowed) {
			t.Errorf("%s: %v", p, err)
		}
	}
	if _, err := readAll(t, c, upstream.File{URL: cdn.URL + "/hop0", Size: 4}); !errors.Is(err, upstream.ErrDownload) {
		t.Errorf("4 redirects: %v", err)
	}
	if otherHits.Load() != 0 {
		t.Fatal("the foreign host was contacted")
	}
}

// Review Focus #1: the API size bounds the jar, the CDN must send exactly that many bytes.
func TestDownloadSizeChecks(t *testing.T) {
	cdn, hits := cdnServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/length":
			w.Header().Set("Content-Length", "5")
			_, _ = w.Write([]byte("12345"))
		case "/more": // chunked: no Content-Length
			_, _ = w.Write([]byte("1234"))
			w.(http.Flusher).Flush()
			_, _ = w.Write([]byte("5678"))
		case "/fewer":
			_, _ = w.Write([]byte("12"))
			w.(http.Flusher).Flush()
		}
	})
	c := newClient(t, nil, cdn, clock.NewFake(t0))
	for _, size := range []int64{0, -1, upstream.MaxJarBytes + 1} {
		if _, err := c.Download(bg, upstream.File{URL: cdn.URL + "/length", Size: size}); !errors.Is(err, upstream.ErrDownload) {
			t.Errorf("size %d: %v", size, err)
		}
	}
	if hits.Load() != 0 {
		t.Fatal("an out-of-range size reached the CDN")
	}
	if _, err := c.Download(bg, upstream.File{URL: cdn.URL + "/length", Size: 4}); !errors.Is(err, upstream.ErrDownload) {
		t.Errorf("Content-Length 5 for 4 announced bytes: %v", err)
	}
	got, err := readAll(t, c, upstream.File{URL: cdn.URL + "/more", Size: 4})
	if !errors.Is(err, upstream.ErrDownload) || len(got) > 4 {
		t.Errorf("more bytes: %q, %v", got, err)
	}
	if _, err := readAll(t, c, upstream.File{URL: cdn.URL + "/fewer", Size: 4}); !errors.Is(err, upstream.ErrDownload) {
		t.Errorf("fewer bytes: %v", err)
	}
}

func TestDownloadStatus(t *testing.T) {
	cdn, _ := cdnServer(t, func(w http.ResponseWriter, r *http.Request) {
		code, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
		w.WriteHeader(code)
	})
	c := newClient(t, nil, cdn, clock.NewFake(t0))
	for code, want := range map[int]error{404: upstream.ErrDownload, 403: upstream.ErrDownload, 429: upstream.ErrUnavailable, 503: upstream.ErrUnavailable} {
		if _, err := c.Download(bg, upstream.File{URL: fmt.Sprintf("%s/%d", cdn.URL, code), Size: 1}); !errors.Is(err, want) {
			t.Errorf("HTTP %d: %v, want %v", code, err, want)
		}
	}
}

// A CDN that stops sending ends the download after the read timeout, not after minutes.
func TestDownloadReadTimeout(t *testing.T) {
	cdn, _ := cdnServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stall" {
			_, _ = w.Write([]byte("12"))
			w.(http.Flusher).Flush()
		}
		<-r.Context().Done() // "/silent": not even headers
	})
	c := newClient(t, nil, cdn, clock.NewFake(t0), func(o *upstream.Options) { o.ReadTimeout = 100 * time.Millisecond })
	start := time.Now()
	if _, err := readAll(t, c, upstream.File{URL: cdn.URL + "/stall", Size: 10}); !errors.Is(err, upstream.ErrDownload) {
		t.Errorf("stall: %v", err)
	}
	if _, err := c.Download(bg, upstream.File{URL: cdn.URL + "/silent", Size: 10}); !errors.Is(err, upstream.ErrUnavailable) {
		t.Errorf("silent: %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("timeouts took %s", d)
	}
}
