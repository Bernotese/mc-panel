package auth

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"mcpanel/internal/secretbox"
)

func TestTOTPKey(t *testing.T) {
	secret, url, err := newTOTPKey("jannes")
	if err != nil {
		t.Fatal(err)
	}
	if len(secret) != 32 { // 20 bytes base32 without padding
		t.Fatalf("secret len = %d (%q)", len(secret), secret)
	}
	for _, part := range []string{"otpauth://totp/", "issuer=mc-panel", "secret=" + secret, "period=30", "digits=6"} {
		if !strings.Contains(url, part) {
			t.Errorf("url %q missing %q", url, part)
		}
	}
}

func TestMatchTOTP(t *testing.T) {
	secret, _, err := newTOTPKey("jannes")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 8, 12, 0, 10, 0, time.UTC)
	cur := totpStep(now)
	if cur != now.Unix()/30 {
		t.Fatalf("step = %d", cur)
	}
	for offset, wantOK := range map[int64]bool{-2: false, -1: true, 0: true, 1: true, 2: false} {
		code, err := totpCodeAt(secret, cur+offset)
		if err != nil {
			t.Fatal(err)
		}
		step, ok := matchTOTP(secret, code, now)
		if ok != wantOK {
			t.Errorf("offset %d: ok=%v want %v", offset, ok, wantOK)
		}
		if ok && step != cur+offset {
			// a code can collide with a neighbouring step only by chance (1e-6); treat as failure
			t.Errorf("offset %d: step=%d want %d", offset, step, cur+offset)
		}
	}
	code, _ := totpCodeAt(secret, cur)
	if _, ok := matchTOTP(secret, " "+code[:3]+" "+code[3:]+" ", now); !ok {
		t.Error("spaces inside the code must be ignored")
	}
	for _, bad := range []string{"", "12345", "1234567", "abcdef", "12345a"} {
		if _, ok := matchTOTP(secret, bad, now); ok {
			t.Errorf("matchTOTP(%q) = ok", bad)
		}
	}
}

func TestSealTOTPSecret(t *testing.T) {
	box, err := secretbox.New(bytes.Repeat([]byte{9}, secretbox.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	enc, err := sealTOTPSecret(box, 7, "JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc, []byte("JBSWY3DPEHPK3PXP")) {
		t.Fatal("secret stored in plain text")
	}
	if got, err := openTOTPSecret(box, 7, enc); err != nil || got != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("open = %q, %v", got, err)
	}
	if _, err := openTOTPSecret(box, 8, enc); err == nil {
		t.Fatal("secret of user 7 must not open for user 8 (AAD)")
	}
}
