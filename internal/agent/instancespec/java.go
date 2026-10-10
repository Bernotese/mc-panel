package instancespec

import (
	"strconv"
	"strings"
)

// JavaFor derives the itzg image variant from the Minecraft version (spec §6.1). Modpacks
// default to java21; unknown formats (snapshots, LATEST) get the newest image.
func JavaFor(t Type, version string) Java {
	if t.IsModpack() {
		return Java21
	}
	parts := strings.Split(version, ".")
	major, ok := leadingInt(parts[0])
	if !ok || major >= 26 || major != 1 || len(parts) < 2 {
		return Java25
	}
	minor, ok := leadingInt(parts[1])
	if !ok {
		return Java25
	}
	patch := 0
	if len(parts) >= 3 {
		patch, _ = leadingInt(parts[2])
	}
	switch {
	case minor <= 16:
		return Java8
	case minor <= 19, minor == 20 && patch <= 4:
		return Java17
	case minor <= 21:
		return Java21
	default:
		return Java25
	}
}

// leadingInt parses the leading decimal digits of s ("4-rc1" -> 4).
func leadingInt(s string) (int, bool) {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(s[:end])
	return n, err == nil
}
