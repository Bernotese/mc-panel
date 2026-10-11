package console

import (
	"regexp"
	"strconv"
	"strings"
)

// onlinePlayer is the OnlinePlayer schema.
type onlinePlayer struct {
	Name string  `json:"name"`
	UUID *string `json:"uuid"`
}

const (
	maxListedPlayers = 500
	maxPlayerName    = 64 // runes; Minecraft names have at most 16, the rest is defensive
)

var (
	// listHeaders match the first line of "list" answers: vanilla (also Paper), Bukkit, Spigot.
	listHeaders = []*regexp.Regexp{
		regexp.MustCompile(`^There are (\d+) of a max(?:imum)? of (\d+) players online:?`),
		regexp.MustCompile(`^There are (\d+)/(\d+) players online:?`),
		regexp.MustCompile(`^There are (\d+) out of maximum (\d+) players online[.:]?`),
	}
	nameAndUUID = regexp.MustCompile(`^(.+) \(([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\)$`)
)

// parseList parses the answer of "list uuids" (spec §6.3). Formatting codes (§x) are removed
// first. ok is false for any other text, including a count that does not fit into an int.
func parseList(out string) (players []onlinePlayer, maxPlayers int, ok bool) {
	s := strings.TrimSpace(stripFormatting(out))
	for _, re := range listHeaders {
		m := re.FindStringSubmatchIndex(s)
		if m == nil {
			continue
		}
		if _, err := strconv.Atoi(s[m[2]:m[3]]); err != nil {
			return nil, 0, false
		}
		maxP, err := strconv.Atoi(s[m[4]:m[5]])
		if err != nil {
			return nil, 0, false
		}
		return splitNames(s[m[1]:]), maxP, true
	}
	return nil, 0, false
}

// splitNames splits "A (uuid), B" (comma or line separated) into players.
func splitNames(rest string) []onlinePlayer {
	players := []onlinePlayer{}
	for _, f := range strings.FieldsFunc(rest, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		p := onlinePlayer{Name: f}
		if m := nameAndUUID.FindStringSubmatch(f); m != nil {
			id := strings.ToLower(m[2])
			p.Name, p.UUID = m[1], &id
		}
		if r := []rune(p.Name); len(r) > maxPlayerName {
			p.Name = string(r[:maxPlayerName])
		}
		players = append(players, p)
		if len(players) == maxListedPlayers {
			break
		}
	}
	return players
}

// stripFormatting removes Minecraft formatting codes: '§' and the rune after it.
func stripFormatting(s string) string {
	if !strings.ContainsRune(s, '§') {
		return s
	}
	var b strings.Builder
	skip := false
	for _, r := range s {
		switch {
		case skip:
			skip = false
		case r == '§':
			skip = true
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
