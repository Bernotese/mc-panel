// Package defense implements rate limits, jails, IP rules and GeoIP.
// guard.go is a Phase-0 contract used by stream A; the implementation lives in stream B.
package defense

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"
)

type AttemptStage string

const (
	StagePassword AttemptStage = "password"
	StageTOTP     AttemptStage = "totp"
	StageWebAuthn AttemptStage = "webauthn"
	StageRecovery AttemptStage = "recovery"
	StageSudo     AttemptStage = "sudo"
)

type Attempt struct {
	IP        netip.Addr
	Username  string
	UserID    *int64
	Stage     AttemptStage
	Success   bool
	Reason    string // e.g. "bad_password", "unknown_user", "bad_code"
	UserAgent string
}

var (
	ErrJailed       = errors.New("defense: ip is jailed")
	ErrIPBlocked    = errors.New("defense: ip blocked by rule or country")
	ErrAdminNetwork = errors.New("defense: admin login not allowed from this network")
)

type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("defense: rate limited, retry after %s", e.RetryAfter)
}

// LoginGuard is consulted by the auth handlers.
type LoginGuard interface {
	// CheckLogin is called before verifying a password. Returns nil, ErrJailed or *RateLimitError.
	CheckLogin(ctx context.Context, ip netip.Addr, username string) error
	// CheckSecondFactor is called before verifying a 2FA/sudo code. key identifies the session.
	CheckSecondFactor(ctx context.Context, ip netip.Addr, key string) error
	// CheckAdminNetwork returns ErrAdminNetwork if admin logins are restricted and ip is not allowlisted.
	CheckAdminNetwork(ctx context.Context, ip netip.Addr) error
	// RecordAttempt stores the attempt and jails the IP when the failure threshold is reached.
	RecordAttempt(ctx context.Context, a Attempt) error
}

// GeoResolver maps an IP to an ISO 3166-1 alpha-2 country code ("" if unknown).
type GeoResolver interface {
	Country(ip netip.Addr) string
}

type NopGeo struct{}

func (NopGeo) Country(netip.Addr) string { return "" }
