/** IANA names the browser knows; V8 leaves out "UTC", old browsers have no Intl.supportedValuesOf at all. */
function supportedTimeZones(): string[] {
  const intl = Intl as typeof Intl & { supportedValuesOf?: (key: 'timeZone') => string[] }
  try {
    return intl.supportedValuesOf?.('timeZone') ?? []
  } catch {
    return []
  }
}

/** The stored value first (also an unknown one stays selectable), then UTC, then the browser's list. */
export function timeZoneOptions(current: string, supported: string[] = supportedTimeZones()): string[] {
  return [...new Set([current, 'UTC', ...supported])]
}
