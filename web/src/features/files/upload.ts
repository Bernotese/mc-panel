import { api } from '@/api/client'
import { getCsrfToken, setCsrfToken } from '@/api/csrf'
import { toApiError } from '@/api/errors'
import { apiHandlers } from '@/api/handlers'
import type { UploadResult } from '@/api/types'

export interface UploadOptions {
  overwrite: boolean
  signal?: AbortSignal
  onProgress?(loaded: number, total: number): void
}

/**
 * The connection broke off before the whole file was sent. Over HTTP/1.1 an early answer of the panel (409 file_exists,
 * 413, 507) can reach the browser like this, because the server closes the connection while the upload still runs.
 */
export class UploadInterruptedError extends Error {
  constructor() {
    super('Upload interrupted')
    this.name = 'UploadInterruptedError'
  }
}

function parse(text: string): unknown {
  try {
    return text ? (JSON.parse(text) as unknown) : null
  } catch {
    return text
  }
}

/**
 * XMLHttpRequest instead of fetch: only XHR reports upload progress. The File is handed to send() as is, so the
 * browser streams it from disk (no copy in memory, spec §5.1). Mirrors the fetch middleware: X-CSRF-Token,
 * 401 → login, one retry with a fresh token after csrf_invalid.
 */
export function uploadFile(serverId: number, path: string, file: Blob, options: UploadOptions): Promise<UploadResult> {
  return send(serverId, path, file, options, true)
}

export type UploadTransport = typeof uploadFile

function send(serverId: number, path: string, file: Blob, options: UploadOptions, mayRetry: boolean): Promise<UploadResult> {
  return new Promise<UploadResult>((resolve, reject) => {
    if (options.signal?.aborted) {
      reject(new DOMException('Upload cancelled', 'AbortError'))
      return
    }
    const query = new URLSearchParams({ path, overwrite: options.overwrite ? 'true' : 'false' })
    const xhr = new XMLHttpRequest()
    xhr.open('POST', `${window.location.origin}/api/servers/${serverId}/files/upload?${query.toString()}`)
    xhr.setRequestHeader('Content-Type', 'application/octet-stream')
    xhr.setRequestHeader('Accept', 'application/json')
    const token = getCsrfToken()
    if (token) xhr.setRequestHeader('X-CSRF-Token', token)

    const onAbort = () => xhr.abort()
    options.signal?.addEventListener('abort', onAbort, { once: true })
    const done = () => options.signal?.removeEventListener('abort', onAbort)

    let sent = 0
    xhr.upload.onprogress = (event) => {
      if (!event.lengthComputable) return
      sent = event.loaded
      options.onProgress?.(event.loaded, event.total)
    }
    xhr.onabort = () => {
      done()
      reject(new DOMException('Upload cancelled', 'AbortError'))
    }
    xhr.onerror = () => {
      done()
      reject(sent < file.size ? new UploadInterruptedError() : new TypeError('Network error'))
    }
    xhr.onload = () => {
      done()
      const body = parse(xhr.responseText)
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve(body as UploadResult)
        return
      }
      const headers = new Headers()
      const retryAfter = xhr.getResponseHeader('Retry-After')
      if (retryAfter) headers.set('Retry-After', retryAfter)
      const error = toApiError(new Response(null, { status: xhr.status, headers }), body)
      if (xhr.status === 401 && error.code === 'unauthorized') {
        setCsrfToken('')
        apiHandlers().onUnauthorized()
      }
      if (mayRetry && xhr.status === 403 && error.code === 'csrf_invalid') {
        // /auth/me is in ME_PATHS: the API middleware stores the fresh token before the retry reads it.
        api
          .GET('/auth/me')
          .then(() => send(serverId, path, file, options, false))
          .then(resolve, reject)
        return
      }
      reject(error)
    }
    xhr.send(file)
  })
}
