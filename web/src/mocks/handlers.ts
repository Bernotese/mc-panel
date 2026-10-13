// web/src/mocks/handlers.ts
import type { HttpHandler } from 'msw'
import { adminOperations } from './handlers/admin'
import { auditOperations } from './handlers/audit'
import { authOperations } from './handlers/auth'
import { backupOperations } from './handlers/backups'
import { consoleOperations } from './handlers/console'
import { filesOperations } from './handlers/files'
import { meOperations } from './handlers/me'
import { monitoringOperations } from './handlers/monitoring'
import { pluginsOperations } from './handlers/plugins'
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
  ...filesOperations,
  ...pluginsOperations,
  ...backupOperations,
  ...monitoringOperations,
  ...auditOperations,
  ...securityOperations,
]
export const handlers: HttpHandler[] = operations.map((o) => o.handler)
