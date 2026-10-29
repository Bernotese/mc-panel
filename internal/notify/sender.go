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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/Bernotese/mc-panel/internal/clock"
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
// benchmarking, reserved/broadcast, the NAT64 prefixes, and the IPv6 ranges that
// embed or tunnel IPv4 addresses or are deprecated site-local: 6to4 (2002::/16,
// e.g. 2002:7f00:1:: = 127.0.0.1), Teredo (2001::/32), fec0::/10 and the
// IPv4-compatible ::/96.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("fec0::/10"),
	netip.MustParsePrefix("::/96"),
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
// internal destinations, including every address of the host's own interfaces
// (public ones too: that traffic would reach local services over lo). The check
// runs on the resolved address right before connecting, so it also covers DNS
// names and DNS rebinding. Environment proxies are ignored (they would bypass
// the check).
func NewRestrictedHTTPClient(clk clock.Clock) *http.Client {
	local := newLocalAddrs(clk, net.InterfaceAddrs)
	dialer := &net.Dialer{
		Timeout: httpTimeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return errBlockedDestination
			}
			a, err := netip.ParseAddr(host)
			if err != nil || blockedDestination(a) || local.contains(a) {
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
	if resp.StatusCode == http.StatusTooManyRequests {
		if d, ok := parseRetryAfter(resp.Header.Get("Retry-After")); ok {
			return &retryAfterError{err: err, after: d}
		}
		return err
	}
	if resp.StatusCode >= 500 {
		return err
	}
	return &PermanentError{Err: err}
}

// retryAfterError is a rate-limited delivery whose target named a wait time (Discord answers
// 429 with Retry-After in seconds, possibly fractional).
type retryAfterError struct {
	err   error
	after time.Duration
}

func (e *retryAfterError) Error() string { return e.err.Error() }
func (e *retryAfterError) Unwrap() error { return e.err }

// parseRetryAfter reads a Retry-After value in (fractional) seconds. HTTP dates are ignored.
func parseRetryAfter(v string) (time.Duration, bool) {
	secs, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || secs <= 0 || secs > 3600 {
		return 0, false
	}
	return time.Duration(secs * float64(time.Second)), true
}

var defaultTitles = map[EventType]string{
	EventNewDeviceLogin:         "Login from a new device",
	EventJailed:                 "IP jailed",
	EventPermissionsChanged:     "Roles or permissions changed",
	EventUserCreated:            "User created",
	EventUserDeleted:            "User deleted",
	Event2FAChanged:             "Two-factor authentication changed",
	EventAuditChainBroken:       "Audit log chain is broken",
	EventBackupFailed:           "Backup failed",
	EventDevSyncFailed:          "Dev sync failed",
	EventServerDown:             "Server down",
	EventServerRecovered:        "Server back online",
	EventServerCrashLoop:        "Crash loop",
	EventServerRecoveryRequired: "A restore could not be completed",
	EventHostDiskLow:            "Low disk space on the host",
	EventHostDiskOK:             "Disk space OK again",
	Event2FALockout:             "Second factor locked",
	Event2FAFailed:              "Wrong second factor after a correct password",
	EventRecoveryCodeUsed:       "Recovery code used",
	EventPasswordChanged:        "Password changed",
	EventTest:                   "mc-panel test notification",
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

// localAddrsTTL is how long the list of interface addresses is reused.
const localAddrsTTL = time.Minute

// localAddrs caches the host's interface addresses for the dial check.
type localAddrs struct {
	clock clock.Clock
	list  func() ([]net.Addr, error)

	mu      sync.Mutex
	addrs   map[netip.Addr]struct{}
	fetched time.Time
}

func newLocalAddrs(c clock.Clock, list func() ([]net.Addr, error)) *localAddrs {
	return &localAddrs{clock: c, list: list}
}

// contains reports whether a is an address of a local interface. If the interfaces cannot be
// listed, it fails closed: every address counts as local, and the next call tries again.
func (l *localAddrs) contains(a netip.Addr) bool {
	a = a.Unmap().WithZone("")
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	if l.addrs == nil || now.Sub(l.fetched) >= localAddrsTTL {
		addrs, err := l.list()
		l.fetched = now
		if err != nil {
			l.addrs = nil
			return true
		}
		l.addrs = make(map[netip.Addr]struct{}, len(addrs))
		for _, na := range addrs {
			if p, err := netip.ParsePrefix(na.String()); err == nil {
				l.addrs[p.Addr().Unmap().WithZone("")] = struct{}{}
			} else if ip, err := netip.ParseAddr(na.String()); err == nil {
				l.addrs[ip.Unmap().WithZone("")] = struct{}{}
			}
		}
	}
	_, ok := l.addrs[a]
	return ok
}
