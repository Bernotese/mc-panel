import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { Server } from '@/api/types'
import { PageHeader } from '@/components/page-header'
import { errorMessage } from '@/lib/error-message'
import { createServer, serverSettingsQueryOptions, serversKey } from './api'
import { emptyServerForm, type ServerFormResult } from './form-model'
import { ServerForm } from './server-form'

export function CreateServerPage({ onCreated }: { onCreated(server: Server): void }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const settings = useQuery(serverSettingsQueryOptions)
  // Errors (409 base_domain_missing, 400 …) are rendered by the form; a cancelled sudo dialog stays silent.
  const create = useMutation({
    mutationFn: (result: ServerFormResult) => createServer({ ...result, eula_accepted: true }),
    onSuccess: (server) => {
      toast.success(t('servers.create.created', { name: server.name }))
      void queryClient.invalidateQueries({ queryKey: serversKey })
      onCreated(server)
    },
  })

  let body: ReactNode
  if (settings.isPending) {
    body = <p className="text-muted-foreground">{t('common.loading')}</p>
  } else if (settings.isError) {
    body = (
      <p role="alert" className="text-destructive">
        {errorMessage(settings.error, t)}
      </p>
    )
  } else {
    body = (
      <>
        {settings.data.base_domain === null ? (
          <div role="status" className="mb-6 max-w-3xl rounded-md border p-4 text-sm">
            <p className="font-medium">{t('servers.create.base_domain_missing_title')}</p>
            <p className="text-muted-foreground mt-1">{t('servers.create.base_domain_missing')}</p>
          </div>
        ) : null}
        <ServerForm
          mode="create"
          initial={emptyServerForm()}
          baseDomain={settings.data.base_domain}
          curseforgeKeySet={settings.data.curseforge_api_key_set}
          submitLabel={t('servers.create.submit')}
          pending={create.isPending}
          error={create.error}
          onSubmit={(result) => create.mutate(result)}
        />
      </>
    )
  }

  return (
    <>
      <PageHeader title={t('servers.create.title')} description={t('servers.create.description')} />
      {body}
    </>
  )
}
