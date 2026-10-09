package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

const tokenBytes = 32

// newToken returns 256 random bits encoded as unpadded base64url (43 characters).
func newToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken returns the SHA-256 of the token; only this hash is stored.
func hashToken(tok string) []byte {
	sum := sha256.Sum256([]byte(tok))
	return sum[:]
}

// validTokenFormat rejects garbage cookies before they reach the database.
func validTokenFormat(tok string) bool {
	if len(tok) != base64.RawURLEncoding.EncodedLen(tokenBytes) {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(tok)
	return err == nil && len(b) == tokenBytes
}
