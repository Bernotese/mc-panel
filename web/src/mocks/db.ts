import type {
  AgentStatus,
  AttemptStage,
  AuditEntry,
  AuditResult,
  Catalog,
  InstanceConfig,
  InstanceType,
  IPRule,
  Jail,
  Locale,
  LoginAttempt,
  Me,
  NewDeviceLogin,
  Notifier,
  OnlinePlayer,
  Passkey,
  PermissionGrant,
  PlayerBan,
  PlayerEntry,
  PlayerOp,
  Role,
  SecondFactor,
  SecuritySettings,
  ServerProperties,
  ServerStatus,
  Session,
  Stage,
  User,
} from '@/api/types'

/** Seeded credentials for MSW mode (local UI development and tests only). */
export const MOCK_PASSWORD = 'correct-horse-battery'
export const MOCK_TOTP_CODE = '123456'
export const MOCK_TOTP_SECRET = 'JBSWY3DPEHPK3PXP'
export const MOCK_RECOVERY_CODES = [
  'abcdefghjk',
  'mnpqrstuvw',
  'xyz2345678',
  'a2b3c4d5e6',
  'f7g8h9jkmn',
  'pqrstuvwxy',
  'z23456789a',
  'bcdefghjkm',
  'npqrstuvwx',
  'yz23456789',
]
export const MOCK_CLIENT_IP = '198.51.100.7'
export const WEBAUTHN_CHALLENGE = 'bW9jay1jaGFsbGVuZ2U'
export const SUDO_MS = 10 * 60 * 1000
const MINUTE = 60_000
export const HOUR = 60 * MINUTE
export const DAY = 24 * HOUR
const RECOVERY_ALPHABET = 'abcdefghjkmnpqrstuvwxyz23456789'
const UA_MAC_SAFARI =
  'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15'
const UA_IPHONE =
  'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1'
const UA_WIN_FIREFOX = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:131.0) Gecko/20100101 Firefox/131.0'
const UA_LINUX_CHROME =
  'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36'

export interface MockUser {
  id: number
  username: string
  password: string
  role: Role
  locale: Locale
  totpEnabled: boolean
  totpPending: boolean
  passkeys: Passkey[]
  recoveryCodes: string[]
  createdAt: number
  lastLoginAt: number | null
}

export interface MockSession {
  id: number
  userId: number
  stage: Stage
  csrf: string
  sudoUntil: number
  ip: string
  country: string
  userAgent: string
  createdAt: number
  lastSeenAt: number
}

/** What the mock "Minecraft server" keeps itself: online players, ops.json, whitelist.json, banned-players.json. */
export interface MockPlayers {
  online: OnlinePlayer[]
  ops: PlayerOp[]
  whitelist: PlayerEntry[]
  bans: PlayerBan[]
}

export function emptyPlayers(): MockPlayers {
  return { online: [], ops: [], whitelist: [], bans: [] }
}

const ALEX_UUID = 'ec561538-f3fd-461d-aff5-086b22154bce'
const STEVE_UUID = '8667ba71-b85a-4004-af54-457a9734eed7'

export interface MockServer {
  id: number
  name: string
  instance: InstanceConfig | null // properties always fully resolved (defaults from spec §7)
  status: ServerStatus
  revision: number // desired revision
  runningRevision: number // revision of the "running container"
  players: MockPlayers
}

const MIB = 1024 * 1024
const GIB = 1024 * MIB

/** Every property set: spec §7 defaults (motd = server name) overridden by p. */
export function resolveProperties(name: string, p: ServerProperties = {}): Required<ServerProperties> {
  return {
    motd: name,
    difficulty: 'normal',
    gamemode: 'survival',
    max_players: 20,
    pvp: true,
    hardcore: false,
    allow_flight: false,
    enable_command_block: false,
    online_mode: true,
    whitelist_enabled: false,
    view_distance: 10,
    simulation_distance: 10,
    spawn_protection: 16,
    seed: '',
    level_type: 'normal',
    ...p,
  }
}

/** Container limit like the agent sets it: memory_mib × 1.25 MiB. */
export function memoryLimitBytes(memoryMib: number): number {
  return memoryMib * 1.25 * MIB
}

export function runningStatus(memoryMib: number, startedAt: number, memoryBytes = Math.round(memoryLimitBytes(memoryMib) * 0.4), cpu = 3): ServerStatus {
  return {
    state: 'running',
    desired_state: 'running',
    restart_required: false,
    operation: '',
    last_error: '',
    started_at: startedAt,
    memory_bytes: memoryBytes,
    memory_limit_bytes: memoryLimitBytes(memoryMib),
    cpu_percent: cpu,
  }
}

export function stoppedStatus(): ServerStatus {
  return {
    state: 'stopped',
    desired_state: 'stopped',
    restart_required: false,
    operation: '',
    last_error: '',
    started_at: 0,
    memory_bytes: 0,
    memory_limit_bytes: 0,
    cpu_percent: 0,
  }
}

export function notConfiguredStatus(): ServerStatus {
  return { ...stoppedStatus(), state: 'not_configured', desired_state: null }
}

/** ServerSettings without unknown_containers (always empty in the mock). */
export interface MockServerSettings {
  base_domain: string | null
  curseforge_api_key_set: boolean
  docker: 'ok' | 'unavailable' | 'unknown'
}

export interface MockDb {
  users: MockUser[]
  sessions: MockSession[]
  currentSessionId: number | null
  servers: MockServer[]
  serverSettings: MockServerSettings
  catalogs: Partial<Record<InstanceType, Catalog>>
  grants: Map<number, PermissionGrant[]>
  audit: AuditEntry[]
  attempts: LoginAttempt[]
  newDeviceLogins: NewDeviceLogin[]
  jails: Jail[]
  ipRules: IPRule[]
  settings: SecuritySettings
  geoipAvailable: boolean
  notifiers: Notifier[]
  agent: AgentStatus
  nextId: number
}

function fakeHash(id: number): string {
  return ((id * 2654435761) >>> 0).toString(16).padStart(8, '0').repeat(8)
}

function seedAttempts(now: number): LoginAttempt[] {
  const ips = ['203.0.113.5', '203.0.113.9', '198.51.100.23', '2001:db8::1f', '192.0.2.44']
  const countries: Record<string, string> = {
    '203.0.113.5': 'CN',
    '203.0.113.9': 'RU',
    '198.51.100.23': 'DE',
    '2001:db8::1f': 'US',
    '192.0.2.44': 'NL',
  }
  const names = ['admin', 'root', 'alice', 'minecraft']
  const out: LoginAttempt[] = []
  for (let i = 0; i < 150; i++) {
    const ip = ips[i % ips.length]
    const success = i % 4 === 0
    out.push({
      id: 1000 + 150 - i,
      ts: now - i * 4 * HOUR - (i % 7) * MINUTE,
      ip,
      username: success ? 'admin' : names[i % names.length],
      stage: success && i % 8 === 0 ? 'totp' : 'password',
      success,
      reason: success ? 'ok' : 'invalid_credentials',
      country: countries[ip],
      user_agent: i % 3 === 0 ? 'python-requests/2.32.3' : UA_MAC_SAFARI,
    })
  }
  return out
}

function seedAudit(now: number): AuditEntry[] {
  const pattern: Array<[string, string, AuditResult]> = [
    ['auth.login', 'admin', 'success'],
    ['user.create', 'user:alice', 'success'],
    ['permissions.set', 'user:alice', 'success'],
    ['auth.login', 'root', 'failure'],
    ['security.jail.create', '203.0.113.5/32', 'success'],
    ['server.power', 'server:survival', 'success'],
    ['user.delete', 'user:carol', 'denied'],
  ]
  return Array.from({ length: 60 }, (_, i): AuditEntry => {
    const [action, target, result] = pattern[i % pattern.length]
    const id = i + 1
    const system = action === 'security.jail.create'
    return {
      id,
      ts: now - (60 - i) * 6 * HOUR,
      actor_type: system ? 'system' : 'user',
      actor_id: system ? null : 1,
      actor_name: system ? 'system' : 'admin',
      action,
      target,
      ip: system ? '' : MOCK_CLIENT_IP,
      result,
      details: system ? { reason: '5 failed logins in 10 min' } : {},
      hash: fakeHash(id),
    }
  })
}

function seed(now: number): MockDb {
  return {
    users: [
      {
        id: 1,
        username: 'admin',
        password: MOCK_PASSWORD,
        role: 'admin',
        locale: 'de',
        totpEnabled: true,
        totpPending: false,
        passkeys: [{ id: 1, name: 'MacBook Touch ID', created_at: now - 20 * DAY, last_used_at: now - 2 * DAY }],
        recoveryCodes: [...MOCK_RECOVERY_CODES],
        createdAt: now - 30 * DAY,
        lastLoginAt: now - DAY,
      },
      {
        id: 2,
        username: 'alice',
        password: MOCK_PASSWORD,
        role: 'user',
        locale: 'de',
        totpEnabled: false,
        totpPending: false,
        passkeys: [],
        recoveryCodes: [],
        createdAt: now - 10 * DAY,
        lastLoginAt: now - 3 * DAY,
      },
      {
        id: 3,
        username: 'newadmin',
        password: MOCK_PASSWORD,
        role: 'admin',
        locale: 'de',
        totpEnabled: false,
        totpPending: false,
        passkeys: [],
        recoveryCodes: [],
        createdAt: now - HOUR,
        lastLoginAt: null,
      },
      {
        id: 4,
        username: 'bob',
        password: MOCK_PASSWORD,
        role: 'user',
        locale: 'en',
        totpEnabled: true,
        totpPending: false,
        passkeys: [],
        recoveryCodes: MOCK_RECOVERY_CODES.slice(0, 5),
        createdAt: now - 5 * DAY,
        lastLoginAt: now - 4 * DAY,
      },
    ],
    sessions: [
      { id: 101, userId: 1, stage: 'full', csrf: 'seed-101', sudoUntil: 0, ip: '198.51.100.23', country: 'DE', userAgent: UA_IPHONE, createdAt: now - 3 * HOUR, lastSeenAt: now - 20 * MINUTE },
      { id: 102, userId: 1, stage: 'full', csrf: 'seed-102', sudoUntil: 0, ip: '203.0.113.200', country: 'AT', userAgent: UA_WIN_FIREFOX, createdAt: now - 8 * HOUR, lastSeenAt: now - 2 * HOUR },
      { id: 103, userId: 2, stage: 'full', csrf: 'seed-103', sudoUntil: 0, ip: '192.0.2.10', country: 'DE', userAgent: UA_LINUX_CHROME, createdAt: now - DAY, lastSeenAt: now - 5 * HOUR },
    ],
    currentSessionId: null,
    servers: [
      {
        id: 1,
        name: 'survival',
        instance: { type: 'paper', version: '1.21.11', loader_version: null, modpack: null, java: 'java21', memory_mib: 4096, properties: resolveProperties('survival') },
        status: runningStatus(4096, now - 2 * HOUR, Math.round(2.1 * GIB), 12.5),
        revision: 1,
        runningRevision: 1,
        players: {
          online: [
            { name: 'Alex', uuid: ALEX_UUID },
            { name: 'Steve', uuid: STEVE_UUID },
          ],
          ops: [{ uuid: ALEX_UUID, name: 'Alex', level: 4 }],
          whitelist: [
            { uuid: ALEX_UUID, name: 'Alex' },
            { uuid: STEVE_UUID, name: 'Steve' },
          ],
          bans: [
            { uuid: '1f0b3c55-6a7d-4e8f-9a0b-1c2d3e4f5a6b', name: 'Griefer123', reason: 'Griefing', source: 'Alex', created: '2026-10-01 18:04:12 +0000', expires: 'forever' },
          ],
        },
      },
      {
        id: 2,
        name: 'creative',
        instance: { type: 'vanilla', version: '26.3', loader_version: null, modpack: null, java: 'java25', memory_mib: 2048, properties: resolveProperties('creative', { gamemode: 'creative' }) },
        status: stoppedStatus(),
        revision: 1,
        runningRevision: 1,
        players: emptyPlayers(),
      },
      { id: 3, name: 'lobby', instance: null, status: notConfiguredStatus(), revision: 0, runningRevision: 0, players: emptyPlayers() },
    ],
    serverSettings: { base_domain: 'mc.example.com', curseforge_api_key_set: false, docker: 'ok' },
    catalogs: {
      vanilla: {
        versions: [
          { id: '26.3', java: 'java25' },
          { id: '26.2', java: 'java25' },
          { id: '1.21.11', java: 'java21' },
          { id: '1.20.4', java: 'java17' },
          { id: '1.16.5', java: 'java8' },
        ],
        loaders: [],
      },
      paper: {
        versions: [
          { id: '26.2', java: 'java25' },
          { id: '1.21.11', java: 'java21' },
          { id: '1.20.4', java: 'java17' },
        ],
        loaders: [],
      },
      purpur: {
        versions: [
          { id: '26.2', java: 'java25' },
          { id: '1.21.11', java: 'java21' },
        ],
        loaders: [],
      },
      fabric: {
        versions: [
          { id: '26.3', java: 'java25' },
          { id: '1.21.11', java: 'java21' },
        ],
        loaders: ['0.19.5', '0.19.4'],
      },
      // Minecraft versions like every other type (plan 02, task 15); the NeoForge build is optional (loader_version).
      neoforge: {
        versions: [
          { id: '26.2', java: 'java25' },
          { id: '1.21.11', java: 'java21' },
        ],
        loaders: [],
      },
    },
    grants: new Map<number, PermissionGrant[]>([
      [
        2,
        [
          { server_id: 1, permissions: ['server.view', 'server.power'] },
          { server_id: 2, permissions: ['server.view'] },
        ],
      ],
    ]),
    audit: seedAudit(now),
    attempts: seedAttempts(now),
    newDeviceLogins: [
      { ts: now - 2 * HOUR, username: 'admin', ip: '203.0.113.200', country: 'AT', user_agent: UA_WIN_FIREFOX },
      { ts: now - 3 * DAY, username: 'alice', ip: '192.0.2.10', country: 'DE', user_agent: UA_LINUX_CHROME },
    ],
    jails: [
      { id: 1, ip_key: '203.0.113.5/32', reason: '5 failed logins in 10 min', level: 1, manual: false, created_at: now - 5 * MINUTE, expires_at: now + 10 * MINUTE, lifted_at: null, lifted_by: null, firewall_synced: true },
      { id: 2, ip_key: '2001:db8::/64', reason: 'repeat offender', level: 3, manual: false, created_at: now - HOUR, expires_at: now + HOUR, lifted_at: null, lifted_by: null, firewall_synced: false },
      { id: 3, ip_key: '192.0.2.44/32', reason: '5 failed logins in 10 min', level: 1, manual: false, created_at: now - 3 * DAY, expires_at: now - 3 * DAY + 15 * MINUTE, lifted_at: now - 3 * DAY + 5 * MINUTE, lifted_by: 'admin', firewall_synced: true },
    ],
    ipRules: [
      { id: 1, cidr: '198.51.100.0/24', kind: 'allow', comment: 'Home network', expires_at: null, created_by: 'admin', created_at: now - 25 * DAY },
      { id: 2, cidr: '192.0.2.0/24', kind: 'deny', comment: 'Scanner', expires_at: now + 7 * DAY, created_by: 'admin', created_at: now - 2 * DAY },
    ],
    settings: { enforce_2fa_for_users: false, admin_allowlist_only: false, allowed_countries: [] },
    geoipAvailable: true,
    notifiers: [
      { id: 1, kind: 'discord', name: 'Discord #alerts', enabled: true, target_masked: 'https://discord.com/api/webhooks/…/****', created_at: now - 20 * DAY },
      { id: 2, kind: 'ntfy', name: 'ntfy Handy', enabled: false, target_masked: 'https://ntfy.sh/mc-…', created_at: now - 4 * DAY },
    ],
    agent: { reachable: true, firewall: 'noop', version: '0.1.0-dev', pending_sync: 1 },
    nextId: 10_000,
  }
}

export const db: MockDb = seed(Date.now())

export function resetDb(now: number = Date.now()): void {
  Object.assign(db, seed(now))
}

export function nextId(): number {
  db.nextId += 1
  return db.nextId
}

function newToken(prefix: string): string {
  return `${prefix}-${nextId()}-${Math.random().toString(36).slice(2, 10)}`
}

export function findUser(id: number): MockUser | undefined {
  return db.users.find((u) => u.id === id)
}

export function findUserByName(name: string): MockUser | undefined {
  return db.users.find((u) => u.username === name)
}

export function currentSession(): MockSession | undefined {
  return db.sessions.find((s) => s.id === db.currentSessionId)
}

export function hasSecondFactor(u: MockUser): boolean {
  return u.totpEnabled || u.passkeys.length > 0
}

function secondFactorsOf(u: MockUser): SecondFactor[] {
  const factors: SecondFactor[] = []
  if (u.totpEnabled) factors.push('totp')
  if (u.passkeys.length > 0) factors.push('webauthn')
  if (u.recoveryCodes.length > 0 && hasSecondFactor(u)) factors.push('recovery')
  return factors
}

export function toMe(u: MockUser, s: MockSession): Me {
  return {
    id: u.id,
    username: u.username,
    role: u.role,
    stage: s.stage,
    locale: u.locale,
    totp_enabled: u.totpEnabled,
    passkey_count: u.passkeys.length,
    recovery_codes_remaining: u.recoveryCodes.length,
    second_factors: secondFactorsOf(u),
    sudo_until: s.sudoUntil,
    csrf_token: s.csrf,
  }
}

export function toUser(u: MockUser): User {
  return {
    id: u.id,
    username: u.username,
    role: u.role,
    totp_enabled: u.totpEnabled,
    passkey_count: u.passkeys.length,
    created_at: u.createdAt,
    last_login_at: u.lastLoginAt,
  }
}

export function toSession(s: MockSession): Session {
  return {
    id: s.id,
    user_id: s.userId,
    username: findUser(s.userId)?.username ?? '?',
    current: s.id === db.currentSessionId,
    ip: s.ip,
    country: s.country,
    user_agent: s.userAgent,
    created_at: s.createdAt,
    last_seen_at: s.lastSeenAt,
    expires_at: s.createdAt + 12 * HOUR,
  }
}

/** Replaces the browser's current session (models the __Host-sid cookie). */
export function startSession(u: MockUser, stage: Stage): MockSession {
  const now = Date.now()
  db.sessions = db.sessions.filter((s) => s.id !== db.currentSessionId)
  const session: MockSession = {
    id: nextId(),
    userId: u.id,
    stage,
    csrf: newToken('csrf'),
    sudoUntil: 0,
    ip: MOCK_CLIENT_IP,
    country: 'DE',
    userAgent: navigator.userAgent,
    createdAt: now,
    lastSeenAt: now,
  }
  db.sessions.push(session)
  db.currentSessionId = session.id
  return session
}

/** New session id + CSRF token after every privilege change (session fixation protection). */
export function rotateSession(s: MockSession): void {
  s.id = nextId()
  s.csrf = newToken('csrf')
  db.currentSessionId = s.id
}

export function stageAfterPassword(u: MockUser): Stage {
  if (hasSecondFactor(u)) return 'preauth'
  if (u.role === 'admin' || db.settings.enforce_2fa_for_users) return 'setup'
  return 'full'
}

export function recordAudit(
  actor: MockUser | null,
  action: string,
  target: string,
  result: AuditResult = 'success',
  details: Record<string, unknown> = {},
): void {
  const id = (db.audit[db.audit.length - 1]?.id ?? 0) + 1
  db.audit.push({
    id,
    ts: Date.now(),
    actor_type: actor ? 'user' : 'system',
    actor_id: actor?.id ?? null,
    actor_name: actor?.username ?? 'system',
    action,
    target,
    ip: actor ? MOCK_CLIENT_IP : '',
    result,
    details,
    hash: fakeHash(id),
  })
}

export function recordAttempt(username: string, stage: AttemptStage, success: boolean, reason: string): void {
  const id = Math.max(0, ...db.attempts.map((a) => a.id)) + 1
  db.attempts.unshift({
    id,
    ts: Date.now(),
    ip: MOCK_CLIENT_IP,
    username,
    stage,
    success,
    reason,
    country: 'DE',
    user_agent: navigator.userAgent,
  })
}

export function generateRecoveryCodes(): string[] {
  return Array.from({ length: 10 }, () =>
    Array.from({ length: 10 }, () => RECOVERY_ALPHABET[Math.floor(Math.random() * RECOVERY_ALPHABET.length)]).join(''),
  )
}

export function normalizeCode(code: string): string {
  return code.toLowerCase().replace(/[\s-]/g, '')
}

/** Mock-only approximation of httpx.IPKey: IPv4 → /32, IPv6 → /64 (no real prefix math). */
export function ipKeyOf(ip: string): string {
  return ip.includes(':') ? `${ip}/64` : `${ip}/32`
}

/** Test/dev helper: puts the "browser" into an authenticated session without HTTP. */
export function loginAs(username: string, opts: { stage?: Stage; sudo?: boolean } = {}): Me {
  const user = findUserByName(username)
  if (!user) throw new Error(`unknown mock user ${username}`)
  const session = startSession(user, opts.stage ?? 'full')
  if (opts.sudo) session.sudoUntil = Date.now() + SUDO_MS
  return toMe(user, session)
}
