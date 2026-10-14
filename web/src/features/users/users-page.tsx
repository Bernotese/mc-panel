import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap, unwrapVoid } from '@/api/errors'
import type { Role, User } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { DataList, type Column } from '@/components/data-list'
import { NativeSelect } from '@/components/native-select'
import { PageHeader } from '@/components/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { useMe } from '@/features/auth/me'
import { errorMessage, useErrorToast } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import { CreateUserDialog } from './create-user-dialog'
import { usersKey } from './users-key'

export function UsersPage() {
  const { t } = useTranslation()
  const me = useMe()
  const fmt = useFormat()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const [creating, setCreating] = useState(false)
  const [deleting, setDeleting] = useState<User | null>(null)
  const [resetting, setResetting] = useState<User | null>(null)
  const users = useQuery({ queryKey: usersKey, queryFn: () => unwrap(api.GET('/users')) })
  const invalidate = () => queryClient.invalidateQueries({ queryKey: usersKey })

  const changeRole = useMutation({
    mutationFn: ({ id, role }: { id: number; role: Role }) => unwrap(api.PATCH('/users/{id}', { params: { path: { id } }, body: { role } })),
    onSuccess: (updated) => {
      toast.success(t('users.role_changed', { name: updated.username, role: t(`users.role.${updated.role}`) }))
      void invalidate()
    },
    onError: (err) => {
      notifyError(err)
      void invalidate()
    },
  })
  const remove = useMutation({
    mutationFn: (u: User) => unwrapVoid(api.DELETE('/users/{id}', { params: { path: { id: u.id } } })),
    onSuccess: (_data, u) => {
      setDeleting(null)
      toast.success(t('users.deleted', { name: u.username }))
      void invalidate()
    },
    onError: notifyError,
  })
  const reset2fa = useMutation({
    mutationFn: (u: User) => unwrapVoid(api.POST('/users/{id}/reset-2fa', { params: { path: { id: u.id } } })),
    onSuccess: (_data, u) => {
      setResetting(null)
      toast.success(t('users.reset_done', { name: u.username }))
      void invalidate()
    },
    onError: notifyError,
  })

  const columns: Column<User>[] = [
    {
      id: 'username',
      header: t('users.col.username'),
      cell: (u) => (
        <span className="font-medium">
          {u.username}
          {u.id === me?.id ? ` ${t('users.you')}` : ''}
        </span>
      ),
    },
    {
      id: 'role',
      header: t('users.col.role'),
      cell: (u) => (
        <NativeSelect
          aria-label={t('users.role_for', { name: u.username })}
          value={u.role}
          disabled={u.id === me?.id || changeRole.isPending}
          onChange={(e) => changeRole.mutate({ id: u.id, role: e.target.value as Role })}
        >
          <option value="admin">{t('users.role.admin')}</option>
          <option value="user">{t('users.role.user')}</option>
        </NativeSelect>
      ),
    },
    {
      id: 'twofa',
      header: t('users.col.twofa'),
      cell: (u) =>
        !u.totp_enabled && u.passkey_count === 0 ? (
          <Badge variant="outline">{t('users.twofa_none')}</Badge>
        ) : (
          <span className="flex flex-wrap gap-1">
            {u.totp_enabled ? <Badge variant="secondary">{t('users.twofa_totp')}</Badge> : null}
            {u.passkey_count > 0 ? <Badge variant="secondary">{t('users.twofa_passkeys', { n: u.passkey_count })}</Badge> : null}
          </span>
        ),
    },
    {
      id: 'last_login',
      header: t('users.col.last_login'),
      className: 'whitespace-nowrap tabular-nums',
      cell: (u) => (u.last_login_at ? fmt.dateTime(u.last_login_at) : t('users.never')),
    },
    {
      id: 'actions',
      header: t('users.col.actions'),
      className: 'text-right',
      cell: (u) => (
        <div className="flex flex-wrap justify-end gap-1.5">
          <Button asChild size="xs" variant="outline">
            <Link to="/users/$id/permissions" params={{ id: String(u.id) }} aria-label={t('users.permissions_for', { name: u.username })}>
              {t('users.permissions')}
            </Link>
          </Button>
          <Button
            type="button"
            size="xs"
            variant="outline"
            aria-label={t('users.reset_for', { name: u.username })}
            disabled={!u.totp_enabled && u.passkey_count === 0}
            onClick={() => setResetting(u)}
          >
            {t('users.reset_2fa')}
          </Button>
          <Button
            type="button"
            size="xs"
            variant="ghost"
            className="text-destructive"
            aria-label={t('users.delete_for', { name: u.username })}
            disabled={u.id === me?.id}
            onClick={() => setDeleting(u)}
          >
            {t('common.delete')}
          </Button>
        </div>
      ),
    },
  ]

  return (
    <>
      <PageHeader
        title={t('users.title')}
        description={t('users.description')}
        actions={
          <Button type="button" onClick={() => setCreating(true)}>
            {t('users.create')}
          </Button>
        }
      />
      {users.isPending ? <p className="text-muted-foreground">{t('common.loading')}</p> : null}
      {users.isError ? (
        <p role="alert" className="text-destructive">
          {errorMessage(users.error, t)}
        </p>
      ) : null}
      {users.data ? <DataList items={users.data} columns={columns} rowKey={(u) => u.id} caption={t('users.caption')} empty={t('users.empty')} /> : null}
      <CreateUserDialog open={creating} onOpenChange={setCreating} />
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={t('users.delete_title', { name: deleting?.username ?? '' })}
        description={t('users.delete_description')}
        confirmLabel={t('common.delete')}
        destructive
        pending={remove.isPending}
        onConfirm={() => deleting && remove.mutate(deleting)}
      />
      <ConfirmDialog
        open={resetting !== null}
        onOpenChange={(open) => !open && setResetting(null)}
        title={t('users.reset_title', { name: resetting?.username ?? '' })}
        description={t('users.reset_description')}
        confirmLabel={t('users.reset_2fa')}
        destructive
        pending={reset2fa.isPending}
        onConfirm={() => resetting && reset2fa.mutate(resetting)}
      />
    </>
  )
}
