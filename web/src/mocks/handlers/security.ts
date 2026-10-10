import { http, HttpResponse } from 'msw'
import type {
  AgentStatus,
  IPRule,
  Jail,
  LoginAttemptPage,
  Notifier,
  NotifierKind,
  SecurityOverview,
  SecurityRange,
  SecuritySettingsResponse,
} from '@/api/types'
import { db, DAY, HOUR, ipKeyOf, MOCK_CLIENT_IP, nextId, recordAudit } from '../db'
import { API, apiError, guard, idParam, noContent, op, paginate, readJson, type MockOperation } from './util'

const RANGES: Record<SecurityRange, { buckets: number; size: number }> = {
  '24h': { buckets: 24, size: HOUR },
  '7d': { buckets: 7, size: DAY },
  '30d': { buckets: 30, size: DAY },
}
const COUNTRY = /^[A-Z]{2}$/
const CIDR = /^[0-9a-fA-F:.]+\/\d{1,3}$/
const IPV4 = /^(\d{1,3}\.){3}\d{1,3}$/

function isActive(j: Jail, now: number): boolean {
  return j.lifted_at === null && j.expires_at > now
}

function settingsResponse(): SecuritySettingsResponse {
  return { ...db.settings, allowed_countries: [...db.settings.allowed_countries], geoip_available: db.geoipAvailable }
}

function maskTarget(kind: NotifierKind, url: string, topic: string): string {
  let origin = url
  try {
    origin = new URL(url).origin
  } catch {
    // keep raw value
  }
  return kind === 'discord' ? `${origin}/api/webhooks/…/****` : `${origin}/${topic.slice(0, 3)}…`
}

function validUrl(url: unknown): url is string {
  return typeof url === 'string' && /^https?:\/\/\S+$/.test(url)
}

function topCounts(keys: string[], limit: number): Array<[string, number]> {
  const counts = new Map<string, number>()
  for (const key of keys) counts.set(key, (counts.get(key) ?? 0) + 1)
  return [...counts.entries()].sort((a, b) => b[1] - a[1]).slice(0, limit)
}

export const securityOperations: MockOperation[] = [
  op(
    'getSecurityOverview',
    http.get(`${API}/security/overview`, ({ request }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      const raw = new URL(request.url).searchParams.get('range') ?? '24h'
      if (raw !== '24h' && raw !== '7d' && raw !== '30d') return apiError(400, 'bad_request', 'range must be 24h, 7d or 30d')
      const { buckets, size } = RANGES[raw]
      const now = Date.now()
      const start = now - buckets * size
      const inRange = db.attempts.filter((a) => a.ts >= start && a.ts <= now)
      const series = Array.from({ length: buckets }, (_, i) => {
        const t = start + i * size
        const slice = inRange.filter((a) => a.ts >= t && a.ts < t + size)
        return { t, success: slice.filter((a) => a.success).length, failure: slice.filter((a) => !a.success).length }
      })
      const failures = inRange.filter((a) => !a.success)
      const countryOf = new Map(failures.map((a) => [ipKeyOf(a.ip), a.country]))
      const overview: SecurityOverview = {
        range: raw,
        series,
        top_ips: topCounts(failures.map((a) => ipKeyOf(a.ip)), 5).map(([ip_key, n]) => ({ ip_key, country: countryOf.get(ip_key) ?? '', failures: n })),
        top_countries: db.geoipAvailable
          ? topCounts(failures.map((a) => a.country).filter(Boolean), 5).map(([country, n]) => ({ country, failures: n }))
          : [],
        active_jails: db.jails.filter((j) => isActive(j, now)).length,
        failures_total: failures.length,
        new_device_logins: db.newDeviceLogins.filter((d) => d.ts >= start),
      }
      return HttpResponse.json(overview)
    }),
  ),

  op(
    'listLoginAttempts',
    http.get(`${API}/security/attempts`, ({ request }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      const url = new URL(request.url)
      const success = url.searchParams.get('success')
      const ip = url.searchParams.get('ip') ?? ''
      const items = [...db.attempts]
        .sort((a, b) => b.ts - a.ts)
        .filter((a) => success === null || String(a.success) === success)
        .filter((a) => !ip || a.ip.includes(ip))
      return HttpResponse.json<LoginAttemptPage>(paginate(items, url))
    }),
  ),

  op(
    'listJails',
    http.get(`${API}/security/jails`, ({ request }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      const activeOnly = new URL(request.url).searchParams.get('active') !== 'false'
      const now = Date.now()
      const jails = [...db.jails].sort((a, b) => b.created_at - a.created_at).filter((j) => !activeOnly || isActive(j, now))
      return HttpResponse.json<Jail[]>(jails)
    }),
  ),

  op(
    'createJail',
    http.post(`${API}/security/jails`, async ({ request }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const body = await readJson<{ ip?: string; duration_minutes?: number; reason?: string }>(request)
      const ip = body?.ip?.trim() ?? ''
      const minutes = body?.duration_minutes ?? 0
      const reason = body?.reason ?? ''
      if (!(IPV4.test(ip) || ip.includes(':'))) return apiError(400, 'validation_failed', 'invalid IP address')
      if (!Number.isInteger(minutes) || minutes < 1 || minutes > 10080) return apiError(400, 'validation_failed', 'duration must be 1-10080 minutes')
      if (reason.length > 200) return apiError(400, 'validation_failed', 'reason too long')
      if (ip === MOCK_CLIENT_IP) return apiError(409, 'conflict', 'refusing to jail your own IP')
      const now = Date.now()
      const jail: Jail = {
        id: nextId(),
        ip_key: ipKeyOf(ip),
        reason,
        level: 1,
        manual: true,
        created_at: now,
        expires_at: now + minutes * 60_000,
        lifted_at: null,
        lifted_by: null,
        firewall_synced: false,
      }
      db.jails.push(jail)
      recordAudit(g.user, 'security.jail.create', jail.ip_key, 'success', { minutes })
      return HttpResponse.json<Jail>(jail, { status: 201 })
    }),
  ),

  op(
    'liftJail',
    http.delete(`${API}/security/jails/:id`, ({ request, params }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const jail = db.jails.find((j) => j.id === idParam(params) && isActive(j, Date.now()))
      if (!jail) return apiError(404, 'not_found', 'jail not found')
      jail.lifted_at = Date.now()
      jail.lifted_by = g.user.username
      recordAudit(g.user, 'security.jail.lift', jail.ip_key)
      return noContent()
    }),
  ),

  op(
    'listIPRules',
    http.get(`${API}/security/ip-rules`, ({ request }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      return HttpResponse.json<IPRule[]>(db.ipRules)
    }),
  ),

  op(
    'createIPRule',
    http.post(`${API}/security/ip-rules`, async ({ request }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const body = await readJson<{ cidr?: string; kind?: string; comment?: string; expires_at?: number | null }>(request)
      const cidr = body?.cidr?.trim() ?? ''
      if (!CIDR.test(cidr)) return apiError(400, 'validation_failed', 'invalid CIDR')
      if (body?.kind !== 'allow' && body?.kind !== 'deny') return apiError(400, 'validation_failed', 'kind must be allow or deny')
      if ((body.comment ?? '').length > 200) return apiError(400, 'validation_failed', 'comment too long')
      if (db.ipRules.some((r) => r.cidr === cidr)) return apiError(409, 'conflict', 'rule already exists')
      if (body.kind === 'deny' && cidr === `${MOCK_CLIENT_IP}/32`) return apiError(409, 'conflict', 'rule would lock you out')
      const rule: IPRule = {
        id: nextId(),
        cidr,
        kind: body.kind,
        comment: body.comment ?? '',
        expires_at: body.expires_at ?? null,
        created_by: g.user.username,
        created_at: Date.now(),
      }
      db.ipRules.push(rule)
      recordAudit(g.user, 'security.ip_rule.create', cidr, 'success', { kind: rule.kind })
      return HttpResponse.json<IPRule>(rule, { status: 201 })
    }),
  ),

  op(
    'deleteIPRule',
    http.delete(`${API}/security/ip-rules/:id`, ({ request, params }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const rule = db.ipRules.find((r) => r.id === idParam(params))
      if (!rule) return apiError(404, 'not_found', 'rule not found')
      const allowRules = db.ipRules.filter((r) => r.kind === 'allow')
      if (db.settings.admin_allowlist_only && rule.kind === 'allow' && allowRules.length === 1) {
        return apiError(409, 'conflict', 'removing the last allow rule would lock you out')
      }
      db.ipRules = db.ipRules.filter((r) => r.id !== rule.id)
      recordAudit(g.user, 'security.ip_rule.delete', rule.cidr)
      return noContent()
    }),
  ),

  op(
    'getSecuritySettings',
    http.get(`${API}/security/settings`, ({ request }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      return HttpResponse.json<SecuritySettingsResponse>(settingsResponse())
    }),
  ),

  op(
    'updateSecuritySettings',
    http.put(`${API}/security/settings`, async ({ request }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const body = await readJson<{ enforce_2fa_for_users?: boolean; admin_allowlist_only?: boolean; allowed_countries?: string[] }>(request)
      if (typeof body?.enforce_2fa_for_users !== 'boolean' || typeof body.admin_allowlist_only !== 'boolean' || !Array.isArray(body.allowed_countries)) {
        return apiError(400, 'bad_request', 'invalid settings')
      }
      if (!body.allowed_countries.every((c) => COUNTRY.test(c))) return apiError(400, 'validation_failed', 'countries must be ISO 3166-1 alpha-2')
      if (body.admin_allowlist_only && !db.ipRules.some((r) => r.kind === 'allow')) {
        return apiError(409, 'conflict', 'allowlist-only without allow rules would lock you out')
      }
      db.settings = {
        enforce_2fa_for_users: body.enforce_2fa_for_users,
        admin_allowlist_only: body.admin_allowlist_only,
        allowed_countries: body.allowed_countries,
      }
      recordAudit(g.user, 'security.settings.update', 'settings', 'success', { ...db.settings })
      return HttpResponse.json<SecuritySettingsResponse>(settingsResponse())
    }),
  ),

  op(
    'listNotifiers',
    http.get(`${API}/security/notifiers`, ({ request }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      return HttpResponse.json<Notifier[]>(db.notifiers)
    }),
  ),

  op(
    'createNotifier',
    http.post(`${API}/security/notifiers`, async ({ request }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const body = await readJson<{ kind?: string; name?: string; enabled?: boolean; url?: string; topic?: string; token?: string }>(request)
      if (body?.kind !== 'discord' && body?.kind !== 'ntfy') return apiError(400, 'validation_failed', 'kind must be discord or ntfy')
      const name = body.name?.trim() ?? ''
      if (name.length < 1 || name.length > 64) return apiError(400, 'validation_failed', 'name must be 1-64 characters')
      if (!validUrl(body.url)) return apiError(400, 'validation_failed', 'url must be http(s)')
      if (body.kind === 'ntfy' && !body.topic) return apiError(400, 'validation_failed', 'ntfy needs a topic')
      if (db.notifiers.some((n) => n.name === name)) return apiError(409, 'conflict', 'name taken')
      const notifier: Notifier = {
        id: nextId(),
        kind: body.kind,
        name,
        enabled: body.enabled ?? true,
        target_masked: maskTarget(body.kind, body.url, body.topic ?? ''),
        created_at: Date.now(),
      }
      db.notifiers.push(notifier)
      recordAudit(g.user, 'security.notifier.create', name)
      return HttpResponse.json<Notifier>(notifier, { status: 201 })
    }),
  ),

  op(
    'updateNotifier',
    http.patch(`${API}/security/notifiers/:id`, async ({ request, params }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const notifier = db.notifiers.find((n) => n.id === idParam(params))
      if (!notifier) return apiError(404, 'not_found', 'notifier not found')
      const body = await readJson<{ name?: string; enabled?: boolean; url?: string; topic?: string; token?: string }>(request)
      if (body?.name !== undefined && (body.name.trim().length < 1 || body.name.trim().length > 64)) {
        return apiError(400, 'validation_failed', 'name must be 1-64 characters')
      }
      if (body?.url !== undefined && !validUrl(body.url)) return apiError(400, 'validation_failed', 'url must be http(s)')
      const newName = body?.name?.trim()
      if (newName !== undefined && db.notifiers.some((n) => n.id !== notifier.id && n.name === newName)) {
        return apiError(400, 'validation_failed', 'name already in use')
      }
      if (newName !== undefined) notifier.name = newName
      if (typeof body?.enabled === 'boolean') notifier.enabled = body.enabled
      if (body?.url !== undefined) notifier.target_masked = maskTarget(notifier.kind, body.url, body.topic ?? '')
      recordAudit(g.user, 'security.notifier.update', notifier.name)
      return HttpResponse.json<Notifier>(notifier)
    }),
  ),

  op(
    'deleteNotifier',
    http.delete(`${API}/security/notifiers/:id`, ({ request, params }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const notifier = db.notifiers.find((n) => n.id === idParam(params))
      if (!notifier) return apiError(404, 'not_found', 'notifier not found')
      db.notifiers = db.notifiers.filter((n) => n.id !== notifier.id)
      recordAudit(g.user, 'security.notifier.delete', notifier.name)
      return noContent()
    }),
  ),

  op(
    'testNotifier',
    http.post(`${API}/security/notifiers/:id/test`, ({ request, params }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      const notifier = db.notifiers.find((n) => n.id === idParam(params))
      if (!notifier) return apiError(404, 'not_found', 'notifier not found')
      // Mock rule for UI work: disabled notifiers simulate a failed delivery (contract: 400 validation_failed + message).
      if (!notifier.enabled) return apiError(400, 'validation_failed', 'delivery failed: HTTP 404 from target')
      return noContent()
    }),
  ),

  op(
    'getAgentStatus',
    http.get(`${API}/security/agent`, ({ request }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      return HttpResponse.json<AgentStatus>(db.agent)
    }),
  ),
]
