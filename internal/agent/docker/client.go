// Package docker is a narrow client for the Docker Engine API over its Unix socket.
// It supports exactly the calls the agent needs and pins API version 1.44, the oldest
// version Docker 29 still accepts. The panel never imports this package.
package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	// APIVersion is the path prefix of every request.
	APIVersion = "/v1.44"
	// DefaultTimeout bounds every call except PullImage, Stop and Restart.
	DefaultTimeout = 30 * time.Second
	// DefaultPullTimeout bounds an image pull (itzg images are several hundred MB).
	DefaultPullTimeout = 30 * time.Minute

	baseURL          = "http://docker"
	maxResponseBytes = 8 << 20
	maxErrorBytes    = 64 << 10
	maxPullBytes     = 64 << 20
	// stopGrace is added to the stop timeout: Docker answers only after the
	// container is down, which takes up to the stop timeout plus SIGKILL handling.
	stopGrace = 30 * time.Second
)

// ErrUnavailable wraps every failure to reach the daemon (socket missing, refused,
// timeout, broken response). Callers report Docker as unavailable.
var ErrUnavailable = errors.New("docker unavailable")

// Error is a non-2xx answer from the daemon.
type Error struct {
	StatusCode int
	Method     string
	Path       string
	Message    string
}

func (e *Error) Error() string {
	return fmt.Sprintf("docker: %s %s: HTTP %d: %s", e.Method, e.Path, e.StatusCode, e.Message)
}

func statusIs(err error, code int) bool {
	var de *Error
	return errors.As(err, &de) && de.StatusCode == code
}

// IsNotFound reports a 404 (no such container, image or network).
func IsNotFound(err error) bool { return statusIs(err, http.StatusNotFound) }

// IsConflict reports a 409 (name in use, container not running, …).
func IsConflict(err error) bool { return statusIs(err, http.StatusConflict) }

// Client is safe for concurrent use.
type Client struct {
	hc          *http.Client
	timeout     time.Duration
	pullTimeout time.Duration
}

func New(socketPath string) *Client {
	return NewWithTimeouts(socketPath, DefaultTimeout, DefaultPullTimeout)
}

func NewWithTimeouts(socketPath string, timeout, pullTimeout time.Duration) *Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
		MaxIdleConns:        4,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     30 * time.Second,
		DisableCompression:  true,
		// Proxy stays nil: HTTP(S)_PROXY must never see Docker API traffic.
	}
	return &Client{
		hc: &http.Client{
			Transport:     tr,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		timeout:     timeout,
		pullTimeout: pullTimeout,
	}
}

// Ping checks that the daemon answers.
func (c *Client) Ping(ctx context.Context) error {
	return c.call(ctx, c.timeout, http.MethodGet, "/_ping", nil, nil, nil)
}

// ImageExists reports whether ref is present locally.
func (c *Client) ImageExists(ctx context.Context, ref string) (bool, error) {
	r, err := ParseImageRef(ref)
	if err != nil {
		return false, err
	}
	err = c.call(ctx, c.timeout, http.MethodGet, "/images/"+r.Pinned()+"/json", nil, nil, nil)
	switch {
	case err == nil:
		return true, nil
	case IsNotFound(err):
		return false, nil
	default:
		return false, err
	}
}

type pullMessage struct {
	Status      string `json:"status"`
	Error       string `json:"error"`
	ErrorDetail *struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
}

// PullImage pulls ref by digest. Docker answers 200 and reports failures inside the
// JSON stream, so the whole stream is read and checked for error messages.
func (c *Client) PullImage(ctx context.Context, ref string) error {
	r, err := ParseImageRef(ref)
	if err != nil {
		return err
	}
	q := url.Values{"fromImage": {r.Repo}, "tag": {r.Digest}}
	ctx, cancel := context.WithTimeout(ctx, c.pullTimeout)
	defer cancel()
	resp, err := c.send(ctx, http.MethodPost, "/images/create", q, nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkStatus(resp, http.MethodPost, "/images/create"); err != nil {
		return err
	}
	lr := &io.LimitedReader{R: resp.Body, N: maxPullBytes + 1}
	dec := json.NewDecoder(lr)
	for {
		var m pullMessage
		err := dec.Decode(&m)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if lr.N <= 0 {
				return fmt.Errorf("%w: pull %s: progress output exceeds %d bytes", ErrUnavailable, r.Pinned(), maxPullBytes)
			}
			return fmt.Errorf("%w: pull %s: read progress: %w", ErrUnavailable, r.Pinned(), err)
		}
		if m.ErrorDetail != nil && m.ErrorDetail.Message != "" {
			return fmt.Errorf("docker: pull %s: %s", r.Pinned(), m.ErrorDetail.Message)
		}
		if m.Error != "" {
			return fmt.Errorf("docker: pull %s: %s", r.Pinned(), m.Error)
		}
	}
	return nil
}

type networkCreate struct {
	Name   string            `json:"Name"`
	Driver string            `json:"Driver"`
	Labels map[string]string `json:"Labels,omitempty"`
}

type networkInfo struct {
	Driver string            `json:"Driver"`
	Labels map[string]string `json:"Labels"`
}

// EnsureNetwork creates the bridge network name unless it exists. An existing
// network is only used if it is a bridge carrying all of labels; anything else
// (e.g. a network of another service that happens to share the name) is refused
// and never modified.
func (c *Client) EnsureNetwork(ctx context.Context, name string, labels map[string]string) error {
	path := "/networks/" + url.PathEscape(name)
	var info networkInfo
	err := c.call(ctx, c.timeout, http.MethodGet, path, nil, nil, &info)
	if IsNotFound(err) {
		err = c.call(ctx, c.timeout, http.MethodPost, "/networks/create", nil,
			networkCreate{Name: name, Driver: "bridge", Labels: labels}, nil)
		if !IsConflict(err) {
			return err
		}
		// Created concurrently: check what exists now.
		err = c.call(ctx, c.timeout, http.MethodGet, path, nil, nil, &info)
	}
	if err != nil {
		return err
	}
	if info.Driver != "bridge" {
		return fmt.Errorf("docker: network %s exists with driver %q and is not managed by mc-panel; leaving it untouched", name, info.Driver)
	}
	for k, v := range labels {
		if got, ok := info.Labels[k]; !ok || got != v {
			return fmt.Errorf("docker: network %s exists but is not managed by mc-panel (label %s=%s missing); leaving it untouched", name, k, v)
		}
	}
	return nil
}

// CreateContainer creates (but does not start) a container and returns its ID.
func (c *Client) CreateContainer(ctx context.Context, name string, cfg ContainerConfig) (string, error) {
	var out struct {
		ID string `json:"Id"`
	}
	if err := c.call(ctx, c.timeout, http.MethodPost, "/containers/create", url.Values{"name": {name}}, cfg, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// Start starts a container; an already running container is not an error.
func (c *Client) Start(ctx context.Context, id string) error {
	return notModifiedOK(c.call(ctx, c.timeout, http.MethodPost, containerPath(id, "start"), nil, nil, nil))
}

// Stop stops a container (SIGTERM, SIGKILL after timeoutSeconds); a stopped
// container is not an error.
func (c *Client) Stop(ctx context.Context, id string, timeoutSeconds int) error {
	q := url.Values{"t": {strconv.Itoa(timeoutSeconds)}}
	return notModifiedOK(c.call(ctx, stopTimeout(timeoutSeconds), http.MethodPost, containerPath(id, "stop"), q, nil, nil))
}

// Restart stops (like Stop) and starts a container.
func (c *Client) Restart(ctx context.Context, id string, timeoutSeconds int) error {
	q := url.Values{"t": {strconv.Itoa(timeoutSeconds)}}
	return c.call(ctx, stopTimeout(timeoutSeconds), http.MethodPost, containerPath(id, "restart"), q, nil, nil)
}

// Kill sends signal (e.g. "SIGHUP") to a running container.
func (c *Client) Kill(ctx context.Context, id, signal string) error {
	return c.call(ctx, c.timeout, http.MethodPost, containerPath(id, "kill"), url.Values{"signal": {signal}}, nil, nil)
}

// Remove deletes a container; force also removes a running one.
func (c *Client) Remove(ctx context.Context, id string, force bool) error {
	return c.call(ctx, c.timeout, http.MethodDelete, containerPath(id, ""), url.Values{"force": {strconv.FormatBool(force)}}, nil, nil)
}

// Inspect returns the container's state, labels and image.
func (c *Client) Inspect(ctx context.Context, id string) (ContainerInfo, error) {
	var out ContainerInfo
	err := c.call(ctx, c.timeout, http.MethodGet, containerPath(id, "json"), nil, nil, &out)
	return out, err
}

// List returns all containers (running or not) that carry the label key.
func (c *Client) List(ctx context.Context, label string) ([]ContainerSummary, error) {
	filters, err := json.Marshal(map[string][]string{"label": {label}})
	if err != nil {
		return nil, err
	}
	var out []ContainerSummary
	q := url.Values{"all": {"1"}, "filters": {string(filters)}}
	if err := c.call(ctx, c.timeout, http.MethodGet, "/containers/json", q, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Stats returns one snapshot without waiting for a second sample.
func (c *Client) Stats(ctx context.Context, id string) (Stats, error) {
	var out Stats
	q := url.Values{"stream": {"false"}, "one-shot": {"true"}}
	err := c.call(ctx, c.timeout, http.MethodGet, containerPath(id, "stats"), q, nil, &out)
	return out, err
}

func containerPath(id, action string) string {
	p := "/containers/" + url.PathEscape(id)
	if action != "" {
		p += "/" + action
	}
	return p
}

func stopTimeout(seconds int) time.Duration {
	return time.Duration(max(seconds, 0))*time.Second + stopGrace
}

func notModifiedOK(err error) error {
	if statusIs(err, http.StatusNotModified) {
		return nil
	}
	return err
}

// call sends one request and decodes a JSON answer into out (if non-nil).
func (c *Client) call(ctx context.Context, timeout time.Duration, method, path string, q url.Values, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := c.send(ctx, method, path, q, in)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkStatus(resp, method, path); err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("%w: %s %s: read response: %w", ErrUnavailable, method, path, err)
	}
	if len(data) > maxResponseBytes {
		return fmt.Errorf("%w: %s %s: response exceeds %d bytes", ErrUnavailable, method, path, maxResponseBytes)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%w: %s %s: decode response: %w", ErrUnavailable, method, path, err)
	}
	return nil
}

// stream sends one request and hands the body of a 2xx answer to the caller, who reads
// and closes it. Only ctx bounds the call: streams (followed logs, exec output) last as
// long as the caller wants.
func (c *Client) stream(ctx context.Context, method, path string, q url.Values, in any) (io.ReadCloser, error) {
	resp, err := c.send(ctx, method, path, q, in) //nolint:bodyclose // the body is returned to the caller, who closes it
	if err != nil {
		return nil, err
	}
	if err := checkStatus(resp, method, path); err != nil {
		_ = resp.Body.Close()
		return nil, err
	}
	return resp.Body, nil
}

func (c *Client) send(ctx context.Context, method, path string, q url.Values, in any) (*http.Response, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, fmt.Errorf("docker: encode %s %s: %w", method, path, err)
		}
		body = bytes.NewReader(b)
	}
	u := baseURL + APIVersion + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, fmt.Errorf("docker: build %s %s: %w", method, path, err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s %s: %w", ErrUnavailable, method, path, err)
	}
	return resp, nil
}

// checkStatus turns a non-2xx answer into *Error with Docker's {"message": …}.
func checkStatus(resp *http.Response, method, path string) error {
	if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
		return nil
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes))
	msg := http.StatusText(resp.StatusCode)
	var m struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &m) == nil && m.Message != "" {
		msg = m.Message
	}
	return &Error{StatusCode: resp.StatusCode, Method: method, Path: path, Message: msg}
}
