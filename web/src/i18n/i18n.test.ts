import { afterEach, describe, expect, it, vi } from 'vitest'
import { de } from './de'
import { en } from './en'
import i18n, { detectLocale } from './index'

function keys(obj: object, prefix = ''): string[] {
  return Object.entries(obj).flatMap(([k, v]) => (typeof v === 'string' ? [`${prefix}${k}`] : keys(v as object, `${prefix}${k}.`)))
}

const ERROR_CODES = [
  'bad_request',
  'validation_failed',
  'unauthorized',
  'invalid_credentials',
  'invalid_code',
  'forbidden',
  'sudo_required',
  'setup_2fa_required',
  'csrf_invalid',
  'not_found',
  'conflict',
  'payload_too_large',
  'rate_limited',
  'jailed',
  'ip_blocked',
  'agent_unavailable',
  'docker_unavailable',
  'base_domain_missing',
  'catalog_unavailable',
  'instance_not_running',
  'rcon_unavailable',
  'files_unavailable',
  'file_exists',
  'file_modified',
  'not_text',
  'not_regular',
  'instance_running',
  'insufficient_storage',
  'plugins_unsupported',
  'plugin_source_unavailable',
  'plugin_not_downloadable',
  'plugin_incompatible',
  'backups_unavailable',
  'backup_running',
  'internal',
]

/** Keys built at runtime (template literals) – kept in sync with the components that use them. */
const DYNAMIC_KEYS = [
  ...['view', 'power', 'console', 'files', 'plugins', 'backups'].map((p) => `permission.server.${p}`),
  ...['light', 'dark', 'system'].map((p) => `theme.${p}`),
  ...['start', 'stop', 'restart'].flatMap((a) => [`servers.power.${a}`, `servers.power_noun.${a}`]),
  ...['admin', 'user'].map((r) => `users.role.${r}`),
  ...['24h', '7d', '30d'].map((r) => `security.overview.ranges.${r}`),
  ...['password', 'totp', 'webauthn', 'recovery', 'sudo'].map((s) => `security.attempts.stage.${s}`),
  ...['allow', 'deny'].map((k) => `security.ip_rules.kind_${k}`),
  ...['discord', 'ntfy'].flatMap((k) => [`security.notifiers.kind_${k}`, `security.notifiers.url_${k}`]),
  ...['success', 'failure', 'denied'].map((r) => `audit.result.${r}`),
  ...['user', 'system', 'cli'].map((a) => `audit.actor_type.${a}`),
  ...['overview', 'attempts', 'jails', 'ip_rules', 'settings', 'notifiers'].map((tab) => `security.tabs.${tab}`),
  ...['de', 'en'].map((l) => `profile.language.${l}`),
  ...['totp', 'webauthn', 'recovery'].map((m) => `second_factor.tab_${m}`),
  ...['dashboard', 'users', 'security', 'audit', 'settings', 'profile'].map((k) => `nav.${k}`),
  ...['ok', 'unavailable', 'unknown'].map((d) => `settings.docker.${d}`),
  ...['connecting', 'live', 'stopped', 'reconnecting', 'unavailable', 'too_many', 'forbidden', 'not_found', 'unauthorized'].map(
    (s) => `console.connection.${s}`,
  ),
  ...['empty', 'too_long', 'control'].map((p) => `console.validation.${p}`),
  ...['name_invalid', 'reason_too_long', 'reason_control'].map((p) => `players.validation.${p}`),
  ...['file', 'dir', 'symlink', 'other'].map((k) => `files.type.${k}`),
  ...['name', 'size', 'mtime'].map((k) => `files.sort.${k}`),
  ...['empty', 'slash', 'invalid', 'dot_dot', 'control', 'too_long', 'component_too_long', 'hidden', 'too_large'].map(
    (p) => `files.validation.${p}`,
  ),
  ...['queued', 'uploading', 'conflict', 'done', 'failed', 'cancelled', 'skipped'].map((s) => `files.uploads.status.${s}`),
  ...['insufficient_storage', 'exists', 'too_large', 'invalid', 'not_found', 'shutdown', 'other'].map((r) => `files.extract.reason.${r}`),
  ...['modrinth', 'hangar', 'manual'].map((s) => `plugins.source.${s}`),
  ...['release', 'beta', 'alpha'].map((c) => `plugins.channel.${c}`),
  ...['pending', 'downloading', 'done', 'failed', 'skipped'].map((s) => `plugins.step.state.${s}`),
  ...['download_failed', 'hash_mismatch', 'file_exists', 'insufficient_storage', 'files_unavailable', 'source_unavailable'].map(
    (c) => `plugins.step.error.${c}`,
  ),
  ...['no_compatible_version', 'client_only', 'not_downloadable', 'too_deep', 'too_many'].map((r) => `plugins.incompatible_reason.${r}`),
  ...['not_configured', 'unknown', 'creating', 'starting', 'running', 'stopping', 'stopped', 'restarting', 'error'].map(
    (s) => `servers.state.${s}`,
  ),
  ...['vanilla', 'paper', 'purpur', 'fabric', 'neoforge', 'modrinth', 'curseforge'].map((k) => `servers.type.${k}`),
  ...['vanilla', 'paper', 'purpur', 'fabric', 'neoforge', 'modrinth', 'curseforge'].map((k) => `servers.type_description.${k}`),
  ...['peaceful', 'easy', 'normal', 'hard'].map((d) => `servers.difficulty.${d}`),
  ...['survival', 'creative', 'adventure', 'spectator'].map((g) => `servers.gamemode.${g}`),
  ...['normal', 'flat', 'large_biomes', 'amplified'].map((l) => `servers.level_type.${l}`),
  ...[
    'pvp',
    'hardcore',
    'allow_flight',
    'enable_command_block',
    'online_mode',
    'whitelist_enabled',
    'max_players',
    'view_distance',
    'simulation_distance',
    'spawn_protection',
  ].map((p) => `servers.props.${p}`),
  ...['pulling', 'creating', 'starting', 'stopping', 'removing'].map((o) => `servers.operation.${o}`),
]

describe('i18n resources', () => {
  it('has identical key sets for de and en', () => {
    expect(keys(en).sort()).toEqual(keys(de).sort())
  })

  it('translates every API error code', () => {
    for (const code of ERROR_CODES) expect(i18n.exists(`errors.${code}`), code).toBe(true)
  })

  it('defines every dynamic key', () => {
    expect(DYNAMIC_KEYS.filter((k) => !i18n.exists(k))).toEqual([])
  })

  it('defines every static t() key used in the source', () => {
    const sources = import.meta.glob(['/src/**/*.{ts,tsx}', '!/src/**/*.test.{ts,tsx}', '!/src/i18n/**', '!/src/routeTree.gen.ts'], {
      query: '?raw',
      import: 'default',
      eager: true,
    }) as Record<string, string>
    const missing: string[] = []
    for (const [file, code] of Object.entries(sources)) {
      for (const match of code.matchAll(/\bt\(\s*'([A-Za-z0-9_.]+)'/g)) {
        if (!i18n.exists(match[1])) missing.push(`${file}: ${match[1]}`)
      }
    }
    expect(missing).toEqual([])
  })

  it('detects the browser language and falls back to German', () => {
    expect(detectLocale(['en-US', 'de'])).toBe('en')
    expect(detectLocale(['fr-FR', 'de-AT'])).toBe('de')
    expect(detectLocale(['fr'])).toBe('de')
    expect(detectLocale([])).toBe('de')
  })

  it('keeps <html lang> in sync with the active locale', async () => {
    await i18n.changeLanguage('en')
    expect(document.documentElement.lang).toBe('en')
    await i18n.changeLanguage('de')
    expect(document.documentElement.lang).toBe('de')
  })

  describe('before login', () => {
    afterEach(() => {
      vi.unstubAllGlobals()
      vi.resetModules()
    })

    it('sets <html lang> from the detected browser language', async () => {
      vi.stubGlobal('navigator', { ...navigator, languages: ['en-GB', 'de'] })
      vi.resetModules()
      document.documentElement.lang = ''
      const fresh = await import('./index')
      await fresh.i18nReady
      expect(document.documentElement.lang).toBe('en')
    })
  })
})
