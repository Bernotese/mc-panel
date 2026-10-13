package files

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

func TestTarToZip(t *testing.T) {
	b := makeTar(t, dir("world/"), reg("world/level.dat", "lvl"), reg("CON.txt", "windows"), dir("bad:dir/"), reg("bad:dir/x", "x"),
		reg("server.properties", "rcon.password=********\n"))
	var out bytes.Buffer
	begun := false
	if err := tarToZip(context.Background(), bytes.NewReader(b), &out, testLimits(), func() { begun = true }); err != nil {
		t.Fatal(err)
	}
	got := zipNames(t, out.Bytes())
	if !begun || got["world/level.dat"] != "lvl" || got["server.properties"] == "" || len(got) != 3 {
		t.Fatalf("zip = %v", got) // world/, world/level.dat, server.properties; CON.txt and bad:dir/ left out
	}
}

func TestTarToZipErrorsBeforeBegin(t *testing.T) {
	begun := false
	err := tarToZip(context.Background(), strings.NewReader("garbage, shorter than one tar block"), io.Discard, testLimits(), func() { begun = true })
	if !errors.Is(err, filesapi.ErrInvalid) || begun {
		t.Fatalf("garbage: %v, begun %v", err, begun)
	}
}

func TestDownloadBackup(t *testing.T) {
	ctx := context.Background()
	s, _, f := newBackupService(t)
	sid := snapshotOf(t, s, f, makeTar(t, reg("a.txt", "hello")))
	var out bytes.Buffer
	started := false
	if err := s.DownloadBackup(ctx, 1, sid, func() { started = true }, &out); err != nil {
		t.Fatal(err)
	}
	if !started || zipNames(t, out.Bytes())["a.txt"] != "hello" {
		t.Fatalf("download = %v", zipNames(t, out.Bytes()))
	}
	// Unknown snapshot and a failing dump are answered before the first byte.
	if err := s.DownloadBackup(ctx, 1, strings.Repeat("d", 64), func() { t.Fatal("started") }, io.Discard); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	f.fail["dump"] = &resticExit{Code: 1, Stderr: "Fatal: pack file is damaged\n"}
	if err := s.DownloadBackup(ctx, 1, sid, func() { t.Fatal("started") }, io.Discard); err == nil || !strings.Contains(err.Error(), "restic dump failed") {
		t.Fatalf("failing dump: %v", err)
	}
	delete(f.fail, "dump")
	// While a forget runs: busy.
	if err := s.jobs.acquire(1, filesapi.JobForget); err != nil {
		t.Fatal(err)
	}
	if err := s.DownloadBackup(ctx, 1, sid, func() {}, io.Discard); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("during forget: %v", err)
	}
	s.jobs.release(1)
}
