import type { TFunction } from 'i18next'
import type { InstanceConfig, JavaVersion } from '@/api/types'
import { isModpack } from './validation'

const GIB = 1024 ** 3

export function formatGiB(bytes: number, locale: string): string {
  return new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }).format(bytes / GIB)
}

export function formatCpu(percent: number, locale: string): string {
  return new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }).format(percent)
}

/** Value of the GiB input ("4", "4.25"); the API speaks MiB. */
export function memoryInputValue(mib: number): string {
  return String(mib / 1024)
}

export function javaLabel(java: JavaVersion): string {
  return `Java ${java.slice('java'.length)}`
}

export function serverAddress(name: string, baseDomain: string | null): string | null {
  return baseDomain ? `${name}.${baseDomain}` : null
}

/** "Paper 1.21.11", "Vanilla (neueste Version)" or "Modrinth-Modpack: cobblemon-fabric". */
export function describeInstance(instance: InstanceConfig, t: TFunction): string {
  const type = t(`servers.type.${instance.type}`)
  if (isModpack(instance.type)) return t('servers.describe.modpack', { type, modpack: instance.modpack ?? '' })
  if (instance.version === 'LATEST') return t('servers.describe.latest', { type })
  return t('servers.describe.version', { type, version: instance.version })
}
