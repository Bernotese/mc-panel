// Package audit records security-relevant actions in a tamper-evident log.
// types.go is a Phase-0 contract; the SQLite implementation lives in stream D.
package audit

import "context"

type Result string

const (
	ResultSuccess Result = "success"
	ResultFailure Result = "failure"
	ResultDenied  Result = "denied"
)

type ActorType string

const (
	ActorUser   ActorType = "user"
	ActorSystem ActorType = "system"
	ActorCLI    ActorType = "cli"
)

// Entry describes one action. If ActorType is empty, the recorder fills actor
// fields from principal.From(ctx) (or ActorSystem if none) and IP from httpx.ClientIP(ctx).
type Entry struct {
	Action    string
	Target    string
	Result    Result
	Details   map[string]any
	ActorType ActorType
	ActorID   *int64
	ActorName string
	IP        string
}

type Recorder interface {
	Record(ctx context.Context, e Entry) error
}

// Actions. Streams must use these constants.
const (
	ActionLogin                = "auth.login"
	ActionLogout               = "auth.logout"
	ActionPasswordChange       = "auth.password_change"
	ActionTOTPEnable           = "auth.totp_enable"
	ActionTOTPDisable          = "auth.totp_disable"
	ActionPasskeyAdd           = "auth.passkey_add"
	ActionPasskeyRemove        = "auth.passkey_remove"
	ActionRecoveryRegenerate   = "auth.recovery_regenerate"
	ActionRecoveryUsed         = "auth.recovery_used"
	ActionSessionRevoke        = "auth.session_revoke"
	ActionSudo                 = "auth.sudo"
	ActionUserCreate           = "user.create"
	ActionUserUpdate           = "user.update"
	ActionUserDelete           = "user.delete"
	ActionUserReset2FA         = "user.reset_2fa"
	ActionPermissionsSet       = "rbac.permissions_set"
	ActionServerCreate         = "server.create"
	ActionServerDelete         = "server.delete"
	ActionServerPower          = "server.power"
	ActionServerUpdate         = "server.update"
	ActionServerSettingsUpdate = "server.settings_update"
	ActionServerConsoleCommand = "server.console_command"
	ActionServerPlayerAction   = "server.player_action"
	ActionJailAuto             = "security.jail_auto"
	ActionJailCreate           = "security.jail_create"
	ActionJailLift             = "security.jail_lift"
	ActionIPRuleCreate         = "security.ip_rule_create"
	ActionIPRuleDelete         = "security.ip_rule_delete"
	ActionSettingsUpdate       = "security.settings_update"
	ActionNotifierCreate       = "security.notifier_create"
	ActionNotifierUpdate       = "security.notifier_update"
	ActionNotifierDelete       = "security.notifier_delete"
)
