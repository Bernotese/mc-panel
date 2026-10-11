// web/src/mocks/handlers.ts
import type { HttpHandler } from 'msw'
import { adminOperations } from './handlers/admin'
import { auditOperations } from './handlers/audit'
import { authOperations } from './handlers/auth'
import { consoleOperations } from './handlers/console'
import { meOperations } from './handlers/me'
import { securityOperations } from './handlers/security'
import { serverOperations } from './handlers/servers'
import { userOperations } from './handlers/users'
import type { MockOperation } from './handlers/util'

export const operations: MockOperation[] = [
  ...authOperations,
  ...meOperations,
  ...adminOperations,
  ...userOperations,
  ...serverOperations,
  ...consoleOperations,
  ...auditOperations,
  ...securityOperations,
]
export const handlers: HttpHandler[] = operations.map((o) => o.handler)
