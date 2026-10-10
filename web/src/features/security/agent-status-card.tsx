import { useQuery } from '@tanstack/react-query'
import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { useFormat } from '@/lib/format'

/** Agent outages are expected in dev and must never break the page: errors render as "unreachable". */
export function AgentStatusCard() {
  const { t } = useTranslation()
  const fmt = useFormat()
  const headingId = useId()
  const agent = useQuery({
    queryKey: ['security', 'agent'],
    queryFn: () => unwrap(api.GET('/security/agent')),
    refetchInterval: 30_000,
    retry: false,
  })
  const reachable = agent.data?.reachable === true

  return (
    <section aria-labelledby={headingId}>
      <Card className="h-full">
        <CardHeader>
          <div className="flex items-center justify-between gap-2">
            <h3 id={headingId} className="font-semibold">
              {t('security.agent.title')}
            </h3>
            {agent.isPending ? null : (
              <Badge variant={reachable ? 'secondary' : 'outline'}>{reachable ? t('security.agent.reachable') : t('security.agent.unreachable')}</Badge>
            )}
          </div>
        </CardHeader>
        <CardContent className="space-y-2 text-sm">
          {agent.isPending ? <p className="text-muted-foreground">{t('common.loading')}</p> : null}
          {!agent.isPending && !reachable ? <p className="text-muted-foreground">{t('security.agent.unreachable_hint')}</p> : null}
          {agent.data ? (
            <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
              <dt className="text-muted-foreground">{t('security.agent.firewall')}</dt>
              <dd>{agent.data.firewall || '—'}</dd>
              <dt className="text-muted-foreground">{t('security.agent.version')}</dt>
              <dd>{agent.data.version || '—'}</dd>
              <dt className="text-muted-foreground">{t('security.agent.pending_sync')}</dt>
              <dd className="tabular-nums">{fmt.number(agent.data.pending_sync)}</dd>
            </dl>
          ) : null}
        </CardContent>
      </Card>
    </section>
  )
}
