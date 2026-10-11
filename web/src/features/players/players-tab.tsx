import { useQuery } from '@tanstack/react-query'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import type { PlayerListFile, Server } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { SectionCard } from '@/features/profile/section-card'
import { errorMessage } from '@/lib/error-message'
import { ONLINE_POLL_MS, onlinePlayersQueryOptions, playerListsQueryOptions } from './api'
import { BanDialog } from './ban-dialog'
import { NameForm } from './name-form'
import { usePlayerAction } from './use-player-action'
import { PLAYER_NAME } from './validation'

function Muted({ children }: { children: ReactNode }) {
  return <p className="text-muted-foreground text-sm">{children}</p>
}

export function PlayersTab({ server, onOpenSettings }: { server: Server; onOpenSettings?: () => void }) {
  const { t } = useTranslation()
  const running = server.status.state === 'running'
  const online = useQuery({ ...onlinePlayersQueryOptions(server.id), refetchInterval: running ? ONLINE_POLL_MS : false })
  const lists = useQuery(playerListsQueryOptions(server.id))
  const action = usePlayerAction(server.id)
  const [banTarget, setBanTarget] = useState<string | null>(null)
  const locked = !running || action.isPending
  const opNames = new Set((lists.data?.ops ?? []).map((o) => o.name.toLowerCase()))
  const whitelistOn = server.instance?.properties.whitelist_enabled === true

  function fileError(file: PlayerListFile) {
    return lists.data?.errors.includes(file) ? (
      <p role="alert" className="text-destructive text-sm">
        {t('players.file_error', { file })}
      </p>
    ) : null
  }

  function listsStatus() {
    if (lists.isPending) return <Muted>{t('common.loading')}</Muted>
    if (lists.isError)
      return (
        <p role="alert" className="text-destructive text-sm">
          {errorMessage(lists.error, t)}
        </p>
      )
    return null
  }

  function onlineList() {
    if (online.isPending) return <Muted>{t('common.loading')}</Muted>
    if (online.isError)
      return (
        <p role="alert" className="text-destructive text-sm">
          {errorMessage(online.error, t)}
        </p>
      )
    if (!online.data.available) return <Muted>{t('players.online.unavailable')}</Muted>
    if (online.data.online.length === 0) return <Muted>{t('players.online.none')}</Muted>
    return (
      <ul className="divide-y">
        {online.data.online.map((player) => {
          const isOp = opNames.has(player.name.toLowerCase())
          // Offline-mode servers allow names the panel rejects; actions on them would only fail.
          const disabled = locked || !PLAYER_NAME.test(player.name)
          // op/deop depend on the ops list: without it a deop could be offered for an op (or the reverse).
          const opDisabled = disabled || !lists.data
          return (
            <li key={player.name} className="flex flex-wrap items-center justify-between gap-2 py-2">
              <span className="flex items-center gap-2 font-medium">
                {player.name}
                {isOp ? <Badge variant="secondary">{t('players.op_badge')}</Badge> : null}
              </span>
              <span className="flex flex-wrap gap-2">
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  disabled={disabled}
                  aria-label={t('players.kick_label', { name: player.name })}
                  onClick={() => action.mutate({ action: 'kick', name: player.name })}
                >
                  {t('players.kick')}
                </Button>
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  disabled={disabled}
                  aria-label={t('players.ban_label', { name: player.name })}
                  onClick={() => setBanTarget(player.name)}
                >
                  {t('players.ban')}
                </Button>
                {isOp ? (
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    disabled={opDisabled}
                    aria-label={t('players.deop_label', { name: player.name })}
                    onClick={() => action.mutate({ action: 'deop', name: player.name })}
                  >
                    {t('players.deop')}
                  </Button>
                ) : (
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    disabled={opDisabled}
                    aria-label={t('players.op_label', { name: player.name })}
                    onClick={() => action.mutate({ action: 'op', name: player.name })}
                  >
                    {t('players.op')}
                  </Button>
                )}
              </span>
            </li>
          )
        })}
      </ul>
    )
  }

  return (
    <div className="space-y-4">
      {!running ? (
        <p role="status" className="rounded-md border p-3 text-sm">
          {t('players.not_running')}
        </p>
      ) : null}

      <SectionCard title={t('players.online.title')}>{onlineList()}</SectionCard>

      <SectionCard title={t('players.whitelist.title')}>
        <div className="space-y-3">
          {whitelistOn ? (
            <Muted>{t('players.whitelist.active')}</Muted>
          ) : onOpenSettings ? (
            <div className="flex flex-wrap items-center gap-2">
              <Muted>{t('players.whitelist.inactive')}</Muted>
              <Button type="button" variant="link" className="h-auto p-0" onClick={onOpenSettings}>
                {t('players.whitelist.open_settings')}
              </Button>
            </div>
          ) : (
            <Muted>{t('players.whitelist.inactive_user')}</Muted>
          )}
          {fileError('whitelist.json')}
          <NameForm
            label={t('players.whitelist.add_label')}
            disabled={locked}
            onAdd={(name) => action.mutateAsync({ action: 'whitelist_add', name })}
          />
          {listsStatus()}
          {lists.data && lists.data.whitelist.length === 0 ? <Muted>{t('players.whitelist.empty')}</Muted> : null}
          {lists.data && lists.data.whitelist.length > 0 ? (
            <ul className="divide-y">
              {lists.data.whitelist.map((entry) => (
                <li key={entry.uuid || entry.name} className="flex items-center justify-between gap-2 py-2">
                  <span className="font-medium">{entry.name}</span>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    disabled={locked || !PLAYER_NAME.test(entry.name)}
                    aria-label={t('players.whitelist_remove_label', { name: entry.name })}
                    onClick={() => action.mutate({ action: 'whitelist_remove', name: entry.name })}
                  >
                    {t('players.whitelist_remove')}
                  </Button>
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      </SectionCard>

      <SectionCard title={t('players.ops.title')}>
        <div className="space-y-3">
          {fileError('ops.json')}
          <NameForm label={t('players.ops.add_label')} disabled={locked} onAdd={(name) => action.mutateAsync({ action: 'op', name })} />
          {listsStatus()}
          {lists.data && lists.data.ops.length === 0 ? <Muted>{t('players.ops.empty')}</Muted> : null}
          {lists.data && lists.data.ops.length > 0 ? (
            <ul className="divide-y">
              {lists.data.ops.map((op) => (
                <li key={op.uuid || op.name} className="flex items-center justify-between gap-2 py-2">
                  <span className="flex items-center gap-2 font-medium">
                    {op.name}
                    <Badge variant="outline">{t('players.ops.level', { level: op.level })}</Badge>
                  </span>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    disabled={locked || !PLAYER_NAME.test(op.name)}
                    aria-label={t('players.deop_label', { name: op.name })}
                    onClick={() => action.mutate({ action: 'deop', name: op.name })}
                  >
                    {t('players.deop')}
                  </Button>
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      </SectionCard>

      <SectionCard title={t('players.bans.title')}>
        <div className="space-y-3">
          {fileError('banned-players.json')}
          {listsStatus()}
          {lists.data && lists.data.bans.length === 0 ? <Muted>{t('players.bans.empty')}</Muted> : null}
          {lists.data && lists.data.bans.length > 0 ? (
            <ul className="divide-y">
              {lists.data.bans.map((ban) => (
                <li key={ban.uuid || ban.name} className="flex flex-wrap items-start justify-between gap-2 py-2">
                  <div className="min-w-0 space-y-1">
                    <p className="font-medium">{ban.name}</p>
                    <dl className="text-muted-foreground grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 text-sm">
                      <dt>{t('players.bans.reason')}</dt>
                      <dd className="break-words">{ban.reason || '—'}</dd>
                      <dt>{t('players.bans.source')}</dt>
                      <dd className="break-words">{ban.source || '—'}</dd>
                      <dt>{t('players.bans.created')}</dt>
                      <dd>{ban.created || '—'}</dd>
                      <dt>{t('players.bans.expires')}</dt>
                      <dd>{ban.expires === 'forever' || !ban.expires ? t('players.bans.forever') : ban.expires}</dd>
                    </dl>
                  </div>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    disabled={locked || !PLAYER_NAME.test(ban.name)}
                    aria-label={t('players.pardon_label', { name: ban.name })}
                    onClick={() => action.mutate({ action: 'pardon', name: ban.name })}
                  >
                    {t('players.pardon')}
                  </Button>
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      </SectionCard>

      {banTarget !== null ? (
        <BanDialog
          key={banTarget}
          name={banTarget}
          open
          pending={locked}
          onOpenChange={(open) => {
            if (!open) setBanTarget(null)
          }}
          onConfirm={(reason) =>
            action.mutate(
              { action: 'ban', name: banTarget, ...(reason ? { reason } : {}) },
              { onSuccess: () => setBanTarget(null) },
            )
          }
        />
      ) : null}
    </div>
  )
}
