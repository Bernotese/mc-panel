package firewall

import (
	"errors"
	"io"
	"log/slog"
	"runtime"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
)

func TestChoose(t *testing.T) {
	cases := []struct {
		name, goos, want string
		unsupported, err bool
	}{
		{name: "noop", goos: "darwin", want: "noop"},
		{name: "noop", goos: "linux", want: "noop"},
		{name: "nftables", goos: "linux", want: "nftables"},
		{name: "nftables", goos: "darwin", unsupported: true, err: true},
		{name: "iptables", goos: "linux", err: true},
		{name: "", goos: "linux", err: true},
	}
	for _, tc := range cases {
		got, err := choose(tc.name, tc.goos)
		if (err != nil) != tc.err || got != tc.want || errors.Is(err, ErrUnsupported) != tc.unsupported {
			t.Errorf("choose(%q, %q) = %q, %v", tc.name, tc.goos, got, err)
		}
	}
}

func TestNewNoop(t *testing.T) {
	cfg := config.Agent{Dev: true, Socket: "/tmp/x.sock", PanelUID: 1000, Firewall: "noop"}
	fw, err := New(cfg, clock.NewFake(time.Unix(0, 0)), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil || fw.Name() != "noop" {
		t.Fatalf("New(noop) = %v, %v", fw, err)
	}
}

func TestNewNFTablesUnsupportedOffLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("nftables on linux is covered by the nftintegration tests")
	}
	cfg := config.Agent{Socket: "/tmp/x.sock", PanelUID: 1000, Firewall: "nftables"}
	if _, err := New(cfg, clock.Real{}, slog.New(slog.NewTextHandler(io.Discard, nil))); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("New(nftables) on %s = %v, want ErrUnsupported", runtime.GOOS, err)
	}
}
