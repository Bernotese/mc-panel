import type { TFunction } from 'i18next'
import { useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import type { Session } from '@/api/types'
import type { Column } from '@/components/data-list'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import type { useFormat } from '@/lib/format'
import { describeUserAgent } from '@/lib/user-agent'

export function useDeviceLabel(): (ua: string) => string {
  const { t } = useTranslation()
  return useCallback(
    (ua: string) => {
      const info = describeUserAgent(ua)
      return t('profile.sessions.device', {
        browser: info.browser ?? t('profile.sessions.unknown'),
        os: info.os ?? t('profile.sessions.unknown'),
      })
    },
    [t],
  )
}

interface SessionColumnsOptions {
  t: TFunction
  fmt: ReturnType<typeof useFormat>
  deviceLabel(ua: string): string
  withUser: boolean
  pending: boolean
  onRevoke(session: Session): void
}

export function buildSessionColumns({ t, fmt, deviceLabel, withUser, pending, onRevoke }: SessionColumnsOptions): Column<Session>[] {
  const columns: Column<Session>[] = [
    {
      id: 'device',
      header: t('profile.sessions.col.device'),
      cell: (s) => (
        <span className="flex flex-wrap items-center gap-2">
          <span title={s.user_agent}>{deviceLabel(s.user_agent)}</span>
          {s.current ? <Badge variant="secondary">{t('profile.sessions.current')}</Badge> : null}
        </span>
      ),
    },
    {
      id: 'location',
      header: t('profile.sessions.col.location'),
      cell: (s) => (
        <span className="font-mono text-xs">
          {s.ip}
          {s.country ? ` (${s.country})` : ''}
        </span>
      ),
    },
    { id: 'last_seen', header: t('profile.sessions.col.last_seen'), cell: (s) => fmt.relative(s.last_seen_at) },
    { id: 'created', header: t('profile.sessions.col.created'), cell: (s) => fmt.dateTime(s.created_at) },
    {
      id: 'actions',
      header: t('profile.sessions.col.actions'),
      cell: (s) =>
        s.current ? null : (
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={pending}
            aria-label={t('profile.sessions.revoke_for', { device: deviceLabel(s.user_agent) })}
            onClick={() => onRevoke(s)}
          >
            {t('profile.sessions.revoke')}
          </Button>
        ),
    },
  ]
  if (withUser) columns.unshift({ id: 'user', header: t('profile.sessions.col.user'), cell: (s) => s.username })
  return columns
}
