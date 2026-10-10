package notify

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/Bernotese/mc-panel/internal/secretbox"
)

type Kind string

const (
	KindDiscord Kind = "discord"
	KindNtfy    Kind = "ntfy"
)

func (k Kind) Valid() bool { return k == KindDiscord || k == KindNtfy }

// Config is stored as encrypted JSON in notifiers.config_enc.
type Config struct {
	URL   string `json:"url"`
	Topic string `json:"topic,omitempty"`
	Token string `json:"token,omitempty"`
}

const (
	maxURLLen   = 2048
	maxTokenLen = 256
)

var (
	topicRE        = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	digitsRE       = regexp.MustCompile(`^[0-9]+$`)
	webhookTokenRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// ValidateConfig checks a notifier target. Error messages never contain secrets.
func ValidateConfig(kind Kind, c Config, dev bool) error {
	if !kind.Valid() {
		return errors.New("kind must be discord or ntfy")
	}
	if c.URL == "" {
		return errors.New("url is required")
	}
	if len(c.URL) > maxURLLen {
		return errors.New("url is too long")
	}
	u, err := url.Parse(c.URL)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return errors.New("url must be an absolute URL")
	}
	if u.User != nil {
		return errors.New("url must not contain credentials")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return errors.New("url must not contain a query or fragment")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !dev {
			return errors.New("url must use https")
		}
	default:
		return errors.New("url must use https")
	}
	host := strings.ToLower(u.Hostname())
	internalHost := strings.TrimSuffix(host, ".") // "localhost." is localhost too
	switch kind {
	case KindDiscord:
		if host != "discord.com" && host != "discordapp.com" {
			return errors.New("discord webhook host must be discord.com or discordapp.com")
		}
		if u.Port() != "" {
			return errors.New("discord webhook URL must not contain a port")
		}
		parts := strings.Split(strings.TrimPrefix(u.EscapedPath(), "/"), "/")
		if len(parts) != 4 || parts[0] != "api" || parts[1] != "webhooks" ||
			!digitsRE.MatchString(parts[2]) || !webhookTokenRE.MatchString(parts[3]) {
			return errors.New("discord webhook URL must look like https://discord.com/api/webhooks/<id>/<token>")
		}
		if c.Topic != "" || c.Token != "" {
			return errors.New("topic and token are only valid for ntfy")
		}
	case KindNtfy:
		if !dev && isInternalHost(internalHost) {
			return errors.New("ntfy server must not be localhost or a loopback, private or link-local address")
		}
		if !topicRE.MatchString(c.Topic) {
			return errors.New("ntfy topic must match ^[A-Za-z0-9_-]{1,64}$")
		}
		bad := func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }
		if len(c.Token) > maxTokenLen || strings.IndexFunc(c.Token, bad) >= 0 {
			return errors.New("ntfy token must be at most 256 characters without whitespace")
		}
	}
	return nil
}

func isInternalHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return blockedDestination(a)
}

// MaskTarget renders a notifier target without secrets (webhook token, topic, access token).
func MaskTarget(kind Kind, c Config) string {
	u, err := url.Parse(c.URL)
	if err != nil || u.Host == "" {
		return "****"
	}
	base := u.Scheme + "://" + u.Host
	switch kind {
	case KindDiscord:
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		id := ""
		if len(parts) >= 3 {
			id = parts[2]
		}
		if len(id) > 4 {
			id = id[:4] + "…"
		}
		return base + "/api/webhooks/" + id + "/****"
	case KindNtfy:
		topic := c.Topic[:min(3, len(c.Topic)/2)]
		s := strings.TrimRight(base+u.Path, "/") + "/" + topic + "****"
		if c.Token != "" {
			s += " (token)"
		}
		return s
	}
	return "****"
}

func configAAD(kind Kind) []byte { return []byte("notifier:" + string(kind)) }

func SealConfig(box *secretbox.Box, kind Kind, c Config) ([]byte, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	return box.Seal(b, configAAD(kind))
}

func OpenConfig(box *secretbox.Box, kind Kind, enc []byte) (Config, error) {
	pt, err := box.Open(enc, configAAD(kind))
	if err != nil {
		return Config{}, fmt.Errorf("notify: decrypt config: %w", err)
	}
	var c Config
	if err := json.Unmarshal(pt, &c); err != nil {
		return Config{}, fmt.Errorf("notify: decode config: %w", err)
	}
	return c, nil
}
