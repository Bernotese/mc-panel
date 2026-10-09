package password_test

import (
	"errors"
	"strings"
	"testing"

	"mcpanel/internal/password"
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

func TestVerifyMalformed(t *testing.T) {
	for _, h := range []string{"!", "", "$argon2id$v=19$m=x$a$b", "$bcrypt$..."} {
		if ok, _, err := password.Verify("x", h); ok || !errors.Is(err, password.ErrMalformedHash) {
			t.Errorf("Verify(%q) = %v, %v", h, ok, err)
		}
	}
}
