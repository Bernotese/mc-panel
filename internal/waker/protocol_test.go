package waker

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
)

// handshake builds a handshake packet as a client sends it.
func handshake(protocol int32, host string, port uint16, next int32) []byte {
	var b []byte
	b = appendVarInt(b, 0x00)
	b = appendVarInt(b, protocol)
	b = appendVarInt(b, int32(len(host)))
	b = append(b, host...)
	b = binary.BigEndian.AppendUint16(b, port)
	b = appendVarInt(b, next)
	return append(appendVarInt(nil, int32(len(b))), b...)
}

func TestReadHandshake(t *testing.T) {
	long := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 62)
	for _, tc := range []struct {
		name string
		in   []byte
		want Handshake
		err  bool
	}{
		{name: "1.21 login", in: handshake(767, "dev-a.example.com", 25565, 2),
			want: Handshake{Protocol: 767, Host: "dev-a.example.com", Port: 25565, Next: 2}},
		{name: "status", in: handshake(-1, "dev-a.example.com", 25565, 1),
			want: Handshake{Protocol: -1, Host: "dev-a.example.com", Port: 25565, Next: 1}},
		{name: "transfer", in: handshake(767, "dev-a.example.com", 25565, 3),
			want: Handshake{Protocol: 767, Host: "dev-a.example.com", Port: 25565, Next: 3}},
		{name: "fml suffix", in: handshake(767, "dev-a.example.com\x00FML3\x00", 25565, 2),
			want: Handshake{Protocol: 767, Host: "dev-a.example.com", Port: 25565, Next: 2}},
		{name: "upper case and trailing dot", in: handshake(767, "DEV-A.example.com.", 25565, 2),
			want: Handshake{Protocol: 767, Host: "dev-a.example.com", Port: 25565, Next: 2}},
		{name: "varint of 6 bytes", in: []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x01}, err: true},
		{name: "length 1025", in: append(appendVarInt(nil, 1025), make([]byte, 1025)...), err: true},
		{name: "length 0", in: []byte{0x00}, err: true},
		{name: "negative length", in: appendVarInt(nil, -1), err: true},
		{name: "truncated", in: handshake(767, "dev-a.example.com", 25565, 2)[:10], err: true},
		{name: "empty", in: nil, err: true},
		{name: "legacy ping", in: []byte{0xFE, 0x01, 0xFA}, err: true},
		{name: "wrong packet id", in: append([]byte{0x02, 0x01}, 0x00), err: true},
		{name: "next state 0", in: handshake(767, "dev-a.example.com", 25565, 0), err: true},
		{name: "next state 4", in: handshake(767, "dev-a.example.com", 25565, 4), err: true},
		{name: "host 256 bytes", in: handshake(767, strings.Repeat("a", 256), 25565, 2), err: true},
		{name: "host of 254 characters", in: handshake(767, long, 25565, 2), err: true},
		{name: "invalid utf-8", in: handshake(767, "dev-a.\xffexample.com", 25565, 2), err: true},
		{name: "kelvin sign", in: handshake(767, "dev-a.examplK.com", 25565, 2), err: true},
		{name: "empty host", in: handshake(767, "", 25565, 2), err: true},
		{name: "only fml", in: handshake(767, "\x00FML3\x00", 25565, 2), err: true},
		{name: "ip address port", in: handshake(767, "dev-a.example.com:25565", 25565, 2), err: true},
		{name: "space", in: handshake(767, "dev-a .example.com", 25565, 2), err: true},
		{name: "two trailing dots", in: handshake(767, "dev-a.example.com..", 25565, 2), err: true},
		{name: "trailing bytes in packet", in: func() []byte {
			p := handshake(767, "dev-a.example.com", 25565, 2)
			p[0]++ // one more byte than the fields
			return append(p, 0x00)
		}(), err: true},
		{name: "string longer than packet", in: func() []byte {
			var b []byte
			b = appendVarInt(b, 0x00)
			b = appendVarInt(b, 767)
			b = appendVarInt(b, 200)
			b = append(b, "dev-a"...)
			return append(appendVarInt(nil, int32(len(b))), b...)
		}(), err: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ReadHandshake(bytes.NewReader(tc.in))
			if tc.err {
				if err == nil {
					t.Fatalf("ReadHandshake = %+v, want an error", got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("ReadHandshake = %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}

func TestReadHandshakeLegacy(t *testing.T) {
	if _, err := ReadHandshake(bytes.NewReader([]byte{0xFE})); !errors.Is(err, ErrLegacyPing) {
		t.Fatalf("err = %v, want ErrLegacyPing", err)
	}
}

// TestReadHandshakeReadsOnlyOnePacket leaves the next packet (login start) in the reader.
func TestReadHandshakeReadsOnlyOnePacket(t *testing.T) {
	login := []byte{0x03, 0x00, 0x01, 'x'}
	r := bytes.NewReader(append(handshake(767, "dev-a.example.com", 25565, 2), login...))
	if _, err := ReadHandshake(r); err != nil {
		t.Fatal(err)
	}
	rest, _ := io.ReadAll(r)
	if !bytes.Equal(rest, login) {
		t.Fatalf("rest = % x, want % x", rest, login)
	}
}

func TestReadPacketLimits(t *testing.T) {
	ok := append(appendVarInt(nil, MaxPacketBytes), make([]byte, MaxPacketBytes)...)
	if _, _, err := readPacket(bytes.NewReader(ok)); err != nil {
		t.Fatalf("packet of %d bytes: %v", MaxPacketBytes, err)
	}
	big := append(appendVarInt(nil, MaxPacketBytes+1), make([]byte, MaxPacketBytes+1)...)
	if _, _, err := readPacket(bytes.NewReader(big)); err == nil {
		t.Fatal("packet above the limit accepted")
	}
}

func TestVarIntRoundTrip(t *testing.T) {
	for _, v := range []int32{0, 1, 127, 128, 255, 25565, 2097151, 2147483647, -1, -2147483648} {
		got, err := readVarInt(bytes.NewReader(appendVarInt(nil, v)))
		if err != nil || got != v {
			t.Fatalf("varint %d: got %d, %v", v, got, err)
		}
	}
}

// countingReader counts the bytes read; a fuzz input is never read beyond one bounded packet.
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

func FuzzReadHandshake(f *testing.F) {
	f.Add(handshake(767, "dev-a.example.com", 25565, 2))
	f.Add(handshake(-1, "dev-a.example.com\x00FML3\x00", 25565, 1))
	f.Add(handshake(767, "DEV-A.example.com.", 25565, 3))
	f.Add([]byte{0xFE, 0x01, 0xFA})
	f.Add([]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x01})
	f.Add(append(appendVarInt(nil, 1025), make([]byte, 1025)...))
	f.Fuzz(func(t *testing.T, in []byte) {
		cr := &countingReader{r: bytes.NewReader(in)}
		h, err := ReadHandshake(cr)
		if cr.n > MaxPacketBytes+maxVarIntBytes {
			t.Fatalf("read %d bytes", cr.n)
		}
		if err != nil {
			return
		}
		if h.Host == "" || len(h.Host) > 253 || h.Host != strings.ToLower(h.Host) || strings.HasSuffix(h.Host, ".") ||
			strings.ContainsAny(h.Host, "\x00 :/") || h.Next < 1 || h.Next > 3 {
			t.Fatalf("accepted %+v", h)
		}
	})
}
