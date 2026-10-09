package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"math/big"
	"strings"
	"unicode"
)

const (
	RecoveryCodeCount = 10
	recoveryCodeLen   = 10
	recoveryAlphabet  = "abcdefghjkmnpqrstuvwxyz23456789"
)

// generateRecoveryCodes returns RecoveryCodeCount distinct codes formatted as "xxxxx-xxxxx".
func generateRecoveryCodes() ([]string, error) {
	alphabetLen := big.NewInt(int64(len(recoveryAlphabet)))
	seen := make(map[string]bool, RecoveryCodeCount)
	codes := make([]string, 0, RecoveryCodeCount)
	for len(codes) < RecoveryCodeCount {
		var sb strings.Builder
		for i := 0; i < recoveryCodeLen; i++ {
			if i == recoveryCodeLen/2 {
				sb.WriteByte('-')
			}
			n, err := rand.Int(rand.Reader, alphabetLen)
			if err != nil {
				return nil, err
			}
			sb.WriteByte(recoveryAlphabet[n.Int64()])
		}
		code := sb.String()
		if seen[code] {
			continue
		}
		seen[code] = true
		codes = append(codes, code)
	}
	return codes, nil
}

func normalizeRecoveryCode(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, s)
}

func hashRecoveryCode(s string) []byte {
	sum := sha256.Sum256([]byte(normalizeRecoveryCode(s)))
	return sum[:]
}
