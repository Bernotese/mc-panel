package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// Sender delivers one event to one target.
type Sender interface {
	Send(ctx context.Context, e Event) error
}

// PermanentError marks failures that retrying cannot fix.
type PermanentError struct{ Err error }

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

const httpTimeout = 5 * time.Second

// NewHTTPClient returns the client used for notifications in development and
// tests: 5 s timeout and no redirects (a redirect could leak the webhook token
// to another host). It connects to any destination; production uses
// NewRestrictedHTTPClient.
func NewHTTPClient() *http.Client {
	return &http.Client{
		Timeout:       httpTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

var errBlockedDestination = errors.New("destination address is not allowed")

// blockedPrefixes are special-purpose ranges that net/netip does not classify
// as private: CGNAT (incl. cloud metadata 100.100.100.200), "this network",
// benchmarking, reserved/broadcast and the NAT64 prefixes.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
}

// blockedDestination reports addresses notifiers must never connect to
// (loopback, private, link-local, unspecified, multicast and the ranges above).
func blockedDestination(a netip.Addr) bool {
	a = a.Unmap().WithZone("")
	if a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsMulticast() || a.IsUnspecified() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// NewRestrictedHTTPClient is like NewHTTPClient but refuses to connect to
// internal destinations. The check runs on the resolved address right before
// connecting, so it also covers DNS names and DNS rebinding. Environment
// proxies are ignored (they would bypass the check).
func NewRestrictedHTTPClient() *http.Client {
	dialer := &net.Dialer{
		Timeout: httpTimeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return errBlockedDestination
			}
			a, err := netip.ParseAddr(host)
			if err != nil || blockedDestination(a) {
				return errBlockedDestination
			}
			return nil
		},
	}
	c := NewHTTPClient()
	c.Transport = &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   httpTimeout,
		ResponseHeaderTimeout: httpTimeout,
		DisableKeepAlives:     true,
	}
	return c
}

func NewSender(kind Kind, c Config, client *http.Client) (Sender, error) {
	switch kind {
	case KindDiscord:
		return &discordSender{url: c.URL, client: client}, nil
	case KindNtfy:
		return &ntfySender{base: c.URL, topic: c.Topic, token: c.Token, client: client}, nil
	}
	return nil, fmt.Errorf("notify: unknown kind %q", kind)
}

// transportError marks failures before an HTTP status was received (DNS,
// connect, TLS, blocked destination). Its text may contain network details, so
// API handlers do not show it to clients.
type transportError struct {
	kind Kind
	err  error
}

func (e *transportError) Error() string {
	return fmt.Sprintf("notify: %s request failed: %v", e.kind, e.err)
}
func (e *transportError) Unwrap() error { return e.err }

// IsTransportError reports whether err is a delivery failure without HTTP response.
func IsTransportError(err error) bool {
	var te *transportError
	return errors.As(err, &te)
}

// requestError removes the request URL (which may contain secrets) from transport errors.
func requestError(kind Kind, err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	var out error = &transportError{kind: kind, err: err}
	if errors.Is(err, errBlockedDestination) {
		out = &PermanentError{Err: out} // retrying cannot help
	}
	return out
}

func checkStatus(kind Kind, resp *http.Response) error {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	err := fmt.Errorf("notify: %s returned status %d", kind, resp.StatusCode)
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return err
	}
	return &PermanentError{Err: err}
}

var defaultTitles = map[EventType]string{
	EventNewDeviceLogin:     "Login from a new device",
	EventJailed:             "IP jailed",
	EventPermissionsChanged: "Roles or permissions changed",
	EventUserCreated:        "User created",
	EventUserDeleted:        "User deleted",
	Event2FAChanged:         "Two-factor authentication changed",
	EventAuditChainBroken:   "Audit log chain is broken",
	EventTest:               "mc-panel test notification",
}

func render(e Event) (title, body string) {
	title = e.Title
	if title == "" {
		title = defaultTitles[e.Type]
	}
	if title == "" {
		title = "mc-panel: " + string(e.Type)
	}
	var b strings.Builder
	if e.Message != "" {
		b.WriteString(e.Message)
		b.WriteString("\n")
	}
	if e.Username != "" {
		fmt.Fprintf(&b, "User: %s\n", e.Username)
	}
	if e.IP != "" {
		fmt.Fprintf(&b, "IP: %s\n", e.IP)
	}
	if !e.Time.IsZero() {
		fmt.Fprintf(&b, "Time: %s\n", e.Time.UTC().Format(time.RFC3339))
	}
	return title, strings.TrimRight(b.String(), "\n")
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
