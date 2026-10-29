import type { SudoRequest } from '@/api/handlers'

/** A request that answered 403 sudo_required: method and OpenAPI path template (e.g. DELETE /servers/{id}). */
export type SudoAction = SudoRequest

/**
 * Keys below `sudo.action` for the sudo routes (`.WithSudo()` in the Go handlers, plus adding a second factor once one
 * exists, `requireSudoIf2FA`); others fall back to `generic`.
 */
export const SUDO_ACTIONS: Readonly<Record<string, string>> = {
  'POST /security/notifiers': 'notifier_create',
  'PATCH /security/notifiers/{id}': 'notifier_update',
  'DELETE /security/notifiers/{id}': 'notifier_delete',
  'POST /me/totp/setup': 'totp_add',
  'POST /me/totp/enable': 'totp_add',
  'DELETE /me/totp': 'totp_disable',
  'POST /me/passkeys/begin': 'passkey_add',
  'POST /me/passkeys/finish': 'passkey_add',
  'DELETE /me/passkeys/{id}': 'passkey_delete',
  'POST /me/recovery-codes': 'recovery_codes',
  'DELETE /admin/sessions/{id}': 'session_revoke',
  'POST /users': 'user_create',
  'PATCH /users/{id}': 'user_update',
  'DELETE /users/{id}': 'user_delete',
  'PUT /users/{id}/permissions': 'user_permissions',
  'POST /users/{id}/reset-2fa': 'user_reset_2fa',
  'POST /security/jails': 'jail_create',
  'DELETE /security/jails/{id}': 'jail_lift',
  'POST /security/ip-rules': 'ip_rule_create',
  'DELETE /security/ip-rules/{id}': 'ip_rule_delete',
  'PUT /security/settings': 'security_settings',
  'PUT /monitoring/settings': 'monitoring_settings',
  'POST /servers': 'server_create',
  'PUT /servers/settings': 'server_settings',
  'PATCH /servers/{id}': 'server_update',
  'DELETE /servers/{id}': 'server_delete',
  'POST /servers/deletions/{id}/retry': 'deletion_retry',
  'POST /servers/deletions/{id}/abandon': 'deletion_abandon',
  'POST /servers/{id}/recovery/resolve': 'recovery_resolve',
  'PUT /servers/{id}/backups/schedule': 'backup_schedule',
  'POST /servers/{id}/backups/{snapshot}/pin': 'backup_pin',
  'POST /servers/{id}/backups/{snapshot}/restore': 'backup_restore',
  'DELETE /servers/{id}/backups/{snapshot}': 'backup_delete',
}

export const GENERIC_SUDO_ACTION = 'sudo.action.generic'

/** The i18n key that names the action in the sudo dialog (SEC-36). */
export function sudoActionKey({ method, path }: SudoAction): string {
  const action = SUDO_ACTIONS[`${method.toUpperCase()} ${path}`]
  return action ? `sudo.action.${action}` : GENERIC_SUDO_ACTION
}
