package agentapi_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

func TestValidateCommand(t *testing.T) {
	valid := []string{
		"list",
		"say Hallo Welt äöü §aGrün",
		"/op Notch", // the panel strips one leading slash; the agent accepts it as text
		"tp @a ~ ~10 ~",
		strings.Repeat("a", agentapi.MaxCommandBytes),
		strings.Repeat("ä", agentapi.MaxCommandBytes/2), // 1000 bytes
		" list ",
	}
	for _, c := range valid {
		if err := agentapi.ValidateCommand(c); err != nil {
			t.Errorf("%q: %v", c, err)
		}
	}
	invalid := map[string]string{
		"empty":         "",
		"too long":      strings.Repeat("a", agentapi.MaxCommandBytes+1),
		"too long utf8": strings.Repeat("ä", agentapi.MaxCommandBytes/2+1),
		"newline":       "say a\nstop",
		"carriage ret":  "say a\rstop",
		"tab":           "say\ta",
		"nul":           "say a\x00b",
		"escape":        "say \x1b[31mred",
		"del":           "say a\x7f",
		"invalid utf8":  "say \xff\xfe",
		"only spaces":   "   ",
	}
	for name, c := range invalid {
		if err := agentapi.ValidateCommand(c); !errors.Is(err, instancespec.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
}

func TestConsoleRequestsValidate(t *testing.T) {
	good := []interface{ Validate() error }{
		agentapi.LogsRequest{ID: 1},
		agentapi.LogsRequest{ID: 1, Tail: agentapi.MaxLogTail},
		agentapi.LogsRequest{ID: 1, Since: 1_760_000_000_000_000_000},
		agentapi.RconRequest{ID: 1, Command: "list"},
		agentapi.PlayersRequest{ID: 1},
	}
	for i, r := range good {
		if err := r.Validate(); err != nil {
			t.Errorf("good[%d] %+v: %v", i, r, err)
		}
	}
	bad := map[string]interface{ Validate() error }{
		"logs id 0":      agentapi.LogsRequest{},
		"logs tail -1":   agentapi.LogsRequest{ID: 1, Tail: -1},
		"logs tail 1001": agentapi.LogsRequest{ID: 1, Tail: agentapi.MaxLogTail + 1},
		"logs since -1":  agentapi.LogsRequest{ID: 1, Since: -1},
		"rcon id 0":      agentapi.RconRequest{Command: "list"},
		"rcon command":   agentapi.RconRequest{ID: 1, Command: "a\nb"},
		"players id 0":   agentapi.PlayersRequest{},
	}
	for name, r := range bad {
		if err := r.Validate(); !errors.Is(err, instancespec.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
}
