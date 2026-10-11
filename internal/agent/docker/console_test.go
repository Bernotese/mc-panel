package docker_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/agent/docker/dockertest"
)

func frame(st docker.StreamType, s string) []byte {
	var b bytes.Buffer
	if err := docker.WriteFrame(&b, st, []byte(s)); err != nil {
		panic(err)
	}
	return b.Bytes()
}

// readFrames demultiplexes r completely; every frame as "<stream>:<data>".
func readFrames(t *testing.T, r io.Reader) []string {
	t.Helper()
	d := docker.NewDemuxer(r)
	var out []string
	for {
		f, err := d.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		out = append(out, fmt.Sprintf("%d:%s", f.Stream, f.Data))
	}
}

// logText drops the "<timestamp> " prefix Docker adds with timestamps=1.
func logText(t *testing.T, f string) string {
	t.Helper()
	stream, payload, _ := strings.Cut(f, ":")
	ts, text, ok := strings.Cut(payload, " ")
	if !ok {
		t.Fatalf("frame without timestamp: %q", f)
	}
	if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
		t.Fatalf("timestamp %q: %v", ts, err)
	}
	return stream + ":" + text
}

func TestDemuxer(t *testing.T) {
	stream := slices.Concat(frame(docker.Stdout, "hello\n"), frame(docker.Stderr, "oops\n"),
		frame(docker.Stdout, ""), frame(docker.Stdout, "x"))
	want := []string{"1:hello\n", "2:oops\n", "1:", "1:x"}
	for _, tc := range []struct {
		name string
		r    io.Reader
	}{
		{"whole", bytes.NewReader(stream)},
		{"one byte per read", iotest.OneByteReader(bytes.NewReader(stream))},
		{"half reads", iotest.HalfReader(bytes.NewReader(stream))},
	} {
		if got := readFrames(t, tc.r); !slices.Equal(got, want) {
			t.Fatalf("%s: frames = %q, want %q", tc.name, got, want)
		}
	}
	if got := readFrames(t, bytes.NewReader(nil)); len(got) != 0 {
		t.Fatalf("empty stream = %q", got)
	}
}

func TestDemuxerRejectsBrokenStreams(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want error
	}{
		{"frame larger than 1 MiB", []byte{1, 0, 0, 0, 0, 0x10, 0, 1}, docker.ErrBadFrame},
		{"unknown stream type", []byte{3, 0, 0, 0, 0, 0, 0, 1, 'x'}, docker.ErrBadFrame},
		{"non-zero padding", []byte{1, 0, 1, 0, 0, 0, 0, 1, 'x'}, docker.ErrBadFrame},
		{"cut in header", []byte{1, 0, 0}, io.ErrUnexpectedEOF},
		{"cut in payload", []byte{1, 0, 0, 0, 0, 0, 0, 5, 'a', 'b'}, io.ErrUnexpectedEOF},
	}
	for _, tc := range cases {
		_, err := docker.NewDemuxer(bytes.NewReader(tc.data)).Next()
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: Next = %v, want %v", tc.name, err, tc.want)
		}
	}
	exact := append([]byte{1, 0, 0, 0, 0, 0x10, 0, 0}, make([]byte, docker.MaxFrameBytes)...)
	if f, err := docker.NewDemuxer(bytes.NewReader(exact)).Next(); err != nil || len(f.Data) != docker.MaxFrameBytes {
		t.Fatalf("frame of exactly 1 MiB: %d bytes, %v", len(f.Data), err)
	}
	if err := docker.WriteFrame(io.Discard, docker.Stdout, make([]byte, docker.MaxFrameBytes+1)); !errors.Is(err, docker.ErrBadFrame) {
		t.Fatalf("WriteFrame > 1 MiB = %v", err)
	}
}

func TestContainerLogs(t *testing.T) {
	d := dockertest.New(t)
	d.AddImage(pinned)
	d.AddContainer("mc", docker.ContainerConfig{Image: pinned}, false)
	t1 := d.AppendLog("mc", docker.Stdout, "first")
	d.AppendLog("mc", docker.Stderr, "second")
	d.AppendLog("mc", docker.Stdout, "third")
	c := docker.New(d.Socket)
	ctx := context.Background()

	read := func(o docker.LogsOptions) []string {
		t.Helper()
		body, err := c.ContainerLogs(ctx, "mc", o)
		if err != nil {
			t.Fatalf("ContainerLogs(%+v): %v", o, err)
		}
		defer func() { _ = body.Close() }()
		var out []string
		for _, f := range readFrames(t, body) {
			out = append(out, logText(t, f))
		}
		return out
	}
	if got, want := read(docker.LogsOptions{Tail: 2}), []string{"2:second\n", "1:third\n"}; !slices.Equal(got, want) {
		t.Fatalf("tail 2 = %q, want %q", got, want)
	}
	since := t1.UnixNano() + 1
	if got, want := read(docker.LogsOptions{Tail: -1, Since: since}), []string{"2:second\n", "1:third\n"}; !slices.Equal(got, want) {
		t.Fatalf("since = %q, want %q", got, want)
	}
	if got := read(docker.LogsOptions{Tail: -1}); len(got) != 3 {
		t.Fatalf("all = %q", got)
	}
	if got := read(docker.LogsOptions{Tail: 0}); len(got) != 0 {
		t.Fatalf("tail 0 = %q", got)
	}
	want := []string{
		"follow=0&stderr=1&stdout=1&tail=2&timestamps=1",
		fmt.Sprintf("follow=0&since=%d.%09d&stderr=1&stdout=1&tail=all&timestamps=1", since/int64(time.Second), since%int64(time.Second)),
		"follow=0&stderr=1&stdout=1&tail=all&timestamps=1",
		"follow=0&stderr=1&stdout=1&tail=0&timestamps=1",
	}
	if got := d.LogQueries(); !slices.Equal(got, want) {
		t.Fatalf("queries =\n%q\nwant\n%q", got, want)
	}
	if _, err := c.ContainerLogs(ctx, "missing", docker.LogsOptions{Tail: -1}); !docker.IsNotFound(err) {
		t.Fatalf("logs of a missing container = %v, want 404", err)
	}
}

func TestContainerLogsFollow(t *testing.T) {
	d := dockertest.New(t)
	d.AddImage(pinned)
	d.AddContainer("mc", docker.ContainerConfig{Image: pinned}, true)
	d.AppendLog("mc", docker.Stdout, "history")
	body, err := docker.New(d.Socket).ContainerLogs(context.Background(), "mc", docker.LogsOptions{Follow: true, Tail: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	frames, errc := make(chan string, 8), make(chan error, 1)
	go func() {
		dm := docker.NewDemuxer(body)
		for {
			f, err := dm.Next()
			if err != nil {
				errc <- err
				return
			}
			frames <- string(f.Data)
		}
	}()
	d.AppendLog("mc", docker.Stdout, "live")
	select {
	case f := <-frames:
		if !strings.HasSuffix(f, " live\n") {
			t.Fatalf("first followed frame = %q, want the live line (tail 0 skips history)", f)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no live line")
	}
	d.SetState("mc", "exited", 0)
	select {
	case err := <-errc:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("stream end = %v, want io.EOF", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not end when the container stopped")
	}
}

func TestContainerLogsStopsOnCancel(t *testing.T) {
	d := dockertest.New(t)
	d.AddImage(pinned)
	d.AddContainer("mc", docker.ContainerConfig{Image: pinned}, true)
	ctx, cancel := context.WithCancel(context.Background())
	body, err := docker.New(d.Socket).ContainerLogs(ctx, "mc", docker.LogsOptions{Follow: true, Tail: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	errc := make(chan error, 1)
	go func() {
		_, err := docker.NewDemuxer(body).Next()
		errc <- err
	}()
	cancel()
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("read after cancel succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not end the followed stream")
	}
}

func TestExec(t *testing.T) {
	d := dockertest.New(t)
	d.AddImage(pinned)
	d.AddContainer("mc", docker.ContainerConfig{Image: pinned}, true)
	d.AddContainer("stopped", docker.ContainerConfig{Image: pinned}, false)
	c := docker.New(d.Socket)
	ctx := context.Background()
	cfg := docker.ExecConfig{
		Cmd: []string{"rcon-cli", "list"}, User: "1000:1000", WorkingDir: "/data", Env: []string{"HOME=/data"},
		AttachStdout: true, AttachStderr: true,
	}

	id, err := c.ExecCreate(ctx, "mc", cfg)
	if err != nil || id == "" {
		t.Fatalf("ExecCreate = %q, %v", id, err)
	}
	if info, err := c.ExecInspect(ctx, id); err != nil || info.ExitCode != nil {
		t.Fatalf("inspect before start = %+v, %v (exit code must be unknown)", info, err)
	}
	body, err := c.ExecStart(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	got := readFrames(t, body)
	_ = body.Close()
	if !slices.Equal(got, []string{"1:list\n"}) {
		t.Fatalf("output = %q", got)
	}
	info, err := c.ExecInspect(ctx, id)
	if err != nil || info.Running || info.ExitCode == nil || *info.ExitCode != 0 {
		t.Fatalf("inspect after run = %+v, %v", info, err)
	}
	if recs := d.Execs(); len(recs) != 1 || recs[0].Container != "mc" || !slices.Equal(recs[0].Config.Cmd, cfg.Cmd) ||
		recs[0].Config.User != "1000:1000" || recs[0].Config.WorkingDir != "/data" || !slices.Equal(recs[0].Config.Env, cfg.Env) ||
		recs[0].Config.AttachStdin || !recs[0].Config.AttachStdout || !recs[0].Config.AttachStderr || recs[0].Config.Tty {
		t.Fatalf("exec records = %+v", recs)
	}

	d.SetExecHandler(func(string, docker.ExecConfig) dockertest.ExecResult {
		return dockertest.ExecResult{Stdout: "out", Stderr: "err", ExitCode: 3}
	})
	id, err = c.ExecCreate(ctx, "mc", cfg)
	if err != nil {
		t.Fatal(err)
	}
	body, err = c.ExecStart(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	got = readFrames(t, body)
	_ = body.Close()
	if !slices.Equal(got, []string{"1:out", "2:err"}) {
		t.Fatalf("output = %q", got)
	}
	if info, err := c.ExecInspect(ctx, id); err != nil || info.ExitCode == nil || *info.ExitCode != 3 {
		t.Fatalf("exit code = %+v, %v", info, err)
	}

	if _, err := c.ExecCreate(ctx, "stopped", cfg); !docker.IsConflict(err) {
		t.Fatalf("exec in a stopped container = %v, want 409", err)
	}
	if _, err := c.ExecCreate(ctx, "missing", cfg); !docker.IsNotFound(err) {
		t.Fatalf("exec in a missing container = %v, want 404", err)
	}
	if _, err := c.ExecInspect(ctx, "nope"); !docker.IsNotFound(err) {
		t.Fatalf("inspect of an unknown exec = %v, want 404", err)
	}
}
