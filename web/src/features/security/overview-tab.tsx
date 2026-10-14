import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { SecurityRange } from '@/api/types'
import { Section } from '@/components/section'
import { StatSlot } from '@/components/stat-slot'
import { Button } from '@/components/ui/button'
import { errorMessage } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import { AgentStatusCard } from './agent-status-card'
import { LoginBarChart } from './bar-chart'

const RANGES: SecurityRange[] = ['24h', '7d', '30d']

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <section aria-label={label}>
      <StatSlot label={label} value={value} className="h-full py-3 [&>div:nth-child(2)]:text-3xl" />
    </section>
  )
}

interface ListItem {
  key: string
  label: string
  value: string
}

function ListCard({ title, items, empty }: { title: string; items: ListItem[]; empty: string }) {
  return (
    <Section title={title} headingLevel={3}>
      {items.length === 0 ? (
        <p className="text-muted-foreground text-sm">{empty}</p>
      ) : (
        <ul aria-label={title} className="divide-y text-sm">
          {items.map((item) => (
            <li key={item.key} className="flex justify-between gap-3 py-1.5">
              <span className="min-w-0 break-all">{item.label}</span>
              <span className="text-muted-foreground font-pixel shrink-0 tabular-nums">{item.value}</span>
            </li>
          ))}
        </ul>
      )}
    </Section>
  )
}

export function OverviewTab() {
  const { t } = useTranslation()
  const fmt = useFormat()
  const [range, setRange] = useState<SecurityRange>('24h')
  const overview = useQuery({
    queryKey: ['security', 'overview', range],
    queryFn: () => unwrap(api.GET('/security/overview', { params: { query: { range } } })),
    placeholderData: keepPreviousData,
  })
  const dayFormat = new Intl.DateTimeFormat(fmt.locale, { day: '2-digit', month: '2-digit' })
  const formatTick = (ts: number) => (range === '24h' ? fmt.time(ts) : dayFormat.format(new Date(ts)))
  const data = overview.data

  return (
    <div className="space-y-6">
      <div role="group" aria-label={t('security.overview.range_label')} className="flex flex-wrap gap-2">
        {RANGES.map((r) => (
          <Button key={r} type="button" size="sm" variant={r === range ? 'default' : 'outline'} aria-pressed={r === range} onClick={() => setRange(r)}>
            {t(`security.overview.ranges.${r}`)}
          </Button>
        ))}
      </div>
      {overview.isError ? (
        <p role="alert" className="text-destructive">
          {errorMessage(overview.error, t)}
        </p>
      ) : null}
      {!data ? (
        <p className="text-muted-foreground">{t('common.loading')}</p>
      ) : (
        <>
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            <Stat label={t('security.overview.failures_total')} value={fmt.number(data.failures_total)} />
            <Stat label={t('security.overview.active_jails')} value={fmt.number(data.active_jails)} />
            <AgentStatusCard />
          </div>
          <Section>
            {data.series.length === 0 ? (
              <p className="text-muted-foreground text-sm">{t('security.overview.no_data')}</p>
            ) : (
              <LoginBarChart
                data={data.series}
                title={t('security.overview.chart_title', { range: t(`security.overview.ranges.${data.range}`) })}
                formatTick={formatTick}
                labels={{ time: t('security.overview.time'), success: t('security.overview.success'), failure: t('security.overview.failure') }}
              />
            )}
          </Section>
          <div className="grid gap-6 md:grid-cols-2">
            <ListCard
              title={t('security.overview.top_ips')}
              empty={t('security.overview.no_data')}
              items={data.top_ips.map((ip) => ({
                key: ip.ip_key,
                label: ip.country ? `${ip.ip_key} (${ip.country})` : ip.ip_key,
                value: fmt.number(ip.failures),
              }))}
            />
            <ListCard
              title={t('security.overview.top_countries')}
              empty={t('security.overview.no_countries')}
              items={data.top_countries.map((c) => ({ key: c.country, label: c.country, value: fmt.number(c.failures) }))}
            />
          </div>
          <ListCard
            title={t('security.overview.new_devices')}
            empty={t('security.overview.no_data')}
            items={data.new_device_logins.map((d, i) => ({
              key: `${d.ts}-${i}`,
              label: `${d.username} · ${d.ip}${d.country ? ` (${d.country})` : ''}`,
              value: fmt.dateTime(d.ts),
            }))}
          />
        </>
      )}
    </div>
  )
}
