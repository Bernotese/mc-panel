import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { isApiError } from '@/api/errors'
import type { InstanceConfig, Server, ServerState } from '@/api/types'
import { Button } from '@/components/ui/button'
import { useMe } from '@/features/auth/me'
import { DevSettings } from '@/features/dev/dev-settings-section'
import { memoryLimitsKey, serverKey, serversKey, updateServer } from './api'
import { DeleteServerSection } from './delete-server-section'
import { formFromInstance, type ServerFormResult } from './form-model'
import { ServerForm } from './server-form'
import { WebRoutesSection } from './web-routes-section'

const RUNNING_STATES: ReadonlySet<ServerState> = new Set<ServerState>(['running', 'starting', 'restarting'])

/** The settings area: the instance form (if the server has one), the web interfaces (admins) and the danger zone at the end. */
export function ServerSettingsTab({ server, instance }: { server: Server; instance: InstanceConfig | null }) {
  const { t } = useTranslation()
  const isAdmin = useMe()?.role === 'admin'
  // Bumped by "reload" after a 409 server_modified: re-initialises the form even if only the revision changed.
  const [reloads, setReloads] = useState(0)

  if (server.dev) {
    // A dev takes its spec from its source at every sync: only its own settings, the memory and deleting it remain.
    return (
      <div className="flex flex-col gap-6">
        <DevSettings server={server} />
        <DeleteServerSection server={server} />
      </div>
    )
  }

  return (
    <div className="@container/server-form flex flex-col gap-6">
      {instance ? (
        // A changed instance (after saving or by another admin) re-initialises the form via the key. The revision is left
        // out: a bump alone (base domain or CurseForge key changed) keeps unsaved edits, and saving them reports the conflict.
        <InstanceSettings key={`${instanceKey(instance)}#${reloads}`} server={server} instance={instance} onReloaded={() => setReloads((n) => n + 1)} />
      ) : (
        <p className="bevel-inset px-4 py-3 text-sm">{t('servers.detail.not_configured')}</p>
      )}
      {/* Re-initialised when the stored routes change (after saving or by another admin). */}
      {instance && isAdmin ? <WebRoutesSection key={JSON.stringify(server.web_routes ?? [])} server={server} /> : null}
      <DeleteServerSection server={server} />
    </div>
  )
}

/** The instance as the form sees it: everything but the revision. */
function instanceKey(instance: InstanceConfig): string {
  return JSON.stringify({ ...instance, revision: undefined }) // JSON leaves undefined out
}

function InstanceSettings({ server, instance, onReloaded }: { server: Server; instance: InstanceConfig; onReloaded(): void }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  // The revision the form was loaded with: the panel applies the save only to it (expected_revision), so a change by
  // someone else in the meantime is never silently reverted (409 server_modified).
  const [revision] = useState(instance.revision)
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
        expected_revision: revision,
      }),
    onSuccess: (updated) => {
      queryClient.setQueryData(serverKey(server.id), updated)
      void queryClient.invalidateQueries({ queryKey: serversKey, exact: true })
      void queryClient.invalidateQueries({ queryKey: memoryLimitsKey })
      toast.success(updated.status.restart_required ? t('servers.settings.saved_restart') : t('servers.settings.saved'))
    },
  })
  const modified = isApiError(save.error, 'server_modified')
  const reload = useMutation({
    mutationFn: () => queryClient.invalidateQueries({ queryKey: serverKey(server.id) }),
    onSettled: onReloaded,
  })

  return (
    <>
      <ServerForm
        mode="edit"
        initial={formFromInstance(server.name, instance)}
        submitLabel={t('servers.settings.save')}
        pending={save.isPending}
        error={modified ? null : save.error}
        notice={RUNNING_STATES.has(server.status.state) ? t('servers.settings.running_notice') : undefined}
        onSubmit={(result) => save.mutate(result)}
      />
      {modified ? (
        <div role="alert" className="bevel-inset flex flex-wrap items-center gap-3 px-4 py-3 text-sm">
          <p className="min-w-0 flex-1">{t('servers.settings.modified')}</p>
          <Button type="button" variant="outline" disabled={reload.isPending} onClick={() => reload.mutate()}>
            {t('servers.settings.reload')}
          </Button>
        </div>
      ) : null}
    </>
  )
}
