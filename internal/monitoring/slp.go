package monitoring

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"time"
)

// Server List Ping (spec §3.2): the answer comes from a server we do not trust, so every
// length is bounded before anything is allocated and only three fields are read.
const (
	slpMaxPacket = 64 << 10
	slpMaxNames  = 12
	slpMaxCount  = 100000
	slpMaxHost   = 255
	slpPort      = 25565
)

var (
	errSLP    = errors.New("monitoring: server list ping failed")
	errVarInt = errors.New("varint too long")
	slpNameRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,16}$`)
)

type slpResult struct {
	Online *int
	Max    *int
	Names  []string
}

// slpPing connects to addr (mc-router) and asks for the status of host. The whole exchange
// has pingTimeout; every error wraps errSLP.
func slpPing(ctx context.Context, addr, host string) (slpResult, error) {
	if host == "" || len(host) > slpMaxHost {
		return slpResult{}, fmt.Errorf("%w: invalid host", errSLP)
	}
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return slpResult{}, fmt.Errorf("%w: %w", errSLP, err)
	}
	defer func() { _ = conn.Close() }()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return slpResult{}, fmt.Errorf("%w: %w", errSLP, err)
	}
	// A cancelled parent (panel shutdown) ends the exchange before the deadline.
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()

	if _, err := conn.Write(append(handshakePacket(host), statusRequest()...)); err != nil {
		return slpResult{}, fmt.Errorf("%w: %w", errSLP, err)
	}
	payload, err := readStatus(bufio.NewReader(conn))
	if err != nil {
		return slpResult{}, fmt.Errorf("%w: %w", errSLP, err)
	}
	return parseStatus(payload)
}

// handshakePacket: id 0, protocol -1 (status only), server address, port, next state 1.
func handshakePacket(host string) []byte {
	var b []byte
	b = appendVarInt(b, 0x00)
	b = appendVarInt(b, -1)
	b = appendVarInt(b, int32(len(host)))
	b = append(b, host...)
	b = binary.BigEndian.AppendUint16(b, slpPort)
	b = appendVarInt(b, 1)
	return append(appendVarInt(nil, int32(len(b))), b...)
}

func statusRequest() []byte { return []byte{0x01, 0x00} } // length 1, packet id 0

// readStatus reads one status response packet and returns its JSON string.
func readStatus(br *bufio.Reader) ([]byte, error) {
	n, err := readVarInt(br)
	if err != nil {
		return nil, err
	}
	if n <= 0 || n > slpMaxPacket {
		return nil, fmt.Errorf("packet length %d", n)
	}
	p := make([]byte, n)
	if _, err := io.ReadFull(br, p); err != nil {
		return nil, err
	}
	r := bytes.NewReader(p)
	id, err := readVarInt(r)
	if err != nil {
		return nil, err
	}
	if id != 0 {
		return nil, fmt.Errorf("packet id %d", id)
	}
	sl, err := readVarInt(r)
	if err != nil {
		return nil, err
	}
	if sl < 0 || int(sl) != r.Len() {
		return nil, fmt.Errorf("string length %d, %d bytes left", sl, r.Len())
	}
	return p[len(p)-int(sl):], nil
}

func parseStatus(payload []byte) (slpResult, error) {
	var st struct {
		Players *struct {
			Max    *int `json:"max"`
			Online *int `json:"online"`
			Sample []struct {
				Name string `json:"name"`
			} `json:"sample"`
		} `json:"players"`
	}
	if err := json.Unmarshal(payload, &st); err != nil {
		return slpResult{}, fmt.Errorf("%w: json: %w", errSLP, err)
	}
	var res slpResult
	if st.Players == nil {
		return res, nil
	}
	for _, v := range []*int{st.Players.Max, st.Players.Online} {
		if v != nil && (*v < 0 || *v > slpMaxCount) {
			return slpResult{}, fmt.Errorf("%w: player count %d out of range", errSLP, *v)
		}
	}
	res.Online, res.Max = st.Players.Online, st.Players.Max
	for _, s := range st.Players.Sample {
		if len(res.Names) == slpMaxNames {
			break
		}
		if slpNameRe.MatchString(s.Name) {
			res.Names = append(res.Names, s.Name)
		}
	}
	return res, nil
}

func appendVarInt(b []byte, v int32) []byte {
	u := uint32(v)
	for u >= 0x80 {
		b = append(b, byte(u)|0x80)
		u >>= 7
	}
	return append(b, byte(u))
}

// readVarInt reads at most 5 bytes (a 32-bit VarInt).
func readVarInt(r io.ByteReader) (int32, error) {
	var u uint32
	for i := range 5 {
		c, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		u |= uint32(c&0x7f) << (7 * i)
		if c&0x80 == 0 {
			return int32(u), nil
		}
	}
	return 0, errVarInt
}
