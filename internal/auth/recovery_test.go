package auth

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"regexp"
	"testing"
)

var recoveryFormat = regexp.MustCompile(`^[abcdefghjkmnpqrstuvwxyz23456789]{5}-[abcdefghjkmnpqrstuvwxyz23456789]{5}$`)

func TestGenerateRecoveryCodes(t *testing.T) {
	codes, err := generateRecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != RecoveryCodeCount || RecoveryCodeCount != 10 {
		t.Fatalf("len = %d", len(codes))
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if !recoveryFormat.MatchString(c) {
			t.Errorf("bad format %q", c)
		}
		if seen[c] {
			t.Errorf("duplicate %q", c)
		}
		seen[c] = true
	}
	other, _ := generateRecoveryCodes()
	if other[0] == codes[0] && other[1] == codes[1] {
		t.Fatal("codes must be random")
	}
}

func TestRecoveryCodeNormalization(t *testing.T) {
	e := newTestEnv(t)
	if got := normalizeRecoveryCode(" ABCDE-fghjk \t"); got != "abcdefghjk" {
		t.Fatalf("normalize = %q", got)
	}
	if !bytes.Equal(e.h.hashRecoveryCode("abcde-fghjk"), e.h.hashRecoveryCode("ABCDE FGHJK")) {
		t.Fatal("hash must be computed over the normalized code")
	}
	if bytes.Equal(e.h.hashRecoveryCode("abcde-fghjk"), e.h.hashRecoveryCode("abcde-fghjm")) {
		t.Fatal("different codes must hash differently")
	}
}

// Recovery codes are stored as HMAC-SHA256 under a key derived from the secretbox key, so a
// leaked database alone does not allow an offline search of the (50-bit) code space.
func TestRecoveryCodeHashIsKeyedHMAC(t *testing.T) {
	e := newTestEnv(t)
	got := e.h.hashRecoveryCode("ABCDE-fghjk")
	key := e.h.box.DeriveKey("recovery-codes")
	m := hmac.New(sha256.New, key)
	m.Write([]byte("abcdefghjk"))
	if !bytes.Equal(got, m.Sum(nil)) {
		t.Fatal("hash must be HMAC-SHA256(DeriveKey(\"recovery-codes\"), normalized code)")
	}
	plain := sha256.Sum256([]byte("abcdefghjk"))
	if bytes.Equal(got, plain[:]) {
		t.Fatal("hash must not be plain SHA-256")
	}
}
