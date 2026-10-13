package monitoring

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// cReadHandshake parses a framed handshake packet (the server side of the protocol) and
// returns protocol version, address, port and next state.
func cReadHandshake(t *testing.T, b []byte) (proto int32, host string, port uint16, next int32) {
	t.Helper()
	br := bufio.NewReader(bytes.NewReader(b))
	n, err := readVarInt(br)
	if err != nil || int(n) != len(b)-len(appendVarInt(nil, n)) {
		t.Fatalf("frame length %d of %d bytes (%v)", n, len(b), err)
	}
	if id, _ := readVarInt(br); id != 0 {
		t.Fatalf("handshake id %d", id)
	}
	proto, _ = readVarInt(br)
	hl, _ := readVarInt(br)
	hb := make([]byte, hl)
	if _, err := io.ReadFull(br, hb); err != nil {
		t.Fatal(err)
	}
	if err := binary.Read(br, binary.BigEndian, &port); err != nil {
		t.Fatal(err)
	}
	next, _ = readVarInt(br)
	if _, err := br.ReadByte(); !errors.Is(err, io.EOF) {
		t.Fatal("trailing bytes after the handshake")
	}
	return proto, string(hb), port, next
}

// cStatusPacket frames a status response with the given JSON.
func cStatusPacket(json string) []byte {
	var body bytes.Buffer
	body.Write(appendVarInt(nil, 0))
	body.Write(appendVarInt(nil, int32(len(json))))
	body.WriteString(json)
	return append(appendVarInt(nil, int32(body.Len())), body.Bytes()...)
}

func cIntp(v int) *int { return &v }

func TestSLPHandshakePacket(t *testing.T) {
	proto, host, port, next := cReadHandshake(t, handshakePacket("survival.mc.example.com"))
	if proto != -1 || host != "survival.mc.example.com" || port != 25565 || next != 1 {
		t.Fatalf("handshake = %d %q %d %d", proto, host, port, next)
	}
	if !bytes.Equal(statusRequest(), []byte{0x01, 0x00}) {
		t.Fatalf("status request = %v", statusRequest())
	}
}

func TestSLPPingParsesPlayers(t *testing.T) {
	ps := newPingServer(t)
	ps.set("survival.mc.example.com", pingAnswer{json: `{"version":{"name":"1.21.4","protocol":769},
		"players":{"max":20,"online":3,"sample":[{"name":"Alice","id":"x"},{"name":"bob_2"},{"name":"<b>evil</b>"},
		{"name":"waytoolongname12345"},{"name":"tab\tname"},{"name":""}]},
		"description":{"text":"hi"},"favicon":"data:image/png;base64,AAAA"}`})
	res, err := slpPing(context.Background(), ps.addr(), "survival.mc.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if ps.hits("survival.mc.example.com") != 1 {
		t.Fatal("the handshake must carry the server's FQDN")
	}
	if res.Online == nil || res.Max == nil || *res.Online != 3 || *res.Max != 20 {
		t.Fatalf("players = %v/%v", res.Online, res.Max)
	}
	if strings.Join(res.Names, ",") != "Alice,bob_2" {
		t.Fatalf("names = %v (invalid names must be dropped)", res.Names)
	}
}

func TestSLPPingWithoutPlayersObject(t *testing.T) {
	ps := newPingServer(t)
	ps.set("a.example.com", pingAnswer{json: `{"version":{"name":"x","protocol":1}}`})
	res, err := slpPing(context.Background(), ps.addr(), "a.example.com")
	if err != nil || res.Online != nil || res.Max != nil || len(res.Names) != 0 {
		t.Fatalf("res = %+v err = %v", res, err)
	}
}

func TestSLPPingAtMostTwelveNames(t *testing.T) {
	names := make([]string, 20)
	for i := range names {
		names[i] = "p" + string(rune('a'+i))
	}
	ps := newPingServer(t)
	ps.set("a.example.com", pingAnswer{json: statusJSON(40, 100, names...)})
	res, err := slpPing(context.Background(), ps.addr(), "a.example.com")
	if err != nil || len(res.Names) != slpMaxNames || res.Names[11] != "pl" {
		t.Fatalf("names = %v err = %v", res.Names, err)
	}
}

func TestSLPPingRejectsHostileAnswers(t *testing.T) {
	deep := strings.Repeat("[", 20000) + strings.Repeat("]", 20000)
	cases := map[string]pingAnswer{
		"oversized packet": {raw: appendVarInt(nil, slpMaxPacket+1)},
		"six-byte varint":  {raw: []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0x01}},
		"negative length":  {raw: appendVarInt(nil, -5)},
		"zero length":      {raw: appendVarInt(nil, 0)},
		"wrong packet id":  {raw: append(appendVarInt(nil, 4), 0x01, 0x02, '{', '}')},
		"string longer than packet": {raw: func() []byte {
			p := append([]byte{0x00}, appendVarInt(nil, 1000)...)
			return append(appendVarInt(nil, int32(len(p))), p...)
		}()},
		"string shorter than packet": {raw: func() []byte {
			p := append([]byte{0x00}, appendVarInt(nil, 2)...)
			p = append(p, `{}xx`...)
			return append(appendVarInt(nil, int32(len(p))), p...)
		}()},
		"truncated":       {raw: appendVarInt(nil, 100)},
		"not json":        {raw: cStatusPacket(`nope`)},
		"json bomb":       {raw: cStatusPacket(deep)},
		"negative online": {raw: cStatusPacket(`{"players":{"max":20,"online":-1}}`)},
		"huge max":        {raw: cStatusPacket(`{"players":{"max":100001,"online":1}}`)},
		"string online":   {raw: cStatusPacket(`{"players":{"max":20,"online":"3"}}`)},
		"float online":    {raw: cStatusPacket(`{"players":{"max":20,"online":2.5}}`)},
		"closed":          {drop: true},
	}
	for name, a := range cases {
		t.Run(name, func(t *testing.T) {
			ps := newPingServer(t)
			ps.set("a.example.com", a)
			if _, err := slpPing(context.Background(), ps.addr(), "a.example.com"); !errors.Is(err, errSLP) {
				t.Fatalf("err = %v, want errSLP", err)
			}
		})
	}
}

func TestSLPPingRejectsInvalidHost(t *testing.T) {
	for _, h := range []string{"", strings.Repeat("a", 256)} {
		if _, err := slpPing(context.Background(), "127.0.0.1:1", h); !errors.Is(err, errSLP) {
			t.Fatalf("host %q: err = %v", h, err)
		}
	}
}

func TestSLPPingSilentServerTimesOut(t *testing.T) {
	ps := newPingServer(t)
	ps.set("a.example.com", pingAnswer{hang: true})
	start := time.Now()
	_, err := slpPing(context.Background(), ps.addr(), "a.example.com")
	if !errors.Is(err, errSLP) || time.Since(start) > pingTimeout+time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
}

func TestSLPPingCancelledContext(t *testing.T) {
	ps := newPingServer(t)
	ps.set("a.example.com", pingAnswer{hang: true})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := slpPing(ctx, ps.addr(), "a.example.com"); !errors.Is(err, errSLP) || time.Since(start) > time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
}

func TestSLPPingRefusedConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	if _, err := slpPing(context.Background(), addr, "a.example.com"); !errors.Is(err, errSLP) {
		t.Fatalf("err = %v", err)
	}
}

func TestVarIntRoundTrip(t *testing.T) {
	for _, v := range []int32{0, 1, 127, 128, 255, 25565, 2097151, 2147483647, -1, -2147483648} {
		got, err := readVarInt(bytes.NewReader(appendVarInt(nil, v)))
		if err != nil || got != v {
			t.Fatalf("%d → %d (%v)", v, got, err)
		}
	}
	if b := appendVarInt(nil, -1); len(b) != 5 {
		t.Fatalf("-1 must take 5 bytes, got %d", len(b))
	}
	if _, err := readVarInt(bytes.NewReader([]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x01})); !errors.Is(err, errVarInt) {
		t.Fatalf("six-byte varint: %v", err)
	}
}
