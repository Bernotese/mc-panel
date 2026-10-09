import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap, unwrapVoid } from '@/api/errors'
import type { Notifier } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { DataList, type Column } from '@/components/data-list'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { errorMessage, useErrorToast } from '@/lib/error-message'
import { NotifierDialog } from './notifier-dialog'
import { notifiersKey } from './security-key'

export function NotifiersTab() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const [editing, setEditing] = useState<Notifier | null>(null)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [deleting, setDeleting] = useState<Notifier | null>(null)
  const notifiers = useQuery({ queryKey: notifiersKey, queryFn: () => unwrap(api.GET('/security/notifiers')) })
  const invalidate = () => queryClient.invalidateQueries({ queryKey: notifiersKey })

  const toggle = useMutation({
    mutationFn: ({ notifier, enabled }: { notifier: Notifier; enabled: boolean }) =>
      unwrap(api.PATCH('/security/notifiers/{id}', { params: { path: { id: notifier.id } }, body: { enabled } })),
    onSuccess: (saved) => {
      toast.success(t('security.notifiers.updated', { name: saved.name }))
      void invalidate()
    },
    onError: notifyError,
  })
  const test = useMutation({
    mutationFn: (notifier: Notifier) => unwrapVoid(api.POST('/security/notifiers/{id}/test', { params: { path: { id: notifier.id } } })),
    onSuccess: (_data, notifier) => toast.success(t('security.notifiers.test_sent', { name: notifier.name })),
    onError: notifyError,
  })
  const remove = useMutation({
    mutationFn: (notifier: Notifier) => unwrapVoid(api.DELETE('/security/notifiers/{id}', { params: { path: { id: notifier.id } } })),
    onSuccess: (_data, notifier) => {
      setDeleting(null)
      toast.success(t('security.notifiers.deleted', { name: notifier.name }))
      void invalidate()
    },
    onError: notifyError,
  })

  const openDialog = (notifier: Notifier | null) => {
    setEditing(notifier)
    setDialogOpen(true)
  }

  const columns: Column<Notifier>[] = [
    { id: 'name', header: t('security.notifiers.col.name'), cell: (n) => <span className="font-medium">{n.name}</span> },
    { id: 'kind', header: t('security.notifiers.col.kind'), cell: (n) => t(`security.notifiers.kind_${n.kind}`) },
    { id: 'target', header: t('security.notifiers.col.target'), cell: (n) => <span className="font-mono text-xs break-all">{n.target_masked}</span> },
    {
      id: 'enabled',
      header: t('security.notifiers.col.enabled'),
      cell: (n) => (
        <Switch
          checked={n.enabled}
          disabled={toggle.isPending}
          aria-label={t('security.notifiers.enabled_for', { name: n.name })}
          onCheckedChange={(enabled) => toggle.mutate({ notifier: n, enabled })}
        />
      ),
    },
    {
      id: 'actions',
      header: t('security.notifiers.col.actions'),
      cell: (n) => (
        <div className="flex flex-wrap gap-2">
          <Button type="button" size="sm" variant="outline" aria-label={t('security.notifiers.test_for', { name: n.name })} onClick={() => test.mutate(n)}>
            {t('security.notifiers.test')}
          </Button>
          <Button type="button" size="sm" variant="outline" aria-label={t('security.notifiers.edit_for', { name: n.name })} onClick={() => openDialog(n)}>
            {t('common.edit')}
          </Button>
          <Button
            type="button"
            size="sm"
            variant="ghost"
            className="text-destructive"
            aria-label={t('security.notifiers.delete_for', { name: n.name })}
            onClick={() => setDeleting(n)}
          >
            {t('common.delete')}
          </Button>
        </div>
      ),
    },
  ]

  return (
    <div className="space-y-4">
      <div className="flex justify-end">
        <Button type="button" onClick={() => openDialog(null)}>
          {t('security.notifiers.create')}
        </Button>
      </div>
      {notifiers.isError ? (
        <p role="alert" className="text-destructive">
          {errorMessage(notifiers.error, t)}
        </p>
      ) : null}
      {notifiers.isPending ? (
        <p className="text-muted-foreground">{t('common.loading')}</p>
      ) : (
        <DataList items={notifiers.data ?? []} columns={columns} rowKey={(n) => n.id} caption={t('security.notifiers.caption')} empty={t('security.notifiers.empty')} />
      )}
      <NotifierDialog open={dialogOpen} onOpenChange={setDialogOpen} notifier={editing} />
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={t('security.notifiers.delete_title', { name: deleting?.name ?? '' })}
        description={t('security.notifiers.delete_description')}
        confirmLabel={t('common.delete')}
        destructive
        pending={remove.isPending}
        onConfirm={() => deleting && remove.mutate(deleting)}
      />
    </div>
  )
}
