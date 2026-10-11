package agentapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

// Console endpoints (sub-project 3a): container log stream, RCON commands and player lists.
const (
	PathInstanceLogs    = "/v1/instances/logs"
	PathInstanceRcon    = "/v1/instances/rcon"
	PathInstancePlayers = "/v1/instances/players"

	ErrorCodeNotFound        = "not_found"        // 404: container or data directory missing
	ErrorCodeNotRunning      = "not_running"      // 409: rcon on a container that is not running
	ErrorCodeRconUnavailable = "rcon_unavailable" // 503: rcon-cli exited non-zero

	MaxLogTail         = 1000
	MaxCommandBytes    = 1000
	MaxLogLineBytes    = 8 << 10
	MaxRconOutputBytes = 64 << 10

	StreamOut = "out"
	StreamErr = "err"

	// HeaderContainerRunning is set by the logs endpoint on its 200 answer: "true" or "false",
	// the container state when the stream was opened.
	HeaderContainerRunning = "X-Container-Running"
)

var (
	// ErrNotFound: the container (logs, rcon) or the data directory (players) does not exist.
	ErrNotFound = errors.New("not found")
	// ErrNotRunning: RCON needs a running container.
	ErrNotRunning = errors.New("instance not running")
	// ErrRconUnavailable: rcon-cli failed, e.g. while the server is still starting.
	ErrRconUnavailable = errors.New("rcon unavailable")
)

// LogsRequest asks for the container log. Since > 0 (Docker timestamp in ns) wins over Tail.
type LogsRequest struct {
	ID    int64 `json:"id"`
	Tail  int   `json:"tail"`
	Since int64 `json:"since"`
}

func (r LogsRequest) Validate() error {
	switch {
	case r.ID <= 0:
		return fmt.Errorf("%w: id: must be > 0", instancespec.ErrInvalid)
	case r.Tail < 0 || r.Tail > MaxLogTail:
		return fmt.Errorf("%w: tail: must be 0..%d", instancespec.ErrInvalid, MaxLogTail)
	case r.Since < 0:
		return fmt.Errorf("%w: since: must be >= 0", instancespec.ErrInvalid)
	}
	return nil
}

// LogLine is one NDJSON line of the logs stream.
type LogLine struct {
	TS     int64  `json:"ts"`     // Docker timestamp, ns since epoch
	Stream string `json:"stream"` // StreamOut | StreamErr
	Line   string `json:"line"`   // without timestamp and trailing \r\n, <= MaxLogLineBytes, valid UTF-8
}

type RconRequest struct {
	ID      int64  `json:"id"`
	Command string `json:"command"`
}

func (r RconRequest) Validate() error {
	if r.ID <= 0 {
		return fmt.Errorf("%w: id: must be > 0", instancespec.ErrInvalid)
	}
	return ValidateCommand(r.Command)
}

type RconResponse struct {
	Output string `json:"output"`
}

// ValidateCommand checks a console command (shared by panel and agent): 1..MaxCommandBytes
// bytes, valid UTF-8, no control character (< 0x20, so no newline or tab, and no 0x7f), not
// only spaces. rcon-cli gets it as one argv element, never through a shell.
func ValidateCommand(cmd string) error {
	switch {
	case len(cmd) == 0 || len(cmd) > MaxCommandBytes:
		return fmt.Errorf("%w: command: must be 1..%d bytes", instancespec.ErrInvalid, MaxCommandBytes)
	case !utf8.ValidString(cmd):
		return fmt.Errorf("%w: command: must be valid UTF-8", instancespec.ErrInvalid)
	case strings.ContainsFunc(cmd, func(r rune) bool { return r < 0x20 || r == 0x7f }):
		return fmt.Errorf("%w: command: must not contain control characters", instancespec.ErrInvalid)
	case strings.Trim(cmd, " ") == "":
		return fmt.Errorf("%w: command: must not be blank", instancespec.ErrInvalid)
	}
	return nil
}

type PlayersRequest struct {
	ID int64 `json:"id"`
}

func (r PlayersRequest) Validate() error {
	if r.ID <= 0 {
		return fmt.Errorf("%w: id: must be > 0", instancespec.ErrInvalid)
	}
	return nil
}

type PlayerOp struct {
	UUID  string `json:"uuid"`
	Name  string `json:"name"`
	Level int    `json:"level"`
}

type PlayerEntry struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

type PlayerBan struct {
	UUID    string `json:"uuid"`
	Name    string `json:"name"`
	Reason  string `json:"reason"`
	Source  string `json:"source"`
	Created string `json:"created"`
	Expires string `json:"expires"`
}

// PlayersResponse: slices are never nil (JSON []); Errors lists unreadable file names
// ("ops.json", "whitelist.json", "banned-players.json").
type PlayersResponse struct {
	Ops       []PlayerOp    `json:"ops"`
	Whitelist []PlayerEntry `json:"whitelist"`
	Bans      []PlayerBan   `json:"bans"`
	Errors    []string      `json:"errors"`
}

// LogStream is an open logs stream. Next returns io.EOF when the agent ended the stream
// (container stopped/removed); any other error means the stream broke. Close is idempotent.
// Running reports whether the container was running when the stream was opened (header
// HeaderContainerRunning; a missing or other value counts as false).
type LogStream interface {
	Next() (LogLine, error)
	Close() error
	Running() bool
}

// ConsoleClient is the panel-side view of the console endpoints (implemented by *agent.Client).
type ConsoleClient interface {
	Logs(ctx context.Context, req LogsRequest) (LogStream, error)
	Rcon(ctx context.Context, id int64, command string) (string, error)
	Players(ctx context.Context, id int64) (PlayersResponse, error)
}
