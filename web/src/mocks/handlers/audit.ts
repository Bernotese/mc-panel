import { http, HttpResponse } from 'msw'
import type { AuditPage, AuditVerify } from '@/api/types'
import { db } from '../db'
import { API, guard, op, paginate, type MockOperation } from './util'

export const auditOperations: MockOperation[] = [
  op(
    'listAudit',
    http.get(`${API}/audit`, ({ request }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      const url = new URL(request.url)
      const actor = url.searchParams.get('actor')?.toLowerCase() ?? ''
      const action = url.searchParams.get('action') ?? ''
      const result = url.searchParams.get('result') ?? ''
      const from = Number(url.searchParams.get('from') ?? '0') || 0
      const to = Number(url.searchParams.get('to') ?? '0') || Number.MAX_SAFE_INTEGER
      const entries = [...db.audit]
        .reverse()
        .filter((e) => !actor || e.actor_name.toLowerCase().includes(actor))
        .filter((e) => !action || e.action.startsWith(action))
        .filter((e) => !result || e.result === result)
        .filter((e) => e.ts >= from && e.ts <= to)
      return HttpResponse.json<AuditPage>(paginate(entries, url))
    }),
  ),
  op(
    'verifyAudit',
    http.get(`${API}/audit/verify`, ({ request }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      return HttpResponse.json<AuditVerify>({ ok: true, checked: db.audit.length, first_bad_id: null })
    }),
  ),
]
