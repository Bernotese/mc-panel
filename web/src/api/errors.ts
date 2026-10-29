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

// A Record over the contract's codes: tsc fails as soon as api/openapi.yaml adds a code that is missing here.
const KNOWN_CODES: Record<ErrorCode, true> = {
  bad_request: true,
  validation_failed: true,
  unauthorized: true,
  invalid_credentials: true,
  invalid_code: true,
  forbidden: true,
  sudo_required: true,
  setup_2fa_required: true,
  password_change_required: true,
  csrf_invalid: true,
  not_found: true,
  conflict: true,
  payload_too_large: true,
  rate_limited: true,
  jailed: true,
  ip_blocked: true,
  agent_unavailable: true,
  docker_unavailable: true,
  base_domain_missing: true,
  catalog_unavailable: true,
  instance_not_running: true,
  rcon_unavailable: true,
  files_unavailable: true,
  file_exists: true,
  file_modified: true,
  not_text: true,
  not_regular: true,
  instance_running: true,
  insufficient_storage: true,
  plugins_unsupported: true,
  plugin_source_unavailable: true,
  plugin_not_downloadable: true,
  plugin_incompatible: true,
  mrpack_incompatible: true,
  mrpack_version_mismatch: true,
  backups_unavailable: true,
  backup_running: true,
  server_modified: true,
  data_state_unknown: true,
  recovery_required: true,
  dev_exists: true,
  not_dev: true,
  dev_managed: true,
  internal: true,
}

function isErrorCode(value: unknown): value is ErrorCode {
  return typeof value === 'string' && Object.hasOwn(KNOWN_CODES, value)
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
