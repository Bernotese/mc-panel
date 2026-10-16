package secretbox_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Bernotese/mc-panel/internal/secretbox"
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

// A crash while creating the key can leave a truncated temp file behind; the next start must
// still create a complete key and remove nothing but that leftover.
func TestLoadOrCreateKeyIgnoresLeftoverTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.key")
	if err := os.WriteFile(path+".tmp", []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	k, err := secretbox.LoadOrCreateKey(path)
	if err != nil || len(k) != secretbox.KeySize {
		t.Fatalf("create: len=%d err=%v", len(k), err)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(onDisk, k) {
		t.Fatalf("key on disk differs: %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp file still present: %v", err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %v", info.Mode().Perm())
	}
}

func TestDeriveKey(t *testing.T) {
	b1, _ := secretbox.New(bytes.Repeat([]byte{7}, secretbox.KeySize))
	b1again, _ := secretbox.New(bytes.Repeat([]byte{7}, secretbox.KeySize))
	b2, _ := secretbox.New(bytes.Repeat([]byte{8}, secretbox.KeySize))
	k := b1.DeriveKey("recovery-codes")
	if len(k) != 32 {
		t.Fatalf("len = %d", len(k))
	}
	if !bytes.Equal(k, b1again.DeriveKey("recovery-codes")) {
		t.Fatal("derivation must be deterministic")
	}
	if bytes.Equal(k, b1.DeriveKey("other")) || bytes.Equal(k, b2.DeriveKey("recovery-codes")) {
		t.Fatal("derived keys must depend on purpose and master key")
	}
	if bytes.Equal(k, bytes.Repeat([]byte{7}, secretbox.KeySize)) {
		t.Fatal("derived key must not be the master key")
	}
}
