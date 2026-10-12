package filesapi

import (
	"fmt"
	"net/url"
	"strings"
)

// checksumHexLen is the digest length in hex characters per algorithm.
var checksumHexLen = map[string]int{AlgoSHA256: 64, AlgoSHA512: 128}

// Validate reports ErrInvalid unless s names AlgoSHA256 with 64 or AlgoSHA512 with 128
// lowercase hex characters.
func (s Checksum) Validate() error {
	n, ok := checksumHexLen[s.Algo]
	if !ok {
		return fmt.Errorf("%w: checksum algorithm must be %s or %s", ErrInvalid, AlgoSHA256, AlgoSHA512)
	}
	if len(s.Hex) != n || !isLowerHex(s.Hex) {
		return fmt.Errorf("%w: %s must be %d lowercase hex characters", ErrInvalid, s.Algo, n)
	}
	return nil
}

// Matches reports whether the digests computed while writing (lowercase hex) satisfy s; only
// the digest of s.Algo is compared. An invalid s matches nothing.
func (s Checksum) Matches(sha256Hex, sha512Hex string) bool {
	if s.Validate() != nil {
		return false
	}
	switch s.Algo {
	case AlgoSHA256:
		return s.Hex == sha256Hex
	case AlgoSHA512:
		return s.Hex == sha512Hex
	}
	return false
}

func isLowerHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// ChecksumFromQuery reads the optional digest of an upload request (QuerySHA256 or
// QuerySHA512). ok is false when neither is present. Both at once, a repeated parameter or a
// malformed value are ErrInvalid.
func ChecksumFromQuery(q url.Values) (sum Checksum, ok bool, err error) {
	var found []Checksum
	for _, algo := range []string{QuerySHA256, QuerySHA512} {
		vs, present := q[algo]
		if !present {
			continue
		}
		if len(vs) != 1 {
			return Checksum{}, false, fmt.Errorf("%w: %s given more than once", ErrInvalid, algo)
		}
		found = append(found, Checksum{Algo: algo, Hex: vs[0]})
	}
	switch len(found) {
	case 0:
		return Checksum{}, false, nil
	case 1:
		if err := found[0].Validate(); err != nil {
			return Checksum{}, false, err
		}
		return found[0], true, nil
	}
	return Checksum{}, false, fmt.Errorf("%w: at most one of %s and %s", ErrInvalid, QuerySHA256, QuerySHA512)
}

// ValidJarDir reports whether dir is one of the directories /v1/jars lists.
func ValidJarDir(dir string) bool { return dir == DirPlugins || dir == DirMods }

// IsJarName reports whether a base name is a jar: "<x>.jar" (enabled) or "<x>.jar.disabled"
// (disabled), compared case-insensitively, with at least one character before ".jar". It does
// not check for "/" or hidden names; callers use CleanPath and IsHidden for that.
func IsJarName(name string) (enabled, ok bool) {
	switch {
	case hasSuffixFold(name, JarSuffix+DisabledSuffix):
		return false, true
	case hasSuffixFold(name, JarSuffix):
		return true, true
	}
	return false, false
}

// hasSuffixFold: name ends with suffix (ASCII case-insensitive) and has something before it.
func hasSuffixFold(name, suffix string) bool {
	return len(name) > len(suffix) && strings.EqualFold(name[len(name)-len(suffix):], suffix)
}
