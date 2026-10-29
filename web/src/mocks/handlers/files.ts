import { http, HttpResponse } from 'msw'
import type { ExtractRequest, FileEntry, FileJob, RenameFileRequest, WriteFileRequest } from '@/api/types'
import { db, recordAudit, type MockNode, type MockServer } from '../db'
import { API, apiError, guard, idParam, noContent, op, readJson, serverPermissions, type Authed, type ErrorResponse, type MockOperation } from './util'

const JOB_MS = 6000
const TEXT_EXT = /\.(properties|ya?ml|json|toml|txt|cfg|conf|ini|log|md|sk|mcmeta)$/i
// eslint-disable-next-line no-control-regex -- matches control characters on purpose (filesapi.CleanPath)
const CONTROL_RE = /[\u0000-\u001f\u007f\\]/
const TOP_HIDDEN = new Set(['.rcon-cli.env', '.rcon-cli.yaml'])

/** The mock's version token of a file (the files service hashes device, inode, size and times). */
function versionOf(node: MockNode): string {
  return `${node.mtime.toString(36)}-${node.size}`
}

type Params = Parameters<typeof idParam>[0]

/** Same rules as filesapi.CleanPath; null if invalid. "" = root. */
export function cleanPath(raw: unknown): string | null {
  if (raw === undefined || raw === null || raw === '' || raw === '.') return ''
  if (typeof raw !== 'string' || new TextEncoder().encode(raw).length > 4096 || CONTROL_RE.test(raw) || raw.startsWith('/')) return null
  const p = raw.endsWith('/') ? raw.slice(0, -1) : raw
  const parts = p.split('/')
  if (parts.some((c) => c === '' || c === '.' || c === '..' || new TextEncoder().encode(c).length > 255)) return null
  return p
}

/** Same rules as filesapi.IsHidden, case-insensitive (macOS file systems are). */
function isHidden(p: string): boolean {
  if (p === '') return false
  const parts = p.toLowerCase().split('/')
  return TOP_HIDDEN.has(parts[0]) || parts.some((c) => c.startsWith('.mcpanel-'))
}

function hidden(): ErrorResponse {
  return apiError(404, 'not_found', 'no such file')
}

/** Session, server.files and server lookup shared by every file operation. */
function access(request: Request, params: Params): { g: Authed; server: MockServer; root: MockNode | undefined } | ErrorResponse {
  const g = guard(request)
  if (g instanceof Response) return g
  const id = idParam(params)
  if (!serverPermissions(g.user, id).includes('server.files')) return apiError(403, 'forbidden', 'missing permission server.files')
  const server = db.servers.find((s) => s.id === id)
  if (!server?.instance) return apiError(404, 'not_found', 'server not found')
  return { g, server, root: db.files[id] }
}

/** Resolves a cleaned path without following symlinks. */
function lookup(root: MockNode, p: string): MockNode | undefined {
  let node: MockNode | undefined = root
  for (const c of p === '' ? [] : p.split('/')) {
    if (node?.type !== 'dir') return undefined
    node = node.children?.[c]
  }
  return node
}

function split(p: string): { dir: string; name: string } {
  const i = p.lastIndexOf('/')
  return i < 0 ? { dir: '', name: p } : { dir: p.slice(0, i), name: p.slice(i + 1) }
}

function parentDir(root: MockNode, p: string): MockNode | undefined {
  const parent = lookup(root, split(p).dir)
  return parent?.type === 'dir' ? parent : undefined
}

function noData(): ErrorResponse {
  return apiError(404, 'not_found', 'the server has never been started (no data yet)')
}

function invalid(): ErrorResponse {
  return apiError(400, 'validation_failed', 'invalid path')
}

function target(server: MockServer): string {
  return `server:${server.id}`
}

function entries(dir: MockNode, path: string): FileEntry[] {
  return Object.entries(dir.children ?? {})
    .filter(([name]) => !isHidden(path === '' ? name : `${path}/${name}`))
    .map(([name, n]) => ({ name, type: n.type, size: n.size, mtime: n.mtime }))
    .sort((a, b) => (a.type === 'dir') === (b.type === 'dir') ? a.name.localeCompare(b.name) : a.type === 'dir' ? -1 : 1)
}

/** Progress of a mock extract job: linear over JOB_MS, then done with the new folder created once. */
function jobNow(id: number): FileJob | null {
  const job = db.fileJobs[id]
  if (!job) return null
  const share = Math.min(1, (Date.now() - job.startedMs) / JOB_MS)
  if (job.state === 'running') {
    job.bytes = Math.round(job.total_bytes * share)
    job.entries = Math.round(job.total_entries * share)
    if (share >= 1) {
      job.state = 'done'
      job.finished_at = Date.now()
      const root = db.files[id]
      const parent = root && parentDir(root, job.dest)
      if (parent?.children) {
        parent.children[split(job.dest).name] = {
          type: 'dir',
          mtime: Date.now(),
          size: 0,
          children: { 'level.dat': { type: 'file', mtime: Date.now(), size: 4096 }, region: { type: 'dir', mtime: Date.now(), size: 0, children: {} } },
        }
      }
    }
  }
  const { startedMs, ...wire } = job
  void startedMs // internal clock of the mock, not part of the wire format
  return wire
}

export const filesOperations: MockOperation[] = [
  op(
    'listFiles',
    http.get(`${API}/servers/:id/files`, ({ request, params }) => {
      const a = access(request, params)
      if (a instanceof Response) return a
      const p = cleanPath(new URL(request.url).searchParams.get('path'))
      if (p === null) return invalid()
      if (!a.root) return noData()
      if (isHidden(p)) return hidden()
      const node = lookup(a.root, p)
      if (!node) return apiError(404, 'not_found', 'no such directory')
      if (node.type !== 'dir') return apiError(400, 'not_regular', 'not a directory')
      return HttpResponse.json({ path: p, entries: entries(node, p), truncated: false })
    }),
  ),
  op(
    'readFile',
    http.get(`${API}/servers/:id/files/content`, ({ request, params }) => {
      const a = access(request, params)
      if (a instanceof Response) return a
      const p = cleanPath(new URL(request.url).searchParams.get('path'))
      if (p === null || p === '') return invalid()
      if (!a.root) return noData()
      const node = isHidden(p) ? undefined : lookup(a.root, p)
      if (!node) return apiError(404, 'not_found', 'no such file')
      if (node.type !== 'file') return apiError(400, 'not_regular', 'not a regular file')
      if (node.content === undefined) return apiError(415, 'not_text', 'not a text file')
      return HttpResponse.json({ path: p, content: node.content, mtime: node.mtime, size: node.size, version: versionOf(node) })
    }),
  ),
  op(
    'writeFile',
    http.put(`${API}/servers/:id/files/content`, async ({ request, params }) => {
      const a = access(request, params)
      if (a instanceof Response) return a
      const body = await readJson<WriteFileRequest>(request)
      const p = cleanPath(body?.path)
      const expected: unknown = body?.expected_mtime
      const version: unknown = body?.expected_version
      if (!body || p === null || p === '' || typeof body.content !== 'string' || (expected !== null && typeof expected !== 'number')) return invalid()
      if (version !== undefined && (typeof version !== 'string' || version.length < 1 || version.length > 64 || expected === null)) return invalid()
      if (!a.root) return noData()
      if (isHidden(p)) return hidden()
      const parent = parentDir(a.root, p)
      if (!parent?.children) return apiError(404, 'not_found', 'no such directory')
      const { name } = split(p)
      const existing = parent.children[name]
      if (body.expected_mtime === null && existing) return apiError(409, 'file_exists', 'file exists')
      if (body.expected_mtime !== null) {
        if (!existing) return apiError(404, 'not_found', 'no such file')
        if (existing.type !== 'file') return apiError(400, 'not_regular', 'not a regular file')
        if (existing.mtime !== body.expected_mtime || (version !== undefined && version !== versionOf(existing))) {
          recordAudit(a.g.user, 'file.write', target(a.server), 'failure', { path: p })
          return apiError(409, 'file_modified', 'file changed since it was opened')
        }
      }
      const mtime = Math.max(Date.now(), (existing?.mtime ?? 0) + 1)
      const size = new TextEncoder().encode(body.content).length
      const node: MockNode = { type: 'file', mtime, size, content: body.content }
      parent.children[name] = node
      recordAudit(a.g.user, 'file.write', target(a.server), 'success', { path: p })
      return HttpResponse.json({ mtime, size, version: versionOf(node) })
    }),
  ),
  op(
    'uploadFile',
    http.post(`${API}/servers/:id/files/upload`, async ({ request, params }) => {
      const a = access(request, params)
      if (a instanceof Response) return a
      const url = new URL(request.url)
      const p = cleanPath(url.searchParams.get('path'))
      if (p === null || p === '') return invalid()
      if (!a.root) return noData()
      if (isHidden(p)) return hidden()
      const parent = parentDir(a.root, p)
      if (!parent?.children) return apiError(404, 'not_found', 'no such directory')
      const { name } = split(p)
      const overwrite = url.searchParams.get('overwrite') === 'true'
      const existing = parent.children[name]
      if (existing && !overwrite) return apiError(409, 'file_exists', 'file exists')
      if (existing && existing.type !== 'file') return apiError(400, 'not_regular', 'not a regular file')
      const data = new Uint8Array(await request.arrayBuffer())
      parent.children[name] = TEXT_EXT.test(name)
        ? { type: 'file', mtime: Date.now(), size: data.length, content: new TextDecoder().decode(data) }
        : { type: 'file', mtime: Date.now(), size: data.length }
      recordAudit(a.g.user, 'file.upload', target(a.server), 'success', { path: p, size: data.length })
      return HttpResponse.json({ size: data.length }, { status: 201 })
    }),
  ),
  op(
    'downloadFile',
    http.get(`${API}/servers/:id/files/download`, ({ request, params }) => {
      const a = access(request, params)
      if (a instanceof Response) return a
      const p = cleanPath(new URL(request.url).searchParams.get('path'))
      if (p === null) return invalid()
      if (!a.root) return noData()
      const node = isHidden(p) ? undefined : lookup(a.root, p)
      if (!node) return apiError(404, 'not_found', 'no such file')
      if (node.type === 'symlink' || node.type === 'other') return apiError(400, 'not_regular', 'not a regular file')
      const base = p === '' ? a.server.name : split(p).name
      recordAudit(a.g.user, 'file.download', target(a.server), 'success', { path: p })
      if (node.type === 'dir') {
        const emptyZip = new Uint8Array([0x50, 0x4b, 0x05, 0x06, ...new Array<number>(18).fill(0)])
        return new HttpResponse(emptyZip, {
          headers: { 'Content-Type': 'application/zip', 'Content-Disposition': `attachment; filename*=UTF-8''${encodeURIComponent(`${base}.zip`)}` },
        })
      }
      const bytes = node.content !== undefined ? new TextEncoder().encode(node.content) : new Uint8Array(Math.min(node.size, 1024))
      return new HttpResponse(bytes, {
        headers: { 'Content-Type': 'application/octet-stream', 'Content-Disposition': `attachment; filename*=UTF-8''${encodeURIComponent(base)}` },
      })
    }),
  ),
  op(
    'makeDirectory',
    http.post(`${API}/servers/:id/files/mkdir`, async ({ request, params }) => {
      const a = access(request, params)
      if (a instanceof Response) return a
      const p = cleanPath((await readJson<{ path?: string }>(request))?.path)
      if (p === null || p === '') return invalid()
      if (!a.root) return noData()
      if (isHidden(p)) return hidden()
      const parent = parentDir(a.root, p)
      if (!parent?.children) return apiError(404, 'not_found', 'no such directory')
      const { name } = split(p)
      if (parent.children[name]) return apiError(409, 'file_exists', 'exists')
      parent.children[name] = { type: 'dir', mtime: Date.now(), size: 0, children: {} }
      recordAudit(a.g.user, 'file.mkdir', target(a.server), 'success', { path: p })
      return new HttpResponse(null, { status: 201 })
    }),
  ),
  op(
    'renameFile',
    http.post(`${API}/servers/:id/files/rename`, async ({ request, params }) => {
      const a = access(request, params)
      if (a instanceof Response) return a
      const body = await readJson<RenameFileRequest>(request)
      const from = cleanPath(body?.from)
      const to = cleanPath(body?.to)
      if (from === null || to === null || from === '' || to === '') return invalid()
      if (to === from || to.startsWith(`${from}/`)) return apiError(400, 'validation_failed', 'cannot move a folder into itself')
      if (!a.root) return noData()
      if (isHidden(from) || isHidden(to)) return hidden()
      const src = parentDir(a.root, from)
      const dst = parentDir(a.root, to)
      const node = src?.children?.[split(from).name]
      if (!node || !dst?.children) return apiError(404, 'not_found', 'no such file')
      if (dst.children[split(to).name]) return apiError(409, 'file_exists', 'exists')
      delete src!.children![split(from).name]
      dst.children[split(to).name] = node
      recordAudit(a.g.user, 'file.rename', target(a.server), 'success', { from, to })
      return noContent()
    }),
  ),
  op(
    'deleteFile',
    http.post(`${API}/servers/:id/files/delete`, async ({ request, params }) => {
      const a = access(request, params)
      if (a instanceof Response) return a
      const p = cleanPath((await readJson<{ path?: string }>(request))?.path)
      if (p === null || p === '') return invalid()
      if (!a.root) return noData()
      if (isHidden(p)) return hidden()
      const parent = parentDir(a.root, p)
      const { name } = split(p)
      if (!parent?.children?.[name]) return apiError(404, 'not_found', 'no such file')
      delete parent.children[name]
      recordAudit(a.g.user, 'file.delete', target(a.server), 'success', { path: p })
      return noContent()
    }),
  ),
  op(
    'extractArchive',
    http.post(`${API}/servers/:id/files/extract`, async ({ request, params }) => {
      const a = access(request, params)
      if (a instanceof Response) return a
      const body = await readJson<ExtractRequest>(request)
      const p = cleanPath(body?.path)
      const dest = cleanPath(body?.dest)
      if (p === null || dest === null || p === '' || dest === '') return invalid()
      if (!a.root) return noData()
      if (isHidden(p) || isHidden(dest)) return hidden()
      // Like the panel: without the actual state (agent unreachable) nothing is extracted.
      if (a.server.status.state === 'unknown') return apiError(503, 'agent_unavailable', 'agent not reachable')
      // A crashed server (state error, container exited) counts as stopped, like the panel's state check.
      if (a.server.status.state !== 'stopped' && a.server.status.state !== 'error') return apiError(409, 'instance_running', 'stop the server first')
      const zip = lookup(a.root, p)
      if (!zip) return apiError(404, 'not_found', 'no such file')
      if (zip.type !== 'file' || !p.toLowerCase().endsWith('.zip')) return apiError(400, 'not_regular', 'not a zip file')
      const existing = lookup(a.root, dest)
      if (existing && (existing.type !== 'dir' || Object.keys(existing.children ?? {}).length > 0)) {
        return apiError(409, 'file_exists', 'destination is not empty')
      }
      if (!parentDir(a.root, dest)) return apiError(404, 'not_found', 'no such directory')
      if (db.fileJobs[a.server.id]?.state === 'running') return apiError(429, 'rate_limited', 'an extract job is running')
      const now = Date.now()
      db.fileJobs[a.server.id] = {
        id: `job-${now}`,
        state: 'running',
        path: p,
        dest,
        bytes: 0,
        entries: 0,
        total_bytes: zip.size * 3,
        total_entries: 1200,
        error: '',
        started_at: now,
        finished_at: 0,
        startedMs: now,
      }
      recordAudit(a.g.user, 'file.extract', target(a.server), 'success', { path: p, dest })
      return HttpResponse.json(jobNow(a.server.id), { status: 202 })
    }),
  ),
  op(
    'getFileJob',
    http.get(`${API}/servers/:id/files/jobs`, ({ request, params }) => {
      const a = access(request, params)
      if (a instanceof Response) return a
      return HttpResponse.json({ job: jobNow(a.server.id) })
    }),
  ),
]
