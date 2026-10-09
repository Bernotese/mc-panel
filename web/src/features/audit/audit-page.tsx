import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { AuditEntry, AuditResult } from '@/api/types'
import { DataList, type Column } from '@/components/data-list'
import { NativeSelect } from '@/components/native-select'
import { PageHeader } from '@/components/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { errorMessage } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import { EMPTY_FILTERS, toAuditQuery, type AuditFilters } from './audit-filters'

const RESULT_VARIANT: Record<AuditResult, 'secondary' | 'destructive' | 'outline'> = {
  success: 'secondary',
  failure: 'destructive',
  denied: 'outline',
}

function ChainStatus() {
  const { t } = useTranslation()
  const verify = useQuery({ queryKey: ['audit', 'verify'], queryFn: () => unwrap(api.GET('/audit/verify')) })
  let text = t('audit.chain_checking')
  let variant: 'secondary' | 'destructive' | 'outline' = 'outline'
  if (verify.isError) {
    text = t('audit.chain_error')
    variant = 'destructive'
  } else if (verify.data?.ok) {
    text = t('audit.chain_ok', { checked: verify.data.checked })
    variant = 'secondary'
  } else if (verify.data) {
    text = t('audit.chain_broken', { id: verify.data.first_bad_id ?? '?' })
    variant = 'destructive'
  }
  return (
    <div className="flex flex-wrap items-center gap-2">
      <span role="status">
        <Badge variant={variant}>{text}</Badge>
      </span>
      <Button type="button" size="sm" variant="outline" disabled={verify.isFetching} onClick={() => void verify.refetch()}>
        {t('audit.verify_again')}
      </Button>
    </div>
  )
}

export function AuditPage() {
  const { t } = useTranslation()
  const fmt = useFormat()
  const id = useId()
  const [draft, setDraft] = useState<AuditFilters>(EMPTY_FILTERS)
  const [filters, setFilters] = useState<AuditFilters>(EMPTY_FILTERS)
  const entries = useInfiniteQuery({
    queryKey: ['audit', 'entries', filters],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) => unwrap(api.GET('/audit', { params: { query: { ...toAuditQuery(filters), cursor: pageParam, limit: 50 } } })),
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  })
  const items = entries.data?.pages.flatMap((page) => page.items) ?? []
  const update = (patch: Partial<AuditFilters>) => setDraft({ ...draft, ...patch })

  const columns: Column<AuditEntry>[] = [
    { id: 'time', header: t('audit.col.time'), cell: (e) => fmt.dateTime(e.ts) },
    {
      id: 'actor',
      header: t('audit.col.actor'),
      cell: (e) => (
        <span>
          {e.actor_name} <span className="text-muted-foreground text-xs">({t(`audit.actor_type.${e.actor_type}`)})</span>
        </span>
      ),
    },
    { id: 'action', header: t('audit.col.action'), cell: (e) => <code className="text-xs">{e.action}</code> },
    { id: 'target', header: t('audit.col.target'), cell: (e) => <span className="break-all">{e.target}</span> },
    { id: 'ip', header: t('audit.col.ip'), cell: (e) => <span className="font-mono text-xs">{e.ip || '—'}</span> },
    { id: 'result', header: t('audit.col.result'), cell: (e) => <Badge variant={RESULT_VARIANT[e.result]}>{t(`audit.result.${e.result}`)}</Badge> },
    {
      id: 'details',
      header: t('audit.col.details'),
      cell: (e) => {
        const json = JSON.stringify(e.details)
        return json === '{}' ? (
          '—'
        ) : (
          <code className="block max-w-64 truncate text-xs" title={json}>
            {json}
          </code>
        )
      },
    },
    {
      id: 'hash',
      header: t('audit.col.hash'),
      cell: (e) => (
        <code className="text-muted-foreground text-xs" title={e.hash}>
          {e.hash.slice(0, 12)}
        </code>
      ),
    },
  ]

  return (
    <>
      <PageHeader title={t('audit.title')} description={t('audit.description')} actions={<ChainStatus />} />
      <form
        className="mb-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-6 lg:items-end"
        onSubmit={(e) => {
          e.preventDefault()
          setFilters(draft)
        }}
      >
        <div className="space-y-1">
          <Label htmlFor={`${id}-actor`}>{t('audit.actor')}</Label>
          <Input id={`${id}-actor`} value={draft.actor} onChange={(e) => update({ actor: e.target.value })} />
        </div>
        <div className="space-y-1">
          <Label htmlFor={`${id}-action`}>{t('audit.action')}</Label>
          <Input id={`${id}-action`} value={draft.action} spellCheck={false} onChange={(e) => update({ action: e.target.value })} />
        </div>
        <div className="space-y-1">
          <Label htmlFor={`${id}-result`}>{t('audit.result_label')}</Label>
          <NativeSelect id={`${id}-result`} className="w-full" value={draft.result} onChange={(e) => update({ result: e.target.value as AuditResult | '' })}>
            <option value="">{t('audit.result_all')}</option>
            <option value="success">{t('audit.result.success')}</option>
            <option value="failure">{t('audit.result.failure')}</option>
            <option value="denied">{t('audit.result.denied')}</option>
          </NativeSelect>
        </div>
        <div className="space-y-1">
          <Label htmlFor={`${id}-from`}>{t('audit.from')}</Label>
          <Input id={`${id}-from`} type="date" value={draft.from} onChange={(e) => update({ from: e.target.value })} />
        </div>
        <div className="space-y-1">
          <Label htmlFor={`${id}-to`}>{t('audit.to')}</Label>
          <Input id={`${id}-to`} type="date" value={draft.to} onChange={(e) => update({ to: e.target.value })} />
        </div>
        <div className="flex gap-2">
          <Button type="submit" variant="secondary">
            {t('common.apply_filters')}
          </Button>
          <Button
            type="button"
            variant="ghost"
            onClick={() => {
              setDraft(EMPTY_FILTERS)
              setFilters(EMPTY_FILTERS)
            }}
          >
            {t('common.reset_filters')}
          </Button>
        </div>
      </form>
      {entries.isError ? (
        <p role="alert" className="text-destructive">
          {errorMessage(entries.error, t)}
        </p>
      ) : null}
      {entries.isPending ? (
        <p className="text-muted-foreground">{t('common.loading')}</p>
      ) : (
        <DataList items={items} columns={columns} rowKey={(e) => e.id} caption={t('audit.caption')} empty={t('audit.empty')} />
      )}
      {entries.hasNextPage ? (
        <Button type="button" variant="outline" className="mt-4" disabled={entries.isFetchingNextPage} onClick={() => void entries.fetchNextPage()}>
          {t('common.load_more')}
        </Button>
      ) : null}
    </>
  )
}
