import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
import { findDevArtifacts } from './check-dist.mjs'

let dir = ''

function write(name: string, content = ''): void {
  fs.mkdirSync(path.dirname(path.join(dir, name)), { recursive: true })
  fs.writeFileSync(path.join(dir, name), content)
}

describe('check-dist (SEC-42)', () => {
  afterEach(() => fs.rmSync(dir, { recursive: true, force: true }))

  it('accepts a clean build', () => {
    dir = fs.mkdtempSync(path.join(os.tmpdir(), 'dist-'))
    write('index.html', '<script type="module" src="/assets/index-abc.js"></script>')
    write('assets/index-abc.js', 'console.log("app")')
    write('assets/font.woff2', 'msw sourceMappingURL=') // binary assets are not searched
    expect(findDevArtifacts(dir)).toEqual([])
  })

  it('reports the MSW worker, source maps and mock code', () => {
    dir = fs.mkdtempSync(path.join(os.tmpdir(), 'dist-'))
    write('mockServiceWorker.js')
    write('assets/index-abc.js.map', '{}')
    write('assets/index-abc.js', 'x()\n//# sourceMappingURL=index-abc.js.map')
    write('assets/mocks-def.js', 'import{setupWorker}from"msw/browser"')
    expect(findDevArtifacts(dir).sort()).toEqual([
      'assets/index-abc.js.map: must not be shipped',
      'assets/index-abc.js: contains /sourceMappingURL=/',
      'assets/mocks-def.js: contains /\\bmsw\\b/',
      'assets/mocks-def.js: contains /setupWorker/',
      'mockServiceWorker.js: must not be shipped',
    ])
  })
})
