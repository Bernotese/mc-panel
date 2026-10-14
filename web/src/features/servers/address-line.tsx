import { Copy } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'

export function AddressLine({ address }: { address: string | null }) {
  const { t } = useTranslation()
  if (!address) return <p className="text-muted-foreground text-sm">{t('servers.address.missing')}</p>

  async function copy(value: string) {
    try {
      await navigator.clipboard.writeText(value)
      toast.success(t('servers.address.copied'))
    } catch {
      toast.error(t('servers.address.copy_failed'))
    }
  }

  const label = t('servers.address.copy', { address })
  return (
    <div className="flex min-w-0 items-center gap-1">
      <span className="bg-surface-2 text-primary truncate px-1.5 py-0.5 font-mono text-sm" title={address}>
        {address}
      </span>
      <Button type="button" variant="ghost" size="icon-sm" aria-label={label} title={label} onClick={() => void copy(address)}>
        <Copy aria-hidden="true" />
      </Button>
    </div>
  )
}
