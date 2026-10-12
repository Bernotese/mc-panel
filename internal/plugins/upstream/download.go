package upstream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// idle cancels a request when it made no progress for d (headers or one read).
type idle struct {
	d      time.Duration
	t      *time.Timer
	cancel context.CancelFunc
	mu     sync.Mutex
	fired  bool
}

func newIdle(d time.Duration, cancel context.CancelFunc) *idle {
	i := &idle{d: d, cancel: cancel}
	i.t = time.AfterFunc(d, func() {
		i.mu.Lock()
		i.fired = true
		i.mu.Unlock()
		cancel()
	})
	return i
}

func (i *idle) reset() { i.t.Reset(i.d) }

func (i *idle) stop() {
	i.t.Stop()
	i.cancel()
}

func (i *idle) timedOut() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.fired
}

// open sends a GET to an allowed host (redirects are checked by allowedRedirect). The headers
// must arrive within the read timeout. On success the caller must close the body and stop the
// timer.
func (c *Client) open(ctx context.Context, raw, accept string) (*http.Response, *idle, error) {
	u, err := url.Parse(raw)
	if err != nil || !c.HostAllowed(u) {
		return nil, nil, fmt.Errorf("%w: %w: %q", ErrDownload, ErrHostNotAllowed, raw)
	}
	ctx, cancel := context.WithCancel(ctx)
	t := newIdle(c.readTimeout, cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		t.stop()
		return nil, nil, fmt.Errorf("%w: build request: %w", ErrDownload, err)
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", accept)
	resp, err := c.dl.Do(req)
	if err != nil {
		t.stop()
		switch {
		case errors.Is(err, ErrDownload):
			return nil, nil, err // refused redirect
		case t.timedOut():
			return nil, nil, fmt.Errorf("%w: no answer from %s within %s", ErrUnavailable, u.Host, c.readTimeout)
		}
		return nil, nil, fmt.Errorf("%w: GET %s: %w", ErrUnavailable, u.Redacted(), err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		t.stop()
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return nil, nil, fmt.Errorf("%w: GET %s: HTTP %d", ErrUnavailable, u.Redacted(), resp.StatusCode)
		}
		return nil, nil, fmt.Errorf("%w: GET %s: HTTP %d", ErrDownload, u.Redacted(), resp.StatusCode)
	}
	return resp, t, nil
}

// Download opens the jar f for streaming (spec §4): only from allowed hosts, the size the API
// announced must be 1..MaxJarBytes and equal the CDN's Content-Length, the body fails with
// ErrDownload if the CDN sends more or fewer bytes or stalls longer than the read timeout.
func (c *Client) Download(ctx context.Context, f File) (io.ReadCloser, error) {
	if f.Size <= 0 || f.Size > MaxJarBytes {
		return nil, fmt.Errorf("%w: announced size %d outside 1..%d bytes", ErrDownload, f.Size, MaxJarBytes)
	}
	resp, t, err := c.open(ctx, f.URL, "application/java-archive, application/octet-stream, */*")
	if err != nil {
		return nil, err
	}
	if resp.ContentLength >= 0 && resp.ContentLength != f.Size {
		_ = resp.Body.Close()
		t.stop()
		return nil, fmt.Errorf("%w: the source sends %d bytes, the API announced %d", ErrDownload, resp.ContentLength, f.Size)
	}
	return &sizedBody{rc: resp.Body, idle: t, want: f.Size}, nil
}

// sizedBody passes exactly want bytes through and fails on any deviation.
type sizedBody struct {
	rc   io.ReadCloser
	idle *idle
	want int64
	n    int64
	err  error
}

func (b *sizedBody) Read(p []byte) (int, error) {
	if b.err != nil {
		return 0, b.err
	}
	b.idle.reset()
	n, err := b.rc.Read(p)
	b.n += int64(n)
	switch {
	case b.n > b.want:
		n -= int(b.n - b.want)
		b.n = b.want
		b.err = fmt.Errorf("%w: the source sent more than the announced %d bytes", ErrDownload, b.want)
		return n, b.err
	case errors.Is(err, io.EOF) && b.n < b.want:
		b.err = fmt.Errorf("%w: the source sent %d of %d bytes", ErrDownload, b.n, b.want)
		return n, b.err
	case err != nil && !errors.Is(err, io.EOF):
		if b.idle.timedOut() {
			b.err = fmt.Errorf("%w: no data for %s", ErrDownload, b.idle.d)
		} else {
			b.err = fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		return n, b.err
	}
	return n, err
}

func (b *sizedBody) Close() error {
	b.idle.stop()
	return b.rc.Close()
}
