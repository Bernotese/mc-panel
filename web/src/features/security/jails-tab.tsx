import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap, unwrapVoid } from '@/api/errors'
import type { Jail } from '@/api/types'
import { DataList, type Column } from '@/components/data-list'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { errorMessage, useErrorToast } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import { CreateJailDialog } from './create-jail-dialog'
import { securityKey } from './security-key'

/** "Now" is the moment the list was fetched, so render stays pure. */
function isActive(jail: Jail, fetchedAt: number): boolean {
  return jail.lifted_at === null && jail.expires_at > fetchedAt
}

export function JailsTab() {
  const { t } = useTranslation()
  const fmt = useFormat()
  const id = useId()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const [onlyActive, setOnlyActive] = useState(true)
  const [creating, setCreating] = useState(false)
  const jails = useQuery({
    queryKey: ['security', 'jails', onlyActive],
    queryFn: () => unwrap(api.GET('/security/jails', { params: { query: { active: onlyActive } } })),
  })
  const lift = useMutation({
    mutationFn: (jail: Jail) => unwrapVoid(api.DELETE('/security/jails/{id}', { params: { path: { id: jail.id } } })),
    onSuccess: (_data, jail) => {
      toast.success(t('security.jails.lifted', { ip: jail.ip_key }))
      void queryClient.invalidateQueries({ queryKey: securityKey })
    },
    onError: notifyError,
  })
  const fetchedAt = jails.dataUpdatedAt

  const status = (jail: Jail) => {
    if (jail.lifted_at !== null) return t('security.jails.status_lifted', { by: jail.lifted_by ?? '?' })
    return isActive(jail, fetchedAt) ? t('security.jails.status_active') : t('security.jails.status_expired')
  }

  const columns: Column<Jail>[] = [
    {
      id: 'ip',
      header: t('security.jails.col.ip'),
      cell: (j) => (
        <span className="flex flex-wrap items-center gap-2">
          <span className="font-mono text-xs">{j.ip_key}</span>
          {j.manual ? <Badge variant="outline">{t('security.jails.manual')}</Badge> : null}
        </span>
      ),
    },
    { id: 'reason', header: t('security.jails.col.reason'), cell: (j) => j.reason },
    { id: 'level', header: t('security.jails.col.level'), cell: (j) => fmt.number(j.level) },
    { id: 'since', header: t('security.jails.col.since'), cell: (j) => fmt.dateTime(j.created_at) },
    { id: 'until', header: t('security.jails.col.until'), cell: (j) => fmt.dateTime(j.expires_at) },
    { id: 'status', header: t('security.jails.col.status'), cell: status },
    {
      id: 'firewall',
      header: t('security.jails.col.firewall'),
      cell: (j) => <Badge variant={j.firewall_synced ? 'secondary' : 'outline'}>{j.firewall_synced ? t('security.jails.synced') : t('security.jails.pending')}</Badge>,
    },
    {
      id: 'actions',
      header: '',
      cell: (j) =>
        isActive(j, fetchedAt) ? (
          <Button
            type="button"
            size="sm"
            variant="outline"
            aria-label={t('security.jails.lift_for', { ip: j.ip_key })}
            disabled={lift.isPending}
            onClick={() => lift.mutate(j)}
          >
            {t('security.jails.lift')}
          </Button>
        ) : null,
    },
  ]

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <Switch id={`${id}-active`} checked={onlyActive} onCheckedChange={setOnlyActive} />
          <Label htmlFor={`${id}-active`}>{t('security.jails.only_active')}</Label>
        </div>
        <Button type="button" variant="destructive" onClick={() => setCreating(true)}>
          {t('security.jails.create')}
        </Button>
      </div>
      {jails.isError ? (
        <p role="alert" className="text-destructive">
          {errorMessage(jails.error, t)}
        </p>
      ) : null}
      {jails.isPending ? (
        <p className="text-muted-foreground">{t('common.loading')}</p>
      ) : (
        <DataList items={jails.data ?? []} columns={columns} rowKey={(j) => j.id} caption={t('security.jails.caption')} empty={t('security.jails.empty')} />
      )}
      <CreateJailDialog open={creating} onOpenChange={setCreating} />
    </div>
  )
}
