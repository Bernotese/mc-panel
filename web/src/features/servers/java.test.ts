import fs from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'
import type { InstanceType, JavaVersion, Server } from '@/api/types'
import { loginAs } from '@/mocks/db'
import { callApi } from '@/test/call-api'
import { javaFor } from './java'

interface JavaCase {
  type: InstanceType
  version: string
  java: JavaVersion
}

// Same fixture as internal/agent/instancespec/java_test.go – Go and TypeScript must agree on every case.
const cases = JSON.parse(
  fs.readFileSync(path.resolve(import.meta.dirname, '../../../../internal/agent/instancespec/testdata/java_cases.json'), 'utf8'),
) as JavaCase[]

describe('javaFor', () => {
  it('matches every shared case of instancespec.JavaFor', () => {
    expect(cases.length).toBeGreaterThanOrEqual(10)
    const mismatches = cases
      .filter((c) => javaFor(c.type, c.version) !== c.java)
      .map((c) => `${c.type} ${JSON.stringify(c.version)}: got ${javaFor(c.type, c.version)}, want ${c.java}`)
    expect(mismatches).toEqual([])
  })

  it('treats empty or garbled versions like unknown formats', () => {
    expect(javaFor('paper', '')).toBe('java25')
    expect(javaFor('paper', 'foo')).toBe('java25')
    expect(javaFor('paper', '1.x')).toBe('java25')
    expect(javaFor('vanilla', '1')).toBe('java25')
    expect(javaFor('modrinth', '1.12.2')).toBe('java21')
  })
})

describe('mock java derivation', () => {
  it('derives java with javaFor when createServer omits it', async () => {
    const me = loginAs('admin', { sudo: true })
    const res = await callApi('POST', '/servers', {
      csrf: me.csrf_token,
      body: { name: 'legacy', type: 'paper', version: '1.16.5', memory_mib: 2048, eula_accepted: true },
    })
    expect(res.status).toBe(201)
    expect((res.body as Server).instance?.java).toBe('java8')
  })
})
