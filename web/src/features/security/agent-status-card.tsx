import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import { Section } from '@/components/section'
import { Badge } from '@/components/ui/badge'
import { useFormat } from '@/lib/format'

/** Agent outages are expected in dev and must never break the page: errors render as "unreachable". */
export function AgentStatusCard() {
  const { t } = useTranslation()
  const fmt = useFormat()
  const agent = useQuery({
    queryKey: ['security', 'agent'],
    queryFn: () => unwrap(api.GET('/security/agent')),
    refetchInterval: 30_000,
    retry: false,
  })
  const reachable = agent.data?.reachable === true

  return (
    <Section
      title={t('security.agent.title')}
      headingLevel={3}
      className="h-full"
      actions={
        agent.isPending ? null : (
          <Badge variant={reachable ? 'secondary' : 'outline'}>{reachable ? t('security.agent.reachable') : t('security.agent.unreachable')}</Badge>
        )
      }
    >
      <div className="space-y-2 text-sm">
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
      </div>
    </Section>
  )
}
