import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { errorMessage } from '@/lib/error-message'
import { cn } from '@/lib/utils'
import { serversKey } from './servers'

const SERVER_NAME = /^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$/

export function CreateServerDialog({ open, onOpenChange }: { open: boolean; onOpenChange(open: boolean): void }) {
  const { t } = useTranslation()
  const id = useId()
  const queryClient = useQueryClient()
  const [name, setName] = useState('')
  const trimmed = name.trim()
  const invalid = trimmed !== '' && !SERVER_NAME.test(trimmed)

  const create = useMutation({
    mutationFn: () => unwrap(api.POST('/servers', { body: { name: trimmed } })),
    onSuccess: (server) => {
      toast.success(t('dashboard.created', { name: server.name }))
      setName('')
      onOpenChange(false)
      void queryClient.invalidateQueries({ queryKey: serversKey })
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
          <DialogTitle>{t('dashboard.create_server')}</DialogTitle>
          <DialogDescription>{t('dashboard.create_description')}</DialogDescription>
        </DialogHeader>
        <form
          className="space-y-4"
          noValidate
          onSubmit={(e) => {
            e.preventDefault()
            if (trimmed && !invalid) create.mutate()
          }}
        >
          <div className="space-y-2">
            <Label htmlFor={id}>{t('dashboard.name_label')}</Label>
            <Input
              id={id}
              value={name}
              onChange={(e) => setName(e.target.value)}
              autoCapitalize="none"
              spellCheck={false}
              maxLength={32}
              aria-invalid={invalid || undefined}
              aria-describedby={`${id}-hint`}
            />
            <p id={`${id}-hint`} className={cn('text-sm', invalid ? 'text-destructive' : 'text-muted-foreground')}>
              {invalid ? t('dashboard.name_invalid') : t('dashboard.name_hint')}
            </p>
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
            <Button type="submit" disabled={create.isPending || !trimmed || invalid}>
              {t('common.create')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
