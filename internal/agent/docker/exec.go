package docker

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// ExecConfig is the body of POST /containers/{id}/exec. Only fields mc-panel sets exist.
type ExecConfig struct {
	Cmd          []string `json:"Cmd"`
	User         string   `json:"User,omitempty"`
	WorkingDir   string   `json:"WorkingDir,omitempty"`
	Env          []string `json:"Env,omitempty"`
	AttachStdin  bool     `json:"AttachStdin"`
	AttachStdout bool     `json:"AttachStdout"`
	AttachStderr bool     `json:"AttachStderr"`
	Tty          bool     `json:"Tty"`
}

// ExecInfo is the subset of GET /exec/{id}/json mc-panel reads; ExitCode is nil while
// Docker does not know it yet.
type ExecInfo struct {
	Running  bool `json:"Running"`
	ExitCode *int `json:"ExitCode"`
}

type execStart struct {
	Detach bool `json:"Detach"`
	Tty    bool `json:"Tty"`
}

// ExecCreate prepares cmd in a running container and returns the exec ID
// (404: no such container, 409: container not running).
func (c *Client) ExecCreate(ctx context.Context, id string, cfg ExecConfig) (string, error) {
	var out struct {
		ID string `json:"Id"`
	}
	if err := c.call(ctx, c.timeout, http.MethodPost, containerPath(id, "exec"), nil, cfg, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("%w: exec create %s: empty exec id", ErrUnavailable, id)
	}
	return out.ID, nil
}

// ExecStart runs a created exec attached (Detach and Tty false) and returns its output as
// a multiplexed stream. Without an Upgrade header Docker answers 200 and streams until
// the process ends; stdin stays closed, so no connection hijack is needed. Only ctx
// bounds the call.
func (c *Client) ExecStart(ctx context.Context, execID string) (io.ReadCloser, error) {
	return c.stream(ctx, http.MethodPost, execPath(execID, "start"), nil, execStart{})
}

// ExecInspect returns whether the exec still runs and its exit code.
func (c *Client) ExecInspect(ctx context.Context, execID string) (ExecInfo, error) {
	var out ExecInfo
	err := c.call(ctx, c.timeout, http.MethodGet, execPath(execID, "json"), nil, nil, &out)
	return out, err
}

func execPath(id, action string) string { return "/exec/" + url.PathEscape(id) + "/" + action }
