import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ShieldMinus } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { Server } from '@/api/types'
import { Section } from '@/components/section'
import { Button } from '@/components/ui/button'
import { NameForm } from '@/features/players/name-form'
import { errorMessage, useErrorToast } from '@/lib/error-message'
import { addDevOp, devOpsKey, devOpsQueryOptions, removeDevOp } from './api'

/**
 * Spec §5: the dev's own ops list, kept across syncs and re-applied after every start. Names are stored even while the
 * dev sleeps, so the form stays usable when it is stopped.
 */
export function DevOpsSection({ server }: { server: Server }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const ops = useQuery(devOpsQueryOptions(server.id))
  const add = useMutation({
    mutationFn: (name: string) => addDevOp(server.id, name),
    onSuccess: (data, name) => {
      queryClient.setQueryData(devOpsKey(server.id), data)
      toast.success(t('dev.ops.added', { name }))
    },
    onError: notifyError,
  })
  const remove = useMutation({
    mutationFn: (name: string) => removeDevOp(server.id, name),
    onSuccess: (data, name) => {
      queryClient.setQueryData(devOpsKey(server.id), data)
      toast.success(t('dev.ops.removed', { name }))
    },
    onError: notifyError,
  })
  const busy = add.isPending || remove.isPending

  return (
    <Section title={t('dev.ops.title')} description={t('dev.ops.description')}>
      <div className="space-y-3">
        <NameForm label={t('dev.ops.add_label')} disabled={busy} onAdd={(name) => add.mutateAsync(name)} />
        {ops.isPending ? <p className="text-muted-foreground text-sm">{t('common.loading')}</p> : null}
        {ops.isError ? (
          <p role="alert" className="text-destructive text-sm">
            {errorMessage(ops.error, t)}
          </p>
        ) : null}
        {ops.data && ops.data.names.length === 0 ? <p className="text-muted-foreground text-sm">{t('dev.ops.empty')}</p> : null}
        {ops.data && ops.data.names.length > 0 ? (
          <ul aria-label={t('dev.ops.title')} className="divide-y">
            {ops.data.names.map((name) => {
              const label = t('dev.ops.remove_label', { name })
              return (
                <li key={name} className="flex items-center justify-between gap-2 py-1.5">
                  <span className="font-medium">{name}</span>
                  <Button type="button" size="icon-sm" variant="secondary" disabled={busy} aria-label={label} title={label} onClick={() => remove.mutate(name)}>
                    <ShieldMinus aria-hidden="true" />
                  </Button>
                </li>
              )
            })}
          </ul>
        ) : null}
      </div>
    </Section>
  )
}
