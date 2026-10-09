// web/src/lib/user-agent.ts
export interface DeviceInfo {
  browser: string | null
  os: string | null
}

/** Coarse, privacy-friendly summary for session lists (order matters: Edge/Chrome/Safari share tokens). */
export function describeUserAgent(ua: string): DeviceInfo {
  let browser: string | null = null
  if (/Edg\//.test(ua)) browser = 'Edge'
  else if (/Firefox\//.test(ua)) browser = 'Firefox'
  else if (/Chrome\//.test(ua)) browser = 'Chrome'
  else if (/Safari\//.test(ua)) browser = 'Safari'

  let os: string | null = null
  if (/iPhone|iPad/.test(ua)) os = 'iOS'
  else if (/Android/.test(ua)) os = 'Android'
  else if (/Mac OS X/.test(ua)) os = 'macOS'
  else if (/Windows/.test(ua)) os = 'Windows'
  else if (/Linux/.test(ua)) os = 'Linux'

  return { browser, os }
}
