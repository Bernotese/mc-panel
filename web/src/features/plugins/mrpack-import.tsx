import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowRight, PackageOpen } from 'lucide-react'
import { useId, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import type { TFunction } from 'i18next'
import { toast } from 'sonner'
import { isApiError, isSudoCancelled } from '@/api/errors'
import type { MrpackPlan, MrpackPlanEntry, PluginJob, Server } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { useMe } from '@/features/auth/me'
import { catalogQueryOptions, serverKey, serversKey, updateServer } from '@/features/servers/api'
import { javaFor } from '@/features/servers/java'
import { errorMessage } from '@/lib/error-message'
import { importMrpack, mrpackPreviewQueryOptions, refreshJobOnConflict } from './api'
import { jobStartErrorMessage, pluginErrorMessage } from './messages'
import {
  entryTitle,
  excludeList,
  groupPlan,
  importCount,
  jsonBytes,
  loaderMismatch,
  MAX_BODY_BYTES,
  minecraftMismatch,
  toRequestIndex,
  versionSwitch,
} from './mrpack-model'
import { MrpackReadError, readMrpackIndex } from './mrpack-read'
import { PluginIcon } from './plugin-icon'

export interface MrpackImportProps {
  server: Server
  /** A job runs on this server: importing is locked like installing (spec §7.1 of TP3c). */
  busy: boolean
  onStarted(job: PluginJob): void
}

/** Spec §5: "Modpack importieren (.mrpack)" in the head of the mods area (fabric/neoforge only, decided by the caller). */
export function MrpackImport({ server, busy, onStarted }: MrpackImportProps) {
  const { t } = useTranslation()
  const input = useRef<HTMLInputElement>(null)
  const [pack, setPack] = useState<{ file: File; token: number } | null>(null)
  const tokens = useRef(0)
  return (
    <>
      <Button type="button" variant="outline" size="sm" disabled={busy} onClick={() => input.current?.click()}>
        <PackageOpen aria-hidden="true" />
        {t('plugins.mrpack.button')}
      </Button>
      <input
        ref={input}
        type="file"
        accept=".mrpack"
        className="hidden"
        aria-label={t('plugins.mrpack.file_label')}
        onChange={(e) => {
          const file = e.target.files?.[0]
          e.target.value = '' // the same file can be chosen again
          if (file) setPack({ file, token: ++tokens.current })
        }}
      />
      {pack ? (
        <MrpackDialog
          key={pack.token}
          server={server}
          file={pack.file}
          token={pack.token}
          busy={busy}
          onClose={() => setPack(null)}
          onStarted={(job) => {
            setPack(null)
            onStarted(job)
          }}
        />
      ) : null}
    </>
  )
}

function readErrorMessage(err: unknown, t: TFunction): string {
  if (err instanceof MrpackReadError) return t(`plugins.mrpack.read_error.${err.code}`)
  return t('plugins.mrpack.read_error.not_zip')
}

function Group({ title, count, children, collapsed = false }: { title: string; count: number; children: ReactNode; collapsed?: boolean }) {
  const id = useId()
  if (count === 0) return null
  const heading = (
    <span id={id} className="text-sm font-medium">
      {title} <span className="text-muted-foreground tabular-nums">({count})</span>
    </span>
  )
  const list = <ul className="mt-1.5 space-y-1.5 text-sm">{children}</ul>
  if (collapsed) {
    return (
      <section aria-labelledby={id}>
        <details>
          <summary className="cursor-pointer">{heading}</summary>
          {list}
        </details>
      </section>
    )
  }
  return (
    <section aria-labelledby={id}>
      <h3>{heading}</h3>
      {list}
    </section>
  )
}

interface MrpackDialogProps {
  server: Server
  file: File
  token: number
  busy: boolean
  onClose(): void
  onStarted(job: PluginJob): void
}

function MrpackDialog({ server, file, token, busy, onClose, onStarted }: MrpackDialogProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const isAdmin = useMe()?.role === 'admin'
  const [deselected, setDeselected] = useState<ReadonlySet<string>>(new Set())
  const [accepted, setAccepted] = useState(false)
  // The file is read locally (spec §2); a query keeps the result for the dialog's lifetime only.
  const read = useQuery({
    queryKey: ['plugin-mrpack-file', server.id, token],
    queryFn: async ({ signal }) => toRequestIndex(await readMrpackIndex(file, signal)),
    staleTime: Infinity,
    gcTime: 0,
    retry: false,
  })
  const index = read.data
  const preview = useQuery(mrpackPreviewQueryOptions(server.id, token, index))
  const plan = preview.data

  const switchVersion = useMutation({
    mutationFn: async (p: MrpackPlan) => {
      const instance = server.instance
      if (!instance) throw new Error('no instance')
      const catalog = await queryClient.fetchQuery(catalogQueryOptions(instance.type)).catch(() => undefined)
      const java = catalog?.versions.find((v) => v.id === p.game_version.pack)?.java ?? javaFor(instance.type, p.game_version.pack)
      return updateServer(server.id, versionSwitch(p, java))
    },
    onSuccess: (updated) => {
      queryClient.setQueryData(serverKey(server.id), updated)
      void queryClient.invalidateQueries({ queryKey: serversKey, exact: true })
      toast.success(t('plugins.mrpack.switched'))
      setAccepted(false)
      void preview.refetch()
    },
  })

  const start = useMutation({
    mutationFn: (p: MrpackPlan) => {
      if (!index) throw new Error('no index')
      const body = { index, exclude: excludeList(p, deselected), accept_mismatch: p.version_mismatch && accepted }
      if (jsonBytes(body) > MAX_BODY_BYTES) throw new MrpackReadError('too_large_to_send', 'the request exceeds 1 MiB')
      return importMrpack(server.id, body)
    },
    onSuccess: (job) => {
      toast.success(t('plugins.mrpack.started'))
      onStarted(job)
    },
    onError: (err) => {
      refreshJobOnConflict(queryClient, server.id, err)
      // The server's version changed meanwhile, or the disk did: show the new plan instead of a stale one.
      if (isApiError(err, 'mrpack_version_mismatch') || isApiError(err, 'conflict')) void preview.refetch()
    },
  })

  function toggle(sha512: string, on: boolean) {
    const next = new Set(deselected)
    if (on) next.delete(sha512)
    else next.add(sha512)
    setDeselected(next)
  }

  /** pad: another entry of the group has a checkbox, so this one keeps the column free to stay aligned. */
  function entryLine(e: MrpackPlanEntry, extra?: ReactNode, pad = false) {
    const title = entryTitle(e)
    const selectable = e.optional && (e.action === 'install' || e.action === 'update')
    return (
      <li key={e.sha512} className="flex min-w-0 items-start gap-2">
        {selectable ? (
          <input
            type="checkbox"
            className="accent-primary mt-1 size-4 shrink-0"
            aria-label={t('plugins.mrpack.include_label', { title })}
            checked={!deselected.has(e.sha512)}
            onChange={(ev) => toggle(e.sha512, ev.target.checked)}
          />
        ) : pad ? (
          <span aria-hidden="true" className="size-4 shrink-0" />
        ) : null}
        <PluginIcon url={e.project?.icon_url} />
        <div className="min-w-0 flex-1">
          <p className="break-words">
            <span className="font-medium">{title}</span> {e.version ? <span className="text-muted-foreground">{e.version.name}</span> : null}
            {e.optional ? (
              <Badge variant="outline" className="ml-1">
                {t('plugins.mrpack.optional')}
              </Badge>
            ) : null}
          </p>
          {extra ?? (title !== e.file ? <p className="text-muted-foreground font-mono text-xs break-all">{e.file}</p> : null)}
        </div>
      </li>
    )
  }

  function skipReason(e: MrpackPlanEntry) {
    const reason = e.reason ?? 'incompatible'
    return (
      <>
        {entryTitle(e) !== e.file ? <p className="text-muted-foreground font-mono text-xs break-all">{e.file}</p> : null}
        <p className="text-xs">{t(`plugins.mrpack.reason.${reason}`)}</p>
      </>
    )
  }

  function mismatchBox(p: MrpackPlan) {
    const loaderName = t(`plugins.mrpack.loader.${p.loader.name}`)
    const target = [
      minecraftMismatch(p) ? t('plugins.mrpack.minecraft', { version: p.game_version.pack }) : null,
      loaderMismatch(p) ? `${loaderName} ${p.loader.pack}` : null,
    ]
      .filter(Boolean)
      .join(' · ')
    return (
      <div role="alert" className="border-status-starting/60 space-y-3 border p-3 text-sm">
        <p className="font-medium">{t('plugins.mrpack.mismatch_title')}</p>
        <p>
          {t('plugins.mrpack.mismatch_pack', {
            minecraft: p.game_version.pack,
            loader: loaderName,
            loader_version: p.loader.pack,
          })}
          <br />
          {t('plugins.mrpack.mismatch_server', {
            minecraft: p.game_version.server,
            loader: loaderName,
            loader_version: p.loader.server ?? t('plugins.mrpack.loader_newest'),
          })}
        </p>
        {isAdmin ? (
          <div className="space-y-1">
            <Button type="button" size="sm" variant="outline" disabled={switchVersion.isPending || busy} onClick={() => switchVersion.mutate(p)}>
              {t('plugins.mrpack.switch', { target })}
            </Button>
            {switchVersion.isError && !isSudoCancelled(switchVersion.error) ? (
              <p className="text-destructive">{errorMessage(switchVersion.error, t)}</p>
            ) : null}
          </div>
        ) : null}
        <label className="flex items-start gap-2">
          <input type="checkbox" className="accent-primary mt-0.5 size-4" checked={accepted} onChange={(e) => setAccepted(e.target.checked)} />
          <span>{t('plugins.mrpack.accept_mismatch')}</span>
        </label>
      </div>
    )
  }

  function body() {
    if (read.isPending) return <p className="text-muted-foreground text-sm">{t('plugins.mrpack.reading')}</p>
    if (read.isError) {
      return (
        <p role="alert" className="text-destructive text-sm">
          {readErrorMessage(read.error, t)}
        </p>
      )
    }
    if (preview.isError) {
      return (
        <div role="alert" className="space-y-2 text-sm">
          <p className="text-destructive">{pluginErrorMessage(preview.error, t)}</p>
          {isApiError(preview.error, 'mrpack_incompatible') || isApiError(preview.error, 'validation_failed') ? null : (
            <Button type="button" variant="outline" size="sm" disabled={preview.isFetching} onClick={() => void preview.refetch()}>
              {t('common.retry')}
            </Button>
          )}
        </div>
      )
    }
    if (!plan) return <p className="text-muted-foreground text-sm">{t('plugins.mrpack.preview_loading')}</p>
    const groups = groupPlan(plan)
    return (
      <div className="space-y-4">
        {plan.version_mismatch ? mismatchBox(plan) : null}
        <Group title={t('plugins.mrpack.group.install')} count={groups.install.length}>
          {groups.install.map((e) => entryLine(e, undefined, groups.install.some((x) => x.optional)))}
        </Group>
        <Group title={t('plugins.mrpack.group.update')} count={groups.update.length}>
          {groups.update.map((e) =>
            entryLine(
              e,
              <p className="text-muted-foreground flex flex-wrap items-center gap-1 font-mono text-xs break-all">
                <span>{e.replaces}</span>
                <ArrowRight aria-label={t('plugins.mrpack.replaced_by')} className="size-3" />
                <span>{e.file}</span>
              </p>,
              groups.update.some((x) => x.optional),
            ),
          )}
        </Group>
        <Group title={t('plugins.mrpack.group.unchanged')} count={groups.unchanged.length} collapsed>
          {groups.unchanged.map((e) => entryLine(e))}
        </Group>
        <Group title={t('plugins.mrpack.group.skip')} count={groups.skip.length}>
          {groups.skip.map((e) => entryLine(e, skipReason(e)))}
        </Group>
        <Group title={t('plugins.mrpack.group.kept')} count={plan.kept.length} collapsed>
          {plan.kept.map((f) => (
            <li key={f} className="font-mono text-xs break-all">
              {f}
            </li>
          ))}
        </Group>
        {plan.ignored_files > 0 ? <p className="text-muted-foreground text-xs">{t('plugins.mrpack.ignored', { count: plan.ignored_files })}</p> : null}
        {importCount(plan, deselected) === 0 ? <p className="text-muted-foreground text-sm">{t('plugins.mrpack.nothing')}</p> : null}
        {start.isError ? (
          <p role="alert" className="text-destructive text-sm">
            {start.error instanceof MrpackReadError ? readErrorMessage(start.error, t) : jobStartErrorMessage(start.error, t)}
          </p>
        ) : null}
      </div>
    )
  }

  const count = plan ? importCount(plan, deselected) : 0
  const blocked = !plan || busy || count === 0 || start.isPending || switchVersion.isPending || preview.isFetching || (plan.version_mismatch && !accepted)
  return (
    <Dialog open onOpenChange={(open) => (open ? undefined : onClose())}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{t('plugins.mrpack.title')}</DialogTitle>
          <DialogDescription>
            {plan
              ? t('plugins.mrpack.description_pack', { name: plan.pack.name, version: plan.pack.version_id || '–' })
              : t('plugins.mrpack.description', { file: file.name })}
          </DialogDescription>
        </DialogHeader>
        {body()}
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button type="button" disabled={blocked} onClick={() => plan && start.mutate(plan)}>
            {t('plugins.mrpack.submit', { count })}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
