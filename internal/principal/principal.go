// Package principal carries the authenticated user of a request through the context.
package principal

import (
	"context"
	"time"
)

type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

// Stage of a session: preauth = password ok, second factor pending;
// setup = password ok, 2FA must be configured first; full = fully authenticated.
type Stage string

const (
	StagePreAuth Stage = "preauth"
	StageSetup   Stage = "setup"
	StageFull    Stage = "full"
)

type Principal struct {
	UserID    int64
	Username  string
	Role      Role
	Stage     Stage
	SessionID int64
	SudoUntil time.Time
	CSRFToken string
}

func (p Principal) IsAdmin() bool { return p.Role == RoleAdmin }

type ctxKey struct{}

func With(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

func From(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}
