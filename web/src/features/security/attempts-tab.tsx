import { useInfiniteQuery } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { LoginAttempt } from '@/api/types'
import { DataList, type Column } from '@/components/data-list'
import { NativeSelect } from '@/components/native-select'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { errorMessage } from '@/lib/error-message'
import { useFormat } from '@/lib/format'

type ResultFilter = 'all' | 'success' | 'failure'

interface Filters {
  result: ResultFilter
  ip: string
}

export function AttemptsTab() {
  const { t } = useTranslation()
  const fmt = useFormat()
  const id = useId()
  const [result, setResult] = useState<ResultFilter>('all')
  const [ipInput, setIpInput] = useState('')
  const [filters, setFilters] = useState<Filters>({ result: 'all', ip: '' })

  const attempts = useInfiniteQuery({
    queryKey: ['security', 'attempts', filters],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) =>
      unwrap(
        api.GET('/security/attempts', {
          params: {
            query: {
              success: filters.result === 'all' ? undefined : filters.result === 'success',
              ip: filters.ip || undefined,
              cursor: pageParam,
              limit: 50,
            },
          },
        }),
      ),
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  })
  const items = attempts.data?.pages.flatMap((page) => page.items) ?? []

  const columns: Column<LoginAttempt>[] = [
    { id: 'time', header: t('security.attempts.col.time'), cell: (a) => fmt.dateTime(a.ts) },
    { id: 'username', header: t('security.attempts.col.username'), cell: (a) => a.username },
    {
      id: 'ip',
      header: t('security.attempts.col.ip'),
      cell: (a) => (
        <span className="font-mono text-xs">
          {a.ip}
          {a.country ? ` (${a.country})` : ''}
        </span>
      ),
    },
    { id: 'stage', header: t('security.attempts.col.stage'), cell: (a) => t(`security.attempts.stage.${a.stage}`) },
    {
      id: 'result',
      header: t('security.attempts.col.result'),
      cell: (a) => (
        <Badge variant={a.success ? 'secondary' : 'destructive'}>
          {a.success ? t('security.attempts.result_success') : t('security.attempts.result_failure')}
        </Badge>
      ),
    },
    { id: 'reason', header: t('security.attempts.col.reason'), cell: (a) => a.reason },
    {
      id: 'user_agent',
      header: t('security.attempts.col.user_agent'),
      cell: (a) => (
        <span className="block max-w-56 truncate" title={a.user_agent}>
          {a.user_agent}
        </span>
      ),
    },
  ]

  return (
    <div className="space-y-4">
      <form
        className="flex flex-col gap-3 sm:flex-row sm:items-end"
        onSubmit={(e) => {
          e.preventDefault()
          setFilters({ result, ip: ipInput.trim() })
        }}
      >
        <div className="space-y-1">
          <Label htmlFor={`${id}-result`}>{t('security.attempts.result_label')}</Label>
          <NativeSelect id={`${id}-result`} className="w-full sm:w-auto" value={result} onChange={(e) => setResult(e.target.value as ResultFilter)}>
            <option value="all">{t('security.attempts.result_all')}</option>
            <option value="success">{t('security.attempts.result_success')}</option>
            <option value="failure">{t('security.attempts.result_failure')}</option>
          </NativeSelect>
        </div>
        <div className="space-y-1">
          <Label htmlFor={`${id}-ip`}>{t('security.attempts.ip_label')}</Label>
          <Input id={`${id}-ip`} value={ipInput} onChange={(e) => setIpInput(e.target.value)} autoComplete="off" spellCheck={false} />
        </div>
        <Button type="submit" variant="secondary">
          {t('common.apply_filters')}
        </Button>
      </form>
      {attempts.isError ? (
        <p role="alert" className="text-destructive">
          {errorMessage(attempts.error, t)}
        </p>
      ) : null}
      {attempts.isPending ? (
        <p className="text-muted-foreground">{t('common.loading')}</p>
      ) : (
        <DataList items={items} columns={columns} rowKey={(a) => a.id} caption={t('security.attempts.caption')} empty={t('security.attempts.empty')} />
      )}
      {attempts.hasNextPage ? (
        <Button type="button" variant="outline" disabled={attempts.isFetchingNextPage} onClick={() => void attempts.fetchNextPage()}>
          {t('common.load_more')}
        </Button>
      ) : null}
    </div>
  )
}
