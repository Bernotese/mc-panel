import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { isApiError } from '@/api/errors'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { PageHeader } from '@/components/page-header'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useMe } from '@/features/auth/me'
import { BackupsTab } from '@/features/backups/backups-tab'
import { ConsoleTab } from '@/features/console/console-tab'
import { leaveTexts, type FilesBusy } from '@/features/files/busy'
import { FilesTab } from '@/features/files/files-tab'
import { isExtracting, useFileJob } from '@/features/files/use-file-job'
import { MonitoringTab } from '@/features/monitoring/monitoring-tab'
import { PlayersTab } from '@/features/players/players-tab'
import { pluginKindOf } from '@/features/plugins/model'
import { PluginsTab } from '@/features/plugins/plugins-tab'
import { errorMessage } from '@/lib/error-message'
import { serverQueryOptions } from './api'
import { DeleteServerSection } from './delete-server-section'
import { describeInstance } from './format'
import { ServerOverview } from './server-overview'
import { ServerSettingsTab } from './server-settings-tab'
import { detailPollInterval } from './status'
import { StatusBadge } from './status-badge'

type DetailTab = 'overview' | 'monitoring' | 'console' | 'players' | 'files' | 'plugins' | 'backups' | 'settings' | 'delete'

/** Keyed by id: the router reuses the component when only the :id parameter changes, but tab and console must reset. */
export function ServerDetailPage({ serverId }: { serverId: number }) {
  return <ServerDetail key={serverId} serverId={serverId} />
}

function ServerDetail({ serverId }: { serverId: number }) {
  const { t } = useTranslation()
  const me = useMe()
  const isAdmin = me?.role === 'admin'
  const query = useQuery({
    ...serverQueryOptions(serverId),
    // Fast while the server changes state; no polling at all without data or after 403/404.
    refetchInterval: (q) => detailPollInterval(q.state.data, q.state.error),
  })
  const [tab, setTab] = useState<DetailTab>('overview')
  const [filesBusy, setFilesBusy] = useState<FilesBusy | null>(null)
  const [pendingTab, setPendingTab] = useState<DetailTab | null>(null)
  // Users with server.files see the lock; everyone else gets the panel's 409 "extracting" on start.
  const filesAllowed = query.data?.permissions.includes('server.files') === true && query.data.instance !== null
  const fileJob = useFileJob(serverId, filesAllowed)
  const extracting = isExtracting(fileJob.data)

  if (query.isPending) return <p className="text-muted-foreground">{t('common.loading')}</p>
  // A failed refetch keeps the old data: after 403/404 the server is gone or hidden, so stale data must not look live.
  const gone = isApiError(query.error, 'not_found') || isApiError(query.error, 'forbidden')
  if (!query.data || gone) {
    let message = errorMessage(query.error, t)
    if (isApiError(query.error, 'not_found')) message = t('servers.detail.not_found')
    else if (isApiError(query.error, 'forbidden')) message = t('servers.detail.forbidden')
    return (
      <div className="space-y-3">
        <p role="alert" className="text-destructive">
          {message}
        </p>
        <Link to="/" className="text-primary underline underline-offset-4">
          {t('not_found.home')}
        </Link>
      </div>
    )
  }

  const server = query.data
  const header = (
    <PageHeader
      title={server.name}
      description={server.instance ? describeInstance(server.instance, t) : undefined}
      actions={<StatusBadge state={server.status.state} />}
    />
  )

  if (server.instance === null) {
    return (
      <>
        {header}
        <div className="max-w-3xl space-y-4">
          <p className="text-muted-foreground">{t('servers.detail.not_configured')}</p>
          {isAdmin ? <DeleteServerSection server={server} /> : null}
        </div>
      </>
    )
  }

  const instance = server.instance
  const canConsole = server.permissions.includes('server.console')
  const canFiles = server.permissions.includes('server.files')
  // Spec §7.1: paper/purpur → "Plugins", fabric/neoforge → "Mods"; vanilla and modpacks get no tab, so the UI never
  // meets 409 plugins_unsupported. null = no tab (type without plugins/mods or no server.plugins).
  const pluginsKind = server.permissions.includes('server.plugins') ? pluginKindOf(instance.type) : null
  // Backups spec §6.1: every instance type has backups; the tab needs server.backups.
  const canBackups = server.permissions.includes('server.backups')
  // Monitoring spec §7.2: everyone with server.view (and an instance) sees the tab.
  const available: DetailTab[] = ['overview', 'monitoring']
  if (canConsole) available.push('console', 'players')
  if (canFiles) available.push('files')
  if (pluginsKind) available.push('plugins')
  if (canBackups) available.push('backups')
  if (isAdmin) available.push('settings', 'delete')
  // A tab whose permission was revoked while it was open falls back to the overview.
  const active = available.includes(tab) ? tab : 'overview'

  const leave = leaveTexts(filesBusy ?? { unsaved: true, uploads: false }, t)

  function changeTab(next: DetailTab) {
    // Radix unmounts inactive tabs: leaving "files" would silently drop unsaved editor changes and cancel uploads.
    if (active === 'files' && next !== 'files' && filesBusy) setPendingTab(next)
    else setTab(next)
  }

  return (
    <>
      {header}
      <Tabs value={active} onValueChange={(value) => changeTab(value as DetailTab)}>
        {/* Up to nine tabs are wider than a phone: the bar scrolls, not the page. */}
        <TabsList className="max-w-full justify-start overflow-x-auto">
          <TabsTrigger value="overview">{t('servers.detail.tabs.overview')}</TabsTrigger>
          <TabsTrigger value="monitoring">{t('servers.detail.tabs.monitoring')}</TabsTrigger>
          {canConsole ? <TabsTrigger value="console">{t('servers.detail.tabs.console')}</TabsTrigger> : null}
          {canConsole ? <TabsTrigger value="players">{t('servers.detail.tabs.players')}</TabsTrigger> : null}
          {canFiles ? <TabsTrigger value="files">{t('servers.detail.tabs.files')}</TabsTrigger> : null}
          {pluginsKind ? (
            <TabsTrigger value="plugins">{pluginsKind === 'mod' ? t('servers.detail.tabs.mods') : t('servers.detail.tabs.plugins')}</TabsTrigger>
          ) : null}
          {canBackups ? <TabsTrigger value="backups">{t('servers.detail.tabs.backups')}</TabsTrigger> : null}
          {isAdmin ? <TabsTrigger value="settings">{t('servers.detail.tabs.settings')}</TabsTrigger> : null}
          {isAdmin ? <TabsTrigger value="delete">{t('servers.detail.tabs.delete')}</TabsTrigger> : null}
        </TabsList>
        <TabsContent value="overview" className="pt-4">
          <ServerOverview server={server} instance={instance} extracting={extracting} />
        </TabsContent>
        <TabsContent value="monitoring" className="pt-4">
          <MonitoringTab server={server} />
        </TabsContent>
        {canConsole ? (
          <TabsContent value="console" className="pt-4">
            <ConsoleTab server={server} />
          </TabsContent>
        ) : null}
        {canConsole ? (
          <TabsContent value="players" className="pt-4">
            <PlayersTab server={server} onOpenSettings={isAdmin ? () => setTab('settings') : undefined} />
          </TabsContent>
        ) : null}
        {canFiles ? (
          <TabsContent value="files" className="pt-4">
            <FilesTab server={server} onBusyChange={setFilesBusy} onOpenSettings={isAdmin ? () => changeTab('settings') : undefined} />
          </TabsContent>
        ) : null}
        {pluginsKind ? (
          <TabsContent value="plugins" className="pt-4">
            <PluginsTab server={server} kind={pluginsKind} />
          </TabsContent>
        ) : null}
        {canBackups ? (
          <TabsContent value="backups" className="pt-4">
            <BackupsTab server={server} />
          </TabsContent>
        ) : null}
        {isAdmin ? (
          <TabsContent value="settings" className="pt-4">
            <ServerSettingsTab server={server} instance={instance} />
          </TabsContent>
        ) : null}
        {isAdmin ? (
          <TabsContent value="delete" className="pt-4">
            <DeleteServerSection server={server} />
          </TabsContent>
        ) : null}
      </Tabs>
      <ConfirmDialog
        open={pendingTab !== null}
        onOpenChange={(open) => (open ? undefined : setPendingTab(null))}
        title={leave.title}
        description={leave.description}
        confirmLabel={leave.confirm}
        destructive
        onConfirm={() => {
          setFilesBusy(null)
          if (pendingTab) setTab(pendingTab)
          setPendingTab(null)
        }}
      />
    </>
  )
}
