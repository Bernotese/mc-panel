package upstream

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
)

// IconURLAllowed reports whether raw may be fetched by the icon proxy (spec §6.8).
func (c *Client) IconURLAllowed(raw string) bool {
	if raw == "" || len(raw) > MaxIconURLLen {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && c.HostAllowed(u)
}

// Icon fetches an icon of at most MaxIconBytes; only PNG, JPEG, WebP and GIF pass (by magic
// bytes, never by the Content-Type the CDN claims).
func (c *Client) Icon(ctx context.Context, raw string) (Icon, error) {
	if !c.IconURLAllowed(raw) {
		return Icon{}, fmt.Errorf("%w: %w: icon URL", ErrDownload, ErrHostNotAllowed)
	}
	resp, t, err := c.open(ctx, raw, "image/png, image/jpeg, image/webp, image/gif")
	if err != nil {
		return Icon{}, err
	}
	defer t.stop()
	defer func() { _ = resp.Body.Close() }()
	if resp.ContentLength > MaxIconBytes {
		return Icon{}, fmt.Errorf("%w: icon larger than %d bytes", ErrDownload, MaxIconBytes)
	}
	b, err := io.ReadAll(io.LimitReader(&idleReader{r: resp.Body, idle: t}, MaxIconBytes+1))
	if err != nil {
		return Icon{}, fmt.Errorf("%w: %w", ErrDownload, err)
	}
	if len(b) > MaxIconBytes {
		return Icon{}, fmt.Errorf("%w: icon larger than %d bytes", ErrDownload, MaxIconBytes)
	}
	ct := DetectImage(b)
	if ct == "" {
		return Icon{}, fmt.Errorf("%w: unknown format", ErrNotImage)
	}
	return Icon{Data: b, ContentType: ct}, nil
}

type idleReader struct {
	r    io.Reader
	idle *idle
}

func (r *idleReader) Read(p []byte) (int, error) {
	r.idle.reset()
	return r.r.Read(p)
}

// DetectImage returns the image type of b by its magic bytes, "" for anything else (SVG
// included).
func DetectImage(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF}):
		return "image/jpeg"
	case bytes.HasPrefix(b, []byte("GIF87a")), bytes.HasPrefix(b, []byte("GIF89a")):
		return "image/gif"
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "image/webp"
	}
	return ""
}
