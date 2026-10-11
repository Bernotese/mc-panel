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

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/banrules"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
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
	Code       string // agentapi.ErrorCode*, may be empty
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("agent: HTTP %d: %s", e.StatusCode, e.Message)
}

// Is maps status codes to the sentinel errors: 409 -> ErrConflict, 429 -> agentapi.ErrBusy,
// other 4xx -> ErrInvalid, everything else non-2xx -> agentapi.ErrUnavailable; a 503 with
// code docker_unavailable additionally matches agentapi.ErrDockerUnavailable. The console
// codes not_found (404), not_running (409) and rcon_unavailable (503) additionally match
// agentapi.ErrNotFound, ErrNotRunning and ErrRconUnavailable.
func (e *StatusError) Is(target error) bool {
	// Console codes first: they are more specific than the status-based rules below.
	switch target {
	case agentapi.ErrNotFound:
		return e.StatusCode == http.StatusNotFound && e.Code == agentapi.ErrorCodeNotFound
	case agentapi.ErrNotRunning:
		return e.StatusCode == http.StatusConflict && e.Code == agentapi.ErrorCodeNotRunning
	case agentapi.ErrRconUnavailable:
		return e.StatusCode == http.StatusServiceUnavailable && e.Code == agentapi.ErrorCodeRconUnavailable
	}
	switch {
	case target == agentapi.ErrDockerUnavailable:
		return e.StatusCode == http.StatusServiceUnavailable && e.Code == agentapi.ErrorCodeDockerUnavailable
	case e.StatusCode == http.StatusConflict:
		return target == banrules.ErrConflict
	case e.StatusCode == http.StatusTooManyRequests:
		return target == agentapi.ErrBusy
	case e.StatusCode >= 400 && e.StatusCode < 500:
		// Firewall callers check banrules.ErrInvalid, instance callers instancespec.ErrInvalid.
		return target == banrules.ErrInvalid || target == instancespec.ErrInvalid
	default:
		// 1xx, 3xx, 5xx and anything else the agent never sends: treat as
		// a transient/unknown condition, never as a permanent rejection.
		return target == agentapi.ErrUnavailable
	}
}

// Client talks to the agent over its Unix socket. It implements
// agentapi.Client and is safe for concurrent use.
type Client struct {
	hc                *http.Client
	timeout           time.Duration
	logsHeaderTimeout time.Duration
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
		timeout:           timeout,
		logsHeaderTimeout: LogsHeaderTimeout,
	}
}

func (c *Client) Health(ctx context.Context) (agentapi.HealthResponse, error) {
	var out agentapi.HealthResponse
	if err := c.do(ctx, http.MethodGet, agentapi.PathHealth, nil, &out); err != nil {
		return agentapi.HealthResponse{}, err
	}
	if out.Status != "ok" {
		return agentapi.HealthResponse{}, fmt.Errorf("%w: unexpected health status %q", agentapi.ErrUnavailable, out.Status)
	}
	return out, nil
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

var _ agentapi.InstancesClient = (*Client)(nil)

func (c *Client) Instances(ctx context.Context) (agentapi.InstancesResponse, error) {
	var out agentapi.InstancesResponse
	if err := c.do(ctx, http.MethodGet, agentapi.PathInstances, nil, &out); err != nil {
		return agentapi.InstancesResponse{}, err
	}
	return out, nil
}

func (c *Client) Ensure(ctx context.Context, req agentapi.EnsureRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, agentapi.PathInstanceEnsure, req, nil)
}

func (c *Client) Power(ctx context.Context, id int64, action agentapi.PowerAction) error {
	if id <= 0 || !agentapi.ValidPowerAction(action) {
		return fmt.Errorf("%w: %w: power: id must be > 0 and action start, stop or restart", ErrInvalid, instancespec.ErrInvalid)
	}
	return c.do(ctx, http.MethodPost, agentapi.PathInstancePower, agentapi.PowerRequest{ID: id, Action: action}, nil)
}

func (c *Client) Remove(ctx context.Context, id int64) error {
	if id <= 0 {
		return fmt.Errorf("%w: %w: remove: id must be > 0", ErrInvalid, instancespec.ErrInvalid)
	}
	return c.do(ctx, http.MethodPost, agentapi.PathInstanceRemove, agentapi.RemoveRequest{ID: id}, nil)
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	return c.doTimeout(ctx, c.timeout, method, path, in, out)
}

func (c *Client) doTimeout(ctx context.Context, timeout time.Duration, method, path string, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := newRequest(ctx, method, path, in)
	if err != nil {
		return err
	}
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
		return statusError(resp.StatusCode, data)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%w: decode response: %w", agentapi.ErrUnavailable, err)
	}
	return nil
}

// newRequest builds a JSON request to the agent (body only if in != nil).
func newRequest(ctx context.Context, method, path string, in any) (*http.Request, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, fmt.Errorf("agent: encode request: %w", err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("agent: build request: %w", err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}

// statusError turns a non-2xx answer into a *StatusError (message and code from the body).
func statusError(status int, data []byte) *StatusError {
	msg := http.StatusText(status)
	var er agentapi.ErrorResponse
	if json.Unmarshal(data, &er) == nil && er.Error != "" {
		msg = er.Error
	}
	return &StatusError{StatusCode: status, Message: msg, Code: er.Code}
}
