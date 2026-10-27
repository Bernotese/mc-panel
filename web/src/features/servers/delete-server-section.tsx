import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { isApiError } from '@/api/errors'
import type { Server } from '@/api/types'
import { Section } from '@/components/section'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useErrorToast } from '@/lib/error-message'
import { deleteServer, serverKey, serversKey } from './api'

/**
 * Deleting needs the server name typed in; the text names the deleted/ folder on the host (spec §3.3). The backups stay
 * on the host unless the checkbox asks the panel to purge them too (backups spec §5.6).
 */
export function DeleteServerSection({ server }: { server: Server }) {
  const { t } = useTranslation()
  const id = useId()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const [typed, setTyped] = useState('')
  const [purge, setPurge] = useState(false)
  const matches = typed.trim() === server.name
  // Dev instances spec §2: a prod with a dev is not deleted (409 dev_exists); the dev goes first.
  const blockedByDev = server.dev_id !== undefined

  const remove = useMutation({
    mutationFn: () => deleteServer(server.id, purge),
    onSuccess: () => {
      toast.success(t('servers.delete.deleted', { name: server.name }))
      // A deleted dev leads back to its source.
      if (server.dev) void navigate({ to: '/servers/$id', params: { id: String(server.dev.source_id) } })
      else void navigate({ to: '/' })
      queryClient.removeQueries({ queryKey: serverKey(server.id), exact: true })
      void queryClient.invalidateQueries({ queryKey: serversKey, exact: true })
    },
    onError: (err) => {
      // 409 conflict: an extract or a restore is rewriting the server's data; deleting has to wait for it.
      if (isApiError(err, 'conflict')) toast.error(t('servers.delete.busy'))
      else if (isApiError(err, 'dev_exists')) toast.error(t('dev.delete.blocked'))
      else notifyError(err)
    },
  })

  return (
    <Section title={server.dev ? t('dev.delete.title') : t('servers.delete.title')} tone="danger">
      <form
        className="flex flex-col gap-4"
        noValidate
        onSubmit={(e) => {
          e.preventDefault()
          if (matches) remove.mutate()
        }}
      >
        <p className="text-sm">
          {server.dev
            ? t('dev.delete.description', { name: server.name, source: server.dev.source_name })
            : server.instance
              ? t('servers.delete.description', { name: server.name })
              : t('servers.delete.description_not_configured')}
        </p>
        {blockedByDev ? (
          <p role="status" className="bevel-inset px-3 py-2 text-sm">
            {t('dev.delete.blocked')}{' '}
            <Link to="/servers/$id/settings" params={{ id: String(server.dev_id) }} className="text-primary underline underline-offset-4">
              {t('dev.delete.blocked_link', { name: `dev-${server.name}` })}
            </Link>
          </p>
        ) : null}
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
        {server.instance && !server.dev ? (
          <div className="space-y-1">
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                className="accent-primary size-4"
                checked={purge}
                aria-describedby={`${id}-purge-hint`}
                disabled={remove.isPending}
                onChange={(e) => setPurge(e.target.checked)}
              />
              {t('servers.delete.purge_backups')}
            </label>
            <p id={`${id}-purge-hint`} className="text-muted-foreground text-sm">
              {t('servers.delete.purge_backups_hint')}
            </p>
            {/* Purging the repository can take a while (backups spec §5.6). */}
            <p role="status" className="text-sm">
              {remove.isPending && purge ? t('servers.delete.deleting_with_backups') : null}
            </p>
          </div>
        ) : null}
        <Button type="submit" variant="destructive" disabled={!matches || remove.isPending || blockedByDev}>
          {server.dev ? t('dev.delete.submit') : t('servers.delete.submit')}
        </Button>
      </form>
    </Section>
  )
}
