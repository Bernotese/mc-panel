import { Link } from '@tanstack/react-router'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import type { HostSnapshot } from '@/api/types'
import { Section } from '@/components/section'
import { StatSlot } from '@/components/stat-slot'
import { formatCpu, formatGiB } from '@/features/servers/format'
import { useFormat } from '@/lib/format'
import { Meter } from './meter'
import { formatDecimal } from './model'

/** Keeps the bar off the value; StatSlot puts the hint into a truncating row. */
function Bar({ children }: { children: ReactNode }) {
  return <div className="pt-1.5">{children}</div>
}

/** Admin-only host values on the dashboard (spec §5.1); bars stay Meter (aria-valuenow in bytes). */
export function HostCard({ host }: { host: HostSnapshot }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const low = host.alerts.includes('host.disk_low')
  const gib = (n: number) => formatGiB(n, fmt.locale)
  const cpu = host.cpu_percent === null ? null : t('monitoring.percent', { value: formatCpu(host.cpu_percent, fmt.locale) })
  const mem =
    host.memory_used !== null && host.memory_total !== null
      ? t('monitoring.host.memory_value', { used: gib(host.memory_used), total: gib(host.memory_total) })
      : null
  const disk =
    host.disk_free !== null && host.disk_total !== null ? t('monitoring.host.disk_value', { free: gib(host.disk_free), total: gib(host.disk_total) }) : null
  return (
    <Section
      title={t('monitoring.host.title')}
      description={t('monitoring.host.description')}
      actions={
        <Link to="/settings" className="text-primary text-sm underline underline-offset-4">
          {t('monitoring.host.settings_link')}
        </Link>
      }
    >
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <StatSlot
          label={t('monitoring.host.cpu')}
          value={cpu}
          hint={
            host.cpu_percent !== null && cpu ? (
              <Bar>
                <Meter label={t('monitoring.host.cpu')} value={Math.min(100, Math.max(0, host.cpu_percent))} max={100} valueText={cpu} />
              </Bar>
            ) : undefined
          }
        />
        <StatSlot label={t('monitoring.host.load')} value={host.load1 === null ? null : formatDecimal(host.load1, fmt.locale, 2)} />
        <StatSlot
          label={t('monitoring.host.memory')}
          value={mem}
          hint={
            host.memory_used !== null && host.memory_total !== null && mem ? (
              <Bar>
                <Meter label={t('monitoring.host.memory')} value={host.memory_used} max={host.memory_total} valueText={mem} />
              </Bar>
            ) : undefined
          }
        />
        <StatSlot
          label={t('monitoring.host.disk')}
          value={disk}
          tone={low ? 'danger' : 'default'}
          hint={
            host.disk_free !== null && host.disk_total !== null && disk ? (
              <Bar>
                <Meter label={t('monitoring.host.disk')} value={host.disk_total - host.disk_free} max={host.disk_total} danger={low} valueText={disk} />
              </Bar>
            ) : undefined
          }
        />
      </div>
      {low ? (
        <p role="status" className="text-destructive mt-3 text-sm">
          {t('monitoring.host.disk_low')}
        </p>
      ) : null}
    </Section>
  )
}
