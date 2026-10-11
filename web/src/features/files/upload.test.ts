import { http, HttpResponse } from 'msw'
import { afterEach, describe, expect, it } from 'vitest'
import { setCsrfToken } from '@/api/csrf'
import { ApiError } from '@/api/errors'
import { server } from '@/mocks/node'
import { uploadFile } from './upload'

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
})
