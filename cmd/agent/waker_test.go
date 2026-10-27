package main

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// withUID makes runWaker see uid until the test ends.
func withUID(t *testing.T, uid int) {
	t.Helper()
	old := getuid
	getuid = func() int { return uid }
	t.Cleanup(func() { getuid = old })
}

func TestWakerRefusesRoot(t *testing.T) {
	withUID(t, 0)
	err := run(context.Background(), []string{"waker", "-listen", "127.0.0.1:0"}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("run waker as root = %v, want a refusal", err)
	}
}

func TestWakerRejectsBadArguments(t *testing.T) {
	withUID(t, 65534)
	for _, args := range [][]string{{"waker", "-config", "/etc/mc-panel/agent.toml"}, {"waker", "-dev"}, {"waker", "serve"}} {
		if err := run(context.Background(), args, io.Discard, io.Discard); err == nil {
			t.Errorf("run(%v) = nil, want error", args)
		}
	}
}

// freePort returns a loopback address that was free a moment ago.
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func appendVarInt(b []byte, v int32) []byte {
	u := uint32(v) //nolint:gosec // G115: two's complement VarInt
	for u >= 0x80 {
		b = append(b, byte(u)|0x80)
		u >>= 7
	}
	return append(b, byte(u))
}

func TestWakerServesLogin(t *testing.T) {
	withUID(t, 65534)
	addr := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout, stderr syncBuffer
	done := make(chan error, 1)
	go func() { done <- run(ctx, []string{"waker", "-listen", addr}, &stdout, &stderr) }()

	var conn net.Conn
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			conn = c
			break
		}
		select {
		case err := <-done:
			t.Fatalf("run returned early: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("waker not listening: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	host := "dev-survival.mc.example.com"
	var hs []byte
	hs = appendVarInt(hs, 0)
	hs = appendVarInt(hs, 767)
	hs = appendVarInt(hs, int32(len(host)))
	hs = append(hs, host...)
	hs = binary.BigEndian.AppendUint16(hs, 25565)
	hs = appendVarInt(hs, 2)
	msg := append(appendVarInt(nil, int32(len(hs))), hs...)
	msg = append(msg, 0x07, 0x00, 0x05, 'S', 't', 'e', 'v', 'e')
	if _, err := conn.Write(msg); err != nil {
		t.Fatal(err)
	}
	reply, _ := io.ReadAll(conn)
	if !strings.Contains(string(reply), wakerKick) {
		t.Fatalf("reply %q lacks the kick text", reply)
	}
	if got := stdout.String(); got != "wake "+host+"\n" {
		t.Fatalf("stdout = %q", got)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("waker did not stop after cancel")
	}
	if stderr.String() != "" {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
