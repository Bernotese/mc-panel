import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { isSudoCancelled } from '@/api/errors'
import type { Server } from '@/api/types'
import { Section } from '@/components/section'
import { Button } from '@/components/ui/button'
import { memoryLimitsQueryOptions, serverKey, serversKey } from '@/features/servers/api'
import { memoryLimitsFrom } from '@/features/servers/form-model'
import { memoryInputValue } from '@/features/servers/format'
import { MemoryField } from '@/features/servers/server-form'
import { memoryMiBFromGiB, validateMemoryMiB } from '@/features/servers/validation'
import { errorMessage } from '@/lib/error-message'
import { createDev } from './api'
import { DevBadge } from './dev-badge'
import { canHaveDev, defaultDevMemoryMiB, devName } from './model'

/**
 * Spec §8 on a prod's overview: a link to its dev, or (admins) "Create dev instance" with the suggested memory and the
 * warning that the dev takes as much disk as the server itself.
 */
export function CreateDevSection({ server, isAdmin }: { server: Server; isAdmin: boolean }) {
  const { t } = useTranslation()
  if (server.dev || !server.instance) return null
  if (server.dev_id !== undefined) {
    return (
      <Section title={t('dev.create.title')} className="xl:col-span-2">
        <p className="flex flex-wrap items-center gap-2 text-sm">
          {t('dev.create.exists')}
          <Link to="/servers/$id" params={{ id: String(server.dev_id) }} className="text-primary font-medium underline underline-offset-4">
            {devName(server.name)}
          </Link>
          <DevBadge source={server.name} />
        </p>
      </Section>
    )
  }
  if (!isAdmin) return null
  return <CreateDevForm server={server} />
}

function CreateDevForm({ server }: { server: Server }) {
  const { t } = useTranslation()
  const id = useId()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const limits = useQuery(memoryLimitsQueryOptions)
  const bounds = memoryLimitsFrom(limits.data)
  const suggested = defaultDevMemoryMiB(server, bounds.max)
  const [text, setText] = useState<string | null>(null) // null = follow the suggestion
  const value = text ?? memoryInputValue(suggested)
  const mib = memoryMiBFromGiB(value)
  const error = validateMemoryMiB(mib, bounds) ?? undefined
  const allowed = canHaveDev(server.name)
  const name = devName(server.name)

  const create = useMutation({
    mutationFn: (memory: number) => createDev(server.id, { memory_mib: memory }),
    onSuccess: (dev) => {
      queryClient.setQueryData(serverKey(dev.id), dev)
      void queryClient.invalidateQueries({ queryKey: serversKey })
      toast.success(t('dev.create.created', { name: dev.name }))
      void navigate({ to: '/servers/$id', params: { id: String(dev.id) } })
    },
  })

  return (
    <Section title={t('dev.create.title')} description={t('dev.create.description', { source: server.name, name })} className="xl:col-span-2">
      {allowed ? (
        <form
          className="flex flex-col gap-4"
          noValidate
          onSubmit={(e) => {
            e.preventDefault()
            if (!error && mib !== null) create.mutate(mib)
          }}
        >
          <ul className="text-muted-foreground list-disc space-y-1 pl-5 text-sm">
            <li>{t('dev.create.point_sync', { source: server.name })}</li>
            <li>{t('dev.create.point_sleep')}</li>
            <li>{t('dev.create.point_ops')}</li>
          </ul>
          <div className="max-w-xl">
            <MemoryField
              id={`${id}-memory`}
              text={value}
              error={text === null ? undefined : error}
              limits={limits.data}
              ownMiB={0}
              recommendedMiB={undefined}
              onChange={setText}
            />
          </div>
          <p role="note" className="text-status-starting text-sm">
            {t('dev.create.disk_warning', { source: server.name })}
          </p>
          {create.error && !isSudoCancelled(create.error) ? (
            <p role="alert" className="text-destructive text-sm">
              {errorMessage(create.error, t)}
            </p>
          ) : null}
          <div>
            <Button type="submit" disabled={create.isPending || error !== undefined}>
              {t('dev.create.submit', { name })}
            </Button>
          </div>
        </form>
      ) : (
        <p className="text-muted-foreground text-sm">{t('dev.create.name_too_long', { name })}</p>
      )}
    </Section>
  )
}
