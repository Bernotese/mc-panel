import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { isApiError } from '@/api/errors'
import type { InstanceConfig, Server, ServerState } from '@/api/types'
import { useMe } from '@/features/auth/me'
import { DevSettings } from '@/features/dev/dev-settings-section'
import { useReportUnsaved } from '@/lib/unsaved-changes'
import { useRemoteDraft } from '@/lib/use-remote-draft'
import { memoryLimitsKey, serverKey, serversKey, updateServer } from './api'
import { DeleteServerSection } from './delete-server-section'
import { serverFormChanges } from './draft-changes'
import { DraftConflict } from './draft-conflict'
import { formFromInstance, rebaseServerForm, sameFormValue, type ServerFormResult } from './form-model'
import { ServerForm } from './server-form'
import { UnsavedGuard } from './unsaved-guard'
import { WebRoutesSection } from './web-routes-section'

const RUNNING_STATES: ReadonlySet<ServerState> = new Set<ServerState>(['running', 'starting', 'restarting'])

/**
 * The settings area: the instance form (if the server has one), the web interfaces (admins) and the danger zone at the
 * end. Unsaved changes survive polling and changes elsewhere, and leaving asks first (issue #154).
 */
export function ServerSettingsTab({ server, instance }: { server: Server; instance: InstanceConfig | null }) {
  const { t } = useTranslation()
  const isAdmin = useMe()?.role === 'admin'

  if (server.dev) {
    // A dev takes its spec from its source at every sync: only its own settings, the memory and deleting it remain.
    return (
      <UnsavedGuard>
        <div className="flex flex-col gap-6">
          <DevSettings server={server} />
          <DeleteServerSection server={server} />
        </div>
      </UnsavedGuard>
    )
  }

  return (
    <UnsavedGuard>
      <div className="@container/server-form flex flex-col gap-6">
        {instance ? <InstanceSettings server={server} instance={instance} /> : <p className="bevel-inset px-4 py-3 text-sm">{t('servers.detail.not_configured')}</p>}
        {instance && isAdmin ? <WebRoutesSection server={server} /> : null}
        <DeleteServerSection server={server} />
      </div>
    </UnsavedGuard>
  )
}

function InstanceSettings({ server, instance }: { server: Server; instance: InstanceConfig }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  // The draft keeps its base: the panel applies the save only to that revision (expected_revision), so a change by
  // someone else in the meantime is never silently reverted (409 server_modified). A clean form follows every change.
  const d = useRemoteDraft(instance, { toDraft: (i) => formFromInstance(server.name, i), merge: rebaseServerForm, same: sameFormValue })
  useReportUnsaved(d.dirty)
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
        expected_revision: d.base.revision,
      }),
    onSuccess: (updated) => {
      queryClient.setQueryData(serverKey(server.id), updated)
      if (updated.instance) d.saved(updated.instance)
      void queryClient.invalidateQueries({ queryKey: serversKey, exact: true })
      void queryClient.invalidateQueries({ queryKey: memoryLimitsKey })
      toast.success(updated.status.restart_required ? t('servers.settings.saved_restart') : t('servers.settings.saved'))
    },
    // The newer stored value is needed to compare with and to rebase onto.
    onError: (err) => {
      if (isApiError(err, 'server_modified')) void queryClient.invalidateQueries({ queryKey: serverKey(server.id) })
    },
  })
  const rejected = isApiError(save.error, 'server_modified')
  const resolve = (action: () => void) => {
    action()
    save.reset()
  }

  return (
    <>
      {d.conflict || rejected ? (
        <DraftConflict
          reason={rejected ? 'rejected' : 'remote'}
          changes={serverFormChanges(d.baseDraft, d.draft, d.remoteDraft, t)}
          // After a 409 the stored value is newer than the base; until it has loaded there is nothing to rebase onto.
          waiting={rejected && instance.revision === d.base.revision}
          onReload={() => resolve(d.reload)}
          onKeepMine={() => resolve(d.keepMine)}
        />
      ) : null}
      <ServerForm
        key={d.generation}
        mode="edit"
        initial={d.start}
        baseline={d.baseDraft}
        onDraftChange={(value) => {
          d.setDraft(value)
          // Back to the stored values after a refusal: nothing left to resolve, and the clean form follows the server again.
          if (rejected && sameFormValue(value, d.baseDraft)) save.reset()
        }}
        submitLabel={t('servers.settings.save')}
        pending={save.isPending}
        error={rejected ? null : save.error}
        notice={RUNNING_STATES.has(server.status.state) ? t('servers.settings.running_notice') : undefined}
        onSubmit={(result) => save.mutate(result)}
      />
    </>
  )
}
