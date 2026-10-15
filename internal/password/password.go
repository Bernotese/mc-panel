// Package password hashes and validates user passwords (argon2id, PHC string format).
package password

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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
	// ErrBusy is returned when no argon2 slot became free within the wait budget (or the
	// context ended first). Handlers answer 429 rate_limited with Retry-After: 1.
	ErrBusy = errors.New("password: too many concurrent password operations")
)

// limiter bounds the number of concurrent argon2id computations process-wide. Each one
// allocates 64 MiB and burns CPU; without a bound a login flood could exhaust memory and
// get the OOM killer to pick a Minecraft JVM on the same host.
type limiter struct {
	slots chan struct{}
	// reserved, if not nil, is an extra slot only for callers marked with WithReservedSlot
	// (logins from known devices): a flood of other logins cannot take it (SEC-09).
	reserved     chan struct{}
	wait         time.Duration
	active, peak atomic.Int64 // observability for tests
}

func newLimiter(slots int, wait time.Duration) *limiter {
	return &limiter{slots: make(chan struct{}, slots), wait: wait}
}

// newDefaultLimiter splits max(2, NumCPU/2) slots into a general pool and one reserved slot.
func newDefaultLimiter() *limiter {
	l := newLimiter(max(2, runtime.NumCPU()/2)-1, DefaultWaitBudget)
	l.reserved = make(chan struct{}, 1)
	return l
}

// DefaultWaitBudget is how long a caller waits for a free argon2 slot before ErrBusy.
const DefaultWaitBudget = 2 * time.Second

var lim = newDefaultLimiter()

type reservedSlotKey struct{}

// WithReservedSlot marks ctx so that Hash/Verify may also use the reserved argon2 slot. The
// login handler sets it for known devices (an IP key with a recent full login of the user), so
// they can still sign in while unknown addresses saturate the general pool.
func WithReservedSlot(ctx context.Context) context.Context {
	return context.WithValue(ctx, reservedSlotKey{}, true)
}

func hasReservedSlot(ctx context.Context) bool {
	v, _ := ctx.Value(reservedSlotKey{}).(bool)
	return v
}

// acquire takes a slot, waiting at most the wait budget. The returned release must be called.
func (l *limiter) acquire(ctx context.Context) (release func(), err error) {
	var reserved chan struct{} // nil (never ready in select) unless the caller may use it
	if hasReservedSlot(ctx) {
		reserved = l.reserved
	}
	var got chan struct{}
	select { // the general pool first, so the reserved slot stays free while it has room
	case l.slots <- struct{}{}:
		got = l.slots
	default:
	}
	if got == nil {
		select {
		case reserved <- struct{}{}:
			got = reserved
		default:
		}
	}
	if got == nil {
		t := time.NewTimer(l.wait)
		defer t.Stop()
		select {
		case l.slots <- struct{}{}:
			got = l.slots
		case reserved <- struct{}{}:
			got = reserved
		case <-t.C:
			return nil, ErrBusy
		case <-ctx.Done():
			return nil, ErrBusy
		}
	}
	n := l.active.Add(1)
	for {
		p := l.peak.Load()
		if n <= p || l.peak.CompareAndSwap(p, n) {
			break
		}
	}
	return func() { l.active.Add(-1); <-got }, nil
}

// commonGz is the list of common passwords that Validate rejects: every entry of at least
// MinLength characters, lower-cased, deduplicated and sorted, one per line. Sources (SecLists,
// https://github.com/danielmiessler/SecLists, MIT licence, see common.LICENSE):
// Passwords/Common-Credentials/xato-net-10-million-passwords-1000000.txt and
// 100k-most-used-passwords-NCSC.txt, plus the earlier top-100k list.
//
//go:embed common.txt.gz
var commonGz []byte

// common returns the sorted list. The decompressed text is kept as one string and the
// entries are substrings of it, so the ~47k entries cost little more than the text itself.
var common = sync.OnceValue(func() []string {
	zr, err := gzip.NewReader(bytes.NewReader(commonGz))
	if err != nil {
		panic("password: common list: " + err.Error())
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		panic("password: common list: " + err.Error())
	}
	text := string(raw)
	words := make([]string, 0, strings.Count(text, "\n"))
	for line := range strings.SplitSeq(text, "\n") {
		if line != "" {
			words = append(words, line)
		}
	}
	slices.Sort(words) // already sorted; keeps the binary search correct regardless
	return words
})

func isCommon(lower string) bool {
	_, found := slices.BinarySearch(common(), lower)
	return found
}

// minDistinct is the number of different characters a password needs at least: "aaaaaaaaaaaa"
// or "abababababab" pass the length check but are guessed immediately.
const minDistinct = 4

func tooFewDistinct(pw string) bool {
	seen := make(map[rune]struct{}, minDistinct)
	for _, r := range pw {
		seen[r] = struct{}{}
		if len(seen) >= minDistinct {
			return false
		}
	}
	return true
}

func Validate(pw, username string) error {
	n := utf8.RuneCountInString(pw)
	if n < MinLength {
		return ErrTooShort
	}
	if n > MaxLength {
		return ErrTooLong
	}
	lower := strings.ToLower(pw)
	if isCommon(lower) || tooFewDistinct(lower) {
		return ErrCommon
	}
	if u := strings.ToLower(username); len(u) >= 3 && strings.Contains(lower, u) {
		return ErrContainsUsername
	}
	return nil
}

// Hash is HashContext with context.Background().
func Hash(pw string) (string, error) { return HashContext(context.Background(), pw) }

// HashContext hashes pw with the current parameters. It returns ErrBusy if no argon2 slot
// became free within the wait budget.
func HashContext(ctx context.Context, pw string) (string, error) {
	release, err := lim.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	return hashUnlimited(pw)
}

func hashUnlimited(pw string) (string, error) {
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

// Verify is VerifyContext with context.Background().
func Verify(pw, encoded string) (ok, needsRehash bool, err error) {
	return VerifyContext(context.Background(), pw, encoded)
}

// VerifyContext checks pw against a PHC-encoded argon2id hash. A malformed hash is rejected
// before any KDF work; otherwise ErrBusy is returned if no argon2 slot became free in time.
func VerifyContext(ctx context.Context, pw, encoded string) (ok, needsRehash bool, err error) {
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
	release, err := lim.acquire(ctx)
	if err != nil {
		return false, false, err
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	release()
	ok = subtle.ConstantTimeCompare(got, want) == 1
	needsRehash = ok && (m != memory || t != iterations || p != threads || uint32(len(want)) != keyLen)
	return ok, needsRehash, nil
}

// dummyHash is computed lazily on first use (outside the limiter, so it can never fail with
// ErrBusy). A failure to produce it panics rather than silently disabling timing equalisation.
var dummyHash = sync.OnceValue(func() string {
	h, err := hashUnlimited("dummy password for timing equalisation")
	if err != nil {
		panic("password: cannot generate dummy hash: " + err.Error())
	}
	return h
})

// Warmup precomputes the hash used by DummyVerify. Servers should call it once at
// startup so the first unknown-username login costs the same as later ones.
func Warmup() { _ = dummyHash() }

// DummyVerify burns the same CPU/memory as a real Verify (used for unknown usernames).
func DummyVerify(pw string) { _ = DummyVerifyContext(context.Background(), pw) }

// DummyVerifyContext is DummyVerify that reports ErrBusy like VerifyContext, so callers can
// answer an unknown username exactly like a known one when the limiter is saturated.
func DummyVerifyContext(ctx context.Context, pw string) error {
	_, _, err := VerifyContext(ctx, pw, dummyHash())
	return err
}

// BlockForTesting replaces the process-wide limiter with one whose only general slot is taken
// and whose wait budget is wait, so every Hash/Verify returns ErrBusy after wait, except for
// callers marked with WithReservedSlot: the reserved slot stays free. unblock restores the
// previous limiter. Test-only: other packages' handler tests use it to exercise the 429 path.
func BlockForTesting(wait time.Duration) (unblock func()) {
	old := lim
	l := newLimiter(1, wait)
	l.reserved = make(chan struct{}, 1)
	l.slots <- struct{}{}
	lim = l
	return func() { lim = old }
}
