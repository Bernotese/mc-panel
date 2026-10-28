import { describe, expect, it } from 'vitest'
import type { InstanceConfig } from '@/api/types'
import i18n from '@/i18n'
import { describeInstance, formatCpu, formatGiB, javaLabel, memoryInputValue, serverAddress } from './format'

const GIB = 1024 ** 3
const t = i18n.t.bind(i18n)

function instance(patch: Partial<InstanceConfig>): InstanceConfig {
  return { type: 'paper', version: '1.21.11', loader_version: null, modpack: null, java: 'java21', memory_mib: 4096, properties: {}, revision: 1, ...patch }
}

describe('servers format', () => {
  it('formats bytes as GiB with at most one decimal', () => {
    expect(formatGiB(2 * GIB, 'de')).toBe('2')
    expect(formatGiB(2.5 * GIB, 'de')).toBe('2,5')
    expect(formatGiB(2.5 * GIB, 'en')).toBe('2.5')
  })

  it('formats CPU percent per locale', () => {
    expect(formatCpu(12.5, 'de')).toBe('12,5')
    expect(formatCpu(3, 'en')).toBe('3')
  })

  it('converts MiB into the GiB input value', () => {
    expect(memoryInputValue(4096)).toBe('4')
    expect(memoryInputValue(4352)).toBe('4.25')
  })

  it('labels java versions and builds addresses', () => {
    expect(javaLabel('java8')).toBe('Java 8')
    expect(javaLabel('java25')).toBe('Java 25')
    expect(serverAddress('survival', 'mc.example.com')).toBe('survival.mc.example.com')
    expect(serverAddress('survival', null)).toBeNull()
  })

  it('describes an instance', () => {
    expect(describeInstance(instance({}), t)).toBe('Paper 1.21.11')
    expect(describeInstance(instance({ type: 'vanilla', version: 'LATEST', java: 'java25' }), t)).toBe('Vanilla (neueste Version)')
    expect(describeInstance(instance({ type: 'modrinth', version: '', modpack: 'cobblemon-fabric' }), t)).toBe(
      'Modrinth-Modpack: cobblemon-fabric',
    )
  })
})
