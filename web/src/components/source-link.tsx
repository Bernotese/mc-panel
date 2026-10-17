import { useQuery } from '@tanstack/react-query'
import { CodeXml } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import { buttonVariants } from '@/components/ui/button'
import { sourceUrl } from '@/lib/source'
import { cn } from '@/lib/utils'

/**
 * AGPL-3.0 §13: everyone who uses the panel over the network is offered its source. The sign-in
 * pages link the repository; the rail links the running version (RunningSourceLink).
 */
export function SourceLink({ version, className }: { version?: string; className?: string }) {
  const { t } = useTranslation()
  const label = version ? t('about.source_version', { version }) : t('about.source')
  return (
    <a
      href={sourceUrl(version)}
      target="_blank"
      rel="noopener noreferrer"
      aria-label={label}
      title={label}
      className={cn(buttonVariants({ variant: 'ghost', size: 'icon' }), 'text-muted-foreground', className)}
    >
      <CodeXml aria-hidden="true" />
    </a>
  )
}

/** SourceLink with the version of the running panel (GET /about, signed in only). */
export function RunningSourceLink({ className }: { className?: string }) {
  const about = useQuery({ queryKey: ['about'], queryFn: () => unwrap(api.GET('/about')), staleTime: Infinity, retry: false })
  return <SourceLink version={about.data?.version} className={className} />
}
