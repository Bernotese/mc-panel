package agent_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mcpanel/internal/agent"
	"mcpanel/internal/agent/agentapi"
	"mcpanel/internal/agent/banrules"
)

const jsonCT = "application/json"

func serve(h http.Handler, method, path, contentType, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://agent"+path, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestServerHealth(t *testing.T) {
	fw, _ := newNoop()
	srv := agent.NewServer(fw, "1.2.3", testLogger(io.Discard))
	rec := serve(srv, http.MethodGet, agentapi.PathHealth, "", "")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != jsonCT {
		t.Fatalf("status=%d ct=%q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var h agentapi.HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &h); err != nil {
		t.Fatal(err)
	}
	if h != (agentapi.HealthResponse{Status: "ok", Version: "1.2.3", Firewall: "noop"}) {
		t.Fatalf("health = %+v", h)
	}
}

func TestServerBanListUnban(t *testing.T) {
	fw, clk := newNoop()
	var logs syncBuffer
	srv := agent.NewServer(fw, "test", testLogger(&logs))

	for _, body := range []string{
		`{"prefix":"203.0.113.7/32","timeout_seconds":900}`,
		`{"prefix":"2001:db8:1:2::/64","timeout_seconds":691200}`,
	} {
		if rec := serve(srv, http.MethodPost, agentapi.PathBan, jsonCT, body); rec.Code != http.StatusNoContent {
			t.Fatalf("ban %s: status=%d body=%s", body, rec.Code, rec.Body)
		}
	}
	clk.Advance(100 * time.Second)
	rec := serve(srv, http.MethodGet, agentapi.PathBans, "", "")
	want := `{"bans":[{"prefix":"203.0.113.7/32","expires_in_seconds":800},{"prefix":"2001:db8:1:2::/64","expires_in_seconds":691100}]}` + "\n"
	if rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Fatalf("bans: status=%d body=%s", rec.Code, rec.Body)
	}

	for range 2 { // unban is idempotent
		if rec := serve(srv, http.MethodPost, agentapi.PathUnban, jsonCT, `{"prefix":"203.0.113.7/32"}`); rec.Code != http.StatusNoContent {
			t.Fatalf("unban: status=%d body=%s", rec.Code, rec.Body)
		}
	}
	rec = serve(srv, http.MethodGet, agentapi.PathBans, "", "")
	if want := `{"bans":[{"prefix":"2001:db8:1:2::/64","expires_in_seconds":691100}]}` + "\n"; rec.Body.String() != want {
		t.Fatalf("bans after unban: %s", rec.Body)
	}

	for _, want := range []string{`msg="firewall ban"`, "prefix=203.0.113.7/32", "timeout_s=900", "backend=noop", `msg="firewall unban"`} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log missing %q:\n%s", want, logs.String())
		}
	}
}

func TestServerEmptyBanListIsArray(t *testing.T) {
	fw, _ := newNoop()
	srv := agent.NewServer(fw, "test", testLogger(io.Discard))
	if rec := serve(srv, http.MethodGet, agentapi.PathBans, "", ""); rec.Body.String() != `{"bans":[]}`+"\n" {
		t.Fatalf("empty list body = %s", rec.Body)
	}
}

func TestServerRejectsInvalidRequests(t *testing.T) {
	ban := func(prefix string, timeout string) string {
		return fmt.Sprintf(`{"prefix":%q,"timeout_seconds":%s}`, prefix, timeout)
	}
	cases := []struct {
		name, method, path, ct, body string
		want                         int
	}{
		{"unknown field", "POST", agentapi.PathBan, jsonCT, `{"prefix":"203.0.113.7/32","timeout_seconds":60,"extra":1}`, 400},
		{"missing prefix", "POST", agentapi.PathBan, jsonCT, `{"timeout_seconds":60}`, 400},
		{"missing timeout", "POST", agentapi.PathBan, jsonCT, `{"prefix":"203.0.113.7/32"}`, 400},
		{"ipv4 /24", "POST", agentapi.PathBan, jsonCT, ban("203.0.113.0/24", "60"), 400},
		{"mapped ipv6", "POST", agentapi.PathBan, jsonCT, ban("::ffff:203.0.113.7/128", "60"), 400},
		{"mapped ipv4 space", "POST", agentapi.PathBan, jsonCT, ban("::ffff:0:0/96", "60"), 400},
		{"host bits", "POST", agentapi.PathBan, jsonCT, ban("2001:db8:1:2::5/64", "60"), 400},
		{"zone", "POST", agentapi.PathBan, jsonCT, ban("fe80::1%eth0/128", "60"), 400},
		{"link-local", "POST", agentapi.PathBan, jsonCT, ban("fe80::/64", "60"), 400},
		{"multicast", "POST", agentapi.PathBan, jsonCT, ban("ff02::/64", "60"), 400},
		{"loopback", "POST", agentapi.PathBan, jsonCT, ban("127.0.0.1/32", "60"), 400},
		{"upper case", "POST", agentapi.PathBan, jsonCT, ban("2001:DB8:1:2::/64", "60"), 400},
		{"timeout zero", "POST", agentapi.PathBan, jsonCT, ban("203.0.113.7/32", "0"), 400},
		{"timeout negative", "POST", agentapi.PathBan, jsonCT, ban("203.0.113.7/32", "-1"), 400},
		{"timeout too long", "POST", agentapi.PathBan, jsonCT, ban("203.0.113.7/32", "691201"), 400},
		{"max int64", "POST", agentapi.PathBan, jsonCT, ban("203.0.113.7/32", "9223372036854775807"), 400},
		{"overflow literal", "POST", agentapi.PathBan, jsonCT, ban("203.0.113.7/32", "99999999999999999999"), 400},
		{"float", "POST", agentapi.PathBan, jsonCT, ban("203.0.113.7/32", "1.5"), 400},
		{"exponent", "POST", agentapi.PathBan, jsonCT, ban("203.0.113.7/32", "1e3"), 400},
		{"string timeout", "POST", agentapi.PathBan, jsonCT, ban("203.0.113.7/32", `"60"`), 400},
		{"trailing data", "POST", agentapi.PathBan, jsonCT, ban("203.0.113.7/32", "60") + ban("203.0.113.8/32", "60"), 400},
		{"not json", "POST", agentapi.PathBan, jsonCT, `prefix=203.0.113.7/32`, 400},
		{"too large", "POST", agentapi.PathBan, jsonCT, `{"prefix":"` + strings.Repeat("a", 5000) + `","timeout_seconds":60}`, 413},
		{"wrong content type", "POST", agentapi.PathBan, "text/plain", ban("203.0.113.7/32", "60"), 415},
		{"missing content type", "POST", agentapi.PathBan, "", ban("203.0.113.7/32", "60"), 415},
		{"GET on ban", "GET", agentapi.PathBan, "", "", 405},
		{"POST on bans", "POST", agentapi.PathBans, jsonCT, `{}`, 405},
		{"unknown path", "GET", "/v1/nope", "", "", 404},
		{"unban invalid", "POST", agentapi.PathUnban, jsonCT, `{"prefix":"0.0.0.0/0"}`, 400},
		{"unban unknown field", "POST", agentapi.PathUnban, jsonCT, `{"prefix":"203.0.113.7/32","timeout_seconds":1}`, 400},
	}
	fw, _ := newNoop()
	var logs syncBuffer
	srv := agent.NewServer(fw, "test", testLogger(&logs))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := serve(srv, tc.method, tc.path, tc.ct, tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body)
			}
			if rec.Header().Get("Content-Type") != jsonCT {
				t.Fatalf("content type = %q", rec.Header().Get("Content-Type"))
			}
			var er agentapi.ErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &er); err != nil || er.Error == "" {
				t.Fatalf("error body = %s (%v)", rec.Body, err)
			}
			if tc.want == http.StatusMethodNotAllowed && rec.Header().Get("Allow") == "" {
				t.Fatal("405 without Allow header")
			}
		})
	}
	if bans, err := fw.List(t.Context()); err != nil || len(bans) != 0 {
		t.Fatalf("invalid requests reached the firewall: %+v %v", bans, err)
	}
	if !strings.Contains(logs.String(), `msg="agent request rejected"`) {
		t.Fatal("rejections are not logged")
	}
}

func TestServerAcceptsMaxTimeout(t *testing.T) {
	fw, _ := newNoop()
	srv := agent.NewServer(fw, "test", testLogger(io.Discard))
	if rec := serve(srv, http.MethodPost, agentapi.PathBan, "application/json; charset=utf-8", `{"prefix":"203.0.113.7/32","timeout_seconds":691200}`); rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
}

func TestServerFirewallFailure(t *testing.T) {
	var logs syncBuffer
	srv := agent.NewServer(stubFirewall{err: errors.New("netlink: permission denied")}, "test", testLogger(&logs))
	for _, rec := range []*httptest.ResponseRecorder{
		serve(srv, http.MethodPost, agentapi.PathBan, jsonCT, `{"prefix":"203.0.113.7/32","timeout_seconds":60}`),
		serve(srv, http.MethodPost, agentapi.PathUnban, jsonCT, `{"prefix":"203.0.113.7/32"}`),
		serve(srv, http.MethodGet, agentapi.PathBans, "", ""),
	} {
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
		if body := rec.Body.String(); strings.Contains(body, "netlink") || !strings.Contains(body, "firewall operation failed") {
			t.Fatalf("body = %s", body)
		}
	}
	if !strings.Contains(logs.String(), "netlink: permission denied") {
		t.Fatal("firewall error was not logged")
	}
}

func TestServerConflict(t *testing.T) {
	fw := stubFirewall{err: fmt.Errorf("%w: 2001:db8:1:2::1/128 overlaps 2001:db8:1:2::/64", banrules.ErrConflict)}
	srv := agent.NewServer(fw, "test", testLogger(io.Discard))
	rec := serve(srv, http.MethodPost, agentapi.PathBan, jsonCT, `{"prefix":"2001:db8:1:2::1/128","timeout_seconds":60}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "overlaps") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
}
