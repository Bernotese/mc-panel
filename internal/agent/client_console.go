package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

const (
	// RconTimeout covers the agent's own 10 s exec timeout plus the round trip.
	RconTimeout = 15 * time.Second
	// PlayersTimeout bounds reading the three small list files.
	PlayersTimeout = 5 * time.Second
	// LogsHeaderTimeout bounds the wait for the logs response headers; the stream itself
	// has no deadline (it ends with ctx or with the container).
	LogsHeaderTimeout = 5 * time.Second
	// maxLogLineJSON bounds one NDJSON line (an 8 KiB log line can grow by JSON escaping).
	maxLogLineJSON = 64 << 10
)

var _ agentapi.ConsoleClient = (*Client)(nil)

func (c *Client) Logs(ctx context.Context, req agentapi.LogsRequest) (agentapi.LogStream, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	ctx, cancel := context.WithCancel(ctx)
	hreq, err := newRequest(ctx, http.MethodPost, agentapi.PathInstanceLogs, req)
	if err != nil {
		cancel()
		return nil, err
	}
	hreq.Header.Set("Accept", "application/x-ndjson")
	timer := time.AfterFunc(c.logsHeaderTimeout, cancel)
	resp, err := c.hc.Do(hreq)
	if !timer.Stop() { // the timer fired: ctx is cancelled
		if err == nil {
			_ = resp.Body.Close()
		}
		cancel()
		return nil, fmt.Errorf("%w: logs: no response within %s", agentapi.ErrUnavailable, c.logsHeaderTimeout)
	}
	if err != nil {
		cancel()
		return nil, fmt.Errorf("%w: %w", agentapi.ErrUnavailable, err)
	}
	if resp.StatusCode != http.StatusOK {
		defer cancel()
		defer func() { _ = resp.Body.Close() }()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		return nil, statusError(resp.StatusCode, data)
	}
	return &logStream{
		r: bufio.NewReader(resp.Body), body: resp.Body, cancel: cancel,
		running: resp.Header.Get(agentapi.HeaderContainerRunning) == "true",
	}, nil
}

func (c *Client) Rcon(ctx context.Context, id int64, command string) (string, error) {
	req := agentapi.RconRequest{ID: id, Command: command}
	if err := req.Validate(); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	var out agentapi.RconResponse
	if err := c.doTimeout(ctx, RconTimeout, http.MethodPost, agentapi.PathInstanceRcon, req, &out); err != nil {
		return "", err
	}
	return out.Output, nil
}

func (c *Client) Players(ctx context.Context, id int64) (agentapi.PlayersResponse, error) {
	req := agentapi.PlayersRequest{ID: id}
	if err := req.Validate(); err != nil {
		return agentapi.PlayersResponse{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	var out agentapi.PlayersResponse
	if err := c.doTimeout(ctx, PlayersTimeout, http.MethodPost, agentapi.PathInstancePlayers, req, &out); err != nil {
		return agentapi.PlayersResponse{}, err
	}
	if out.Ops == nil {
		out.Ops = []agentapi.PlayerOp{}
	}
	if out.Whitelist == nil {
		out.Whitelist = []agentapi.PlayerEntry{}
	}
	if out.Bans == nil {
		out.Bans = []agentapi.PlayerBan{}
	}
	if out.Errors == nil {
		out.Errors = []string{}
	}
	return out, nil
}

// logStream reads NDJSON lines from an open logs response.
type logStream struct {
	r       *bufio.Reader
	body    io.Closer
	cancel  context.CancelFunc
	once    sync.Once
	running bool
}

func (s *logStream) Running() bool { return s.running }

func (s *logStream) Next() (agentapi.LogLine, error) {
	for {
		raw, err := readLine(s.r, maxLogLineJSON)
		if errors.Is(err, io.EOF) {
			return agentapi.LogLine{}, io.EOF
		}
		if err != nil {
			return agentapi.LogLine{}, fmt.Errorf("%w: logs: %w", agentapi.ErrUnavailable, err)
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var l agentapi.LogLine
		if err := json.Unmarshal(raw, &l); err != nil {
			return agentapi.LogLine{}, fmt.Errorf("%w: logs: decode line: %w", agentapi.ErrUnavailable, err)
		}
		return l, nil
	}
}

func (s *logStream) Close() error {
	s.once.Do(func() {
		s.cancel()
		_ = s.body.Close()
	})
	return nil
}

// readLine returns the next '\n'-terminated line without the newline. It returns io.EOF only
// at a clean end; a last line without newline is io.ErrUnexpectedEOF, a line over limit bytes
// an error.
func readLine(r *bufio.Reader, limit int) ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if len(line)+len(chunk) > limit+1 {
			return nil, fmt.Errorf("line exceeds %d bytes", limit)
		}
		line = append(line, chunk...)
		switch {
		case err == nil:
			return line[:len(line)-1], nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(line) == 0:
			return nil, io.EOF
		case errors.Is(err, io.EOF):
			return nil, io.ErrUnexpectedEOF
		default:
			return nil, err
		}
	}
}
