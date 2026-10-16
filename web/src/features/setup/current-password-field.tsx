import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

/** Password input for the first second factor (see isFirstFactor). */
export function CurrentPasswordField({ value, onChange }: { value: string; onChange(value: string): void }) {
  const { t } = useTranslation()
  const id = useId()
  return (
    <div className="space-y-2">
      <Label htmlFor={id}>{t('first_factor.password_label')}</Label>
      <Input id={id} type="password" autoComplete="current-password" maxLength={256} value={value} onChange={(e) => onChange(e.target.value)} />
      <p className="text-muted-foreground text-xs">{t('first_factor.password_hint')}</p>
    </div>
  )
}
