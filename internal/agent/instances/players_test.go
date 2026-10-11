package instances_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instances"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

// dataDir creates the data directory of instance id like ensure does.
func (e *env) dataDir(t *testing.T, id int64) string {
	t.Helper()
	dir := instances.DataDir(e.dir, id)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// players calls Players with a deadline: a FIFO must never make it hang.
func (e *env) players(t *testing.T, id int64) (agentapi.PlayersResponse, error) {
	t.Helper()
	type result struct {
		resp agentapi.PlayersResponse
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		resp, err := e.m.Players(context.Background(), id)
		ch <- result{resp, err}
	}()
	select {
	case r := <-ch:
		return r.resp, r.err
	case <-time.After(5 * time.Second):
		t.Fatal("Players hangs")
		return agentapi.PlayersResponse{}, nil
	}
}

func TestPlayersReadsLists(t *testing.T) {
	e := newEnv(t)
	dir := e.dataDir(t, 1)
	writeFile(t, filepath.Join(dir, "ops.json"),
		`[{"uuid":"069a79f4-44e9-4726-a5be-fca90e38aaf5","name":"Notch","level":4,"bypassesPlayerLimit":false}]`)
	writeFile(t, filepath.Join(dir, "whitelist.json"),
		`[{"uuid":"069a79f4-44e9-4726-a5be-fca90e38aaf5","name":"Notch"},{"uuid":"853c80ef-3c37-49fd-aa49-938b674adae6","name":"jeb_"}]`)
	writeFile(t, filepath.Join(dir, "banned-players.json"),
		`[{"uuid":"61699b2e-d327-4a01-9f1e-0ea8c3f06bc6","name":"Griefer","created":"2026-10-11 09:32:51 +0000","source":"Server","expires":"forever","reason":"Banned by an operator."}]`)
	got, err := e.players(t, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := agentapi.PlayersResponse{
		Ops: []agentapi.PlayerOp{{UUID: "069a79f4-44e9-4726-a5be-fca90e38aaf5", Name: "Notch", Level: 4}},
		Whitelist: []agentapi.PlayerEntry{
			{UUID: "069a79f4-44e9-4726-a5be-fca90e38aaf5", Name: "Notch"},
			{UUID: "853c80ef-3c37-49fd-aa49-938b674adae6", Name: "jeb_"},
		},
		Bans: []agentapi.PlayerBan{{
			UUID: "61699b2e-d327-4a01-9f1e-0ea8c3f06bc6", Name: "Griefer", Reason: "Banned by an operator.",
			Source: "Server", Created: "2026-10-11 09:32:51 +0000", Expires: "forever",
		}},
		Errors: []string{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Players =\n%+v\nwant\n%+v", got, want)
	}
}

func TestPlayersMissingFilesAreEmpty(t *testing.T) {
	e := newEnv(t)
	e.dataDir(t, 1)
	got, err := e.players(t, 1)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(got)
	if string(b) != `{"ops":[],"whitelist":[],"bans":[],"errors":[]}` {
		t.Fatalf("Players without files = %s", b)
	}
}

func TestPlayersMissingDataDir(t *testing.T) {
	e := newEnv(t)
	if _, err := e.players(t, 1); !errors.Is(err, agentapi.ErrNotFound) {
		t.Fatalf("missing data directory = %v, want ErrNotFound", err)
	}
	if _, err := e.m.Players(context.Background(), 0); !errors.Is(err, instancespec.ErrInvalid) {
		t.Fatalf("id 0 = %v, want ErrInvalid", err)
	}
}

func TestPlayersRefusesUntrustedFiles(t *testing.T) {
	e := newEnv(t)
	outside := filepath.Join(t.TempDir(), "secret.json")
	writeFile(t, outside, `[{"uuid":"x","name":"LEAKED","level":4}]`)
	cases := []struct {
		name  string
		setup func(t *testing.T, path string)
	}{
		{"absolute symlink to a file outside", func(t *testing.T, path string) {
			if err := os.Symlink(outside, path); err != nil {
				t.Fatal(err)
			}
		}},
		{"symlink to /etc/passwd", func(t *testing.T, path string) {
			if err := os.Symlink("/etc/passwd", path); err != nil {
				t.Fatal(err)
			}
		}},
		{"relative symlink leaving the directory", func(t *testing.T, path string) {
			rel, err := filepath.Rel(filepath.Dir(path), outside)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(rel, path); err != nil {
				t.Fatal(err)
			}
		}},
		{"symlink inside the directory", func(t *testing.T, path string) {
			writeFile(t, filepath.Join(filepath.Dir(path), "other.json"), `[{"uuid":"x","name":"LEAKED","level":4}]`)
			if err := os.Symlink("other.json", path); err != nil {
				t.Fatal(err)
			}
		}},
		{"directory", func(t *testing.T, path string) {
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{"fifo", func(t *testing.T, path string) {
			if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Skipf("mkfifo: %v", err)
			}
		}},
		{"larger than 1 MiB", func(t *testing.T, path string) {
			writeFile(t, path, "["+strings.Repeat(" ", 1<<20)+"]")
		}},
		{"not a list", func(t *testing.T, path string) { writeFile(t, path, `{"name":"LEAKED"}`) }},
		{"broken JSON", func(t *testing.T, path string) { writeFile(t, path, `[{"name":`) }},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := int64(i + 1)
			tc.setup(t, filepath.Join(e.dataDir(t, id), "ops.json"))
			got, err := e.players(t, id)
			if err != nil {
				t.Fatalf("Players = %v, want an answer with errors", err)
			}
			if len(got.Ops) != 0 || !reflect.DeepEqual(got.Errors, []string{"ops.json"}) {
				t.Fatalf("Players = %+v, want no ops and errors [ops.json]", got)
			}
			if strings.Contains(fmt.Sprint(got), "LEAKED") || strings.Contains(fmt.Sprint(got), "root") {
				t.Fatalf("content outside the data directory leaked: %+v", got)
			}
		})
	}
	if !strings.Contains(e.logs.String(), "player list unreadable") || strings.Contains(e.logs.String(), "LEAKED") {
		t.Fatalf("log must name the file, never its content:\n%s", e.logs.String())
	}
}

func TestPlayersRefusesSymlinkedDataDir(t *testing.T) {
	e := newEnv(t)
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "ops.json"), `[{"uuid":"x","name":"LEAKED","level":4}]`)
	if err := os.MkdirAll(filepath.Join(e.dir, "instances"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, instances.DataDir(e.dir, 9)); err != nil {
		t.Fatal(err)
	}
	got, err := e.players(t, 9)
	if err == nil || strings.Contains(fmt.Sprint(got), "LEAKED") {
		t.Fatalf("symlinked data directory = %+v, %v; want an error and nothing read", got, err)
	}
}

func TestPlayersClipsEntries(t *testing.T) {
	e := newEnv(t)
	dir := e.dataDir(t, 1)
	entries := make([]map[string]string, 1500)
	for i := range entries {
		entries[i] = map[string]string{"uuid": fmt.Sprintf("u%d", i), "name": fmt.Sprintf("p%d", i)}
	}
	entries[0]["name"] = strings.Repeat("ä", 300)
	b, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "whitelist.json"), string(b))
	got, err := e.players(t, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Whitelist) != 1000 || utf8.RuneCountInString(got.Whitelist[0].Name) != 256 || got.Whitelist[999].Name != "p999" {
		t.Fatalf("whitelist: %d entries, first name %d runes", len(got.Whitelist), utf8.RuneCountInString(got.Whitelist[0].Name))
	}
}
