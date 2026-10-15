// Package secretbox encrypts small secrets at rest (AES-256-GCM).
package secretbox

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const KeySize = 32

var ErrInvalid = errors.New("secretbox: invalid ciphertext")

type Box struct {
	aead cipher.AEAD
	key  []byte
}

func New(key []byte) (*Box, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("secretbox: key must be %d bytes, got %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead, key: bytes.Clone(key)}, nil
}

// DeriveKey returns a 32-byte subkey for purpose: HMAC-SHA256(master key, "mc-panel/" + purpose).
// Used where a keyed hash is needed (e.g. recovery codes) without reusing the AEAD key itself.
func (b *Box) DeriveKey(purpose string) []byte {
	m := hmac.New(sha256.New, b.key)
	m.Write([]byte("mc-panel/" + purpose))
	return m.Sum(nil)
}

// Seal returns nonce||ciphertext. aad binds the ciphertext to its purpose (e.g. "totp:42").
func (b *Box) Seal(plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, plaintext, aad), nil
}

func (b *Box) Open(ciphertext, aad []byte) ([]byte, error) {
	ns := b.aead.NonceSize()
	if len(ciphertext) < ns+b.aead.Overhead() {
		return nil, ErrInvalid
	}
	pt, err := b.aead.Open(nil, ciphertext[:ns], ciphertext[ns:], aad)
	if err != nil {
		return nil, ErrInvalid
	}
	return pt, nil
}

// LoadOrCreateKey reads a 32-byte key from path or creates it (mode 0600).
func LoadOrCreateKey(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		key := make([]byte, KeySize)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		if err := writeKeyAtomic(path, key); err != nil {
			return nil, fmt.Errorf("secretbox: create key: %w", err)
		}
		return key, nil
	}
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("secretbox: key file %s must not be accessible by group/others (mode %v)", path, info.Mode().Perm())
	}
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(key) != KeySize {
		return nil, fmt.Errorf("secretbox: key file %s has %d bytes, want %d", path, len(key), KeySize)
	}
	return key, nil
}

// writeKeyAtomic writes key to path.tmp, syncs it and renames it to path, then syncs the
// directory: a crash leaves either no key or the complete one, never a truncated key. A
// leftover temp file from an earlier crash is overwritten.
func writeKeyAtomic(path string, key []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil { // the leftover may have had another mode
		f.Close()
		return err
	}
	if _, err := f.Write(key); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
