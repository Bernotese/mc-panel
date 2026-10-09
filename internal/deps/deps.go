//go:build deps

// Package deps pins every third-party dependency used by mc-panel so that
// parallel workstreams never need to edit go.mod. It is never compiled.
package deps

import (
	_ "github.com/BurntSushi/toml"
	_ "github.com/go-chi/chi/v5"
	_ "github.com/go-webauthn/webauthn/webauthn"
	_ "github.com/google/nftables"
	_ "github.com/oschwald/maxminddb-golang/v2"
	_ "github.com/pquerna/otp/totp"
	_ "github.com/pressly/goose/v3"
	_ "golang.org/x/crypto/argon2"
	_ "golang.org/x/sys/unix"
	_ "golang.org/x/time/rate"
	_ "modernc.org/sqlite"
)
