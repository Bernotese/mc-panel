import { http, HttpResponse } from 'msw'
import type { Session } from '@/api/types'
import { db, recordAudit, toSession } from '../db'
import { API, apiError, guard, idParam, noContent, op, type MockOperation } from './util'

export const adminOperations: MockOperation[] = [
  op(
    'listAllSessions',
    http.get(`${API}/admin/sessions`, ({ request }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      return HttpResponse.json<Session[]>(db.sessions.map(toSession))
    }),
  ),
  op(
    'revokeAnySession',
    http.delete(`${API}/admin/sessions/:id`, ({ request, params }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const id = idParam(params)
      const target = db.sessions.find((s) => s.id === id)
      if (!target) return apiError(404, 'not_found', 'session not found')
      db.sessions = db.sessions.filter((s) => s.id !== id)
      if (id === db.currentSessionId) db.currentSessionId = null
      recordAudit(g.user, 'session.revoke', `session:${id}`)
      return noContent()
    }),
  ),
]
