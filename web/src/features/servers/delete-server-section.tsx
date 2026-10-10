import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { Server } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { SectionCard } from '@/features/profile/section-card'
import { useErrorToast } from '@/lib/error-message'
import { deleteServer, serverKey, serversKey } from './api'

/** Deleting needs the server name typed in; the text names the deleted/ folder on the host (spec §3.3). */
export function DeleteServerSection({ server }: { server: Server }) {
  const { t } = useTranslation()
  const id = useId()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const [typed, setTyped] = useState('')
  const matches = typed.trim() === server.name

  const remove = useMutation({
    mutationFn: () => deleteServer(server.id),
    onSuccess: () => {
      toast.success(t('servers.delete.deleted', { name: server.name }))
      void navigate({ to: '/' })
      queryClient.removeQueries({ queryKey: serverKey(server.id), exact: true })
      void queryClient.invalidateQueries({ queryKey: serversKey, exact: true })
    },
    onError: notifyError,
  })

  return (
    <SectionCard title={t('servers.delete.title')} className="max-w-3xl">
      <form
        className="space-y-4"
        noValidate
        onSubmit={(e) => {
          e.preventDefault()
          if (matches) remove.mutate()
        }}
      >
        <p className="text-sm">
          {server.instance ? t('servers.delete.description', { name: server.name }) : t('servers.delete.description_not_configured')}
        </p>
        <div className="space-y-2">
          <Label htmlFor={`${id}-confirm`}>{t('servers.delete.confirm_label', { name: server.name })}</Label>
          <Input
            id={`${id}-confirm`}
            className="sm:w-80"
            value={typed}
            autoCapitalize="none"
            autoComplete="off"
            spellCheck={false}
            onChange={(e) => setTyped(e.target.value)}
          />
        </div>
        <Button type="submit" variant="destructive" disabled={!matches || remove.isPending}>
          {t('servers.delete.submit')}
        </Button>
      </form>
    </SectionCard>
  )
}
