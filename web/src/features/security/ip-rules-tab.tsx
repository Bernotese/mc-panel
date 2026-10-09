import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap, unwrapVoid } from '@/api/errors'
import type { IPRule } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { DataList, type Column } from '@/components/data-list'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { errorMessage, useErrorToast } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import { CreateIpRuleDialog } from './create-ip-rule-dialog'

const rulesKey = ['security', 'ip-rules'] as const

export function IpRulesTab() {
  const { t } = useTranslation()
  const fmt = useFormat()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const [creating, setCreating] = useState(false)
  const [deleting, setDeleting] = useState<IPRule | null>(null)
  const rules = useQuery({ queryKey: rulesKey, queryFn: () => unwrap(api.GET('/security/ip-rules')) })
  const remove = useMutation({
    mutationFn: (rule: IPRule) => unwrapVoid(api.DELETE('/security/ip-rules/{id}', { params: { path: { id: rule.id } } })),
    onSuccess: (_data, rule) => {
      setDeleting(null)
      toast.success(t('security.ip_rules.deleted', { cidr: rule.cidr }))
      void queryClient.invalidateQueries({ queryKey: rulesKey })
    },
    onError: notifyError,
  })

  const columns: Column<IPRule>[] = [
    { id: 'cidr', header: t('security.ip_rules.col.cidr'), cell: (r) => <span className="font-mono text-xs">{r.cidr}</span> },
    {
      id: 'kind',
      header: t('security.ip_rules.col.kind'),
      cell: (r) => <Badge variant={r.kind === 'allow' ? 'secondary' : 'destructive'}>{t(`security.ip_rules.kind_${r.kind}`)}</Badge>,
    },
    { id: 'comment', header: t('security.ip_rules.col.comment'), cell: (r) => r.comment },
    {
      id: 'expires',
      header: t('security.ip_rules.col.expires'),
      cell: (r) => (r.expires_at === null ? t('security.ip_rules.never') : fmt.dateTime(r.expires_at)),
    },
    { id: 'created_by', header: t('security.ip_rules.col.created_by'), cell: (r) => r.created_by },
    { id: 'created_at', header: t('security.ip_rules.col.created_at'), cell: (r) => fmt.dateTime(r.created_at) },
    {
      id: 'actions',
      header: '',
      cell: (r) => (
        <Button
          type="button"
          size="sm"
          variant="ghost"
          className="text-destructive"
          aria-label={t('security.ip_rules.delete_for', { cidr: r.cidr })}
          onClick={() => setDeleting(r)}
        >
          {t('common.delete')}
        </Button>
      ),
    },
  ]

  return (
    <div className="space-y-4">
      <div className="flex justify-end">
        <Button type="button" onClick={() => setCreating(true)}>
          {t('security.ip_rules.create')}
        </Button>
      </div>
      {rules.isError ? (
        <p role="alert" className="text-destructive">
          {errorMessage(rules.error, t)}
        </p>
      ) : null}
      {rules.isPending ? (
        <p className="text-muted-foreground">{t('common.loading')}</p>
      ) : (
        <DataList items={rules.data ?? []} columns={columns} rowKey={(r) => r.id} caption={t('security.ip_rules.caption')} empty={t('security.ip_rules.empty')} />
      )}
      <CreateIpRuleDialog open={creating} onOpenChange={setCreating} />
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={t('security.ip_rules.delete_title', { cidr: deleting?.cidr ?? '' })}
        description={t('security.ip_rules.delete_description')}
        confirmLabel={t('common.delete')}
        destructive
        pending={remove.isPending}
        onConfirm={() => deleting && remove.mutate(deleting)}
      />
    </div>
  )
}
