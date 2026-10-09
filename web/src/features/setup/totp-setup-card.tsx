import { useMutation, useQuery } from '@tanstack/react-query'
import QRCode from 'qrcode'
import { useTranslation } from 'react-i18next'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import { CodeForm } from '@/components/code-form'
import { Button } from '@/components/ui/button'
import { errorMessage } from '@/lib/error-message'

export function TotpSetupCard({ onEnabled }: { onEnabled(codes: string[]): void }) {
  const { t } = useTranslation()
  const setup = useMutation({ mutationFn: () => unwrap(api.POST('/me/totp/setup')) })
  const otpauthUrl = setup.data?.otpauth_url ?? null
  // QR as data: URL in a plain <img> – allowed by CSP img-src 'self' data:, no canvas/script injection.
  const qr = useQuery({
    queryKey: ['totp-qr', otpauthUrl],
    queryFn: () => QRCode.toDataURL(otpauthUrl ?? '', { margin: 1, width: 224, errorCorrectionLevel: 'M' }),
    enabled: otpauthUrl !== null,
    staleTime: Infinity,
    gcTime: 0,
  })
  const enable = useMutation({
    mutationFn: (code: string) => unwrap(api.POST('/me/totp/enable', { body: { code } })),
    onSuccess: (res) => onEnabled(res.codes),
  })

  if (!setup.data) {
    return (
      <div className="space-y-4">
        <p className="text-muted-foreground text-sm">{t('totp_setup.intro')}</p>
        {setup.error ? (
          <p role="alert" className="text-destructive text-sm">
            {errorMessage(setup.error, t)}
          </p>
        ) : null}
        <Button type="button" className="w-full" disabled={setup.isPending} onClick={() => setup.mutate()}>
          {t('totp_setup.start')}
        </Button>
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <p className="text-sm">{t('totp_setup.scan')}</p>
      {qr.data ? (
        <img src={qr.data} alt={t('totp_setup.qr_alt')} width={224} height={224} className="mx-auto rounded-md bg-white p-2" />
      ) : null}
      <p className="text-muted-foreground text-sm">{t('totp_setup.manual')}</p>
      <code data-testid="totp-secret" className="bg-muted block rounded-md p-2 font-mono text-sm break-all">
        {setup.data.secret}
      </code>
      <CodeForm
        label={t('totp_setup.code_label')}
        submitLabel={t('totp_setup.confirm')}
        numeric
        pending={enable.isPending}
        error={enable.error ? errorMessage(enable.error, t) : null}
        onSubmit={(code) => enable.mutate(code)}
      />
    </div>
  )
}
