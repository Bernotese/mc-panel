package defense

import (
	"errors"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// --- minimal MaxMind DB writer (test only) ---

func mmdbString(s string) []byte {
	if len(s) >= 29 {
		panic("mmdbString: string too long for the short encoding")
	}
	return append([]byte{0x40 | byte(len(s))}, s...) // type 2 (UTF-8 string)
}
func mmdbMap(n int) []byte       { return []byte{0xE0 | byte(n)} }              // type 7 (map)
func mmdbUint16(v uint16) []byte { return []byte{0xA2, byte(v >> 8), byte(v)} } // type 5, 2 bytes
func mmdbArray(n int) []byte     { return []byte{byte(n), 0x04} }               // extended type 11 = 7+4
func mmdbUint32(v uint32) []byte { // type 6, 4 bytes
	return []byte{0xC4, byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
}

func countryRecord(code string) []byte {
	var b []byte
	b = append(b, mmdbMap(1)...)
	b = append(b, mmdbString("country")...)
	b = append(b, mmdbMap(1)...)
	b = append(b, mmdbString("iso_code")...)
	b = append(b, mmdbString(code)...)
	return b
}

// writeTestMMDB writes an IPv4-only database mapping each (non-overlapping) prefix
// to {"country":{"iso_code":code}}.
func writeTestMMDB(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	type node struct{ rec [2]int } // >= 0: child node, -1: empty, <= -2: data index -(rec+2)
	nodes := []node{{rec: [2]int{-1, -1}}}
	var data []byte
	var offsets []int
	for cidr, code := range entries {
		p := netip.MustParsePrefix(cidr)
		if !p.Addr().Is4() || p.Bits() == 0 {
			t.Fatalf("writeTestMMDB supports IPv4 prefixes with bits > 0 only: %s", cidr)
		}
		offsets = append(offsets, len(data))
		data = append(data, countryRecord(code)...)
		ref := -(len(offsets) - 1 + 2)
		a := p.Addr().As4()
		cur := 0
		for i := 0; i < p.Bits(); i++ {
			bit := int(a[i/8]>>(7-uint(i%8))) & 1
			if i == p.Bits()-1 {
				if nodes[cur].rec[bit] != -1 {
					t.Fatalf("overlapping prefix %s", cidr)
				}
				nodes[cur].rec[bit] = ref
				break
			}
			next := nodes[cur].rec[bit]
			if next <= -2 {
				t.Fatalf("overlapping prefix %s", cidr)
			}
			if next == -1 {
				nodes = append(nodes, node{rec: [2]int{-1, -1}})
				next = len(nodes) - 1
				nodes[cur].rec[bit] = next
			}
			cur = next
		}
	}
	n := len(nodes)
	var buf []byte
	for _, nd := range nodes {
		for _, r := range nd.rec {
			v := n // "no data"
			if r >= 0 {
				v = r
			} else if r <= -2 {
				v = n + 16 + offsets[-(r+2)]
			}
			buf = append(buf, byte(v>>16), byte(v>>8), byte(v))
		}
	}
	buf = append(buf, make([]byte, 16)...) // data section separator
	buf = append(buf, data...)
	buf = append(buf, "\xAB\xCD\xEFMaxMind.com"...)
	buf = append(buf, mmdbMap(9)...)
	kv := func(k string, v []byte) {
		buf = append(buf, mmdbString(k)...)
		buf = append(buf, v...)
	}
	desc := append(mmdbMap(1), mmdbString("en")...)
	desc = append(desc, mmdbString("test")...)
	kv("binary_format_major_version", mmdbUint16(2))
	kv("binary_format_minor_version", mmdbUint16(0))
	kv("build_epoch", mmdbUint32(1759924800))
	kv("database_type", mmdbString("Test-Country"))
	kv("description", desc)
	kv("ip_version", mmdbUint16(4))
	kv("languages", append(mmdbArray(1), mmdbString("en")...))
	kv("node_count", mmdbUint32(uint32(n)))
	kv("record_size", mmdbUint16(24))
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestGeoIPWithMMDBFixture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.mmdb")
	writeTestMMDB(t, path, map[string]string{"203.0.113.0/24": "DE", "198.51.100.0/24": "us"})
	g, err := OpenGeoIP(path)
	if err != nil {
		t.Fatalf("OpenGeoIP: %v", err)
	}
	defer g.Close()
	cases := map[string]string{
		"203.0.113.7":        "DE",
		"::ffff:203.0.113.7": "DE",
		"198.51.100.1":       "US", // normalized to upper case
		"192.0.2.1":          "",   // not in database
		"2001:db8::1":        "",   // IPv6 lookup in IPv4-only database fails → ""
		"10.0.0.1":           "",   // private: never looked up
	}
	for ip, want := range cases {
		if got := g.Country(addr(ip)); got != want {
			t.Errorf("Country(%s) = %q, want %q", ip, got, want)
		}
	}
}

type fakeLookup struct {
	calls int
	code  string
	err   error
}

func (f *fakeLookup) lookupCountry(netip.Addr) (string, error) { f.calls++; return f.code, f.err }
func (f *fakeLookup) Close() error                             { return nil }

func TestGeoIPSeam(t *testing.T) {
	f := &fakeLookup{code: "de"}
	g := &GeoIP{l: f}
	for _, ip := range []string{"127.0.0.1", "::1", "192.168.1.1", "fe80::1", "224.0.0.1"} {
		if got := g.Country(addr(ip)); got != "" {
			t.Errorf("Country(%s) = %q, want empty", ip, got)
		}
	}
	if g.Country(netip.Addr{}) != "" || f.calls != 0 {
		t.Fatalf("local/invalid addresses must not be looked up (calls=%d)", f.calls)
	}
	if got := g.Country(addr("203.0.113.7")); got != "DE" {
		t.Fatalf("Country = %q, want DE", got)
	}
	f.err = errors.New("boom")
	if got := g.Country(addr("203.0.113.7")); got != "" {
		t.Fatalf("lookup error must yield empty country, got %q", got)
	}
	f.err, f.code = nil, "Germany"
	if got := g.Country(addr("203.0.113.7")); got != "" {
		t.Fatalf("invalid code must yield empty country, got %q", got)
	}
}

func TestLoadGeo(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	dir := t.TempDir()
	for name, path := range map[string]string{"empty path": "", "missing": filepath.Join(dir, "missing.mmdb")} {
		r, ok, closeFn := LoadGeo(path, log)
		if ok || r.Country(addr("203.0.113.7")) != "" || closeFn() != nil {
			t.Errorf("%s: ok=%v", name, ok)
		}
		if _, isNop := r.(NopGeo); !isNop {
			t.Errorf("%s: resolver = %T, want NopGeo", name, r)
		}
	}
	garbage := filepath.Join(dir, "garbage.mmdb")
	if err := os.WriteFile(garbage, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := LoadGeo(garbage, log); ok {
		t.Error("garbage file must not be available")
	}
	good := filepath.Join(dir, "good.mmdb")
	writeTestMMDB(t, good, map[string]string{"203.0.113.0/24": "DE"})
	r, ok, closeFn := LoadGeo(good, log)
	if !ok || r.Country(addr("203.0.113.7")) != "DE" {
		t.Fatalf("valid database: ok=%v", ok)
	}
	if err := closeFn(); err != nil {
		t.Fatal(err)
	}
}

// TestGeoIPRealDatabase is a manual check against the real DB-IP Lite file;
// it only runs when MCPANEL_GEOIP_TEST_DB points to an .mmdb file.
func TestGeoIPRealDatabase(t *testing.T) {
	path := os.Getenv("MCPANEL_GEOIP_TEST_DB")
	if path == "" {
		t.Skip("set MCPANEL_GEOIP_TEST_DB to a dbip-country-lite .mmdb file")
	}
	g, err := OpenGeoIP(path)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	code := regexp.MustCompile(`^[A-Z]{2}$`)
	for _, ip := range []string{"8.8.8.8", "2001:4860:4860::8888", "::ffff:8.8.8.8"} {
		if got := g.Country(addr(ip)); !code.MatchString(got) {
			t.Errorf("Country(%s) = %q, want ISO code", ip, got)
		}
	}
	if got := g.Country(addr("10.0.0.1")); got != "" {
		t.Errorf("private address: %q", got)
	}
}
