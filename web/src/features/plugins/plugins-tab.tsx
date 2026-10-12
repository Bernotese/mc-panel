import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { isApiError } from '@/api/errors'
import type { InstalledJar, PluginKind, Server } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { SectionCard } from '@/features/profile/section-card'
import { usePower } from '@/features/servers/use-power'
import { errorMessage } from '@/lib/error-message'
import { deletePlugin, inventoryQueryOptions, refreshPluginState, startJob, togglePlugin } from './api'
import { Discover } from './discover'
import { InstallDialog } from './install-dialog'
import { InstalledList } from './installed-list'
import { usePluginErrorToast } from './messages'
import { jarName, sourcesFor, type InstallTarget } from './model'

type View = 'installed' | 'discover'

export interface PluginsTabProps {
  server: Server
  /** From pluginKindOf(instance.type); the detail page shows the tab only when it is not null. */
  kind: PluginKind
}

export function PluginsTab({ server, kind }: PluginsTabProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const notifyError = usePluginErrorToast()
  const power = usePower(server)
  const [view, setView] = useState<View>('installed')
  const [installing, setInstalling] = useState<InstallTarget | null>(null)
  const [removing, setRemoving] = useState<InstalledJar | null>(null)
  const inventory = useQuery(inventoryQueryOptions(server.id))
  const canPower = server.permissions.includes('server.power')
  const dir = inventory.data?.dir ?? (kind === 'plugin' ? 'plugins' : 'mods')

  const toggle = useMutation({
    mutationFn: ({ jar, enabled }: { jar: InstalledJar; enabled: boolean }) => togglePlugin(server.id, jar.file, enabled),
    onSuccess: (_data, { jar, enabled }) => {
      const name = jarName(jar)
      toast.success(enabled ? t('plugins.installed.enabled_done', { name }) : t('plugins.installed.disabled_done', { name }))
      void refreshPluginState(queryClient, server.id)
    },
    onError: notifyError,
  })

  const remove = useMutation({
    mutationFn: (jar: InstalledJar) => deletePlugin(server.id, jar.file),
    onSuccess: (_data, jar) => {
      toast.success(t('plugins.installed.removed', { name: jarName(jar) }))
      setRemoving(null)
      void refreshPluginState(queryClient, server.id)
    },
    onError: notifyError,
  })

  const pendingFile = toggle.isPending ? (toggle.variables?.jar.file ?? null) : remove.isPending ? (remove.variables?.file ?? null) : null

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
          <p role="status" className="rounded-md border p-3 text-sm">
            {t('plugins.installed.source_errors')}
          </p>
        ) : null}
        <InstalledList
          inventory={inv}
          pendingFile={pendingFile}
          onToggle={(jar, enabled) => toggle.mutate({ jar, enabled })}
          onRemove={setRemoving}
          onChangeVersion={changeVersion}
        />
        {inv.truncated ? <p className="text-muted-foreground text-sm">{t('plugins.installed.truncated', { count: inv.jars.length })}</p> : null}
      </div>
    )
  }

  return (
    <div className="space-y-4">
      {inventory.data?.restart_needed ? (
        <div role="status" className="flex flex-col gap-3 rounded-md border p-4 sm:flex-row sm:items-center sm:justify-between">
          <div className="space-y-1">
            <p className="font-medium">{t('plugins.restart.title')}</p>
            <p className="text-muted-foreground text-sm">{t('plugins.restart.description')}</p>
          </div>
          {canPower ? (
            <Button type="button" disabled={power.isPending} onClick={() => power.mutate('restart')}>
              {t('plugins.restart.now')}
            </Button>
          ) : null}
        </div>
      ) : null}

      <Tabs value={view} onValueChange={(value) => setView(value as View)}>
        <TabsList aria-label={t('plugins.views')}>
          <TabsTrigger value="installed">{t('plugins.view.installed')}</TabsTrigger>
          <TabsTrigger value="discover">{t('plugins.view.discover')}</TabsTrigger>
        </TabsList>
        <TabsContent value="installed" className="pt-4">
          <SectionCard title={kind === 'plugin' ? t('plugins.installed.title_plugin') : t('plugins.installed.title_mod')}>{installedBody()}</SectionCard>
        </TabsContent>
        <TabsContent value="discover" className="pt-4">
          <SectionCard title={t('plugins.discover.title')}>
            <Discover serverId={server.id} sources={inventory.data?.sources ?? sourcesFor(kind)} busy={false} onInstall={setInstalling} />
          </SectionCard>
        </TabsContent>
      </Tabs>

      {installing ? (
        <InstallDialog
          serverId={server.id}
          dir={dir}
          target={installing}
          busy={false}
          onClose={() => setInstalling(null)}
          onStarted={(job) => {
            startJob(queryClient, server.id, job)
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
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing)}
        />
      ) : null}
    </div>
  )
}
