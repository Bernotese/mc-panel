package defense

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/netip"
	"regexp"
	"strings"

	"github.com/oschwald/maxminddb-golang/v2"
)

var countryCodeRE = regexp.MustCompile(`^[A-Z]{2}$`)

// countryLookup is the seam between GeoIP and the MMDB reader.
type countryLookup interface {
	lookupCountry(ip netip.Addr) (string, error)
	Close() error
}

type mmdbLookup struct{ r *maxminddb.Reader }

func (m mmdbLookup) lookupCountry(ip netip.Addr) (string, error) {
	var rec struct {
		Country struct {
			ISOCode string `maxminddb:"iso_code"`
		} `maxminddb:"country"`
	}
	if err := m.r.Lookup(ip).Decode(&rec); err != nil {
		return "", err
	}
	return rec.Country.ISOCode, nil
}

func (m mmdbLookup) Close() error { return m.r.Close() }

// GeoIP resolves countries from a local DB-IP Lite (or GeoLite2) country database.
type GeoIP struct{ l countryLookup }

func OpenGeoIP(path string) (*GeoIP, error) {
	r, err := maxminddb.Open(path)
	if err != nil {
		return nil, fmt.Errorf("geoip: open %s: %w", path, err)
	}
	return &GeoIP{l: mmdbLookup{r: r}}, nil
}

func (g *GeoIP) Country(ip netip.Addr) string {
	ip = normalizeIP(ip)
	if !ip.IsValid() || isLocal(ip) || ip.IsMulticast() {
		return ""
	}
	c, err := g.l.lookupCountry(ip)
	if err != nil {
		return ""
	}
	c = strings.ToUpper(c)
	if !countryCodeRE.MatchString(c) {
		return ""
	}
	return c
}

func (g *GeoIP) Close() error { return g.l.Close() }

// LoadGeo opens the database at path. A missing or unreadable database disables
// country features: it returns NopGeo{}, available=false and a no-op closer.
func LoadGeo(path string, log *slog.Logger) (GeoResolver, bool, func() error) {
	nop := func() error { return nil }
	if path == "" {
		return NopGeo{}, false, nop
	}
	g, err := OpenGeoIP(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			log.Info("geoip database not found; country features disabled", "path", path)
		} else {
			log.Warn("geoip database unusable; country features disabled", "path", path, "err", err)
		}
		return NopGeo{}, false, nop
	}
	return g, true, g.Close
}
