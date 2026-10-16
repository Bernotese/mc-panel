package banrules_test

import (
	"errors"
	"math"
	"net/netip"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/banrules"
)

func TestMaxTimeoutSeconds(t *testing.T) {
	if banrules.MaxTimeoutSeconds != 691200 {
		t.Fatalf("MaxTimeoutSeconds = %d, want 691200", banrules.MaxTimeoutSeconds)
	}
}

func TestCheckPrefix(t *testing.T) {
	ok := []string{
		"203.0.113.7/32",
		"198.51.100.255/32",
		"2001:db8:1:2::/64",
		"2001:db8::1/128",
		"2001:db8:1:2:3::/80",
	}
	bad := []string{
		"0.0.0.0/0", "203.0.113.0/24", "127.0.0.1/32", "::1/128", "::/0", "2001:db8::/48",
		"::ffff:203.0.113.7/128", // IPv4-mapped: must be sent as plain IPv4
		"::ffff:0:0/96",          // would cover the whole IPv4-mapped space
		"::ffff:127.0.0.1/128",
		"2001:db8:1:2::5/64",                         // host bits set
		"fe80::/64", "fe80::1/128", "169.254.1.1/32", // link-local
		"ff02::/64", "ff02::1/128", "224.0.0.1/32", // multicast
	}
	for _, s := range ok {
		if err := banrules.CheckPrefix(netip.MustParsePrefix(s)); err != nil {
			t.Errorf("CheckPrefix(%s) = %v, want nil", s, err)
		}
	}
	for _, s := range bad {
		if err := banrules.CheckPrefix(netip.MustParsePrefix(s)); !errors.Is(err, banrules.ErrInvalid) {
			t.Errorf("CheckPrefix(%s) = %v, want ErrInvalid", s, err)
		}
	}
	if err := banrules.CheckPrefix(netip.Prefix{}); !errors.Is(err, banrules.ErrInvalid) {
		t.Errorf("CheckPrefix(zero) = %v, want ErrInvalid", err)
	}
}

func TestParsePrefix(t *testing.T) {
	for _, s := range []string{"203.0.113.7/32", "2001:db8:1:2::/64", "2001:db8::1/128"} {
		p, err := banrules.ParsePrefix(s)
		if err != nil || p.String() != s {
			t.Errorf("ParsePrefix(%q) = %v, %v", s, p, err)
		}
	}
	bad := []string{
		"",
		"203.0.113.7",            // no prefix length
		"fe80::1%eth0/128",       // zone
		"2001:DB8:1:2::/64",      // not canonical (upper case)
		"2001:0db8:1:2::/64",     // not canonical (leading zero)
		" 203.0.113.7/32",        // whitespace
		"203.0.113.7/32 ",        // whitespace
		"203.0.113.07/32",        // leading zero in IPv4
		"203.0.113.7/032",        // leading zero in bits
		"::ffff:203.0.113.7/128", // mapped
		"2001:db8:1:2::5/64",     // host bits
		"203.0.113.0/24",         // too wide
	}
	for _, s := range bad {
		if _, err := banrules.ParsePrefix(s); !errors.Is(err, banrules.ErrInvalid) {
			t.Errorf("ParsePrefix(%q) = %v, want ErrInvalid", s, err)
		}
	}
}

func TestTimeoutFromSeconds(t *testing.T) {
	ok := map[int64]time.Duration{1: time.Second, 900: 15 * time.Minute, 691200: agentapi.MaxBanDuration}
	for s, want := range ok {
		if got, err := banrules.TimeoutFromSeconds(s); err != nil || got != want {
			t.Errorf("TimeoutFromSeconds(%d) = %v, %v; want %v", s, got, err, want)
		}
	}
	for _, s := range []int64{0, -1, 691201, math.MaxInt64, math.MinInt64} {
		if _, err := banrules.TimeoutFromSeconds(s); !errors.Is(err, banrules.ErrInvalid) {
			t.Errorf("TimeoutFromSeconds(%d) = %v, want ErrInvalid", s, err)
		}
	}
}

func TestSecondsFromDuration(t *testing.T) {
	ok := map[time.Duration]int64{
		time.Second:                               1,
		1500 * time.Millisecond:                   2,
		15 * time.Minute:                          900,
		agentapi.MaxBanDuration:                   691200,
		agentapi.MaxBanDuration - time.Nanosecond: 691200,
	}
	for d, want := range ok {
		if got, err := banrules.SecondsFromDuration(d); err != nil || got != want {
			t.Errorf("SecondsFromDuration(%v) = %d, %v; want %d", d, got, err, want)
		}
	}
	for _, d := range []time.Duration{0, 999 * time.Millisecond, -time.Second, agentapi.MaxBanDuration + time.Nanosecond, time.Duration(math.MaxInt64)} {
		if _, err := banrules.SecondsFromDuration(d); !errors.Is(err, banrules.ErrInvalid) {
			t.Errorf("SecondsFromDuration(%v) = %v, want ErrInvalid", d, err)
		}
	}
}

func TestRemainingSeconds(t *testing.T) {
	cases := map[time.Duration]int64{
		0:                            0,
		-5 * time.Second:             0,
		time.Nanosecond:              1,
		time.Second:                  1,
		1001 * time.Millisecond:      2,
		time.Duration(math.MaxInt64): 9223372037,
	}
	for d, want := range cases {
		if got := banrules.RemainingSeconds(d); got != want {
			t.Errorf("RemainingSeconds(%v) = %d, want %d", d, got, want)
		}
	}
}

func TestFindOverlap(t *testing.T) {
	existing := []netip.Prefix{netip.MustParsePrefix("2001:db8:1:2::/64"), netip.MustParsePrefix("203.0.113.7/32")}
	if q, ok := banrules.FindOverlap(netip.MustParsePrefix("2001:db8:1:2::1/128"), existing); !ok || q.String() != "2001:db8:1:2::/64" {
		t.Errorf("expected overlap with /64, got %v %v", q, ok)
	}
	for _, s := range []string{"2001:db8:1:2::/64", "2001:db8:1:3::/64", "203.0.113.8/32", "203.0.113.7/32"} {
		if q, ok := banrules.FindOverlap(netip.MustParsePrefix(s), existing); ok {
			t.Errorf("FindOverlap(%s) = %v, want none (identical prefixes are a re-ban, not an overlap)", s, q)
		}
	}
}
