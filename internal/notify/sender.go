package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
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

// NewHTTPClient returns the client used for notifications: 5 s timeout and no redirects
// (a redirect could leak the webhook token to another host).
func NewHTTPClient() *http.Client {
	return &http.Client{
		Timeout:       httpTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
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

// requestError removes the request URL (which may contain secrets) from transport errors.
func requestError(kind Kind, err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return fmt.Errorf("notify: %s request failed: %w", kind, err)
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
