// web/src/mocks/handlers.ts
import type { HttpHandler } from 'msw'
import { adminOperations } from './handlers/admin'
import { authOperations } from './handlers/auth'
import { meOperations } from './handlers/me'
import type { MockOperation } from './handlers/util'

export const operations: MockOperation[] = [...authOperations, ...meOperations, ...adminOperations]
export const handlers: HttpHandler[] = operations.map((o) => o.handler)
