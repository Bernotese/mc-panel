import { http } from 'msw'
import { API, apiError, guard, op, type MockOperation } from './util'

/** Placeholder until the dev mock is built (implementation plan, Task 9): like the panel skeleton, every dev operation answers 404 not_dev. */
function notYet(admin = false, sudo = false) {
  return ({ request }: { request: Request }) => {
    const g = guard(request, { admin, sudo })
    if (g instanceof Response) return g
    return apiError(404, 'not_dev', 'dev instances are not available yet')
  }
}

export const devOperations: MockOperation[] = [
  op('createDevInstance', http.post(`${API}/servers/:id/dev`, notYet(true, true))),
  op('getDevInstance', http.get(`${API}/servers/:id/dev`, notYet())),
  op('updateDevInstance', http.patch(`${API}/servers/:id/dev`, notYet())),
  op('syncDevInstance', http.post(`${API}/servers/:id/dev/sync`, notYet())),
  op('listDevOps', http.get(`${API}/servers/:id/dev/ops`, notYet())),
  op('addDevOp', http.post(`${API}/servers/:id/dev/ops`, notYet())),
  op('removeDevOp', http.delete(`${API}/servers/:id/dev/ops/:name`, notYet())),
]
