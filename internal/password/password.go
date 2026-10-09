// Package password hashes and validates user passwords (argon2id, PHC string format).
package password

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	MinLength = 12
	MaxLength = 256

	memory     uint32 = 64 * 1024
	iterations uint32 = 3
	threads    uint8  = 2
	saltLen           = 16
	keyLen     uint32 = 32
)

var (
	ErrTooShort         = errors.New("password: too short")
	ErrTooLong          = errors.New("password: too long")
	ErrCommon           = errors.New("password: too common")
	ErrContainsUsername = errors.New("password: must not contain the username")
	ErrMalformedHash    = errors.New("password: malformed hash")
)

//go:embed common.txt
var commonRaw []byte

var common = sync.OnceValue(func() map[string]struct{} {
	m := make(map[string]struct{}, 4096)
	sc := bufio.NewScanner(bytes.NewReader(commonRaw))
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			m[line] = struct{}{}
		}
	}
	return m
})

func Validate(pw, username string) error {
	n := utf8.RuneCountInString(pw)
	if n < MinLength {
		return ErrTooShort
	}
	if n > MaxLength {
		return ErrTooLong
	}
	lower := strings.ToLower(pw)
	if _, ok := common()[lower]; ok {
		return ErrCommon
	}
	if u := strings.ToLower(username); len(u) >= 3 && strings.Contains(lower, u) {
		return ErrContainsUsername
	}
	return nil
}

func Hash(pw string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	return encode(pw, salt, memory, iterations, threads), nil
}

// HashWithParams exists for tests and migrations of old parameters.
func HashWithParams(pw string, m, t uint32, p uint8) string {
	salt := make([]byte, saltLen)
	_, _ = rand.Read(salt)
	return encode(pw, salt, m, t, p)
}

func encode(pw string, salt []byte, m, t uint32, p uint8) string {
	key := argon2.IDKey([]byte(pw), salt, t, m, p, keyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, m, t, p, b64.EncodeToString(salt), b64.EncodeToString(key))
}

func Verify(pw, encoded string) (ok, needsRehash bool, err error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, false, ErrMalformedHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, false, ErrMalformedHash
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil || m == 0 || t == 0 || p == 0 {
		return false, false, ErrMalformedHash
	}
	b64 := base64.RawStdEncoding
	salt, err1 := b64.DecodeString(parts[4])
	want, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false, false, ErrMalformedHash
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	ok = subtle.ConstantTimeCompare(got, want) == 1
	needsRehash = ok && (m != memory || t != iterations || p != threads || uint32(len(want)) != keyLen)
	return ok, needsRehash, nil
}

var dummyHash = sync.OnceValue(func() string {
	h, _ := Hash("dummy password for timing equalisation")
	return h
})

// DummyVerify burns the same CPU/memory as a real Verify (used for unknown usernames).
func DummyVerify(pw string) { _, _, _ = Verify(pw, dummyHash()) }
