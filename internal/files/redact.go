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

// lineAt returns the natural line that starts at pos, as java.util.Properties splits lines
// ("\n", "\r\n" or "\r"): its body is s[pos:end], the next line starts at next.
func lineAt(s string, pos int) (end, next int) {
	i := strings.IndexAny(s[pos:], "\r\n")
	if i < 0 {
		return len(s), len(s)
	}
	end = pos + i
	next = end + 1
	if s[end] == '\r' && next < len(s) && s[next] == '\n' {
		next++
	}
	return end, next
}

// matchPassword matches passwordLine on one natural line (cheap pre-filter first).
func matchPassword(body string) []int {
	if !strings.Contains(body, "rcon.password") {
		return nil
	}
	return passwordLine.FindStringSubmatchIndex(body)
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
// line but continue another logical line. Everything else stays byte for byte. It scans s in
// place: values are substrings of s, so the work is linear in len(s).
func mapPasswordLines(s string, f func(prefix, value string) (string, bool), inner func(prefix, value string) string) string {
	var b strings.Builder
	b.Grow(len(s))
	for pos := 0; pos < len(s); {
		end, next := lineAt(s, pos)
		first := s[pos:end]
		// lastStart..lastEnd is the last natural line of the logical line, lastNext what follows.
		lastStart, lastEnd, lastNext := pos, end, next
		if !commentOrBlank(first) {
			for lastNext < len(s) && continues(s[lastStart:lastEnd]) {
				lastStart = lastNext
				lastEnd, lastNext = lineAt(s, lastStart)
			}
		}
		if m := matchPassword(first); m != nil {
			if out, keep := f(first[:m[3]], s[pos+m[4]:lastEnd]); keep {
				b.WriteString(out)
				b.WriteString(s[lastEnd:lastNext])
			}
			pos = lastNext
			continue
		}
		b.WriteString(s[pos:next])
		for cs := next; cs < lastNext; {
			ce, cn := lineAt(s, cs)
			body := s[cs:ce]
			if inner != nil {
				if m := matchPassword(body); m != nil {
					body = inner(body[:m[3]], body[m[4]:])
				}
			}
			b.WriteString(body)
			b.WriteString(s[ce:cn])
			cs = cn
		}
		pos = lastNext
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
