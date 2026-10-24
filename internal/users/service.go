package users

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"regexp"
	"time"

	"github.com/Bernotese/mc-panel/internal/password"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
)

var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9_.-]{3,32}$`)

var (
	ErrInvalidUsername = errors.New("username must match ^[a-zA-Z0-9_.-]{3,32}$")
	ErrInvalidRole     = errors.New("role must be admin or user")
	ErrUsernameTaken   = errors.New("username already taken")
)

// ValidateUsername reports whether name is acceptable as a username (ErrInvalidUsername if not).
func ValidateUsername(name string) error {
	if !usernameRe.MatchString(name) {
		return ErrInvalidUsername
	}
	return nil
}

func validRole(role string) bool {
	return role == string(principal.RoleAdmin) || role == string(principal.RoleUser)
}

// isValidationError reports whether err is a client input error (→ 400 validation_failed).
func isValidationError(err error) bool {
	for _, target := range []error{
		ErrInvalidUsername, ErrInvalidRole,
		password.ErrTooShort, password.ErrTooLong, password.ErrCommon, password.ErrContainsUsername,
	} {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

// newUser validates the input, hashes the password and inserts the user with a fresh 32-byte
// WebAuthn user handle. Shared by POST /users and `panel admin create`. mustChange makes the
// user change the password at the next sign-in (SEC-45: an admin chose it for them).
func newUser(ctx context.Context, q *store.Queries, now time.Time, username, pw, role string, mustChange bool) (store.User, error) {
	if err := ValidateUsername(username); err != nil {
		return store.User{}, err
	}
	if !validRole(role) {
		return store.User{}, ErrInvalidRole
	}
	if err := password.Validate(pw, username); err != nil {
		return store.User{}, err
	}
	if _, err := q.RbacGetUserByUsername(ctx, username); err == nil {
		return store.User{}, ErrUsernameTaken
	} else if !errors.Is(err, sql.ErrNoRows) {
		return store.User{}, err
	}
	hash, err := password.HashContext(ctx, pw)
	if err != nil {
		return store.User{}, err
	}
	handle := make([]byte, 32)
	if _, err := rand.Read(handle); err != nil {
		return store.User{}, err
	}
	id, err := q.RbacNextUserID(ctx)
	if err != nil {
		return store.User{}, err
	}
	ms := now.UnixMilli()
	u, err := q.RbacCreateUser(ctx, store.RbacCreateUserParams{
		ID: id, Username: username, PasswordHash: hash, Role: role, WebauthnHandle: handle,
		MustChangePassword: boolInt(mustChange), PasswordChangedAt: ms, CreatedAt: ms, UpdatedAt: ms,
	})
	if store.IsUniqueViolation(err) {
		return store.User{}, ErrUsernameTaken
	}
	return u, err
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
