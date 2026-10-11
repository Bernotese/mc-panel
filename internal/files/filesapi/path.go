package filesapi

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	MaxPathBytes      = 4096
	MaxComponentBytes = 255
	hiddenPrefix      = ".mcpanel-"
)

// topHidden are the itzg files holding the RCON password (spec §3.4).
var topHidden = map[string]bool{".rcon-cli.env": true, ".rcon-cli.yaml": true}

// CleanPath validates p per spec §3.4 and returns it normalised: "" for the root, no trailing
// "/". Errors wrap ErrInvalid. Hidden paths are not rejected here (IsHidden decides).
func CleanPath(p string) (string, error) {
	if p == "" || p == "." {
		return "", nil
	}
	if len(p) > MaxPathBytes {
		return "", fmt.Errorf("%w: path longer than %d bytes", ErrInvalid, MaxPathBytes)
	}
	if !utf8.ValidString(p) {
		return "", fmt.Errorf("%w: path is not valid UTF-8", ErrInvalid)
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f || r == '\\' {
			return "", fmt.Errorf("%w: path contains a control character or a backslash", ErrInvalid)
		}
	}
	if strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("%w: path must be relative", ErrInvalid)
	}
	p = strings.TrimSuffix(p, "/")
	for _, c := range strings.Split(p, "/") {
		switch {
		case c == "" || c == "." || c == "..":
			return "", fmt.Errorf("%w: path component %q is not allowed", ErrInvalid, c)
		case len(c) > MaxComponentBytes:
			return "", fmt.Errorf("%w: path component longer than %d bytes", ErrInvalid, MaxComponentBytes)
		}
	}
	return p, nil
}

// IsHidden reports whether a cleaned path is or lies below a hidden name: the top-level RCON
// files or any component starting with ".mcpanel-" (temporary files of the service).
func IsHidden(clean string) bool {
	if clean == "" {
		return false
	}
	parts := strings.Split(clean, "/")
	if topHidden[parts[0]] {
		return true
	}
	for _, c := range parts {
		if strings.HasPrefix(c, hiddenPrefix) {
			return true
		}
	}
	return false
}

// Join joins a cleaned directory path and an entry name ("" + "a" = "a").
func Join(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}
