package notify

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"mcpanel/internal/secretbox"
)

const testWebhook = "https://discord.com/api/webhooks/123456789012345678/SuperSecretToken_abc-XYZ"

func TestValidateConfig(t *testing.T) {
	cases := []struct {
		name  string
		kind  Kind
		cfg   Config
		dev   bool
		valid bool
	}{
		{"discord ok", KindDiscord, Config{URL: testWebhook}, false, true},
		{"discordapp ok", KindDiscord, Config{URL: "https://discordapp.com/api/webhooks/1/abc"}, false, true},
		{"discord wrong host", KindDiscord, Config{URL: "https://evil.example/api/webhooks/1/abc"}, false, false},
		{"discord subdomain", KindDiscord, Config{URL: "https://discord.com.evil.example/api/webhooks/1/abc"}, false, false},
		{"discord wrong path", KindDiscord, Config{URL: "https://discord.com/api/other/1/abc"}, false, false},
		{"discord non-numeric id", KindDiscord, Config{URL: "https://discord.com/api/webhooks/abc/def"}, false, false},
		{"discord missing token", KindDiscord, Config{URL: "https://discord.com/api/webhooks/1"}, false, false},
		{"discord with port", KindDiscord, Config{URL: "https://discord.com:8443/api/webhooks/1/abc"}, false, false},
		{"discord with topic", KindDiscord, Config{URL: testWebhook, Topic: "x"}, false, false},
		{"discord http in prod", KindDiscord, Config{URL: "http://discord.com/api/webhooks/1/abc"}, false, false},
		{"query not allowed", KindDiscord, Config{URL: testWebhook + "?wait=true"}, false, false},
		{"fragment not allowed", KindDiscord, Config{URL: testWebhook + "#x"}, false, false},
		{"credentials not allowed", KindNtfy, Config{URL: "https://user:pw@ntfy.sh", Topic: "alerts"}, false, false},
		{"ntfy ok", KindNtfy, Config{URL: "https://ntfy.sh", Topic: "mc-alerts_1", Token: "tk_abc"}, false, true},
		{"ntfy with path", KindNtfy, Config{URL: "https://example.org/ntfy", Topic: "alerts"}, false, true},
		{"ntfy missing topic", KindNtfy, Config{URL: "https://ntfy.sh"}, false, false},
		{"ntfy bad topic", KindNtfy, Config{URL: "https://ntfy.sh", Topic: "a/b"}, false, false},
		{"ntfy token with space", KindNtfy, Config{URL: "https://ntfy.sh", Topic: "a", Token: "a b"}, false, false},
		{"ntfy token too long", KindNtfy, Config{URL: "https://ntfy.sh", Topic: "a", Token: strings.Repeat("t", 257)}, false, false},
		{"ntfy localhost in prod", KindNtfy, Config{URL: "https://localhost:8080", Topic: "a"}, false, false},
		{"ntfy private ip in prod", KindNtfy, Config{URL: "https://10.0.0.5", Topic: "a"}, false, false},
		{"ntfy loopback v6 in prod", KindNtfy, Config{URL: "https://[::1]:2586", Topic: "a"}, false, false},
		{"ntfy http localhost in dev", KindNtfy, Config{URL: "http://127.0.0.1:2586", Topic: "a"}, true, true},
		{"ntfy http in prod", KindNtfy, Config{URL: "http://ntfy.sh", Topic: "a"}, false, false},
		{"relative url", KindNtfy, Config{URL: "/ntfy", Topic: "a"}, false, false},
		{"ftp scheme", KindNtfy, Config{URL: "ftp://ntfy.sh", Topic: "a"}, true, false},
		{"too long", KindNtfy, Config{URL: "https://ntfy.sh/" + strings.Repeat("a", 2048), Topic: "a"}, false, false},
		{"unknown kind", Kind("slack"), Config{URL: "https://hooks.slack.com/x"}, false, false},
	}
	for _, tc := range cases {
		err := ValidateConfig(tc.kind, tc.cfg, tc.dev)
		if (err == nil) != tc.valid {
			t.Errorf("%s: err = %v, valid want %v", tc.name, err, tc.valid)
		}
		if err != nil && (strings.Contains(err.Error(), "SuperSecretToken") || strings.Contains(err.Error(), "tk_abc")) {
			t.Errorf("%s: error leaks secret: %v", tc.name, err)
		}
	}
}

func TestMaskTargetHidesSecrets(t *testing.T) {
	d := MaskTarget(KindDiscord, Config{URL: testWebhook})
	if d != "https://discord.com/api/webhooks/1234…/****" || strings.Contains(d, "SuperSecret") {
		t.Fatalf("discord mask = %q", d)
	}
	n := MaskTarget(KindNtfy, Config{URL: "https://ntfy.sh", Topic: "mcpanel-alerts-x7", Token: "tk_secret"})
	if n != "https://ntfy.sh/mcp**** (token)" || strings.Contains(n, "alerts") || strings.Contains(n, "tk_secret") {
		t.Fatalf("ntfy mask = %q", n)
	}
	short := MaskTarget(KindNtfy, Config{URL: "https://example.org/ntfy/", Topic: "abcd"})
	if short != "https://example.org/ntfy/ab****" {
		t.Fatalf("short topic mask = %q", short)
	}
	if got := MaskTarget(KindNtfy, Config{URL: "::"}); got != "****" {
		t.Fatalf("unparsable URL mask = %q", got)
	}
}

func TestSealOpenConfig(t *testing.T) {
	box, err := secretbox.New(bytes.Repeat([]byte{3}, secretbox.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{URL: "https://ntfy.sh", Topic: "alerts", Token: "tk_secret"}
	enc, err := SealConfig(box, KindNtfy, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc, []byte("tk_secret")) {
		t.Fatal("ciphertext contains plaintext token")
	}
	got, err := OpenConfig(box, KindNtfy, enc)
	if err != nil || got != cfg {
		t.Fatalf("OpenConfig = %+v, %v", got, err)
	}
	if _, err := OpenConfig(box, KindDiscord, enc); !errors.Is(err, secretbox.ErrInvalid) {
		t.Fatalf("wrong kind (AAD) must fail with ErrInvalid, got %v", err)
	}
}
