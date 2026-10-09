package secretbox_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"mcpanel/internal/secretbox"
)

func TestSealOpenRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{7}, secretbox.KeySize)
	b, err := secretbox.New(key)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := b.Seal([]byte("JBSWY3DPEHPK3PXP"), []byte("totp:1"))
	if err != nil {
		t.Fatal(err)
	}
	pt, err := b.Open(ct, []byte("totp:1"))
	if err != nil || string(pt) != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("Open = %q, %v", pt, err)
	}
	if _, err := b.Open(ct, []byte("totp:2")); !errors.Is(err, secretbox.ErrInvalid) {
		t.Fatalf("wrong AAD must fail with ErrInvalid, got %v", err)
	}
	ct[len(ct)-1] ^= 1
	if _, err := b.Open(ct, []byte("totp:1")); !errors.Is(err, secretbox.ErrInvalid) {
		t.Fatalf("tampered ciphertext must fail, got %v", err)
	}
	if _, err := b.Open([]byte{1, 2}, nil); !errors.Is(err, secretbox.ErrInvalid) {
		t.Fatalf("short ciphertext must fail, got %v", err)
	}
}

func TestNewRejectsBadKey(t *testing.T) {
	if _, err := secretbox.New(make([]byte, 16)); err == nil {
		t.Fatal("expected error for 16-byte key")
	}
}

func TestLoadOrCreateKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.key")
	k1, err := secretbox.LoadOrCreateKey(path)
	if err != nil || len(k1) != secretbox.KeySize {
		t.Fatalf("create: len=%d err=%v", len(k1), err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %v", info.Mode().Perm())
	}
	k2, err := secretbox.LoadOrCreateKey(path)
	if err != nil || !bytes.Equal(k1, k2) {
		t.Fatalf("reload mismatch err=%v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := secretbox.LoadOrCreateKey(path); err == nil {
		t.Fatal("expected error for world-readable key file")
	}
}
