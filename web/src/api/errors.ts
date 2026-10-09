import type { ErrorCode } from './types'

export type ApiErrorCode = ErrorCode | 'unknown'

interface ApiErrorInit {
  status: number
  code: ApiErrorCode
  message: string
  requestId?: string
  retryAfterSeconds?: number
  sudoCancelled?: boolean
}

export class ApiError extends Error {
  readonly status: number
  readonly code: ApiErrorCode
  readonly requestId?: string
  readonly retryAfterSeconds?: number
  /** The 403 sudo_required was returned because the user dismissed the sudo dialog – nothing to report. */
  readonly sudoCancelled: boolean

  constructor(init: ApiErrorInit) {
    super(init.message)
    this.name = 'ApiError'
    this.status = init.status
    this.code = init.code
    this.requestId = init.requestId
    this.retryAfterSeconds = init.retryAfterSeconds
    this.sudoCancelled = init.sudoCancelled === true
  }
}

const cancelledSudoResponses = new WeakSet<Response>()

/** Called by the API middleware when the user closed the sudo dialog without confirming. */
export function markSudoCancelled(response: Response): Response {
  cancelledSudoResponses.add(response)
  return response
}

export function isSudoCancelled(err: unknown): boolean {
  return err instanceof ApiError && err.sudoCancelled
}

const KNOWN_CODES: ReadonlySet<string> = new Set<ErrorCode>([
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
  'internal',
])

function isErrorCode(value: unknown): value is ErrorCode {
  return typeof value === 'string' && KNOWN_CODES.has(value)
}

export function toApiError(response: Response, body: unknown): ApiError {
  const retryHeader = response.headers.get('Retry-After')
  const sudoCancelled = cancelledSudoResponses.has(response)
  const retryAfterSeconds = retryHeader && /^\d+$/.test(retryHeader) ? Number(retryHeader) : undefined
  if (typeof body === 'object' && body !== null) {
    const b = body as { error?: unknown; message?: unknown; request_id?: unknown }
    if (isErrorCode(b.error)) {
      return new ApiError({
        status: response.status,
        code: b.error,
        message: typeof b.message === 'string' ? b.message : b.error,
        requestId: typeof b.request_id === 'string' ? b.request_id : undefined,
        retryAfterSeconds,
        sudoCancelled,
      })
    }
  }
  return new ApiError({
    status: response.status,
    code: 'unknown',
    message: typeof body === 'string' && body ? body : `HTTP ${response.status}`,
    retryAfterSeconds,
  })
}

export function isApiError(err: unknown, code?: ApiErrorCode): err is ApiError {
  return err instanceof ApiError && (code === undefined || err.code === code)
}

export async function unwrap<T>(promise: Promise<{ data?: T; error?: unknown; response: Response }>): Promise<T> {
  const { data, error, response } = await promise
  if (!response.ok) throw toApiError(response, error)
  return data as T
}

export async function unwrapVoid(promise: Promise<{ error?: unknown; response: Response }>): Promise<void> {
  const { error, response } = await promise
  if (!response.ok) throw toApiError(response, error)
}
