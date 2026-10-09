package auth

import (
	"bytes"
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
	if got := normalizeRecoveryCode(" ABCDE-fghjk \t"); got != "abcdefghjk" {
		t.Fatalf("normalize = %q", got)
	}
	if !bytes.Equal(hashRecoveryCode("abcde-fghjk"), hashRecoveryCode("ABCDE FGHJK")) {
		t.Fatal("hash must be computed over the normalized code")
	}
	if bytes.Equal(hashRecoveryCode("abcde-fghjk"), hashRecoveryCode("abcde-fghjm")) {
		t.Fatal("different codes must hash differently")
	}
}
