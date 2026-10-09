package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"time"

	"mcpanel/internal/agent/agentapi"
	"mcpanel/internal/agent/banrules"
)

const (
	// DefaultTimeout bounds every client call, including connect.
	DefaultTimeout   = 3 * time.Second
	maxResponseBytes = 4 << 20
	baseURL          = "http://agent"
)

var (
	// ErrInvalid: the request can never succeed; do not retry.
	ErrInvalid = banrules.ErrInvalid
	// ErrConflict: the prefix overlaps a different, already banned prefix.
	ErrConflict = banrules.ErrConflict
)

// StatusError is a non-2xx answer from the agent.
type StatusError struct {
	StatusCode int
	Message    string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("agent: HTTP %d: %s", e.StatusCode, e.Message)
}

// Is maps status codes to the sentinel errors: 409 -> ErrConflict, other 4xx
// -> ErrInvalid, 5xx -> agentapi.ErrUnavailable.
func (e *StatusError) Is(target error) bool {
	switch {
	case e.StatusCode == http.StatusConflict:
		return target == banrules.ErrConflict
	case e.StatusCode >= 500:
		return target == agentapi.ErrUnavailable
	case e.StatusCode >= 400:
		return target == banrules.ErrInvalid
	}
	return false
}

// Client talks to the agent over its Unix socket. It implements
// agentapi.Client and is safe for concurrent use.
type Client struct {
	hc      *http.Client
	timeout time.Duration
}

var _ agentapi.Client = (*Client)(nil)

func NewClient(socketPath string) *Client { return NewClientWithTimeout(socketPath, DefaultTimeout) }

func NewClientWithTimeout(socketPath string, timeout time.Duration) *Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
		MaxIdleConns:        4,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     30 * time.Second,
		DisableCompression:  true,
		// Proxy stays nil: HTTP(S)_PROXY must never redirect agent traffic.
	}
	return &Client{
		hc: &http.Client{
			Transport:     tr,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		timeout: timeout,
	}
}

func (c *Client) Health(ctx context.Context) (agentapi.HealthResponse, error) {
	var out agentapi.HealthResponse
	err := c.do(ctx, http.MethodGet, agentapi.PathHealth, nil, &out)
	return out, err
}

func (c *Client) Ban(ctx context.Context, prefix netip.Prefix, d time.Duration) error {
	if err := banrules.CheckPrefix(prefix); err != nil {
		return err
	}
	secs, err := banrules.SecondsFromDuration(d)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, agentapi.PathBan, agentapi.BanRequest{Prefix: prefix.String(), TimeoutSeconds: secs}, nil)
}

func (c *Client) Unban(ctx context.Context, prefix netip.Prefix) error {
	if err := banrules.CheckPrefix(prefix); err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, agentapi.PathUnban, agentapi.UnbanRequest{Prefix: prefix.String()}, nil)
}

func (c *Client) Bans(ctx context.Context) ([]agentapi.Ban, error) {
	var out agentapi.BansResponse
	if err := c.do(ctx, http.MethodGet, agentapi.PathBans, nil, &out); err != nil {
		return nil, err
	}
	return out.Bans, nil
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("agent: encode request: %w", err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, body)
	if err != nil {
		return fmt.Errorf("agent: build request: %w", err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", agentapi.ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("%w: read response: %w", agentapi.ErrUnavailable, err)
	}
	if len(data) > maxResponseBytes {
		return fmt.Errorf("%w: response exceeds %d bytes", agentapi.ErrUnavailable, maxResponseBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := http.StatusText(resp.StatusCode)
		var er agentapi.ErrorResponse
		if json.Unmarshal(data, &er) == nil && er.Error != "" {
			msg = er.Error
		}
		return &StatusError{StatusCode: resp.StatusCode, Message: msg}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%w: decode response: %w", agentapi.ErrUnavailable, err)
	}
	return nil
}
