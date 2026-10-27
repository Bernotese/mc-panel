package waker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
)

const (
	testMOTD = "Dev schläft – verbinden zum Aufwecken"
	testKick = "Dev startet (oder wird gerade synchronisiert). Bitte in ca. 1 Minute neu verbinden."
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func testConfig(out io.Writer, clk clock.Clock) Config {
	return Config{
		MOTD: testMOTD, Kick: testKick, MaxConns: 64, MaxPerIP: 4,
		Deadline: 5 * time.Second, WakeEvery: 10 * time.Second, Out: out, Clock: clk,
	}
}

// startServer runs Serve on a loopback listener until the test ends.
func startServer(t *testing.T, c Config) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, ln, c) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("Serve did not return after cancel")
		}
	})
	return ln.Addr().String()
}

func dial(t *testing.T, addr string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	return conn
}

func mustWrite(t *testing.T, w io.Writer, b []byte) {
	t.Helper()
	if _, err := w.Write(b); err != nil {
		t.Fatal(err)
	}
}

// readStringPacket reads one packet with id want whose body is a single string.
func readStringPacket(t *testing.T, r *bufio.Reader, want int32) string {
	t.Helper()
	id, body, err := readPacket(r)
	if err != nil {
		t.Fatalf("read packet: %v", err)
	}
	if id != want {
		t.Fatalf("packet id %d, want %d", id, want)
	}
	br := bytes.NewReader(body)
	n, err := readVarInt(br)
	if err != nil || int(n) != br.Len() {
		t.Fatalf("string length %d, %d bytes left, %v", n, br.Len(), err)
	}
	return string(body[len(body)-int(n):])
}

// login sends a login handshake plus login start and returns the disconnect text.
func login(t *testing.T, addr, host string) string {
	t.Helper()
	conn := dial(t, addr)
	mustWrite(t, conn, handshake(767, host, 25565, 2))
	mustWrite(t, conn, []byte{0x07, 0x00, 0x05, 'S', 't', 'e', 'v', 'e'})
	var msg struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(readStringPacket(t, bufio.NewReader(conn), 0x00)), &msg); err != nil {
		t.Fatal(err)
	}
	return msg.Text
}

// closed reports whether the server closed conn within d without sending anything.
func closed(conn net.Conn, d time.Duration) bool {
	_ = conn.SetReadDeadline(time.Now().Add(d))
	n, err := conn.Read(make([]byte, 1))
	return n == 0 && err != nil && !errors.Is(err, context.DeadlineExceeded) && !isTimeout(err)
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func TestServeStatus(t *testing.T) {
	out := &syncBuffer{}
	addr := startServer(t, testConfig(out, clock.Real{}))
	conn := dial(t, addr)
	mustWrite(t, conn, handshake(-1, "dev-a.example.com", 25565, 1))
	mustWrite(t, conn, []byte{0x01, 0x00})
	r := bufio.NewReader(conn)
	var st struct {
		Version struct {
			Name     string `json:"name"`
			Protocol int32  `json:"protocol"`
		} `json:"version"`
		Players struct {
			Max    *int `json:"max"`
			Online *int `json:"online"`
		} `json:"players"`
		Description struct {
			Text string `json:"text"`
		} `json:"description"`
	}
	if err := json.Unmarshal([]byte(readStringPacket(t, r, 0x00)), &st); err != nil {
		t.Fatal(err)
	}
	if st.Version.Name != "mc-panel" || st.Version.Protocol != -1 || st.Description.Text != testMOTD ||
		st.Players.Max == nil || *st.Players.Max != 0 || st.Players.Online == nil || *st.Players.Online != 0 {
		t.Fatalf("status = %+v", st)
	}
	ping := binary.BigEndian.AppendUint64([]byte{0x09, 0x01}, 0x0102030405060708)
	mustWrite(t, conn, ping)
	id, body, err := readPacket(r)
	if err != nil || id != 0x01 || !bytes.Equal(body, ping[2:]) {
		t.Fatalf("pong = %d % x %v", id, body, err)
	}
	if !closed(conn, 2*time.Second) {
		t.Fatal("connection not closed after the pong")
	}
	if out.String() != "" {
		t.Fatalf("a status ping wrote %q", out.String())
	}
}

func TestServeLoginWakes(t *testing.T) {
	out := &syncBuffer{}
	addr := startServer(t, testConfig(out, clock.Real{}))
	if got := login(t, addr, "DEV-A.example.com.\x00FML3\x00"); got != testKick {
		t.Fatalf("disconnect = %q", got)
	}
	if got := out.String(); got != "wake dev-a.example.com\n" {
		t.Fatalf("out = %q", got)
	}
}

func TestServeLoginWithoutLoginStart(t *testing.T) {
	out := &syncBuffer{}
	c := testConfig(out, clock.Real{})
	c.Deadline = 300 * time.Millisecond
	addr := startServer(t, c)
	conn := dial(t, addr)
	mustWrite(t, conn, handshake(767, "dev-a.example.com", 25565, 2))
	if !closed(conn, 3*time.Second) {
		t.Fatal("connection not closed after the deadline")
	}
	if got := out.String(); got != "wake dev-a.example.com\n" {
		t.Fatalf("out = %q", got)
	}
}

func TestServeWakeDedup(t *testing.T) {
	out := &syncBuffer{}
	clk := clock.NewFake(time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC))
	addr := startServer(t, testConfig(out, clk))
	login(t, addr, "dev-a.example.com")
	clk.Advance(9 * time.Second)
	login(t, addr, "dev-a.example.com")
	login(t, addr, "dev-b.example.com")
	if got := out.String(); got != "wake dev-a.example.com\nwake dev-b.example.com\n" {
		t.Fatalf("out = %q", got)
	}
	clk.Advance(time.Second)
	login(t, addr, "dev-a.example.com")
	if got := out.String(); got != "wake dev-a.example.com\nwake dev-b.example.com\nwake dev-a.example.com\n" {
		t.Fatalf("out = %q", got)
	}
}

func TestServeLimits(t *testing.T) {
	t.Run("total", func(t *testing.T) {
		c := testConfig(io.Discard, clock.Real{})
		c.MaxPerIP = 100
		addr := startServer(t, c)
		for range c.MaxConns {
			dial(t, addr)
		}
		over := dial(t, addr)
		if !closed(over, 2*time.Second) {
			t.Fatal("connection above MaxConns not closed")
		}
	})
	t.Run("per ip", func(t *testing.T) {
		addr := startServer(t, testConfig(io.Discard, clock.Real{}))
		conns := make([]net.Conn, 0, 4)
		for range 4 {
			conns = append(conns, dial(t, addr))
		}
		over := dial(t, addr)
		if !closed(over, 2*time.Second) {
			t.Fatal("5th connection of one address not closed")
		}
		// The slots are released when a connection ends.
		_ = conns[0].Close()
		time.Sleep(100 * time.Millisecond)
		if got := login(t, addr, "dev-a.example.com"); got != testKick {
			t.Fatalf("after a slot was freed: %q", got)
		}
	})
	t.Run("deadline", func(t *testing.T) {
		c := testConfig(io.Discard, clock.Real{})
		c.Deadline = 200 * time.Millisecond
		addr := startServer(t, c)
		conn := dial(t, addr)
		mustWrite(t, conn, []byte{0x10}) // a packet that never completes
		if !closed(conn, 3*time.Second) {
			t.Fatal("idle connection not closed after the deadline")
		}
	})
}

func TestServeLegacyPing(t *testing.T) {
	out := &syncBuffer{}
	addr := startServer(t, testConfig(out, clock.Real{}))
	conn := dial(t, addr)
	mustWrite(t, conn, []byte{0xFE, 0x01, 0xFA})
	if !closed(conn, 2*time.Second) {
		t.Fatal("legacy ping not closed")
	}
	if out.String() != "" {
		t.Fatalf("out = %q", out.String())
	}
}

func TestServeGarbage(t *testing.T) {
	out := &syncBuffer{}
	c := testConfig(out, clock.Real{})
	c.Deadline = 500 * time.Millisecond // an incomplete packet (HTTP) waits for the deadline
	addr := startServer(t, c)
	for _, in := range [][]byte{
		[]byte("GET / HTTP/1.1\r\nHost: dev-a.example.com\r\n\r\n"),
		handshake(767, "dev-a.example.com:25565", 25565, 2),
		append(appendVarInt(nil, 1025), make([]byte, 1025)...),
		{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
	} {
		conn := dial(t, addr)
		mustWrite(t, conn, in)
		if !closed(conn, 3*time.Second) {
			t.Fatalf("% x: not closed", in[:min(len(in), 8)])
		}
	}
	if out.String() != "" {
		t.Fatalf("out = %q", out.String())
	}
}

func TestServeStatusWrongPacket(t *testing.T) {
	addr := startServer(t, testConfig(io.Discard, clock.Real{}))
	conn := dial(t, addr)
	mustWrite(t, conn, handshake(-1, "dev-a.example.com", 25565, 1))
	mustWrite(t, conn, []byte{0x02, 0x05, 0x00}) // not a status request
	if !closed(conn, 2*time.Second) {
		t.Fatal("not closed")
	}
}

func TestServeConfigErrors(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	c := testConfig(io.Discard, clock.Real{})
	c.Out = nil
	if err := Serve(context.Background(), ln, c); err == nil || !strings.Contains(err.Error(), "Out") {
		t.Fatalf("Serve without Out: %v", err)
	}
}
