package auth

import (
	"bytes"
	"crypto/sha256"
	"testing"
)

func TestNewToken(t *testing.T) {
	a, err := newToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := newToken()
	if len(a) != 43 || a == b {
		t.Fatalf("tokens %q / %q", a, b)
	}
	if !validTokenFormat(a) {
		t.Fatal("generated token must have valid format")
	}
	for _, bad := range []string{"", "short", a + "x", a[:42] + "!", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=="} {
		if validTokenFormat(bad) {
			t.Errorf("validTokenFormat(%q) = true", bad)
		}
	}
	want := sha256.Sum256([]byte(a))
	if !bytes.Equal(hashToken(a), want[:]) {
		t.Fatal("hashToken must be SHA-256 of the token string")
	}
}
