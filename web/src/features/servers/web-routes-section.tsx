import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Plus, X } from 'lucide-react'
import { useId, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { isApiError, isSudoCancelled } from '@/api/errors'
import type { Server } from '@/api/types'
import { Section } from '@/components/section'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { errorMessage } from '@/lib/error-message'
import { useReportUnsaved } from '@/lib/unsaved-changes'
import { useRemoteDraft } from '@/lib/use-remote-draft'
import { cn } from '@/lib/utils'
import { serverKey, serversKey, updateServer } from './api'
import { DraftConflict } from './draft-conflict'
import {
  DEFAULT_ROOT_PORT,
  MAX_WEB_ROUTES,
  rebaseWebDraft,
  sameWebDraft,
  webDraftFromRoutes,
  webDraftRoutes,
  webPathError,
  webPortError,
  webRouteUrl,
  type WebRouteInput,
  type WebRouteRow,
} from './web-routes'

const STATUS_DOT: Record<Server['web_status'], string> = {
  off: 'bg-status-offline',
  pending: 'bg-status-starting',
  active: 'bg-status-online shadow-[0_0_6px_var(--status-online)]',
  error: 'bg-status-error',
}

/**
 * Root slot and path rows share the grid: path, port, action (on phones the path gets a line of its own); errors and
 * the address span the row.
 */
const ROW_GRID = 'grid grid-cols-[minmax(0,1fr)_auto] items-end gap-x-3 gap-y-2 sm:grid-cols-[minmax(0,18rem)_9rem_auto]'
const PATH_CELL = 'col-span-full space-y-2 sm:col-span-1'

type PathRow = WebRouteRow
type FieldError = 'invalid' | 'reserved' | 'duplicate' | null

/** The routes as one line for the conflict box: "/ → 8100, /map/ → 8123". */
function describeRoutes(routes: readonly WebRouteInput[], none: string): string {
  return routes.length === 0 ? none : routes.map((r) => `/${r.path ? `${r.path}/` : ''} → ${r.port}`).join(', ')
}

/** The 409 the panel answers when the base domain is gone; the section then shows the same hint as without one. */
function isBaseDomainRequired(err: unknown): boolean {
  return isApiError(err, 'conflict') && err.message === 'base domain required'
}

/** `…` in the text marks file names and settings; odd parts become <code>, never HTML. */
function withCode(text: string): ReactNode[] {
  return text.split('`').map((part, i) => (i % 2 === 1 ? <code key={i} className="text-foreground font-mono">{part}</code> : part))
}

/**
 * Web port paths spec §5: publishes ports of the container at https://<name>.<base domain>/ (the root slot) and
 * under /<path>/ (the path rows). Admins only; the stored routes come sorted, root first.
 */
export function WebRoutesSection({ server }: { server: Server }) {
  const { t } = useTranslation()
  const id = useId()
  const queryClient = useQueryClient()
  const stored = server.web_routes ?? []
  // Issue #154: a draft survives polling and changes elsewhere; the routes carry no revision, so a newer stored list is
  // shown for comparison and "keep" puts only the own changes onto it, path by path.
  const d = useRemoteDraft(
    stored.map(({ path, port }) => ({ path, port })),
    { toDraft: webDraftFromRoutes, merge: rebaseWebDraft, same: sameWebDraft },
  )
  useReportUnsaved(d.dirty)
  const { rootOn, rootPort, rows } = d.draft
  const setRootOn = (on: boolean) => d.setDraft((x) => ({ ...x, rootOn: on }))
  const setRootPort = (port: string) => d.setDraft((x) => ({ ...x, rootPort: port }))
  const setRows = (f: (rows: PathRow[]) => PathRow[]) => d.setDraft((x) => ({ ...x, rows: f(x.rows) }))

  const save = useMutation({
    mutationFn: (routes: WebRouteInput[]) => updateServer(server.id, { web_routes: routes }),
    onSuccess: (updated) => {
      queryClient.setQueryData(serverKey(server.id), updated)
      d.saved((updated.web_routes ?? []).map(({ path, port }) => ({ path, port })))
      void queryClient.invalidateQueries({ queryKey: serversKey, exact: true })
      toast.success(updated.web_status === 'off' ? t('servers.web.saved_off') : t('servers.web.saved_on'))
    },
  })

  const noDomain = server.address === null || isBaseDomainRequired(save.error)
  const disabled = noDomain || save.isPending
  const next = webDraftRoutes(d.draft)
  const dirty = d.dirty
  const published = (rootOn ? 1 : 0) + rows.length
  const otherError = save.error && !noDomain && !isSudoCancelled(save.error) ? errorMessage(save.error, t) : null

  // Ports are unique across the published routes, paths across the rows (spec §2).
  const ports = [...(rootOn ? [rootPort.trim()] : []), ...rows.map((r) => r.port.trim())].filter((p) => webPortError(p) === null).map(Number)
  const portError = (raw: string): FieldError => webPortError(raw.trim()) ?? (ports.filter((p) => p === Number(raw.trim())).length > 1 ? 'duplicate' : null)
  const rootPortError = rootOn ? portError(rootPort) : null
  const rowErrors = rows.map((r, i) => ({
    path: webPathError(
      r.path.trim(),
      rows.filter((_, j) => j !== i).map((o) => o.path.trim()),
    ) as FieldError,
    port: portError(r.port),
  }))
  const invalid = rootPortError !== null || rowErrors.some((e) => e.path !== null || e.port !== null)

  /** A stored route links to its address; an unsaved one only shows where it will be. */
  function address(path: string, port: string): ReactNode {
    if (!server.address) return null
    const url = webRouteUrl(server.address, path)
    const live = stored.find((r) => r.path === path && String(r.port) === port.trim())?.url
    return (
      <p className="col-span-full flex min-w-0 flex-wrap items-baseline gap-x-2 text-sm">
        <span className="text-muted-foreground">{t('servers.web.url_label')}</span>
        {live ? (
          <a href={live} target="_blank" rel="noopener noreferrer" className="text-primary font-mono break-all underline underline-offset-4">
            {live}
          </a>
        ) : (
          <span className="text-muted-foreground font-mono break-all">{url}</span>
        )}
      </p>
    )
  }

  function fieldError(errId: string, err: FieldError, kind: 'path' | 'port'): ReactNode {
    if (err === null) return null
    const key = kind === 'path' ? `servers.web.path_${err}` : `servers.web.port_${err}`
    return (
      <p id={errId} className="text-destructive col-span-full text-sm">
        {t(key)}
      </p>
    )
  }

  function updateRow(key: number, patch: Partial<PathRow>) {
    setRows((rs) => rs.map((r) => (r.key === key ? { ...r, ...patch } : r)))
  }

  return (
    <Section title={t('servers.web.title')} description={t('servers.web.description')}>
      <form
        className="flex flex-col gap-4"
        noValidate
        onSubmit={(e) => {
          e.preventDefault()
          setRows((rs) => rs.map((r) => ({ ...r, touched: true })))
          if (noDomain || invalid || !dirty) return
          save.mutate(next)
        }}
      >
        {d.conflict ? (
          <DraftConflict
            reason="remote"
            changes={[
              {
                key: 'routes',
                label: t('servers.web.title'),
                theirs: describeRoutes(webDraftRoutes(d.remoteDraft), t('servers.draft.none')),
                mine: describeRoutes(next, t('servers.draft.none')),
              },
            ]}
            keepHint={t('servers.draft.keep_hint_routes')}
            onReload={d.reload}
            onKeepMine={d.keepMine}
          />
        ) : null}
        {noDomain ? (
          <div role="status" className="border-status-starting/60 border p-3 text-sm">
            <p>{t('servers.web.no_domain')}</p>
            <Link to="/settings" className="text-primary mt-2 inline-block underline underline-offset-4">
              {t('settings.link')}
            </Link>
          </div>
        ) : null}

        <div className="space-y-3 border p-3">
          <div className="flex items-center justify-between gap-4">
            <Label htmlFor={`${id}-root`}>{t('servers.web.root_publish')}</Label>
            <Switch
              id={`${id}-root`}
              checked={rootOn}
              disabled={disabled}
              onCheckedChange={(on) => {
                setRootOn(on)
                if (on && rootPort.trim() === '') setRootPort(String(DEFAULT_ROOT_PORT))
              }}
            />
          </div>
          <div className={ROW_GRID}>
            <div className={PATH_CELL}>
              <span className="text-sm leading-none font-medium">{t('servers.web.path')}</span>
              <div className="bevel-inset text-muted-foreground flex h-9 items-center px-3 font-mono text-sm">/</div>
            </div>
            <div className="space-y-2">
              <Label htmlFor={`${id}-root-port`}>{t('servers.web.port')}</Label>
              <Input
                id={`${id}-root-port`}
                aria-label={t('servers.web.port_root')}
                type="number"
                inputMode="numeric"
                min={1024}
                max={65535}
                value={rootPort}
                disabled={!rootOn || disabled}
                aria-invalid={rootPortError !== null || undefined}
                aria-describedby={rootPortError !== null ? `${id}-root-port-error` : undefined}
                onChange={(e) => setRootPort(e.target.value)}
              />
            </div>
            <div aria-hidden="true" />
            {fieldError(`${id}-root-port-error`, rootPortError, 'port')}
            {rootOn ? address('', rootPort) : null}
          </div>
        </div>

        <div className="space-y-3">
          {rows.length > 0 ? (
            <ul className="flex flex-col gap-3" aria-label={t('servers.web.paths_label')}>
              {rows.map((row, i) => {
                const n = i + 1
                const err = rowErrors[i]
                const showPath = err.path !== null && (row.touched || row.path.trim() !== '')
                const showPort = err.port !== null && (row.touched || row.port.trim() !== '')
                const rid = `${id}-row-${row.key}`
                return (
                  <li key={row.key} className="border p-3">
                    <div className={ROW_GRID}>
                      <div className={PATH_CELL}>
                        <Label htmlFor={`${rid}-path`}>{t('servers.web.path')}</Label>
                        <div className="flex items-center gap-1 font-mono">
                          <span aria-hidden="true" className="text-muted-foreground">
                            /
                          </span>
                          <Input
                            id={`${rid}-path`}
                            aria-label={t('servers.web.path_n', { n })}
                            className="font-mono"
                            autoCapitalize="none"
                            autoCorrect="off"
                            spellCheck={false}
                            maxLength={32}
                            value={row.path}
                            disabled={disabled}
                            aria-invalid={showPath || undefined}
                            aria-describedby={showPath ? `${rid}-path-error` : undefined}
                            onChange={(e) => updateRow(row.key, { path: e.target.value })}
                            onBlur={() => updateRow(row.key, { touched: true })}
                          />
                          <span aria-hidden="true" className="text-muted-foreground">
                            /
                          </span>
                        </div>
                      </div>
                      <div className="space-y-2">
                        <Label htmlFor={`${rid}-port`}>{t('servers.web.port')}</Label>
                        <Input
                          id={`${rid}-port`}
                          aria-label={t('servers.web.port_n', { n })}
                          type="number"
                          inputMode="numeric"
                          min={1024}
                          max={65535}
                          value={row.port}
                          disabled={disabled}
                          aria-invalid={showPort || undefined}
                          aria-describedby={showPort ? `${rid}-port-error` : undefined}
                          onChange={(e) => updateRow(row.key, { port: e.target.value })}
                          onBlur={() => updateRow(row.key, { touched: true })}
                        />
                      </div>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        aria-label={t('servers.web.remove', { n })}
                        disabled={disabled}
                        onClick={() => setRows((rs) => rs.filter((r) => r.key !== row.key))}
                      >
                        <X aria-hidden="true" />
                      </Button>
                      {showPath ? fieldError(`${rid}-path-error`, err.path, 'path') : null}
                      {showPort ? fieldError(`${rid}-port-error`, err.port, 'port') : null}
                      {err.path === null ? address(row.path.trim(), row.port) : null}
                    </div>
                  </li>
                )
              })}
            </ul>
          ) : null}
          <div className="flex flex-wrap items-center gap-3">
            <Button
              type="button"
              variant="outline"
              disabled={disabled || published >= MAX_WEB_ROUTES}
              onClick={() => setRows((rs) => [...rs, { key: Math.max(-1, ...rs.map((r) => r.key)) + 1, path: '', port: '', touched: false }])}
            >
              <Plus aria-hidden="true" />
              {t('servers.web.add')}
            </Button>
            {published >= MAX_WEB_ROUTES ? <span className="text-muted-foreground text-sm">{t('servers.web.add_limit', { max: MAX_WEB_ROUTES })}</span> : null}
          </div>
        </div>

        <dl className="grid grid-cols-[auto_minmax(0,1fr)] items-center gap-x-4 gap-y-2 text-sm">
          <dt className="text-muted-foreground">{t('servers.web.status_label')}</dt>
          <dd className="flex min-w-0 flex-wrap items-center gap-2">
            <Badge variant="outline">
              <span aria-hidden="true" className={cn('size-1.5 shrink-0', STATUS_DOT[server.web_status])} />
              {t(`servers.web.status.${server.web_status}`)}
            </Badge>
            {server.web_status === 'error' && server.web_error ? <span className="text-status-error break-words">{server.web_error}</span> : null}
          </dd>
        </dl>
        <div className="bevel-inset text-muted-foreground space-y-2 px-3 py-2 text-sm">
          <p>{withCode(t('servers.web.hint'))}</p>
          <p>{withCode(t('servers.web.prefix_hint'))}</p>
        </div>
        {otherError ? (
          <p role="alert" className="text-destructive text-sm">
            {otherError}
          </p>
        ) : null}
        <div>
          <Button type="submit" disabled={noDomain || !dirty || invalid || save.isPending}>
            {t('servers.web.save')}
          </Button>
        </div>
      </form>
    </Section>
  )
}
