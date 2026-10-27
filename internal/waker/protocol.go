// Package waker is the listener of the mcpanel-waker container (dev instances spec §6.2):
// while a dev instance sleeps, mc-router sends its hostname here. It speaks just enough of
// the Minecraft protocol to answer a server list ping and to refuse a login with a fixed
// text, and it reports every login attempt as one "wake <host>" line on its output, which
// the agent reads from the container log. Everything it reads comes from the internet, so
// every length is bounded before anything is allocated.
package waker

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

const (
	// MaxPacketBytes bounds one packet (length prefix not counted); a handshake needs at
	// most about 270 bytes.
	MaxPacketBytes = 1024
	// maxVarIntBytes is the longest VarInt of the protocol (32 bits in 7-bit groups).
	maxVarIntBytes = 5
	// maxHostBytes bounds the raw server address of a handshake (the protocol's
	// String(255)), including a Forge suffix like "\x00FML3\x00".
	maxHostBytes = 255

	// Next states of a handshake.
	NextStatus   = 1
	NextLogin    = 2
	NextTransfer = 3 // a login after a transfer packet (1.20.5+)

	legacyPing = 0xFE
)

var (
	// ErrProtocol reports input that is not a valid packet of the expected kind.
	ErrProtocol = errors.New("waker: protocol error")
	// ErrLegacyPing reports the pre-1.7 server list ping (first byte 0xFE); it is not answered.
	ErrLegacyPing = errors.New("waker: legacy server list ping")
)

// Handshake is the first packet of every connection.
type Handshake struct {
	Protocol int32
	// Host is the normalised server address: cut at the first NUL (Forge appends
	// "\x00FML…\x00"), one trailing dot removed, ASCII lower case; always a valid DNS name.
	Host string
	Port uint16
	Next int32
}

// ReadHandshake reads exactly one packet from r and parses it as a handshake. It never reads
// more than one bounded packet, so a login start that follows stays in r.
func ReadHandshake(r io.Reader) (Handshake, error) {
	var first [1]byte
	if _, err := io.ReadFull(r, first[:]); err != nil {
		return Handshake{}, fmt.Errorf("%w: %w", ErrProtocol, err)
	}
	if first[0] == legacyPing {
		return Handshake{}, ErrLegacyPing
	}
	id, body, err := readPacketAfter(r, first[0])
	if err != nil {
		return Handshake{}, err
	}
	if id != 0x00 {
		return Handshake{}, fmt.Errorf("%w: handshake packet id %d", ErrProtocol, id)
	}
	br := bytes.NewReader(body)
	var h Handshake
	if h.Protocol, err = readVarInt(br); err != nil {
		return Handshake{}, err
	}
	raw, err := readString(br, maxHostBytes)
	if err != nil {
		return Handshake{}, err
	}
	if err := binary.Read(br, binary.BigEndian, &h.Port); err != nil {
		return Handshake{}, fmt.Errorf("%w: port: %w", ErrProtocol, err)
	}
	if h.Next, err = readVarInt(br); err != nil {
		return Handshake{}, err
	}
	if br.Len() != 0 {
		return Handshake{}, fmt.Errorf("%w: %d bytes after the handshake", ErrProtocol, br.Len())
	}
	if h.Next != NextStatus && h.Next != NextLogin && h.Next != NextTransfer {
		return Handshake{}, fmt.Errorf("%w: next state %d", ErrProtocol, h.Next)
	}
	if h.Host, err = normaliseHost(raw); err != nil {
		return Handshake{}, err
	}
	return h, nil
}

// normaliseHost turns a raw server address into the FQDN mc-router routed: up to the first
// NUL, without one trailing dot, lower case. Only ASCII is accepted (DNS names are ASCII,
// and unicode case folding would map e.g. the Kelvin sign onto "k").
func normaliseHost(raw string) (string, error) {
	if !utf8.ValidString(raw) {
		return "", fmt.Errorf("%w: server address is not UTF-8", ErrProtocol)
	}
	host, _, _ := strings.Cut(raw, "\x00")
	host = strings.TrimSuffix(host, ".")
	b := []byte(host)
	for i, c := range b {
		switch {
		case c >= 0x80:
			return "", fmt.Errorf("%w: server address is not ASCII", ErrProtocol)
		case 'A' <= c && c <= 'Z':
			b[i] = c + ('a' - 'A')
		}
	}
	host = string(b)
	if err := instancespec.CheckDNSName(host); err != nil {
		return "", fmt.Errorf("%w: server address: %w", ErrProtocol, err)
	}
	return host, nil
}

// readPacket reads one length-prefixed packet and returns its id and the rest of its body.
func readPacket(r io.Reader) (int32, []byte, error) {
	var first [1]byte
	if _, err := io.ReadFull(r, first[:]); err != nil {
		return 0, nil, fmt.Errorf("%w: %w", ErrProtocol, err)
	}
	return readPacketAfter(r, first[0])
}

// readPacketAfter is readPacket whose first length byte was already read.
func readPacketAfter(r io.Reader, first byte) (int32, []byte, error) {
	n, err := readVarIntAfter(r, first)
	if err != nil {
		return 0, nil, err
	}
	if n < 1 || n > MaxPacketBytes {
		return 0, nil, fmt.Errorf("%w: packet length %d", ErrProtocol, n)
	}
	p := make([]byte, n)
	if _, err := io.ReadFull(r, p); err != nil {
		return 0, nil, fmt.Errorf("%w: %w", ErrProtocol, err)
	}
	br := bytes.NewReader(p)
	id, err := readVarInt(br)
	if err != nil {
		return 0, nil, err
	}
	return id, p[len(p)-br.Len():], nil
}

// readString reads a VarInt-prefixed string of at most limit bytes.
func readString(br *bytes.Reader, limit int) (string, error) {
	n, err := readVarInt(br)
	if err != nil {
		return "", err
	}
	if n < 0 || int(n) > limit || int(n) > br.Len() {
		return "", fmt.Errorf("%w: string length %d", ErrProtocol, n)
	}
	b := make([]byte, n)
	_, _ = io.ReadFull(br, b) // cannot fail: n <= br.Len()
	return string(b), nil
}

func readVarInt(r io.Reader) (int32, error) {
	var first [1]byte
	if _, err := io.ReadFull(r, first[:]); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrProtocol, err)
	}
	return readVarIntAfter(r, first[0])
}

// readVarIntAfter reads a VarInt whose first byte is b; more than five bytes are an error.
func readVarIntAfter(r io.Reader, b byte) (int32, error) {
	var v uint32
	var buf [1]byte
	for i := range maxVarIntBytes {
		v |= uint32(b&0x7F) << (7 * i)
		if b&0x80 == 0 {
			return int32(v), nil //nolint:gosec // G115: the protocol's VarInt is a two's complement int32
		}
		if i == maxVarIntBytes-1 {
			break
		}
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return 0, fmt.Errorf("%w: %w", ErrProtocol, err)
		}
		b = buf[0]
	}
	return 0, fmt.Errorf("%w: VarInt longer than %d bytes", ErrProtocol, maxVarIntBytes)
}

func appendVarInt(b []byte, v int32) []byte {
	u := uint32(v) //nolint:gosec // G115: the protocol's VarInt is a two's complement int32
	for u >= 0x80 {
		b = append(b, byte(u)|0x80)
		u >>= 7
	}
	return append(b, byte(u))
}

func appendString(b []byte, s string) []byte {
	b = appendVarInt(b, int32(len(s))) //nolint:gosec // G115: only the server's own short JSON texts
	return append(b, s...)
}

// packet frames id and body with the length prefix.
func packet(id int32, body []byte) []byte {
	p := appendVarInt(nil, id)
	p = append(p, body...)
	return append(appendVarInt(nil, int32(len(p))), p...) //nolint:gosec // G115: packets the server builds are small
}

type textComponent struct {
	Text string `json:"text"`
}

// statusResponse is the answer to a status request: the protocol is echoed so the client
// shows the entry as compatible, with 0 of 0 players and motd as the description.
func statusResponse(protocol int32, motd string) ([]byte, error) {
	type version struct {
		Name     string `json:"name"`
		Protocol int32  `json:"protocol"`
	}
	type players struct {
		Max    int `json:"max"`
		Online int `json:"online"`
	}
	j, err := json.Marshal(struct {
		Version     version       `json:"version"`
		Players     players       `json:"players"`
		Description textComponent `json:"description"`
	}{version{"mc-panel", protocol}, players{}, textComponent{motd}})
	if err != nil {
		return nil, err
	}
	return packet(0x00, appendString(nil, string(j))), nil
}

// loginDisconnect is the login state's disconnect packet with text as its reason.
func loginDisconnect(text string) ([]byte, error) {
	j, err := json.Marshal(textComponent{text})
	if err != nil {
		return nil, err
	}
	return packet(0x00, appendString(nil, string(j))), nil
}
