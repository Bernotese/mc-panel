import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { Role } from '@/api/types'
import { NativeSelect } from '@/components/native-select'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { errorMessage } from '@/lib/error-message'
import { cn } from '@/lib/utils'
import { usersKey } from './users-key'


const USERNAME = /^[a-zA-Z0-9_.-]{3,32}$/

export function CreateUserDialog({ open, onOpenChange }: { open: boolean; onOpenChange(open: boolean): void }) {
  const { t } = useTranslation()
  const id = useId()
  const queryClient = useQueryClient()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [role, setRole] = useState<Role>('user')
  const usernameInvalid = username !== '' && !USERNAME.test(username)
  const passwordInvalid = password !== '' && (password.length < 12 || password.length > 256)
  const complete = username !== '' && password !== '' && !usernameInvalid && !passwordInvalid

  const create = useMutation({
    mutationFn: () => unwrap(api.POST('/users', { body: { username, password, role } })),
    onSuccess: (created) => {
      toast.success(t('users.created', { name: created.username }))
      setUsername('')
      setPassword('')
      setRole('user')
      onOpenChange(false)
      void queryClient.invalidateQueries({ queryKey: usersKey })
    },
  })

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) create.reset()
        onOpenChange(next)
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('users.create')}</DialogTitle>
          <DialogDescription>{t('users.create_description')}</DialogDescription>
        </DialogHeader>
        <form
          className="space-y-4"
          noValidate
          onSubmit={(e) => {
            e.preventDefault()
            if (complete) create.mutate()
          }}
        >
          <div className="space-y-2">
            <Label htmlFor={`${id}-username`}>{t('users.username')}</Label>
            <Input
              id={`${id}-username`}
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              autoComplete="off"
              autoCapitalize="none"
              spellCheck={false}
              maxLength={32}
              aria-invalid={usernameInvalid || undefined}
              aria-describedby={`${id}-username-hint`}
            />
            <p id={`${id}-username-hint`} className={cn('text-sm', usernameInvalid ? 'text-destructive' : 'text-muted-foreground')}>
              {usernameInvalid ? t('users.username_invalid') : t('users.username_hint')}
            </p>
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${id}-password`}>{t('users.password')}</Label>
            <Input
              id={`${id}-password`}
              type="password"
              autoComplete="new-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              maxLength={256}
              aria-invalid={passwordInvalid || undefined}
              aria-describedby={`${id}-password-hint`}
            />
            <p id={`${id}-password-hint`} className={cn('text-sm', passwordInvalid ? 'text-destructive' : 'text-muted-foreground')}>
              {passwordInvalid ? t('users.password_invalid') : t('users.password_hint')}
            </p>
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${id}-role`}>{t('users.role_label')}</Label>
            <NativeSelect id={`${id}-role`} className="w-full" value={role} onChange={(e) => setRole(e.target.value as Role)}>
              <option value="user">{t('users.role.user')}</option>
              <option value="admin">{t('users.role.admin')}</option>
            </NativeSelect>
          </div>
          {create.error ? (
            <p role="alert" className="text-destructive text-sm">
              {errorMessage(create.error, t)}
            </p>
          ) : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" disabled={create.isPending || !complete}>
              {t('common.create')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
