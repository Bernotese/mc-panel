import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { errorMessage, useErrorToast } from '@/lib/error-message'
import { formatCountries, parseCountries } from './countries'

const settingsKey = ['security', 'settings'] as const

interface FormState {
  enforce: boolean
  allowlistOnly: boolean
  countries: string
}

function SwitchRow({ id, label, hint, checked, onChange }: { id: string; label: string; hint: string; checked: boolean; onChange(next: boolean): void }) {
  return (
    <div className="flex items-start justify-between gap-4 rounded-md border p-4">
      <div className="space-y-1">
        <Label htmlFor={id}>{label}</Label>
        <p id={`${id}-hint`} className="text-muted-foreground text-sm">
          {hint}
        </p>
      </div>
      <Switch id={id} checked={checked} onCheckedChange={onChange} aria-describedby={`${id}-hint`} />
    </div>
  )
}

export function SettingsTab() {
  const { t } = useTranslation()
  const id = useId()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const settings = useQuery({ queryKey: settingsKey, queryFn: () => unwrap(api.GET('/security/settings')) })
  // null = untouched; the form shows the server state until the admin edits something.
  const [form, setForm] = useState<FormState | null>(null)
  const save = useMutation({
    mutationFn: (value: FormState) =>
      unwrap(
        api.PUT('/security/settings', {
          body: {
            enforce_2fa_for_users: value.enforce,
            admin_allowlist_only: value.allowlistOnly,
            allowed_countries: parseCountries(value.countries).codes,
          },
        }),
      ),
    onSuccess: (result) => {
      queryClient.setQueryData(settingsKey, result)
      setForm(null)
      toast.success(t('security.settings.saved'))
    },
    onError: notifyError,
  })

  if (settings.isPending) return <p className="text-muted-foreground">{t('common.loading')}</p>
  if (settings.isError) {
    return (
      <p role="alert" className="text-destructive">
        {errorMessage(settings.error, t)}
      </p>
    )
  }

  const value: FormState = form ?? {
    enforce: settings.data.enforce_2fa_for_users,
    allowlistOnly: settings.data.admin_allowlist_only,
    countries: formatCountries(settings.data.allowed_countries),
  }
  const update = (patch: Partial<FormState>) => setForm({ ...value, ...patch })
  const parsed = parseCountries(value.countries)
  const geoip = settings.data.geoip_available

  return (
    <form
      className="max-w-3xl space-y-6"
      noValidate
      onSubmit={(e) => {
        e.preventDefault()
        if (parsed.invalid.length === 0) save.mutate(value)
      }}
    >
      <SwitchRow
        id={`${id}-enforce`}
        label={t('security.settings.enforce_2fa')}
        hint={t('security.settings.enforce_2fa_hint')}
        checked={value.enforce}
        onChange={(enforce) => update({ enforce })}
      />
      <SwitchRow
        id={`${id}-allowlist`}
        label={t('security.settings.allowlist_only')}
        hint={t('security.settings.allowlist_only_hint')}
        checked={value.allowlistOnly}
        onChange={(allowlistOnly) => update({ allowlistOnly })}
      />
      <div className="space-y-2 rounded-md border p-4">
        <Label htmlFor={`${id}-countries`}>{t('security.settings.countries')}</Label>
        <Input
          id={`${id}-countries`}
          value={value.countries}
          disabled={!geoip}
          autoComplete="off"
          spellCheck={false}
          aria-invalid={parsed.invalid.length > 0 || undefined}
          aria-describedby={`${id}-countries-hint`}
          onChange={(e) => update({ countries: e.target.value })}
        />
        <p id={`${id}-countries-hint`} className="text-muted-foreground text-sm">
          {geoip ? t('security.settings.countries_hint') : t('security.settings.geoip_missing')}
        </p>
        {parsed.invalid.length > 0 ? (
          <p className="text-destructive text-sm">{t('security.settings.countries_invalid', { codes: parsed.invalid.join(', ') })}</p>
        ) : null}
      </div>
      <Button type="submit" disabled={form === null || save.isPending || parsed.invalid.length > 0}>
        {t('common.save')}
      </Button>
    </form>
  )
}
