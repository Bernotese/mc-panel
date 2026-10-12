import { useMutation, useQuery } from '@tanstack/react-query'
import { ExternalLink } from 'lucide-react'
import { useId, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import type { PluginJob, PluginPlanEntry } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { formatBytes } from '@/features/files/format'
import { useFormat } from '@/lib/format'
import { installPlugin, resolveQueryOptions, versionsQueryOptions } from './api'
import { installErrorMessage, pluginErrorMessage } from './messages'
import { canInstall, defaultVersion, externalHref, type InstallTarget } from './model'

export interface InstallDialogProps {
  serverId: number
  /** "plugins" or "mods", for the hint on externally available versions. */
  dir: string
  target: InstallTarget
  /** A job runs on this server: installing is locked (spec §7.1). */
  busy: boolean
  onClose(): void
  /** Called with the 202 job; the caller starts the polling and closes the dialog. */
  onStarted(job: PluginJob): void
}

function PreviewSection({ title, children }: { title: string; children: ReactNode }) {
  const id = useId()
  return (
    <section aria-labelledby={id} className="space-y-1">
      <h3 id={id} className="text-sm font-medium">
        {title}
      </h3>
      <ul className="space-y-1.5 text-sm">{children}</ul>
    </section>
  )
}

/** Spec §7.1: version choice (default newest release) and the preview from resolve; the job re-resolves anyway (§6.4). */
export function InstallDialog({ serverId, dir, target, busy, onClose, onStarted }: InstallDialogProps) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const group = useId()
  const [chosen, setChosen] = useState<string | null>(null)
  const versions = useQuery(versionsQueryOptions(serverId, target.source, target.project))
  const list = versions.data?.versions ?? []
  const selected = chosen ?? defaultVersion(list, target.version)
  const resolve = useQuery({ ...resolveQueryOptions(serverId, target.source, target.project, selected ?? ''), enabled: selected !== null })
  const install = useMutation({
    mutationFn: (version: string) => installPlugin(serverId, { source: target.source, project: target.project, version }),
    onSuccess: (job) => onStarted(job),
  })
  const plan = resolve.data
  const blocked = busy || selected === null || !plan || !canInstall(plan) || install.isPending

  function entry(e: PluginPlanEntry) {
    return (
      <li key={`${e.source}:${e.project_id}`} className="min-w-0">
        <span className="font-medium">{e.title}</span> <span className="text-muted-foreground">{e.version_name}</span>
        {e.channel !== 'release' ? (
          <Badge variant="outline" className="ml-1">
            {t(`plugins.channel.${e.channel}`)}
          </Badge>
        ) : null}
        <span className="text-muted-foreground block font-mono text-xs break-all">
          {e.filename} · {formatBytes(e.size, fmt.locale)}
        </span>
        {e.required_by !== null ? <span className="text-muted-foreground block text-xs">{t('plugins.install.required_by', { title: e.required_by })}</span> : null}
      </li>
    )
  }

  function versionList() {
    if (versions.isPending) return <p className="text-muted-foreground text-sm">{t('plugins.install.versions_loading')}</p>
    if (versions.isError) {
      return (
        <div role="alert" className="space-y-2 text-sm">
          <p className="text-destructive">{pluginErrorMessage(versions.error, t)}</p>
          <Button type="button" variant="outline" size="sm" disabled={versions.isFetching} onClick={() => void versions.refetch()}>
            {t('common.retry')}
          </Button>
        </div>
      )
    }
    if (list.length === 0) return <p className="text-muted-foreground text-sm">{t('plugins.install.no_versions')}</p>
    const external = list.some((v) => !v.downloadable)
    return (
      <fieldset className="space-y-2">
        <legend className="text-sm font-medium">{t('plugins.install.versions')}</legend>
        <ul className="max-h-60 space-y-2 overflow-y-auto rounded-md border p-2">
          {list.map((v) => {
            const href = v.downloadable ? null : externalHref(v.external_url)
            const label = v.channel === 'release' ? v.name : `${v.name} (${t(`plugins.channel.${v.channel}`)})`
            return (
              <li key={v.id} className={v.downloadable ? 'text-sm' : 'text-muted-foreground text-sm'}>
                <label className="flex items-start gap-2">
                  <input
                    type="radio"
                    name={group}
                    value={v.id}
                    className="accent-primary mt-0.5 size-4"
                    aria-label={label}
                    checked={selected === v.id}
                    disabled={!v.downloadable}
                    onChange={() => {
                      install.reset()
                      setChosen(v.id)
                    }}
                  />
                  <span className="min-w-0 flex-1">
                    <span className="font-medium break-all">{v.name}</span>
                    {v.channel !== 'release' ? (
                      <Badge variant="outline" className="ml-2">
                        {t(`plugins.channel.${v.channel}`)}
                      </Badge>
                    ) : null}
                    <span className="block text-xs">{t('plugins.install.published', { date: fmt.date(v.published_at) })}</span>
                    {!v.downloadable ? <span className="block text-xs">{t('plugins.install.external_only')}</span> : null}
                  </span>
                </label>
                {href ? (
                  <a href={href} target="_blank" rel="noopener noreferrer" className="text-primary ml-6 inline-flex items-center gap-1 text-xs underline-offset-4 hover:underline">
                    {t('plugins.install.external_link')}
                    <ExternalLink aria-hidden="true" className="size-3" />
                  </a>
                ) : null}
              </li>
            )
          })}
        </ul>
        {list.every((v) => !v.downloadable) ? <p className="text-sm">{t('plugins.install.none_downloadable')}</p> : null}
        {external ? <p className="text-muted-foreground text-xs">{t('plugins.install.external_hint', { dir })}</p> : null}
      </fieldset>
    )
  }

  function preview() {
    if (selected === null) return null
    if (resolve.isError) {
      return (
        <p role="alert" className="text-destructive text-sm">
          {pluginErrorMessage(resolve.error, t)}
        </p>
      )
    }
    if (!plan) return <p className="text-muted-foreground text-sm">{t('plugins.install.preview_loading')}</p>
    const main = plan.install.filter((e) => e.required_by === null)
    const deps = plan.install.filter((e) => e.required_by !== null)
    return (
      <div className="space-y-3">
        {main.length > 0 ? <PreviewSection title={t('plugins.install.will_install')}>{main.map(entry)}</PreviewSection> : null}
        {deps.length > 0 ? <PreviewSection title={t('plugins.install.will_install_dep')}>{deps.map(entry)}</PreviewSection> : null}
        {plan.already.length > 0 ? (
          <PreviewSection title={t('plugins.install.already')}>
            {plan.already.map((a) => (
              <li key={`${a.source}:${a.project_id}`}>
                <span className="font-medium">{a.title}</span>
                <span className="text-muted-foreground block font-mono text-xs break-all">{a.file}</span>
              </li>
            ))}
          </PreviewSection>
        ) : null}
        {plan.external.length > 0 ? (
          <PreviewSection title={t('plugins.install.external')}>
            {plan.external.map((x) => {
              const href = externalHref(x.url)
              return (
                <li key={`${x.name}:${x.url}`}>
                  {href ? (
                    <a href={href} target="_blank" rel="noopener noreferrer" className="text-primary inline-flex items-center gap-1 underline-offset-4 hover:underline">
                      {x.name}
                      <ExternalLink aria-hidden="true" className="size-3.5" />
                    </a>
                  ) : (
                    <span>{x.name}</span>
                  )}
                </li>
              )
            })}
          </PreviewSection>
        ) : null}
        {plan.incompatible.length > 0 ? (
          <>
            <PreviewSection title={t('plugins.install.incompatible')}>
              {plan.incompatible.map((x) => (
                <li key={`${x.title}:${x.reason}`}>{t('plugins.install.incompatible_entry', { title: x.title, reason: t(`plugins.incompatible_reason.${x.reason}`) })}</li>
              ))}
            </PreviewSection>
            <p role="alert" className="text-destructive text-sm">
              {t('plugins.install.incompatible_hint')}
            </p>
          </>
        ) : null}
        {plan.incompatible.length === 0 && plan.install.length === 0 ? <p className="text-muted-foreground text-sm">{t('plugins.install.nothing')}</p> : null}
      </div>
    )
  }

  return (
    <Dialog open onOpenChange={(open) => (open ? undefined : onClose())}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t('plugins.install.title', { title: target.title })}</DialogTitle>
          <DialogDescription>{t('plugins.install.description')}</DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          {versionList()}
          {preview()}
          {install.isError ? (
            <p role="alert" className="text-destructive text-sm">
              {installErrorMessage(install.error, t)}
            </p>
          ) : null}
        </div>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button type="button" disabled={blocked} onClick={() => selected !== null && install.mutate(selected)}>
            {t('plugins.install.submit')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
