package instances

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"math/bits"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

// procRoute is the kernel's IPv4 routing table (main table).
const procRoute = "/proc/net/route"

// HostNetworks returns the IPv4 networks the host uses itself: the networks of its
// interface addresses and the destinations of its routes (routed LAN or VPN prefixes),
// without loopback, default routes and the interfaces in skip (Docker's own bridges,
// whose networks come from Docker). A system without /proc/net/route (macOS) only gives
// the interface addresses.
func HostNetworks(skip map[string]bool) ([]netip.Prefix, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []netip.Prefix
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagLoopback != 0 || skip[ifc.Name] {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			return nil, err
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if p, err := netip.ParsePrefix(ipn.String()); err == nil && p.Addr().Is4() {
				out = appendPrefix(out, p.Masked())
			}
		}
	}
	f, err := os.Open(procRoute)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	routes, err := ParseRoutes(f, skip)
	if err != nil {
		return nil, err
	}
	for _, p := range routes {
		out = appendPrefix(out, p)
	}
	return out, nil
}

func appendPrefix(list []netip.Prefix, p netip.Prefix) []netip.Prefix {
	for _, have := range list {
		if have == p {
			return list
		}
	}
	return append(list, p)
}

// ParseRoutes reads /proc/net/route and returns the destinations of the routes that are
// up, without default routes, loopback and the interfaces in skip.
func ParseRoutes(r io.Reader, skip map[string]bool) ([]netip.Prefix, error) {
	var out []netip.Prefix
	sc := bufio.NewScanner(r)
	first := true
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if first { // header
			first = false
			continue
		}
		if len(f) < 8 || skip[f[0]] || f[0] == "lo" {
			continue
		}
		flags, err := strconv.ParseUint(f[3], 16, 32)
		if err != nil || flags&0x1 == 0 { // RTF_UP
			continue
		}
		dst, err1 := hexIPv4(f[1])
		mask, err2 := hexIPv4(f[7])
		if err1 != nil || err2 != nil {
			return nil, errors.New("routes: malformed line " + strconv.Quote(sc.Text()))
		}
		m := binary.BigEndian.Uint32(mask[:])
		ones := bits.OnesCount32(m)
		if ones == 0 || m != ^uint32(0)<<(32-ones) {
			continue // default route, or a non-contiguous mask
		}
		out = appendPrefix(out, netip.PrefixFrom(netip.AddrFrom4(dst), ones).Masked())
	}
	return out, sc.Err()
}

// hexIPv4 decodes an address of /proc/net/route (8 hex digits, host byte order, i.e.
// little-endian on every platform the agent runs on).
func hexIPv4(s string) ([4]byte, error) {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 4 {
		return [4]byte{}, errors.New("routes: bad address " + strconv.Quote(s))
	}
	return [4]byte{b[3], b[2], b[1], b[0]}, nil
}
