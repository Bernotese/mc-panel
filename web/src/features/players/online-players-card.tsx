import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Section } from '@/components/section'
import { Badge } from '@/components/ui/badge'
import { errorMessage } from '@/lib/error-message'
import { ONLINE_POLL_MS, onlinePlayersQueryOptions } from './api'

export function OnlinePlayersCard({ serverId }: { serverId: number }) {
  const { t } = useTranslation()
  const query = useQuery({ ...onlinePlayersQueryOptions(serverId), refetchInterval: ONLINE_POLL_MS })

  let body
  if (query.isPending) body = <p className="text-muted-foreground text-sm">{t('common.loading')}</p>
  else if (query.isError) body = <p className="text-muted-foreground text-sm">{errorMessage(query.error, t)}</p>
  else if (!query.data.available) body = <p className="text-muted-foreground text-sm">{t('players.online.unavailable')}</p>
  else {
    const { online, max } = query.data
    body = (
      <div className="space-y-2">
        <p className="text-2xl font-semibold tabular-nums">{t('players.card.count', { online: online.length, max: max ?? '—' })}</p>
        {online.length === 0 ? (
          <p className="text-muted-foreground text-sm">{t('players.online.none')}</p>
        ) : (
          <ul className="flex flex-wrap gap-1">
            {online.map((player) => (
              <li key={player.name}>
                <Badge variant="outline">{player.name}</Badge>
              </li>
            ))}
          </ul>
        )}
      </div>
    )
  }

  return <Section title={t('players.card.title')}>{body}</Section>
}
