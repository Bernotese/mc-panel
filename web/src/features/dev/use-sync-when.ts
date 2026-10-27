import { useQuery } from '@tanstack/react-query'
import { useMe } from '@/features/auth/me'
import { browserTimeZone } from '@/features/backups/model'
import { serverSettingsQueryOptions } from '@/features/servers/api'
import { useFormat } from '@/lib/format'
import { formatSyncWhen } from './model'

/**
 * Formats sync times in the panel's time zone, where the sync time is set (backup_timezone), so the banner agrees with
 * the settings. Only admins may read that zone; other users see their own local time.
 */
export function useSyncWhen(): (ms: number) => string {
  const fmt = useFormat()
  const isAdmin = useMe()?.role === 'admin'
  const settings = useQuery({ ...serverSettingsQueryOptions, enabled: isAdmin })
  const timeZone = settings.data?.backup_timezone ?? null
  return (ms) => formatSyncWhen(ms, fmt.locale, { timeZone, localZone: browserTimeZone() })
}
