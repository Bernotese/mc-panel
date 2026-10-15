import { useMutation } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import { CodeForm } from '@/components/code-form'
import { QrCode } from '@/components/qr-code'
import { Button } from '@/components/ui/button'
import { errorMessage } from '@/lib/error-message'

export function TotpSetupCard({ onEnabled }: { onEnabled(codes: string[]): void }) {
  const { t } = useTranslation()
  const setup = useMutation({ mutationFn: () => unwrap(api.POST('/me/totp/setup')) })
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
      {/* SVG elements, no data: URL (CSP img-src 'self', SEC-48). */}
      <QrCode text={setup.data.otpauth_url} label={t('totp_setup.qr_alt')} size={224} className="mx-auto block rounded-md bg-white p-2" />
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
