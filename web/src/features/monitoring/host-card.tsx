import { Link } from '@tanstack/react-router'
import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import type { HostSnapshot } from '@/api/types'
import { Card, CardContent, CardDescription, CardHeader } from '@/components/ui/card'
import { formatCpu, formatGiB } from '@/features/servers/format'
import { useFormat } from '@/lib/format'
import { Meter } from './meter'
import { formatDecimal } from './model'

/** Admin-only host card on the dashboard (spec §7.1). */
export function HostCard({ host }: { host: HostSnapshot }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const id = useId()
  const missing = t('monitoring.missing')
  const low = host.alerts.includes('host.disk_low')
  const gib = (n: number) => formatGiB(n, fmt.locale)
  return (
    <section aria-labelledby={id} className="mb-6">
      <Card>
        <CardHeader>
          <h2 id={id} className="leading-none font-semibold">
            {t('monitoring.host.title')}
          </h2>
          <CardDescription>{t('monitoring.host.description')}</CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4 text-sm sm:grid-cols-2 lg:grid-cols-4">
          <div className="space-y-1">
            <p className="text-muted-foreground text-xs">{t('monitoring.host.cpu')}</p>
            <p className="tabular-nums">{host.cpu_percent === null ? missing : t('monitoring.percent', { value: formatCpu(host.cpu_percent, fmt.locale) })}</p>
          </div>
          <div className="space-y-1">
            <p className="text-muted-foreground text-xs">{t('monitoring.host.load')}</p>
            <p className="tabular-nums">{host.load1 === null ? missing : formatDecimal(host.load1, fmt.locale, 2)}</p>
          </div>
          <div className="space-y-1">
            <p className="text-muted-foreground text-xs">{t('monitoring.host.memory')}</p>
            {host.memory_used !== null && host.memory_total !== null ? (
              <>
                <p className="tabular-nums">{t('monitoring.host.memory_value', { used: gib(host.memory_used), total: gib(host.memory_total) })}</p>
                <Meter
                  label={t('monitoring.host.memory')}
                  value={host.memory_used}
                  max={host.memory_total}
                  valueText={t('monitoring.host.memory_value', { used: gib(host.memory_used), total: gib(host.memory_total) })}
                />
              </>
            ) : (
              <p>{missing}</p>
            )}
          </div>
          <div className="space-y-1">
            <p className="text-muted-foreground text-xs">{t('monitoring.host.disk')}</p>
            {host.disk_free !== null && host.disk_total !== null ? (
              <>
                <p className={low ? 'text-destructive tabular-nums' : 'tabular-nums'}>{t('monitoring.host.disk_value', { free: gib(host.disk_free), total: gib(host.disk_total) })}</p>
                <Meter
                  label={t('monitoring.host.disk')}
                  value={host.disk_total - host.disk_free}
                  max={host.disk_total}
                  danger={low}
                  valueText={t('monitoring.host.disk_value', { free: gib(host.disk_free), total: gib(host.disk_total) })}
                />
              </>
            ) : (
              <p>{missing}</p>
            )}
          </div>
          {low ? (
            <p role="status" className="text-destructive sm:col-span-2 lg:col-span-4">
              {t('monitoring.host.disk_low')}
            </p>
          ) : null}
          <Link to="/settings" className="text-primary underline underline-offset-4 sm:col-span-2 lg:col-span-4">
            {t('monitoring.host.settings_link')}
          </Link>
        </CardContent>
      </Card>
    </section>
  )
}
