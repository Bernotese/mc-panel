import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Label } from '@/components/ui/label'
import { cn } from '@/lib/utils'
import type { FieldError } from './validation'

export interface ControlProps {
  id: string
  'aria-describedby'?: string
  'aria-invalid'?: true
}

export interface FieldRowProps {
  id: string
  label: string
  hint?: string
  error?: FieldError
  children(control: ControlProps): ReactNode
}

/** Label, control and one message line: the error replaces the hint and is linked via aria-describedby. */
export function FieldRow({ id, label, hint, error, children }: FieldRowProps) {
  const { t } = useTranslation()
  const messageId = `${id}-message`
  const message = error ? t(error.key, error.params ?? {}) : hint
  return (
    <div className="space-y-2">
      <Label htmlFor={id}>{label}</Label>
      {children({ id, 'aria-describedby': message ? messageId : undefined, 'aria-invalid': error ? true : undefined })}
      {message ? (
        <p id={messageId} className={cn('text-sm', error ? 'text-destructive' : 'text-muted-foreground')}>
          {message}
        </p>
      ) : null}
    </div>
  )
}
