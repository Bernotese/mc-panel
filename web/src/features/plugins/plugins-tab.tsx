import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { isApiError } from '@/api/errors'
import type { InstalledJar, PluginKind, PluginUpdates, Server } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Section } from '@/components/section'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { usePower } from '@/features/servers/use-power'
import { errorMessage } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import {
  checkUpdates,
  deletePlugin,
  inventoryQueryOptions,
  inventoryKey,
  refreshInventory,
  refreshJobOnConflict,
  refreshPluginState,
  startJob,
  togglePlugin,
  updatePlugins,
  updatesKey,
  updatesQueryOptions,
} from './api'
import { Discover } from './discover'
import { InstallDialog } from './install-dialog'
import { InstalledList } from './installed-list'
import { JobProgress } from './job-progress'
import { jobStartErrorMessage, usePluginErrorToast } from './messages'
import { jarName, sourcesFor, updatesByFile, type InstallTarget } from './model'
import { usePluginJob } from './use-plugin-job'

type View = 'installed' | 'discover'

export interface PluginsTabProps {
  server: Server
  /** From pluginKindOf(instance.type); the detail page shows the tab only when it is not null. */
  kind: PluginKind
}

export function PluginsTab({ server, kind }: PluginsTabProps) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const queryClient = useQueryClient()
  const notifyToast = usePluginErrorToast()
  const power = usePower(server)
  const notifyError = (err: unknown) => {
    refreshJobOnConflict(queryClient, server.id, err)
    notifyToast(err)
  }
  // A failed toggle or removal (not_found, file_exists, conflict) may mean the list is stale: the disk is the truth.
  const notifyFileError = (err: unknown) => {
    void queryClient.invalidateQueries({ queryKey: inventoryKey(server.id) })
    notifyError(err)
  }
  const [view, setView] = useState<View>('installed')
  const [installing, setInstalling] = useState<InstallTarget | null>(null)
  const [removing, setRemoving] = useState<InstalledJar | null>(null)
  const [dismissed, setDismissed] = useState<string | null>(null)
  // The panel keeps the last job result (spec §6.4); one that ended before the tab was opened is not shown again.
  const [openedAt] = useState(() => Date.now())
  // A job this tab saw running stays visible when it ends, independent of the panel's clock.
  const [watched, setWatched] = useState<string | null>(null)
  const inventory = useQuery(inventoryQueryOptions(server.id))
  const updates = useQuery({ ...updatesQueryOptions(server.id), enabled: inventory.isSuccess })
  const { job, running } = usePluginJob(server.id)
  if (running && job !== null && job.id !== watched) setWatched(job.id)
  const canPower = server.permissions.includes('server.power')
  const dir = inventory.data?.dir ?? (kind === 'plugin' ? 'plugins' : 'mods')
  const updateMap = updatesByFile(updates.data?.updates)
  const updateCount = updates.data?.updates.length ?? 0

  const toggle = useMutation({
    mutationFn: ({ jar, enabled }: { jar: InstalledJar; enabled: boolean }) => togglePlugin(server.id, jar.file, enabled),
    onSuccess: (_data, { jar, enabled }) => {
      const name = jarName(jar)
      toast.success(enabled ? t('plugins.installed.enabled_done', { name }) : t('plugins.installed.disabled_done', { name }))
      // Returned: the switch stays locked until the new list is there, instead of flipping back for a moment.
      return refreshInventory(queryClient, server.id)
    },
    onError: notifyFileError,
  })

  const remove = useMutation({
    mutationFn: (jar: InstalledJar) => deletePlugin(server.id, jar.file),
    onSuccess: (_data, jar) => {
      toast.success(t('plugins.installed.removed', { name: jarName(jar) }))
      setRemoving(null)
      return refreshPluginState(queryClient, server.id)
    },
    onError: notifyFileError,
  })

  const check = useMutation({
    mutationFn: () => checkUpdates(server.id),
    onSuccess: (data) => {
      queryClient.setQueryData<PluginUpdates>(updatesKey(server.id), data)
      toast.success(data.updates.length > 0 ? t('plugins.updates.found', { count: data.updates.length }) : t('plugins.updates.none'))
    },
    onError: notifyError,
  })

  const update = useMutation({
    mutationFn: (files: string[]) => updatePlugins(server.id, files),
    onSuccess: (started) => {
      startJob(queryClient, server.id, started)
      toast.success(t('plugins.updates.started'))
    },
    onError: (err) => {
      refreshJobOnConflict(queryClient, server.id, err)
      toast.error(jobStartErrorMessage(err, t))
    },
  })

  // Spec §7.1: while a job runs, installing, updating, toggling and removing are locked (the panel answers 409 anyway).
  const busy = running || update.isPending
  const pendingFile = toggle.isPending ? (toggle.variables?.jar.file ?? null) : remove.isPending ? (remove.variables?.file ?? null) : null
  const visibleJob = job !== null && job.id !== dismissed && (job.state === 'running' || job.id === watched || job.finished_at >= openedAt) ? job : null

  function changeVersion(jar: InstalledJar) {
    if (jar.source === 'manual' || jar.project === null) return
    setInstalling({ source: jar.source, project: jar.project.id, title: jarName(jar), version: jar.version?.id ?? null })
  }

  function installedBody() {
    if (inventory.isPending) return <p className="text-muted-foreground text-sm">{t('common.loading')}</p>
    if (inventory.isError) {
      // The tab only exists for plugin/mod types; a type changed meanwhile gets a neutral hint, never the error code.
      if (isApiError(inventory.error, 'plugins_unsupported')) return <p className="text-muted-foreground text-sm">{t('plugins.unsupported')}</p>
      return (
        <p role="alert" className="text-destructive text-sm">
          {errorMessage(inventory.error, t)}
        </p>
      )
    }
    const inv = inventory.data
    return (
      <div className="space-y-3">
        {inv.source_errors.length > 0 ? (
          <p role="status" className="bevel-inset px-3 py-2 text-sm">
            {t('plugins.installed.source_errors')}
          </p>
        ) : null}
        <InstalledList
          inventory={inv}
          updates={updateMap}
          busy={busy}
          pendingFile={pendingFile}
          onToggle={(jar, enabled) => toggle.mutate({ jar, enabled })}
          onRemove={setRemoving}
          onChangeVersion={changeVersion}
          onUpdate={(jar) => update.mutate([jar.file])}
        />
        {inv.truncated ? <p className="text-muted-foreground text-sm">{t('plugins.installed.truncated', { count: inv.jars.length })}</p> : null}
        {updates.isError ? <p className="text-muted-foreground text-sm">{t('plugins.updates.failed')}</p> : null}
        {updates.data && updates.data.checked_at > 0 ? (
          <p className="text-muted-foreground text-xs">{t('plugins.updates.checked_at', { when: fmt.relative(updates.data.checked_at) })}</p>
        ) : null}
      </div>
    )
  }

  const updateActions = (
    <>
      <Button type="button" variant="outline" size="sm" disabled={check.isPending || !inventory.isSuccess} onClick={() => check.mutate()}>
        <RefreshCw aria-hidden="true" />
        {check.isPending ? t('plugins.updates.checking') : t('plugins.updates.check')}
      </Button>
      {updateCount > 0 ? (
        <Button type="button" size="sm" disabled={busy} onClick={() => update.mutate([])}>
          {t('plugins.updates.update_all', { count: updateCount })}
        </Button>
      ) : null}
    </>
  )

  return (
    <div className="flex flex-col gap-6">
      {inventory.data?.restart_needed ? (
        <div role="status" className="border-status-starting/60 bg-card flex flex-col gap-3 border p-4 sm:flex-row sm:items-center sm:justify-between">
          <div className="space-y-1">
            <p className="font-medium">{t('plugins.restart.title')}</p>
            <p className="text-muted-foreground text-sm">{t('plugins.restart.description')}</p>
          </div>
          {canPower ? (
            <Button type="button" disabled={power.isPending || running} onClick={() => power.mutate('restart')}>
              {t('plugins.restart.now')}
            </Button>
          ) : null}
        </div>
      ) : null}

      {visibleJob ? <JobProgress job={visibleJob} onDismiss={() => setDismissed(visibleJob.id)} /> : null}

      <Tabs value={view} onValueChange={(value) => setView(value as View)}>
        <TabsList aria-label={t('plugins.views')}>
          <TabsTrigger value="installed">{t('plugins.view.installed')}</TabsTrigger>
          <TabsTrigger value="discover">{t('plugins.view.discover')}</TabsTrigger>
        </TabsList>
        <TabsContent value="installed" className="pt-4">
          <Section
            title={kind === 'plugin' ? t('plugins.installed.title_plugin') : t('plugins.installed.title_mod')}
            actions={inventory.isError ? undefined : updateActions}
          >
            {installedBody()}
          </Section>
        </TabsContent>
        <TabsContent value="discover" className="pt-4">
          <Section title={t('plugins.discover.title')}>
            <Discover serverId={server.id} kind={kind} sources={inventory.data?.sources ?? sourcesFor(kind)} busy={busy} onInstall={setInstalling} />
          </Section>
        </TabsContent>
      </Tabs>

      {installing ? (
        <InstallDialog
          serverId={server.id}
          dir={dir}
          target={installing}
          busy={busy}
          onClose={() => setInstalling(null)}
          onStarted={(started) => {
            startJob(queryClient, server.id, started)
            toast.success(t('plugins.install.started', { title: installing.title }))
            setInstalling(null)
          }}
        />
      ) : null}
      {removing ? (
        <ConfirmDialog
          open
          onOpenChange={(open) => (open ? undefined : setRemoving(null))}
          title={t('plugins.installed.remove_title', { name: jarName(removing) })}
          description={t('plugins.installed.remove_description', { file: removing.file, dir })}
          confirmLabel={t('plugins.installed.remove_submit')}
          destructive
          pending={remove.isPending || busy}
          onConfirm={() => remove.mutate(removing)}
        />
      ) : null}
    </div>
  )
}
