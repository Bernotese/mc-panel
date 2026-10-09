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
	"strconv"
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

// Accepted ranges for parameters read from a stored hash. Anything outside them is
// treated as malformed, so a corrupted or hostile hash cannot make Verify allocate
// unbounded memory or run for an unbounded time.
const (
	minStoredMemory  uint64 = 8
	maxStoredMemory  uint64 = 256 * 1024
	minStoredTime    uint64 = 1
	maxStoredTime    uint64 = 10
	minStoredThreads uint64 = 1
	maxStoredThreads uint64 = 16
	minSaltLen              = 8
	maxSaltLen              = 64
	minKeyLen               = 16
	maxKeyLen               = 64
)

// parseField strips the expected prefix and parses the rest as a strict unsigned
// decimal of the given bit size. Trailing junk, signs and empty values are rejected.
func parseField(s, prefix string, bitSize int) (uint64, bool) {
	rest, ok := strings.CutPrefix(s, prefix)
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseUint(rest, 10, bitSize)
	if err != nil {
		return 0, false
	}
	return v, true
}

func Verify(pw, encoded string) (ok, needsRehash bool, err error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, false, ErrMalformedHash
	}
	version, okVersion := parseField(parts[2], "v=", 32)
	if !okVersion || version != uint64(argon2.Version) {
		return false, false, ErrMalformedHash
	}
	params := strings.Split(parts[3], ",")
	if len(params) != 3 {
		return false, false, ErrMalformedHash
	}
	mem, okM := parseField(params[0], "m=", 64)
	tim, okT := parseField(params[1], "t=", 64)
	par, okP := parseField(params[2], "p=", 64)
	if !okM || !okT || !okP ||
		mem < minStoredMemory || mem > maxStoredMemory ||
		tim < minStoredTime || tim > maxStoredTime ||
		par < minStoredThreads || par > maxStoredThreads {
		return false, false, ErrMalformedHash
	}
	m, t, p := uint32(mem), uint32(tim), uint8(par)

	b64 := base64.RawStdEncoding
	salt, err1 := b64.DecodeString(parts[4])
	want, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil ||
		len(salt) < minSaltLen || len(salt) > maxSaltLen ||
		len(want) < minKeyLen || len(want) > maxKeyLen {
		return false, false, ErrMalformedHash
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	ok = subtle.ConstantTimeCompare(got, want) == 1
	needsRehash = ok && (m != memory || t != iterations || p != threads || uint32(len(want)) != keyLen)
	return ok, needsRehash, nil
}

// dummyHash is computed lazily on first use. A failure to produce it panics rather
// than silently disabling timing equalisation.
var dummyHash = sync.OnceValue(func() string {
	h, err := Hash("dummy password for timing equalisation")
	if err != nil {
		panic("password: cannot generate dummy hash: " + err.Error())
	}
	return h
})

// Warmup precomputes the hash used by DummyVerify. Servers should call it once at
// startup so the first unknown-username login costs the same as later ones.
func Warmup() { _ = dummyHash() }

// DummyVerify burns the same CPU/memory as a real Verify (used for unknown usernames).
func DummyVerify(pw string) { _, _, _ = Verify(pw, dummyHash()) }
