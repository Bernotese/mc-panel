package files

import (
	"regexp"
	"strings"
)

// redacted replaces the RCON password in server.properties (spec §3.4).
const redacted = "********"

// passwordLine matches a java.util.Properties line defining rcon.password: key, then "=" or
// ":" with optional blanks, or only blanks. Group 1 is everything up to the value.
var passwordLine = regexp.MustCompile(`^([ \t\f]*rcon\.password(?:[ \t\f]*[=:][ \t\f]*|[ \t\f]+))(.*)$`)

// mapPasswordLines calls f for every rcon.password line (without its line ending); f returns
// the replacement line and whether to keep the line at all. Other lines stay untouched.
func mapPasswordLines(s string, f func(prefix, value string) (string, bool)) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, line := range strings.SplitAfter(s, "\n") {
		body := strings.TrimRight(line, "\r\n")
		end := line[len(body):]
		m := passwordLine.FindStringSubmatch(body)
		if m == nil {
			b.WriteString(line)
			continue
		}
		if out, keep := f(m[1], m[2]); keep {
			b.WriteString(out)
			b.WriteString(end)
		}
	}
	return b.String()
}

// redactProperties hides every non-empty rcon.password value.
func redactProperties(s string) string {
	return mapPasswordLines(s, func(prefix, value string) (string, bool) {
		if value == "" {
			return prefix, true
		}
		return prefix + redacted, true
	})
}

// currentPassword returns the value of the last rcon.password line (last definition wins).
func currentPassword(s string) (string, bool) {
	var value string
	found := false
	mapPasswordLines(s, func(prefix, v string) (string, bool) {
		value, found = v, true
		return prefix + v, true
	})
	return value, found
}

// restorePassword puts the current password back into lines that still carry the redacted
// placeholder, so saving server.properties in the editor never sets the password to
// "********". Without a current password such a line is dropped.
func restorePassword(next, current string) string {
	cur, ok := currentPassword(current)
	return mapPasswordLines(next, func(prefix, value string) (string, bool) {
		if value != redacted {
			return prefix + value, true
		}
		if !ok {
			return "", false
		}
		return prefix + cur, true
	})
}
