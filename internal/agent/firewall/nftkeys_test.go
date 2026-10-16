package firewall

import (
	"bytes"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

func a16(s string) []byte {
	b := netip.MustParseAddr(s).As16()
	return b[:]
}

func TestElementForIPv4(t *testing.T) {
	spec, err := elementFor(netip.MustParsePrefix("203.0.113.7/32"))
	if err != nil {
		t.Fatal(err)
	}
	if spec.Set != Set4Name || !bytes.Equal(spec.Key, []byte{203, 0, 113, 7}) || spec.End != nil {
		t.Fatalf("spec = %+v", spec)
	}
}

func TestElementForIPv6(t *testing.T) {
	cases := []struct{ prefix, end string }{
		{"2001:db8:1:2::/64", "2001:db8:1:3::"},
		{"2001:db8::1/128", "2001:db8::2"},
		{"2001:db8:0:ffff::/64", "2001:db8:1::"},
		{"2001:db8:1:2:3::/80", "2001:db8:1:2:4::"},
		{"2001:db8:ffff:ffff:ffff:ffff:ffff:ffff/128", "2001:db9::"},
	}
	for _, tc := range cases {
		p := netip.MustParsePrefix(tc.prefix)
		spec, err := elementFor(p)
		if err != nil {
			t.Fatalf("elementFor(%s): %v", tc.prefix, err)
		}
		if spec.Set != Set6Name || !bytes.Equal(spec.Key, a16(p.Addr().String())) || !bytes.Equal(spec.End, a16(tc.end)) {
			t.Errorf("elementFor(%s) = %+v, want end %s", tc.prefix, spec, tc.end)
		}
	}
	top, err := elementFor(netip.MustParsePrefix("ffff:ffff:ffff:ffff::/64"))
	if err != nil || top.End != nil {
		t.Fatalf("top-of-space range: spec=%+v err=%v, want End == nil", top, err)
	}
}

func TestElementForRejects(t *testing.T) {
	bad := []netip.Prefix{
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("2001:db8:1:2::5/64"),
		netip.MustParsePrefix("::ffff:203.0.113.7/128"),
		netip.MustParsePrefix("2001:db8::/48"),
		{},
	}
	for _, p := range bad {
		if spec, err := elementFor(p); err == nil {
			t.Errorf("elementFor(%s) = %+v, want error", p, spec)
		}
	}
}

func TestPrefixFromRangeRoundTrip(t *testing.T) {
	for _, s := range []string{
		"2001:db8:1:2::/64", "2001:db8::1/128", "2001:db8:0:ffff::/64",
		"2001:db8:1:2:3::/80", "ffff:ffff:ffff:ffff::/64", "2001:db8:1:2::/127",
	} {
		want := netip.MustParsePrefix(s)
		spec, err := elementFor(want)
		if err != nil {
			t.Fatalf("elementFor(%s): %v", s, err)
		}
		got, err := prefixFromRange(spec.Key, spec.End)
		if err != nil || got != want {
			t.Errorf("prefixFromRange(elementFor(%s)) = %v, %v", s, got, err)
		}
	}
}

func TestPrefixFromRangeRejects(t *testing.T) {
	cases := []struct {
		name       string
		start, end []byte
	}{
		{"empty range", a16("2001:db8::"), a16("2001:db8::")},
		{"reversed", a16("2001:db8::2"), a16("2001:db8::1")},
		{"size not power of two", a16("2001:db8::"), a16("2001:db8::3")},
		{"misaligned start", a16("2001:db8::1"), a16("2001:db8::3")},
		{"short key", []byte{1, 2, 3, 4}, a16("2001:db8::1")},
		{"whole address space", a16("::"), nil},
		{"open range not a prefix", a16("2001:db8:5::1"), nil},
	}
	for _, tc := range cases {
		if p, err := prefixFromRange(tc.start, tc.end); err == nil {
			t.Errorf("%s: prefixFromRange = %v, want error", tc.name, p)
		}
	}
}

func TestEntriesFromElements(t *testing.T) {
	v4 := []rawElem{
		{Key: []byte{203, 0, 113, 7}, Timeout: 15 * time.Minute, Expires: 1500 * time.Millisecond},
		{Key: []byte{198, 51, 100, 1}, Timeout: time.Second, Expires: 0},   // expired, not yet collected
		{Key: []byte{1, 2, 3}, Timeout: time.Minute, Expires: time.Minute}, // garbage
	}
	v6 := []rawElem{
		{Key: a16("2001:db8:1:3::"), IntervalEnd: true},                                    // end of A == start of B
		{Key: a16("2001:db8:1:3::"), Timeout: time.Hour, Expires: 30 * time.Minute},        // start of B
		{Key: a16("::"), IntervalEnd: true},                                                // stray end element
		{Key: a16("2001:db8:1:2::"), Timeout: time.Hour, Expires: time.Hour},               // start of A
		{Key: a16("2001:db8:1:4::"), IntervalEnd: true},                                    // end of B
		{Key: a16("ffff:ffff:ffff:ffff::"), Timeout: time.Hour, Expires: 10 * time.Second}, // reaches the top, no end
		{Key: a16("2001:db8:5::1"), Timeout: time.Hour, Expires: time.Hour},                // no matching end -> not a prefix
		{Key: a16("2001:db8:7::"), Timeout: time.Second, Expires: 0},                       // expired start
	}
	entries, skipped := entriesFromElements(v4, v6)
	if skipped != 2 {
		t.Errorf("skipped = %d, want 2", skipped)
	}
	got := toBans(entries)
	want := []agentapi.Ban{
		{Prefix: "203.0.113.7/32", ExpiresInSeconds: 2},
		{Prefix: "2001:db8:1:2::/64", ExpiresInSeconds: 3600},
		{Prefix: "2001:db8:1:3::/64", ExpiresInSeconds: 1800},
		{Prefix: "ffff:ffff:ffff:ffff::/64", ExpiresInSeconds: 10},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("bans = %+v\nwant   %+v", got, want)
	}
}

func TestSplitExisting(t *testing.T) {
	a := netip.MustParsePrefix("203.0.113.7/32")
	b := netip.MustParsePrefix("2001:db8:1:2::/64")
	entries := []entry{{prefix: a}, {prefix: b}}
	exists, others := splitExisting(a, entries)
	if !exists || !slices.Equal(others, []netip.Prefix{b}) {
		t.Fatalf("splitExisting(a) = %v, %v", exists, others)
	}
	exists, others = splitExisting(netip.MustParsePrefix("198.51.100.1/32"), entries)
	if exists || len(others) != 2 {
		t.Fatalf("splitExisting(unknown) = %v, %v", exists, others)
	}
}

func TestEgressElements(t *testing.T) {
	v4, v6 := egressElements([]netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("fe80::/10"),
		netip.MustParsePrefix("169.254.0.0/16"),
		netip.MustParsePrefix("255.255.255.255/32"), // reaches the top: no end element
		netip.MustParsePrefix("fc00::/7"),
	})
	want4 := []rawElem{
		{Key: []byte{10, 0, 0, 0}}, {Key: []byte{11, 0, 0, 0}, IntervalEnd: true},
		{Key: []byte{169, 254, 0, 0}}, {Key: []byte{169, 255, 0, 0}, IntervalEnd: true},
		{Key: []byte{255, 255, 255, 255}},
	}
	want6 := []rawElem{
		{Key: a16("fe80::")}, {Key: a16("fec0::"), IntervalEnd: true},
		{Key: a16("fc00::")}, {Key: a16("fe00::"), IntervalEnd: true},
	}
	eq := func(a, b rawElem) bool { return bytes.Equal(a.Key, b.Key) && a.IntervalEnd == b.IntervalEnd }
	if !slices.EqualFunc(v4, want4, eq) || !slices.EqualFunc(v6, want6, eq) {
		t.Fatalf("v4 = %v\nv6 = %v", v4, v6)
	}
	if v4, v6 := egressElements(nil); len(v4) != 0 || len(v6) != 0 {
		t.Fatalf("empty list = %v %v", v4, v6)
	}
	if v4, _ := egressElements([]netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}); len(v4) != 1 || !bytes.Equal(v4[0].Key, []byte{0, 0, 0, 0}) {
		t.Fatalf("0.0.0.0/0 = %v", v4)
	}
}

func TestEgressSubnets(t *testing.T) {
	got, err := egressSubnets([]netip.Prefix{
		netip.MustParsePrefix("172.18.0.0/16"),
		netip.MustParsePrefix("fd00:1::/64"),
		netip.MustParsePrefix("172.18.0.0/16"), // duplicate
	})
	want := []netip.Prefix{netip.MustParsePrefix("172.18.0.0/16"), netip.MustParsePrefix("fd00:1::/64")}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("egressSubnets = %v, %v; want %v", got, err, want)
	}
	// Fail closed: one unusable subnet (or none at all) means no rule set at all.
	for name, in := range map[string][]netip.Prefix{
		"empty":      nil,
		"invalid":    {netip.MustParsePrefix("172.18.0.0/16"), {}},
		"not masked": {netip.MustParsePrefix("10.0.0.1/8")},
		"4in6":       {netip.MustParsePrefix("::ffff:a00:0/104")},
		"zero bits":  {netip.MustParsePrefix("0.0.0.0/0")},
	} {
		if got, err := egressSubnets(in); err == nil {
			t.Errorf("%s: egressSubnets = %v, want an error", name, got)
		}
	}
}
