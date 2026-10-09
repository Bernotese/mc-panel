package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

type capturedReq struct {
	method, path, contentType, title, auth, priority string
	body                                             []byte
}

// hookServer records requests and answers with the given status codes in order
// (the last one repeats).
type hookServer struct {
	mu       sync.Mutex
	reqs     []capturedReq
	statuses []int
	srv      *httptest.Server
}

func newHookServer(t *testing.T, statuses ...int) *hookServer {
	t.Helper()
	h := &hookServer{statuses: statuses}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		h.mu.Lock()
		h.reqs = append(h.reqs, capturedReq{
			method: r.Method, path: r.URL.Path, contentType: r.Header.Get("Content-Type"),
			title: r.Header.Get("Title"), auth: r.Header.Get("Authorization"), priority: r.Header.Get("Priority"), body: b,
		})
		status := h.statuses[min(len(h.reqs), len(h.statuses))-1]
		h.mu.Unlock()
		if status == http.StatusFound {
			w.Header().Set("Location", "/redirect-target")
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *hookServer) requests() []capturedReq {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]capturedReq(nil), h.reqs...)
}

var evTime = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func TestDiscordSender(t *testing.T) {
	h := newHookServer(t, http.StatusNoContent)
	s, err := NewSender(KindDiscord, Config{URL: h.srv.URL + "/api/webhooks/1/tok"}, NewHTTPClient())
	if err != nil {
		t.Fatal(err)
	}
	err = s.Send(context.Background(), Event{Type: EventJailed, Title: "IP jailed", Message: "jailed for 15m",
		IP: "203.0.113.7/32", Username: "@everyone", Time: evTime})
	if err != nil {
		t.Fatal(err)
	}
	reqs := h.requests()
	if len(reqs) != 1 || reqs[0].method != http.MethodPost || reqs[0].path != "/api/webhooks/1/tok" ||
		reqs[0].contentType != "application/json" {
		t.Fatalf("requests = %+v", reqs)
	}
	var payload struct {
		Content         string `json:"content"`
		AllowedMentions struct {
			Parse []string `json:"parse"`
		} `json:"allowed_mentions"`
	}
	if err := json.Unmarshal(reqs[0].body, &payload); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"**IP jailed**", "jailed for 15m", "IP: 203.0.113.7/32", "User: @everyone", "Time: 2026-10-08T12:00:00Z"} {
		if !strings.Contains(payload.Content, want) {
			t.Errorf("content %q missing %q", payload.Content, want)
		}
	}
	if payload.AllowedMentions.Parse == nil || len(payload.AllowedMentions.Parse) != 0 {
		t.Fatalf("allowed_mentions.parse must be an empty array: %s", reqs[0].body)
	}
}

func TestDiscordContentIsTruncated(t *testing.T) {
	h := newHookServer(t, http.StatusNoContent)
	s, _ := NewSender(KindDiscord, Config{URL: h.srv.URL + "/api/webhooks/1/tok"}, NewHTTPClient())
	if err := s.Send(context.Background(), Event{Type: EventTest, Message: strings.Repeat("ä", 3000)}); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Content string `json:"content"`
	}
	_ = json.Unmarshal(h.requests()[0].body, &payload)
	if n := utf8.RuneCountInString(payload.Content); n > 2000 {
		t.Fatalf("content has %d runes", n)
	}
}

func TestNtfySender(t *testing.T) {
	h := newHookServer(t, http.StatusOK)
	s, err := NewSender(KindNtfy, Config{URL: h.srv.URL + "/", Topic: "alerts", Token: "tk_secret"}, NewHTTPClient())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), Event{Type: EventJailed, Title: "IP jailed", Message: "jailed", IP: "203.0.113.7/32"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), Event{Type: EventNewDeviceLogin, Title: "Login von neuem Gerät", Message: "x"}); err != nil {
		t.Fatal(err)
	}
	reqs := h.requests()
	if len(reqs) != 2 || reqs[0].path != "/alerts" || reqs[0].title != "IP jailed" || reqs[0].auth != "Bearer tk_secret" ||
		reqs[0].priority != "high" || !strings.HasPrefix(reqs[0].contentType, "text/plain") ||
		!strings.Contains(string(reqs[0].body), "IP: 203.0.113.7/32") {
		t.Fatalf("first request = %+v", reqs[0])
	}
	if reqs[1].priority != "" {
		t.Fatalf("new-device events use default priority, got %q", reqs[1].priority)
	}
	decoded, err := new(mime.WordDecoder).DecodeHeader(reqs[1].title)
	if err != nil || decoded != "Login von neuem Gerät" || !strings.HasPrefix(reqs[1].title, "=?utf-8?b?") {
		t.Fatalf("title header = %q (decoded %q, %v)", reqs[1].title, decoded, err)
	}
}

func TestSenderStatusClassification(t *testing.T) {
	cases := map[int]bool{ // status -> permanent?
		http.StatusBadRequest:          true,
		http.StatusNotFound:            true,
		http.StatusFound:               true,
		http.StatusTooManyRequests:     false,
		http.StatusServiceUnavailable:  false,
		http.StatusInternalServerError: false,
	}
	for status, permanent := range cases {
		h := newHookServer(t, status)
		s, _ := NewSender(KindNtfy, Config{URL: h.srv.URL, Topic: "alerts"}, NewHTTPClient())
		err := s.Send(context.Background(), Event{Type: EventTest})
		var pe *PermanentError
		if err == nil || errors.As(err, &pe) != permanent {
			t.Errorf("status %d: err = %v, permanent want %v", status, err, permanent)
		}
		if n := len(h.requests()); n != 1 {
			t.Errorf("status %d: %d requests (redirects must not be followed)", status, n)
		}
	}
}

func TestSenderErrorsDoNotLeakURL(t *testing.T) {
	h := newHookServer(t, http.StatusOK)
	base := h.srv.URL
	h.srv.Close()
	senders := map[string]Config{
		"discord": {URL: base + "/api/webhooks/1/SuperSecretToken"},
		"ntfy":    {URL: base, Topic: "secrettopic", Token: "tk_secret"},
	}
	for kind, cfg := range senders {
		s, _ := NewSender(Kind(kind), cfg, NewHTTPClient())
		err := s.Send(context.Background(), Event{Type: EventTest})
		if err == nil {
			t.Fatalf("%s: expected error", kind)
		}
		for _, secret := range []string{"SuperSecretToken", "secrettopic", "tk_secret"} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("%s: error %q leaks %q", kind, err, secret)
			}
		}
	}
}

func TestRenderDefaults(t *testing.T) {
	title, body := render(Event{Type: EventAuditChainBroken})
	if title != "Audit log chain is broken" || body != "" {
		t.Fatalf("render = %q / %q", title, body)
	}
	if title, _ := render(Event{Type: EventType("x.custom")}); title != "mc-panel: x.custom" {
		t.Fatalf("fallback title = %q", title)
	}
}

func TestNewSenderUnknownKind(t *testing.T) {
	if _, err := NewSender(Kind("slack"), Config{}, NewHTTPClient()); err == nil {
		t.Fatal("expected error")
	}
}
