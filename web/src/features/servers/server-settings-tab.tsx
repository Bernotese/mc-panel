import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { InstanceConfig, Server, ServerState } from '@/api/types'
import { serverKey, serversKey, updateServer } from './api'
import { DeleteServerSection } from './delete-server-section'
import { formFromInstance, type ServerFormResult } from './form-model'
import { ServerForm } from './server-form'

const RUNNING_STATES: ReadonlySet<ServerState> = new Set<ServerState>(['running', 'starting', 'restarting'])

/** The settings area: the instance form (if the server has one) and the danger zone at the end. */
export function ServerSettingsTab({ server, instance }: { server: Server; instance: InstanceConfig | null }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  // Errors are rendered by the form; a cancelled sudo dialog stays silent.
  const save = useMutation({
    // java is always sent: an omitted java would stay stale after a version change (V4: omitted = unchanged).
    mutationFn: (result: ServerFormResult) =>
      updateServer(server.id, {
        version: result.version,
        loader_version: result.loader_version,
        modpack: result.modpack,
        java: result.java,
        memory_mib: result.memory_mib,
        properties: result.properties,
      }),
    onSuccess: (updated) => {
      queryClient.setQueryData(serverKey(server.id), updated)
      void queryClient.invalidateQueries({ queryKey: serversKey, exact: true })
      toast.success(updated.status.restart_required ? t('servers.settings.saved_restart') : t('servers.settings.saved'))
    },
  })

  return (
    <div className="flex max-w-3xl flex-col gap-6">
      {instance ? (
        // A changed instance (after saving or by another admin) re-initialises the form via the key.
        <ServerForm
          key={JSON.stringify(instance)}
          mode="edit"
          initial={formFromInstance(server.name, instance)}
          submitLabel={t('servers.settings.save')}
          pending={save.isPending}
          error={save.error}
          notice={RUNNING_STATES.has(server.status.state) ? t('servers.settings.running_notice') : undefined}
          onSubmit={(result) => save.mutate(result)}
        />
      ) : (
        <p className="bevel-inset px-4 py-3 text-sm">{t('servers.detail.not_configured')}</p>
      )}
      <DeleteServerSection server={server} />
    </div>
  )
}
