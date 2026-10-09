import { useQuery } from '@tanstack/react-query'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { PageHeader } from '@/components/page-header'
import { Button } from '@/components/ui/button'
import { useMe } from '@/features/auth/me'
import { errorMessage } from '@/lib/error-message'
import { CreateServerDialog } from './create-server-dialog'
import { ServerCard } from './server-card'
import { serversQueryOptions } from './servers'

export function DashboardPage() {
  const { t } = useTranslation()
  const me = useMe()
  const isAdmin = me?.role === 'admin'
  const [creating, setCreating] = useState(false)
  const servers = useQuery(serversQueryOptions)

  let content: ReactNode
  if (servers.isPending) {
    content = <p className="text-muted-foreground">{t('common.loading')}</p>
  } else if (servers.isError) {
    content = (
      <p role="alert" className="text-destructive">
        {errorMessage(servers.error, t)}
      </p>
    )
  } else if (servers.data.length === 0) {
    content = <p className="text-muted-foreground">{t('dashboard.empty')}</p>
  } else {
    content = (
      <ul className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {servers.data.map((server) => (
          <li key={server.id}>
            <ServerCard server={server} canDelete={isAdmin} />
          </li>
        ))}
      </ul>
    )
  }

  return (
    <>
      <PageHeader
        title={t('dashboard.title')}
        description={t('dashboard.description')}
        actions={
          isAdmin ? (
            <Button type="button" onClick={() => setCreating(true)}>
              {t('dashboard.create_server')}
            </Button>
          ) : undefined
        }
      />
      {content}
      {isAdmin ? <CreateServerDialog open={creating} onOpenChange={setCreating} /> : null}
    </>
  )
}
