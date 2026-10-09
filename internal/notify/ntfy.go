package notify

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

type ntfySender struct {
	base, topic, token string
	client             *http.Client
}

// headerValue removes control characters and RFC-2047-encodes non-ASCII text.
func headerValue(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return mime.BEncoding.Encode("utf-8", s)
		}
	}
	return s
}

func (n *ntfySender) Send(ctx context.Context, e Event) error {
	title, body := render(e)
	target, err := url.JoinPath(n.base, n.topic)
	if err != nil {
		return &PermanentError{Err: errors.New("notify: ntfy: invalid server URL")}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(body))
	if err != nil {
		return &PermanentError{Err: errors.New("notify: ntfy: invalid server URL")}
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.Header.Set("User-Agent", "mc-panel")
	req.Header.Set("Title", headerValue(title))
	if e.Type == EventJailed || e.Type == EventAuditChainBroken {
		req.Header.Set("Priority", "high")
	}
	if n.token != "" {
		req.Header.Set("Authorization", "Bearer "+n.token)
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return requestError(KindNtfy, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return checkStatus(KindNtfy, resp)
}
