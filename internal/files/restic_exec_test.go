package files

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeBinary writes a shell script that stands in for restic: it records its argv and
// environment next to itself and then acts according to its first argument after the record
// prefix: "cat" copies stdin to stdout, "sleep" waits for SIGINT (exit 130), "fail" exits 12, "prio" prints its nice value and I/O class (Linux).
func fakeBinary(t *testing.T) (bin, record string) {
	t.Helper()
	dir := t.TempDir()
	record = filepath.Join(dir, "rec")
	bin = filepath.Join(dir, "restic")
	script := `#!/bin/sh
printf '%s\n' "$@" > "` + record + `.args"
env > "` + record + `.env"
case "$1" in
  cat) cat ;;
  sleep) trap 'echo interrupted >&2; exit 130' INT; while :; do sleep 0.05; done ;;
  fail) echo "Fatal: wrong password" >&2; exit 12 ;;
  version) echo "restic 0.19.1 compiled with go1.25.1 on linux/amd64" ;;
  prio) cut -d' ' -f19 /proc/self/stat; if command -v ionice >/dev/null; then ionice -p $$; fi ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil { //nolint:gosec // G306: test script below t.TempDir()
		t.Fatal(err)
	}
	return bin, record
}

func TestExecRunnerArgsEnvAndStdout(t *testing.T) {
	bin, rec := fakeBinary(t)
	var out bytes.Buffer
	p, err := execRunner{bin: bin}.Start(context.Background(), resticCmd{
		Args: []string{"cat", "--no-cache"}, Env: []string{"HOME=/h", "RESTIC_PROGRESS_FPS=1"}, Stdin: true, Stdout: &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Stdin().Write([]byte("data.tar bytes")); err != nil {
		t.Fatal(err)
	}
	if err := p.Stdin().Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if out.String() != "data.tar bytes" {
		t.Fatalf("stdout = %q", out.String())
	}
	args, _ := os.ReadFile(rec + ".args")
	if string(args) != "cat\n--no-cache\n" {
		t.Fatalf("args = %q", args)
	}
	env, _ := os.ReadFile(rec + ".env")
	// Only our variables plus what /bin/sh adds itself.
	for _, line := range strings.Split(strings.TrimSpace(string(env)), "\n") {
		k, _, _ := strings.Cut(line, "=")
		if !slices.Contains([]string{"HOME", "RESTIC_PROGRESS_FPS", "PWD", "SHLVL", "_", "OLDPWD"}, k) {
			t.Errorf("unexpected variable in the restic environment: %s", line)
		}
	}
	if !strings.Contains(string(env), "HOME=/h\n") || !strings.Contains(string(env), "PWD=/\n") {
		t.Fatalf("env = %s", env)
	}
}

func TestExecRunnerExitCodeAndStderr(t *testing.T) {
	bin, _ := fakeBinary(t)
	p, err := execRunner{bin: bin}.Start(context.Background(), resticCmd{Args: []string{"fail"}})
	if err != nil {
		t.Fatal(err)
	}
	var ex *resticExit
	if err := p.Wait(); !errors.As(err, &ex) || ex.Code != 12 || !strings.Contains(ex.Stderr, "wrong password") {
		t.Fatalf("wait = %v, want exit 12 with stderr", err)
	}
}

func TestExecRunnerCancelSendsInterrupt(t *testing.T) {
	bin, _ := fakeBinary(t)
	ctx, cancel := context.WithCancel(context.Background())
	p, err := execRunner{bin: bin}.Start(ctx, resticCmd{Args: []string{"sleep"}})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // the trap is installed
	cancel()
	var ex *resticExit
	if err := p.Wait(); !errors.As(err, &ex) || ex.Code != 130 || !strings.Contains(ex.Stderr, "interrupted") {
		t.Fatalf("wait after cancel = %v, want exit 130 after SIGINT", err)
	}
}

func TestExecRunnerKill(t *testing.T) {
	bin, _ := fakeBinary(t)
	p, err := execRunner{bin: bin}.Start(context.Background(), resticCmd{Args: []string{"sleep"}, Stdin: true})
	if err != nil {
		t.Fatal(err)
	}
	p.Kill()
	var ex *resticExit
	if err := p.Wait(); !errors.As(err, &ex) || ex.Code != -1 {
		t.Fatalf("wait after kill = %v, want a signal exit (-1)", err)
	}
	_ = p.Stdin().Close() // after the process is gone: harmless
}

func TestParseResticVersion(t *testing.T) {
	for _, c := range []struct {
		out string
		v   [3]int
		ok  bool
	}{
		{"restic 0.19.1 compiled with go1.25.1 on linux/amd64\n", [3]int{0, 19, 1}, true},
		{"restic 0.17.0 compiled with go1.22.5 on darwin/arm64", [3]int{0, 17, 0}, true},
		{"restic 1.2.3-dev (compiled manually)", [3]int{1, 2, 3}, true},
		{"rustic 0.9.0", [3]int{}, false},
		{"", [3]int{}, false},
	} {
		v, ok := parseResticVersion(c.out)
		if ok != c.ok || v != c.v {
			t.Errorf("%q: %v %v, want %v %v", c.out, v, ok, c.v, c.ok)
		}
	}
	if !versionAtLeast([3]int{0, 17, 0}, "0.17.0") || versionAtLeast([3]int{0, 16, 4}, "0.17.0") || !versionAtLeast([3]int{1, 0, 0}, "0.17.0") {
		t.Fatal("versionAtLeast")
	}
}

func TestTailBufferKeepsTheEnd(t *testing.T) {
	b := &tailBuffer{max: 8}
	_, _ = b.Write([]byte("0123456789"))
	_, _ = b.Write([]byte("ab"))
	if b.String() != "456789ab" {
		t.Fatalf("tail = %q", b.String())
	}
}

func TestLastLine(t *testing.T) {
	for in, want := range map[string]string{
		"a\nFatal: repository is already locked\n\n":                                 "Fatal: repository is already locked",
		`{"message_type":"exit_error","code":1,"message":"Fatal: no such snapshot"}`: "Fatal: no such snapshot",
		strings.Repeat("x", 400):                                                     strings.Repeat("x", 300),
		"":                                                                           "",
	} {
		if got := lastLine(in); got != want {
			t.Errorf("lastLine(%.20q) = %.40q, want %.40q", in, got, want)
		}
	}
}
