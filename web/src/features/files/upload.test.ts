import { http, HttpResponse } from 'msw'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { getCsrfToken, setCsrfToken } from '@/api/csrf'
import { configureApiHandlers } from '@/api/handlers'
import { ApiError } from '@/api/errors'
import { server } from '@/mocks/node'
import { UploadInterruptedError, uploadFile } from './upload'

afterEach(() => setCsrfToken(''))

describe('uploadFile', () => {
  it('posts the raw file with token, path and overwrite flag', async () => {
    setCsrfToken('tok-1')
    let seen: { url: string; token: string | null; type: string | null; body: string } | null = null
    server.use(
      http.post('*/api/servers/:id/files/upload', async ({ request }) => {
        seen = {
          url: request.url,
          token: request.headers.get('X-CSRF-Token'),
          type: request.headers.get('Content-Type'),
          body: await request.text(),
        }
        return HttpResponse.json({ size: 5 }, { status: 201 })
      }),
    )
    const result = await uploadFile(1, 'plugins/a b.txt', new File(['hello'], 'a b.txt'), { overwrite: true })
    expect(result).toEqual({ size: 5 })
    expect(seen).not.toBeNull()
    const url = new URL(seen!.url)
    expect(url.pathname).toBe('/api/servers/1/files/upload')
    expect(url.searchParams.get('path')).toBe('plugins/a b.txt')
    expect(url.searchParams.get('overwrite')).toBe('true')
    expect(seen!.token).toBe('tok-1')
    expect(seen!.type).toBe('application/octet-stream')
    expect(seen!.body).toBe('hello')
  })

  it('turns error answers into ApiErrors', async () => {
    server.use(
      http.post('*/api/servers/:id/files/upload', () =>
        HttpResponse.json({ error: 'file_exists', message: 'exists' }, { status: 409 }),
      ),
    )
    const err = await uploadFile(1, 'a', new File(['x'], 'a'), { overwrite: false }).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).code).toBe('file_exists')
  })

  it('rejects with AbortError when cancelled before sending', async () => {
    const controller = new AbortController()
    controller.abort()
    const err = await uploadFile(1, 'a', new File(['x'], 'a'), { overwrite: false, signal: controller.signal }).catch((e: unknown) => e)
    expect((err as DOMException).name).toBe('AbortError')
  })

  it('fetches a fresh token once after csrf_invalid', async () => {
    let calls = 0
    server.use(
      http.post('*/api/servers/:id/files/upload', ({ request }) => {
        calls += 1
        return request.headers.get('X-CSRF-Token') === 'fresh'
          ? HttpResponse.json({ size: 1 }, { status: 201 })
          : HttpResponse.json({ error: 'csrf_invalid', message: 'csrf' }, { status: 403 })
      }),
      http.get('*/api/auth/me', () => HttpResponse.json({ csrf_token: 'fresh', stage: 'full' })),
    )
    await expect(uploadFile(1, 'a', new File(['x'], 'a'), { overwrite: false })).resolves.toEqual({ size: 1 })
    expect(calls).toBe(2)
  })

  it('aborts the XHR and rejects with AbortError when cancelled in flight', async () => {
    // MSW's XHR interceptor does not honour abort(), so a minimal stand-in checks the wiring.
    const instances: FakeXhr[] = []
    class FakeXhr {
      upload: Record<string, unknown> = {}
      onabort: (() => void) | null = null
      aborted = false
      constructor() {
        instances.push(this)
      }
      open() {}
      setRequestHeader() {}
      send() {}
      abort() {
        this.aborted = true
        this.onabort?.()
      }
    }
    vi.stubGlobal('XMLHttpRequest', FakeXhr)
    try {
      const controller = new AbortController()
      const pending = uploadFile(1, 'a', new File(['x'], 'a'), { overwrite: false, signal: controller.signal }).catch((e: unknown) => e)
      controller.abort()
      expect(((await pending) as DOMException).name).toBe('AbortError')
      expect(instances[0].aborted).toBe(true)
    } finally {
      vi.unstubAllGlobals()
    }
  })

  it('signs the user out on 401 and drops the token', async () => {
    const onUnauthorized = vi.fn()
    configureApiHandlers({ onUnauthorized })
    setCsrfToken('tok')
    server.use(
      http.post('*/api/servers/:id/files/upload', () => HttpResponse.json({ error: 'unauthorized', message: 'no' }, { status: 401 })),
    )
    const err = await uploadFile(1, 'a', new File(['x'], 'a'), { overwrite: false }).catch((e: unknown) => e)
    expect((err as ApiError).code).toBe('unauthorized')
    expect(onUnauthorized).toHaveBeenCalledTimes(1)
    expect(getCsrfToken()).toBe('')
  })

  it('passes Retry-After on', async () => {
    server.use(
      http.post('*/api/servers/:id/files/upload', () =>
        HttpResponse.json({ error: 'rate_limited', message: 'slow down' }, { status: 429, headers: { 'Retry-After': '7' } }),
      ),
    )
    const err = await uploadFile(1, 'a', new File(['x'], 'a'), { overwrite: false }).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).retryAfterSeconds).toBe(7)
  })

  it('rejects with a TypeError on network errors', async () => {
    server.use(http.post('*/api/servers/:id/files/upload', () => HttpResponse.error()))
    const err = await uploadFile(1, 'a', new File([], 'a'), { overwrite: false }).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(TypeError)
  })

  // Review F2: over HTTP/1.1 an early 409/413/507 can arrive as a reset connection while the browser still sends.
  it.each([
    [4, UploadInterruptedError],
    [10, TypeError],
  ])('a network error after %i of 10 bytes rejects with %o', async (loaded, kind) => {
    class FakeXhr {
      upload: { onprogress?: (e: { lengthComputable: boolean; loaded: number; total: number }) => void } = {}
      onerror: (() => void) | null = null
      open() {}
      setRequestHeader() {}
      send() {
        this.upload.onprogress?.({ lengthComputable: true, loaded, total: 10 })
        this.onerror?.()
      }
      abort() {}
    }
    vi.stubGlobal('XMLHttpRequest', FakeXhr)
    try {
      const err = await uploadFile(1, 'a', new File(['0123456789'], 'a'), { overwrite: false }).catch((e: unknown) => e)
      expect(err).toBeInstanceOf(kind)
      if (kind === TypeError) expect(err).not.toBeInstanceOf(UploadInterruptedError)
    } finally {
      vi.unstubAllGlobals()
    }
  })
})
