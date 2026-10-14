import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import { ALL_PERMISSIONS, type Server } from '@/api/types'
import { DataList, type Column } from '@/components/data-list'
import { PageHeader } from '@/components/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { serversQueryOptions } from '@/features/servers/api'
import { errorMessage, useErrorToast } from '@/lib/error-message'
import { fromMatrix, newlyGranted, sameMatrix, toggle, toMatrix, type Matrix } from './permission-matrix'

function Warning({ children }: { children: string }) {
  return (
    <p role="status" data-tone="danger" className="bevel-inset text-foreground border-destructive mb-4 border-l-4 px-3 py-2 text-sm">
      {children}
    </p>
  )
}

export function UserPermissionsPage({ userId }: { userId: number }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const permissionsKey = ['users', userId, 'permissions'] as const
  const user = useQuery({ queryKey: ['users', userId], queryFn: () => unwrap(api.GET('/users/{id}', { params: { path: { id: userId } } })) })
  const servers = useQuery(serversQueryOptions)
  const permissions = useQuery({
    queryKey: permissionsKey,
    queryFn: () => unwrap(api.GET('/users/{id}/permissions', { params: { path: { id: userId } } })),
  })
  // Local edits only; the saved matrix is always derived from the query (no effect-based syncing).
  const [draft, setDraft] = useState<Matrix | null>(null)
  const saved = permissions.data ? toMatrix(permissions.data.grants) : null
  const current = draft ?? saved ?? {}
  const dirty = draft !== null && saved !== null && !sameMatrix(draft, saved)

  const save = useMutation({
    mutationFn: (matrix: Matrix) =>
      unwrap(api.PUT('/users/{id}/permissions', { params: { path: { id: userId } }, body: { grants: fromMatrix(matrix) } })),
    onSuccess: (result) => {
      queryClient.setQueryData(permissionsKey, result)
      setDraft(null)
      toast.success(t('permissions.saved'))
    },
    onError: notifyError,
  })

  if (user.isPending || servers.isPending || permissions.isPending) {
    return <p className="text-muted-foreground">{t('common.loading')}</p>
  }
  if (user.isError || servers.isError || permissions.isError) {
    return (
      <p role="alert" className="text-destructive">
        {errorMessage(user.error ?? servers.error ?? permissions.error, t)}
      </p>
    )
  }

  const targetIsAdmin = user.data.role === 'admin'
  const columns: Column<Server>[] = [
    { id: 'server', header: t('permissions.col_server'), cell: (s) => <span className="font-medium">{s.name}</span> },
    ...ALL_PERMISSIONS.map(
      (p): Column<Server> => ({
        id: p,
        header: t(`permission.${p}`),
        className: 'text-center',
        cell: (s) => (
          <input
            type="checkbox"
            className="accent-primary size-4 align-middle"
            aria-label={t('permissions.checkbox', { server: s.name, permission: t(`permission.${p}`) })}
            checked={targetIsAdmin || (current[s.id]?.includes(p) ?? false)}
            disabled={targetIsAdmin || save.isPending}
            onChange={(e) => setDraft(toggle(current, s.id, p, e.target.checked))}
          />
        ),
      }),
    ),
  ]

  const filesWarning = !targetIsAdmin && saved !== null && newlyGranted(current, saved, 'server.files').length > 0
  // Spec §7.2: a plugin runs code in the container – same trust as server.files.
  const pluginsWarning = !targetIsAdmin && saved !== null && newlyGranted(current, saved, 'server.plugins').length > 0
  // Backups spec §5.10: server.backups reads every file (download) and resets the server.
  const backupsWarning = !targetIsAdmin && saved !== null && newlyGranted(current, saved, 'server.backups').length > 0

  return (
    <>
      <PageHeader
        title={t('permissions.title', { name: user.data.username })}
        description={t('permissions.description')}
        actions={
          <>
            <Button asChild variant="outline">
              <Link to="/users">{t('permissions.back')}</Link>
            </Button>
            <Button type="button" disabled={!dirty || save.isPending || targetIsAdmin} onClick={() => save.mutate(current)}>
              {t('common.save')}
            </Button>
          </>
        }
      />
      {targetIsAdmin ? <p className="text-muted-foreground mb-4 text-sm">{t('permissions.admin_note')}</p> : null}
      {dirty ? (
        <Badge variant="outline" className="mb-4">
          {t('permissions.unsaved')}
        </Badge>
      ) : null}
      {filesWarning ? <Warning>{t('permissions.files_warning')}</Warning> : null}
      {pluginsWarning ? <Warning>{t('permissions.plugins_warning')}</Warning> : null}
      {backupsWarning ? <Warning>{t('permissions.backups_warning')}</Warning> : null}
      <DataList items={servers.data} columns={columns} rowKey={(s) => s.id} caption={t('permissions.caption')} empty={t('permissions.no_servers')} />
    </>
  )
}
