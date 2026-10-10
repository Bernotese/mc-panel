import type { InstanceType, JavaVersion } from '@/api/types'
import { isModpack } from './validation'

/**
 * Exact port of instancespec.JavaFor (spec §6.1): derives the itzg image variant from the Minecraft version.
 * Modpacks default to java21; unknown formats (LATEST, snapshots, year-based versions like 26.1) get the newest image.
 * Both implementations are checked against internal/agent/instancespec/testdata/java_cases.json.
 */
export function javaFor(type: InstanceType, version: string): JavaVersion {
  if (isModpack(type)) return 'java21'
  const parts = version.split('.')
  const major = leadingInt(parts[0])
  if (major !== 1 || parts.length < 2) return 'java25'
  const minor = leadingInt(parts[1])
  if (minor === null) return 'java25'
  const patch = parts.length >= 3 ? (leadingInt(parts[2]) ?? 0) : 0
  if (minor <= 16) return 'java8'
  if (minor <= 19 || (minor === 20 && patch <= 4)) return 'java17'
  if (minor <= 21) return 'java21'
  return 'java25'
}

/** Leading decimal digits of s ("4-rc1" → 4); null when s does not start with a digit. */
function leadingInt(s: string): number | null {
  const match = /^\d+/.exec(s)
  return match ? Number(match[0]) : null
}
