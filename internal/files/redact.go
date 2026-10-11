package files

import (
	"regexp"
	"strings"
)

// redacted replaces the RCON password in server.properties (spec §3.4).
const redacted = "********"

// passwordLine matches the start of a java.util.Properties logical line defining
// rcon.password: key, then "=" or ":" with optional blanks, or only blanks. Group 1 is
// everything up to the value.
var passwordLine = regexp.MustCompile(`^([ \t\f]*rcon\.password(?:[ \t\f]*[=:][ \t\f]*|[ \t\f]+))(.*)$`)

// isServerProps reports whether clean is the top-level server.properties. The comparison
// ignores case: on a case-insensitive file system (macOS dev) every spelling opens it.
func isServerProps(clean string) bool { return strings.EqualFold(clean, serverProps) }

// natLine is a natural line of a properties file and its terminator ("" for the last line).
type natLine struct{ body, end string }

// naturalLines splits s like java.util.Properties: lines end with "\n", "\r\n" or "\r".
func naturalLines(s string) []natLine {
	var out []natLine
	for s != "" {
		i := strings.IndexAny(s, "\r\n")
		if i < 0 {
			out = append(out, natLine{body: s})
			break
		}
		n := 1
		if s[i] == '\r' && i+1 < len(s) && s[i+1] == '\n' {
			n = 2
		}
		out = append(out, natLine{body: s[:i], end: s[i : i+n]})
		s = s[i+n:]
	}
	return out
}

// commentOrBlank reports whether a natural line that starts a logical line is a comment or
// blank; such a line never continues, whatever it ends with.
func commentOrBlank(body string) bool {
	t := strings.TrimLeft(body, " \t\f")
	return t == "" || t[0] == '#' || t[0] == '!'
}

// continues reports whether a natural line continues the logical line: it ends in an odd
// number of backslashes (an even number are escaped backslashes).
func continues(body string) bool {
	return (len(body)-len(strings.TrimRight(body, `\`)))%2 == 1
}

// mapPasswordLines calls f for every logical line that defines rcon.password, with its prefix
// and its raw value (continuation lines included); f returns the replacement and whether to
// keep the line at all. inner (may be nil) maps natural lines that merely look like a password
// line but continue another logical line. Everything else stays byte for byte.
func mapPasswordLines(s string, f func(prefix, value string) (string, bool), inner func(prefix, value string) string) string {
	lines := naturalLines(s)
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(lines); {
		j := i + 1 // lines[i:j] is one logical line
		if !commentOrBlank(lines[i].body) {
			for j < len(lines) && continues(lines[j-1].body) {
				j++
			}
		}
		if m := passwordLine.FindStringSubmatch(lines[i].body); m != nil {
			value := m[2]
			for k := i + 1; k < j; k++ {
				value += lines[k-1].end + lines[k].body
			}
			if out, keep := f(m[1], value); keep {
				b.WriteString(out)
				b.WriteString(lines[j-1].end)
			}
			i = j
			continue
		}
		for k := i; k < j; k++ {
			body := lines[k].body
			if k > i && inner != nil {
				if m := passwordLine.FindStringSubmatch(body); m != nil {
					body = inner(m[1], m[2])
				}
			}
			b.WriteString(body)
			b.WriteString(lines[k].end)
		}
		i = j
	}
	return b.String()
}

// redactProperties hides every non-empty rcon.password value, including its continuation
// lines. A natural line inside another value that looks like a password line is hidden as
// well (it keeps its own continuation, so the structure of the file does not change).
func redactProperties(s string) string {
	return mapPasswordLines(s, func(prefix, value string) (string, bool) {
		if value == "" {
			return prefix, true
		}
		return prefix + redacted, true
	}, func(prefix, value string) string {
		if value == "" {
			return prefix
		}
		if continues(value) {
			return prefix + redacted + `\`
		}
		return prefix + redacted
	})
}

// currentPassword returns the raw value of the last rcon.password definition (last definition
// wins, as in java.util.Properties).
func currentPassword(s string) (string, bool) {
	var value string
	found := false
	mapPasswordLines(s, func(prefix, v string) (string, bool) {
		value, found = v, true
		return prefix + v, true
	}, nil)
	return value, found
}

// restorePassword puts the current password back into rcon.password definitions that still
// carry the redacted placeholder, so saving server.properties in the editor never sets the
// password to "********". Without a current password such a line is dropped. A placeholder
// on a continuation line belongs to another value (for example the public motd) and is never
// replaced.
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
	}, nil)
}
