package upstream

import (
	"regexp"
	"strings"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// MaxFileNameLen bounds a fallback file name including ".jar".
const MaxFileNameLen = 128

var jarNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}\.jar$`)

// ValidJarName reports whether an upstream file name may be used as is (spec §4).
func ValidJarName(name string) bool {
	if !jarNameRe.MatchString(name) {
		return false
	}
	c, err := filesapi.CleanPath(name)
	return err == nil && c == name && !filesapi.IsHidden(c)
}

func isAlnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// FileName returns the upstream name if it is valid, else "<slug>-<version>.jar" with every
// character outside [A-Za-z0-9._+-] replaced by "-", leading non-alphanumerics dropped and cut
// to MaxFileNameLen; "plugin.jar" if nothing usable is left.
func FileName(upstreamName, slug, version string) string {
	if ValidJarName(upstreamName) {
		return upstreamName
	}
	var b strings.Builder
	for _, r := range slug + "-" + version {
		if isAlnum(r) || r == '.' || r == '_' || r == '+' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	base := strings.TrimLeftFunc(b.String(), func(r rune) bool { return !isAlnum(r) })
	if base == "" {
		base = "plugin"
	}
	if limit := MaxFileNameLen - len(".jar"); len(base) > limit {
		base = base[:limit]
	}
	if name := base + ".jar"; ValidJarName(name) {
		return name
	}
	return "plugin.jar"
}
