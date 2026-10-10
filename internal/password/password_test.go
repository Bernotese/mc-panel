package password_test

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/password"
)

func TestValidate(t *testing.T) {
	cases := []struct {
		pw, user string
		want     error
	}{
		{"correct horse battery", "jannes", nil},
		{"short", "jannes", password.ErrTooShort},
		{strings.Repeat("ä", 11), "jannes", password.ErrTooShort},
		{strings.Repeat("ä", 12), "jannes", nil},
		{strings.Repeat("a", 257), "jannes", password.ErrTooLong},
		{"PasswordPassword", "jannes", password.ErrCommon},
		{"my-JANNES-password", "jannes", password.ErrContainsUsername},
	}
	for _, tc := range cases {
		if err := password.Validate(tc.pw, tc.user); !errors.Is(err, tc.want) {
			t.Errorf("Validate(%q) = %v, want %v", tc.pw, err, tc.want)
		}
	}
}

func TestHashVerify(t *testing.T) {
	h, err := password.Hash("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("unexpected encoding %q", h)
	}
	h2, _ := password.Hash("correct horse battery")
	if h == h2 {
		t.Fatal("hashes must be salted")
	}
	ok, rehash, err := password.Verify("correct horse battery", h)
	if !ok || rehash || err != nil {
		t.Fatalf("Verify = %v %v %v", ok, rehash, err)
	}
	if ok, _, _ := password.Verify("wrong horse battery", h); ok {
		t.Fatal("wrong password accepted")
	}
}

func TestVerifyOldParamsNeedsRehash(t *testing.T) {
	// m=19456,t=2,p=1 (older params), password "correct horse battery"
	old := password.HashWithParams("correct horse battery", 19456, 2, 1)
	ok, rehash, err := password.Verify("correct horse battery", old)
	if !ok || !rehash || err != nil {
		t.Fatalf("Verify = %v %v %v", ok, rehash, err)
	}
}

func TestVerifyAcceptsMinimumParamsNeedsRehash(t *testing.T) {
	// Lower bound of the accepted range (m=8 KiB, t=1, p=1) still verifies.
	h := password.HashWithParams("correct horse battery", 8, 1, 1)
	ok, rehash, err := password.Verify("correct horse battery", h)
	if !ok || !rehash || err != nil {
		t.Fatalf("Verify = %v %v %v", ok, rehash, err)
	}
}

func TestVerifyMalformed(t *testing.T) {
	for _, h := range []string{"!", "", "$argon2id$v=19$m=x$a$b", "$bcrypt$..."} {
		if ok, _, err := password.Verify("x", h); ok || !errors.Is(err, password.ErrMalformedHash) {
			t.Errorf("Verify(%q) = %v, %v", h, ok, err)
		}
	}
}

// encodedWith builds a PHC string with the given version and parameter fields and
// valid base64 salt/key of the requested byte lengths, so that only the field
// under test is invalid.
func encodedWith(version, params string, saltLen, keyLen int) string {
	b64 := base64.RawStdEncoding
	salt := b64.EncodeToString(make([]byte, saltLen))
	key := b64.EncodeToString(make([]byte, keyLen))
	return fmt.Sprintf("$argon2id$%s$%s$%s$%s", version, params, salt, key)
}

func TestVerifyRejectsOutOfRangeAndMalformedFields(t *testing.T) {
	cases := []struct {
		name string
		enc  string
	}{
		{"memory overflow uint32 max", encodedWith("v=19", "m=4294967295,t=1,p=1", 16, 32)},
		{"time cost far too large", encodedWith("v=19", "m=65536,t=1000,p=2", 16, 32)},
		{"time cost zero", encodedWith("v=19", "m=65536,t=0,p=2", 16, 32)},
		{"memory below minimum", encodedWith("v=19", "m=7,t=3,p=2", 16, 32)},
		{"memory above maximum", encodedWith("v=19", "m=262145,t=3,p=2", 16, 32)},
		{"parallelism zero", encodedWith("v=19", "m=65536,t=3,p=0", 16, 32)},
		{"parallelism above maximum", encodedWith("v=19", "m=65536,t=3,p=17", 16, 32)},
		{"parallelism overflows uint8", encodedWith("v=19", "m=65536,t=3,p=256", 16, 32)},
		{"trailing junk in version", encodedWith("v=19junk", "m=65536,t=3,p=2", 16, 32)},
		{"unsupported version", encodedWith("v=18", "m=65536,t=3,p=2", 16, 32)},
		{"trailing junk in parallelism", encodedWith("v=19", "m=65536,t=3,p=2xyz", 16, 32)},
		{"trailing junk in memory", encodedWith("v=19", "m=65536junk,t=3,p=2", 16, 32)},
		{"extra parameter field", encodedWith("v=19", "m=65536,t=3,p=2,x=1", 16, 32)},
		{"missing parameter field", encodedWith("v=19", "m=65536,t=3", 16, 32)},
		{"salt too short", encodedWith("v=19", "m=65536,t=3,p=2", 7, 32)},
		{"salt too long", encodedWith("v=19", "m=65536,t=3,p=2", 65, 32)},
		{"key too short", encodedWith("v=19", "m=65536,t=3,p=2", 16, 15)},
		{"key too long", encodedWith("v=19", "m=65536,t=3,p=2", 16, 65)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, _, err := password.Verify("x", tc.enc)
			if ok || !errors.Is(err, password.ErrMalformedHash) {
				t.Errorf("Verify(%q) = ok=%v err=%v, want ErrMalformedHash", tc.enc, ok, err)
			}
		})
	}
}

func TestVerifyHugeMemoryRejectedWithoutWork(t *testing.T) {
	// Regression: m=4294967295 used to be passed straight to argon2.IDKey, which
	// tried to allocate ~4 TiB. It must now be rejected before any KDF work.
	enc := encodedWith("v=19", "m=4294967295,t=1,p=1", 16, 32)
	_, _, err := password.Verify("x", enc)
	if !errors.Is(err, password.ErrMalformedHash) {
		t.Fatalf("Verify = %v, want ErrMalformedHash", err)
	}
}

func TestDummyVerifyDoesNotPanic(t *testing.T) {
	password.Warmup()
	password.DummyVerify("anything")
	password.DummyVerify("")
}
