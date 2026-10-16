package firewall

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
	"net/netip"
	"slices"
	"sort"
	"time"
)

// This file holds the pure logic of the nftables backend. It must not import
// github.com/google/nftables so that it builds and is tested on every OS.

// u128 is a 128-bit unsigned integer used for IPv6 interval arithmetic.
type u128 struct{ hi, lo uint64 }

func u128From(b [16]byte) u128 {
	return u128{hi: binary.BigEndian.Uint64(b[:8]), lo: binary.BigEndian.Uint64(b[8:])}
}

func (u u128) bytes() [16]byte {
	var b [16]byte
	binary.BigEndian.PutUint64(b[:8], u.hi)
	binary.BigEndian.PutUint64(b[8:], u.lo)
	return b
}

// add returns u+v and whether the addition overflowed 2^128.
func (u u128) add(v u128) (u128, bool) {
	lo, carry := bits.Add64(u.lo, v.lo, 0)
	hi, carry := bits.Add64(u.hi, v.hi, carry)
	return u128{hi: hi, lo: lo}, carry != 0
}

// sub returns u-v modulo 2^128.
func (u u128) sub(v u128) u128 {
	lo, borrow := bits.Sub64(u.lo, v.lo, 0)
	hi, _ := bits.Sub64(u.hi, v.hi, borrow)
	return u128{hi: hi, lo: lo}
}

func (u u128) cmp(v u128) int {
	switch {
	case u.hi < v.hi:
		return -1
	case u.hi > v.hi:
		return 1
	case u.lo < v.lo:
		return -1
	case u.lo > v.lo:
		return 1
	}
	return 0
}

// pow2 returns 2^k for 0 <= k <= 127.
func pow2(k int) u128 {
	if k >= 64 {
		return u128{hi: 1 << (k - 64)}
	}
	return u128{lo: 1 << k}
}

// log2 returns k if u == 2^k.
func (u u128) log2() (int, bool) {
	switch {
	case u.hi == 0 && u.lo != 0 && u.lo&(u.lo-1) == 0:
		return bits.TrailingZeros64(u.lo), true
	case u.lo == 0 && u.hi != 0 && u.hi&(u.hi-1) == 0:
		return 64 + bits.TrailingZeros64(u.hi), true
	}
	return 0, false
}

// elementSpec describes the set element(s) representing one banned prefix.
type elementSpec struct {
	Set string
	Key []byte
	End []byte
}

// elementFor maps a canonical ban prefix to its set element(s): IPv4 /32 ->
// one key in mcpanel_jail4; IPv6 /64../128 -> interval [start, end) in
// mcpanel_jail6.
func elementFor(p netip.Prefix) (elementSpec, error) {
	if !p.IsValid() || p != p.Masked() || p.Addr().Is4In6() {
		return elementSpec{}, fmt.Errorf("firewall: %s is not a canonical prefix", p)
	}
	a := p.Addr()
	if a.Is4() {
		if p.Bits() != 32 {
			return elementSpec{}, fmt.Errorf("firewall: IPv4 bans must be /32, got %s", p)
		}
		k := a.As4()
		return elementSpec{Set: Set4Name, Key: k[:]}, nil
	}
	if p.Bits() < 64 {
		return elementSpec{}, fmt.Errorf("firewall: IPv6 bans must be /64 or narrower, got %s", p)
	}
	start := a.As16()
	spec := elementSpec{Set: Set6Name, Key: start[:]}
	if end, overflow := u128From(start).add(pow2(128 - p.Bits())); !overflow {
		e := end.bytes()
		spec.End = e[:]
	}
	return spec, nil
}

// prefixFromRange converts an IPv6 interval [start, end) back into a prefix.
// end == nil means the interval reaches the top of the address space.
func prefixFromRange(start, end []byte) (netip.Prefix, error) {
	if len(start) != 16 || (end != nil && len(end) != 16) {
		return netip.Prefix{}, fmt.Errorf("firewall: bad interval key lengths %d/%d", len(start), len(end))
	}
	s := u128From([16]byte(start))
	var size u128
	if end == nil {
		if s == (u128{}) {
			return netip.Prefix{}, errors.New("firewall: interval covers the whole address space")
		}
		size = u128{}.sub(s)
	} else {
		e := u128From([16]byte(end))
		if e.cmp(s) <= 0 {
			return netip.Prefix{}, fmt.Errorf("firewall: interval end %x is not after start %x", end, start)
		}
		size = e.sub(s)
	}
	k, ok := size.log2()
	if !ok {
		return netip.Prefix{}, errors.New("firewall: interval size is not a power of two")
	}
	p := netip.PrefixFrom(netip.AddrFrom16([16]byte(start)), 128-k)
	if p.Masked() != p {
		return netip.Prefix{}, fmt.Errorf("firewall: interval start %s is not aligned to /%d", p.Addr(), 128-k)
	}
	return p, nil
}

// rawElem is the OS-independent view of a google/nftables SetElement.
type rawElem struct {
	Key         []byte
	IntervalEnd bool
	Timeout     time.Duration
	Expires     time.Duration
}

// entriesFromElements reconstructs bans from the raw elements of both sets.
// Expired elements are dropped; malformed ones are counted in skipped.
func entriesFromElements(v4, v6 []rawElem) ([]entry, int) {
	var entries []entry
	skipped := 0
	expired := func(e rawElem) bool { return e.Timeout > 0 && e.Expires <= 0 }

	for _, e := range v4 {
		if e.IntervalEnd || len(e.Key) != 4 {
			skipped++
			continue
		}
		if expired(e) {
			continue
		}
		entries = append(entries, entry{prefix: netip.PrefixFrom(netip.AddrFrom4([4]byte(e.Key)), 32), remaining: e.Expires})
	}

	var ends []u128
	for _, e := range v6 {
		if !e.IntervalEnd {
			continue
		}
		if len(e.Key) != 16 {
			skipped++
			continue
		}
		ends = append(ends, u128From([16]byte(e.Key)))
	}
	slices.SortFunc(ends, u128.cmp)

	for _, e := range v6 {
		if e.IntervalEnd {
			continue
		}
		if len(e.Key) != 16 {
			skipped++
			continue
		}
		if expired(e) {
			continue
		}
		s := u128From([16]byte(e.Key))
		// Intervals never overlap, so the matching end is the smallest end > start.
		i := sort.Search(len(ends), func(i int) bool { return ends[i].cmp(s) > 0 })
		var end []byte
		if i < len(ends) {
			b := ends[i].bytes()
			end = b[:]
		}
		p, err := prefixFromRange(e.Key, end)
		if err != nil {
			skipped++
			continue
		}
		entries = append(entries, entry{prefix: p, remaining: e.Expires})
	}
	return entries, skipped
}

// splitExisting reports whether p is currently banned and returns all other
// banned prefixes (for the overlap check).
func splitExisting(p netip.Prefix, entries []entry) (bool, []netip.Prefix) {
	exists := false
	var others []netip.Prefix
	for _, e := range entries {
		if e.prefix == p {
			exists = true
			continue
		}
		others = append(others, e.prefix)
	}
	return exists, others
}

// egressElements maps the egress deny prefixes to interval set elements: the start
// key and, unless the range reaches the top of the address space, the exclusive end
// key. Prefixes are canonical and do not overlap (validated in config).
func egressElements(deny []netip.Prefix) (v4, v6 []rawElem) {
	for _, p := range deny {
		a := p.Addr()
		if a.Is4() {
			k := a.As4()
			v4 = append(v4, rawElem{Key: k[:]})
			if end := uint64(binary.BigEndian.Uint32(k[:])) + 1<<(32-p.Bits()); end <= 0xffffffff {
				v4 = append(v4, rawElem{Key: binary.BigEndian.AppendUint32(nil, uint32(end)), IntervalEnd: true})
			}
			continue
		}
		k := a.As16()
		v6 = append(v6, rawElem{Key: k[:]})
		if p.Bits() > 0 {
			if end, overflow := u128From(k).add(pow2(128 - p.Bits())); !overflow {
				e := end.bytes()
				v6 = append(v6, rawElem{Key: e[:], IntervalEnd: true})
			}
		}
	}
	return v4, v6
}

// egressSubnets checks the container subnets a filter rule is built for: each must be
// valid, canonical, not IPv4-mapped and not /0 (that would exempt every destination);
// duplicates are dropped. An unusable subnet or an empty list is an error: the filter
// must never silently cover less than the network.
func egressSubnets(subnets []netip.Prefix) ([]netip.Prefix, error) {
	if len(subnets) == 0 {
		return nil, errors.New("firewall: egress filter: the container network has no subnet")
	}
	var out []netip.Prefix
	for _, p := range subnets {
		if !p.IsValid() || p != p.Masked() || p.Addr().Is4In6() || p.Bits() == 0 {
			return nil, fmt.Errorf("firewall: egress filter: unusable container subnet %s", p)
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out, nil
}
