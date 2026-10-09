package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCommonFlags(t *testing.T) {
	cfgFile := filepath.Join(t.TempDir(), "panel.toml")
	if err := os.WriteFile(cfgFile, []byte("listen = \"127.0.0.1:9999\"\npublic_url = \"https://panel.example.com\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		args       []string
		wantErr    string
		wantRest   []string
		wantListen string
		wantDev    bool
	}{
		{name: "dev only", args: []string{"-dev"}, wantDev: true, wantListen: "127.0.0.1:8080"},
		{name: "dev=false", args: []string{"-config", cfgFile, "--dev=false", "-dev=true", "-dev=false"}, wantListen: "127.0.0.1:9999"},
		{name: "positional config is not a flag", args: []string{"-dev", "-username", "config"},
			wantDev: true, wantRest: []string{"-username", "config"}, wantListen: "127.0.0.1:8080"},
		{name: "positional dev is not a flag", args: []string{"-dev", "-username", "dev"},
			wantDev: true, wantRest: []string{"-username", "dev"}, wantListen: "127.0.0.1:8080"},
		{name: "config separate", args: []string{"-config", cfgFile}, wantListen: "127.0.0.1:9999"},
		{name: "config equals", args: []string{"--config=" + cfgFile}, wantListen: "127.0.0.1:9999"},
		{name: "config missing value", args: []string{"-config"}, wantErr: "needs a value"},
		{name: "config empty value", args: []string{"-config="}, wantErr: "non-empty"},
		{name: "config looks like flag", args: []string{"-config", "-dev"}, wantErr: "looks like a flag"},
		{name: "config equals flag-like", args: []string{"-config=-x"}, wantErr: "looks like a flag"},
		{name: "config twice", args: []string{"-config", cfgFile, "-config=" + cfgFile}, wantErr: "more than once"},
		{name: "dev bad value", args: []string{"-dev=maybe"}, wantErr: "invalid value"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, rest, err := commonFlags(tc.args)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Listen != tc.wantListen || cfg.Dev != tc.wantDev {
				t.Fatalf("listen=%q dev=%v, want %q %v", cfg.Listen, cfg.Dev, tc.wantListen, tc.wantDev)
			}
			if len(rest) != len(tc.wantRest) || (len(rest) > 0 && !reflect.DeepEqual(rest, tc.wantRest)) {
				t.Fatalf("rest = %v, want %v", rest, tc.wantRest)
			}
		})
	}
}

func TestAdminCreateUsername(t *testing.T) {
	tests := []struct {
		args    []string
		want    string
		wantErr string
	}{
		{args: []string{"-username", "admin"}, want: "admin"},
		{args: []string{"-username=config"}, want: "config"},
		{args: nil, wantErr: "-username is required"},
		{args: []string{"-username", ""}, wantErr: "-username is required"},
		{args: []string{"-username", "a b"}, wantErr: "username must match"},
		{args: []string{"-username", "ab"}, wantErr: "username must match"},
		{args: []string{"-username", "admin", "extra"}, wantErr: "unexpected arguments"},
		{args: []string{"-bogus"}, wantErr: "flag provided but not defined"},
	}
	for _, tc := range tests {
		got, err := adminCreateUsername(tc.args)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%v: err = %v, want containing %q", tc.args, err, tc.wantErr)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%v: got %q, %v; want %q", tc.args, got, err, tc.want)
		}
	}
}

func TestAskTwice(t *testing.T) {
	feed := func(answers ...string) func(string) (string, error) {
		i := 0
		return func(string) (string, error) { a := answers[i]; i++; return a, nil }
	}
	if pw, err := askTwice(feed("secret pw 1", "secret pw 1")); err != nil || pw != "secret pw 1" {
		t.Fatalf("match: %q, %v", pw, err)
	}
	if _, err := askTwice(feed("one", "two")); !errors.Is(err, errPasswordMismatch) {
		t.Fatalf("mismatch err = %v", err)
	}
	boom := errors.New("boom")
	if _, err := askTwice(func(string) (string, error) { return "", boom }); !errors.Is(err, boom) {
		t.Fatalf("read error not propagated: %v", err)
	}
}

func TestAdminCreateFailsBeforePrompt(t *testing.T) {
	// stdin is /dev/null (not a terminal anyway); the point is that a bad invocation is
	// rejected by runCLI before openDB/readPassword are reached for a missing username.
	t.Chdir(t.TempDir())
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	err = runCLI("admin create", []string{"-dev"}, f, os.Stdout, os.Stderr)
	if err == nil || !strings.Contains(err.Error(), "-username is required") {
		t.Fatalf("err = %v, want username-required error", err)
	}
}
