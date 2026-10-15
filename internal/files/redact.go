package files

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// redacted replaces the RCON password in server.properties (spec §3.4).
const redacted = "********"

// passwordKey is the key whose value is redacted.
const passwordKey = "rcon.password"

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

// passwordValue parses the start of a java.util.Properties logical line (raw text: its natural
// lines with their continuations) like Properties.load does and reports where the value starts
// when the key is rcon.password. Keys are unescaped first (SEC-37): \uXXXX, \t, \n, \r and \f
// are characters, a backslash before any other character is dropped, and a backslash at the end
// of a natural line continues the line after the leading blanks of the next one. The key must
// be followed by blanks or a '=' or ':' (with optional blanks around it), as before.
func passwordValue(raw string) (int, bool) {
	i := skipBlanks(raw, 0)
	var key [len(passwordKey)]byte
	n := 0
	put := func(r rune) bool {
		if r >= utf8.RuneSelf || n == len(key) {
			return false // rcon.password is ASCII: any other rune or a longer key is not it
		}
		key[n] = byte(r)
		n++
		return true
	}
key:
	for i < len(raw) {
		c := raw[i]
		switch c {
		case '=', ':', ' ', '\t', '\f':
			break key
		case '\\':
			if j, ok := continuation(raw, i); ok {
				i = skipBlanks(raw, j)
				continue
			}
			if i+1 >= len(raw) {
				return 0, false // a trailing backslash: no separator follows
			}
			var r rune
			size := 1
			switch raw[i+1] {
			case 't':
				r = '\t'
			case 'n':
				r = '\n'
			case 'r':
				r = '\r'
			case 'f':
				r = '\f'
			case 'u':
				v, err := strconv.ParseUint(raw[i+2:min(i+6, len(raw))], 16, 16)
				if err != nil || i+6 > len(raw) {
					return 0, false // Properties.load refuses a malformed \u escape
				}
				r, size = rune(v), 5
			default:
				r, size = utf8.DecodeRuneInString(raw[i+1:])
			}
			if !put(r) {
				return 0, false
			}
			i += 1 + size
		default:
			r, size := utf8.DecodeRuneInString(raw[i:])
			if !put(r) {
				return 0, false
			}
			i += size
		}
	}
	if string(key[:n]) != passwordKey || i >= len(raw) {
		return 0, false
	}
	i = skipSeparatorBlanks(raw, i)
	if i < len(raw) && (raw[i] == '=' || raw[i] == ':') {
		i = skipSeparatorBlanks(raw, i+1)
	}
	return i, true
}

// skipBlanks skips spaces, tabs and form feeds from i.
func skipBlanks(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\f') {
		i++
	}
	return i
}

// skipSeparatorBlanks skips blanks and line continuations (with the leading blanks of the next
// natural line) from i.
func skipSeparatorBlanks(s string, i int) int {
	for {
		i = skipBlanks(s, i)
		j, ok := continuation(s, i)
		if !ok {
			return i
		}
		i = j
	}
}

// continuation reports whether s[i] is a backslash that ends a natural line (an unescaped one:
// the caller is at a fresh position of the logical line) and returns the start of the next one.
func continuation(s string, i int) (int, bool) {
	if i+1 >= len(s) || s[i] != '\\' || (s[i+1] != '\n' && s[i+1] != '\r') {
		return 0, false
	}
	j := i + 2
	if s[i+1] == '\r' && j < len(s) && s[j] == '\n' {
		j++
	}
	return j, true
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
		if vs, ok := passwordValue(s[pos:lastEnd]); ok {
			if out, keep := f(s[pos:pos+vs], s[pos+vs:lastEnd]); keep {
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
				if vs, ok := passwordValue(body); ok {
					body = inner(body[:vs], body[vs:])
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
