import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useId, useState } from 'react'
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
import { cn } from '@/lib/utils'
import { serverKey, serversKey, updateServer } from './api'

/** BlueMap's default web server port (web ports spec §1.1). */
const DEFAULT_WEB_PORT = 8100

type PortError = 'invalid' | 'reserved'

/** The port rule of agentapi.ValidWebPort; null = valid. */
function webPortError(raw: string): PortError | null {
  if (!/^\d{1,5}$/.test(raw)) return 'invalid'
  const port = Number(raw)
  if (port === 25565 || port === 25575) return 'reserved'
  return port >= 1024 && port <= 65535 ? null : 'invalid'
}

const STATUS_DOT: Record<Server['web_status'], string> = {
  off: 'bg-status-offline',
  pending: 'bg-status-starting',
  active: 'bg-status-online shadow-[0_0_6px_var(--status-online)]',
  error: 'bg-status-error',
}

/** The 409 the panel answers when the base domain is gone; the section then shows the same hint as without one. */
function isBaseDomainRequired(err: unknown): boolean {
  return isApiError(err, 'conflict') && err.message === 'base domain required'
}

/** Web ports spec §3.4: publishes one port of the container at https://<name>.<base domain>. Admins only. */
export function WebPortSection({ server }: { server: Server }) {
  const { t } = useTranslation()
  const id = useId()
  const queryClient = useQueryClient()
  const published = server.web_port !== undefined
  const [enabled, setEnabled] = useState(published)
  const [port, setPort] = useState(String(server.web_port ?? DEFAULT_WEB_PORT))
  const [submitted, setSubmitted] = useState(false)

  const save = useMutation({
    mutationFn: (webPort: number | null) => updateServer(server.id, { web_port: webPort }),
    onSuccess: (updated) => {
      queryClient.setQueryData(serverKey(server.id), updated)
      void queryClient.invalidateQueries({ queryKey: serversKey, exact: true })
      toast.success(updated.web_status === 'off' ? t('servers.web.saved_off') : t('servers.web.saved_on'))
    },
  })

  const noDomain = server.address === null || isBaseDomainRequired(save.error)
  const portError = enabled ? webPortError(port.trim()) : null
  const showPortError = submitted && portError !== null
  const dirty = enabled !== published || (enabled && port.trim() !== String(server.web_port ?? ''))
  const otherError = save.error && !noDomain && !isSudoCancelled(save.error) ? errorMessage(save.error, t) : null

  return (
    <Section title={t('servers.web.title')} description={t('servers.web.description')}>
      <form
        className="flex flex-col gap-4"
        noValidate
        onSubmit={(e) => {
          e.preventDefault()
          setSubmitted(true)
          if (noDomain || portError !== null) return
          save.mutate(enabled ? Number(port.trim()) : null)
        }}
      >
        {noDomain ? (
          <div role="status" className="border-status-starting/60 border p-3 text-sm">
            <p>{t('servers.web.no_domain')}</p>
            <Link to="/settings" className="text-primary mt-2 inline-block underline underline-offset-4">
              {t('settings.link')}
            </Link>
          </div>
        ) : null}
        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0 space-y-1">
            <Label htmlFor={`${id}-switch`}>{t('servers.web.publish')}</Label>
            {server.address ? (
              <p id={`${id}-switch-hint`} className="text-muted-foreground text-sm break-all">
                {t('servers.web.publish_hint', { host: server.address })}
              </p>
            ) : null}
          </div>
          <Switch
            id={`${id}-switch`}
            checked={enabled}
            disabled={noDomain || save.isPending}
            aria-describedby={server.address ? `${id}-switch-hint` : undefined}
            onCheckedChange={setEnabled}
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor={`${id}-port`}>{t('servers.web.port')}</Label>
          <Input
            id={`${id}-port`}
            type="number"
            inputMode="numeric"
            min={1024}
            max={65535}
            className="sm:w-40"
            value={port}
            disabled={!enabled || noDomain || save.isPending}
            aria-invalid={showPortError || undefined}
            aria-describedby={showPortError ? `${id}-port-error` : undefined}
            onChange={(e) => setPort(e.target.value)}
          />
          {showPortError ? (
            <p id={`${id}-port-error`} className="text-destructive text-sm">
              {t(portError === 'reserved' ? 'servers.web.port_reserved' : 'servers.web.port_invalid')}
            </p>
          ) : null}
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
          {server.web_url ? (
            <>
              <dt className="text-muted-foreground">{t('servers.web.url_label')}</dt>
              <dd className="min-w-0">
                <a href={server.web_url} target="_blank" rel="noopener noreferrer" className="text-primary font-mono break-all underline underline-offset-4">
                  {server.web_url}
                </a>
              </dd>
            </>
          ) : null}
        </dl>
        <p className="bevel-inset text-muted-foreground px-3 py-2 text-sm">
          {/* `…` in the text marks file names and settings; odd parts become <code>, never HTML. */}
          {t('servers.web.hint')
            .split('`')
            .map((part, i) => (i % 2 === 1 ? <code key={i} className="text-foreground font-mono">{part}</code> : part))}
        </p>
        {otherError ? (
          <p role="alert" className="text-destructive text-sm">
            {otherError}
          </p>
        ) : null}
        <div>
          <Button type="submit" disabled={noDomain || !dirty || save.isPending}>
            {t('servers.web.save')}
          </Button>
        </div>
      </form>
    </Section>
  )
}
