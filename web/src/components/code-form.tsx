import { useId, useState } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

export interface CodeFormProps {
  label: string
  submitLabel: string
  numeric?: boolean
  pending?: boolean
  error?: string | null
  onSubmit(code: string): void
}

export function CodeForm({ label, submitLabel, numeric = false, pending = false, error = null, onSubmit }: CodeFormProps) {
  const id = useId()
  const errorId = `${id}-error`
  const [code, setCode] = useState('')
  const trimmed = code.trim()
  return (
    <form
      className="space-y-4"
      noValidate
      onSubmit={(e) => {
        e.preventDefault()
        if (trimmed) onSubmit(trimmed)
      }}
    >
      <div className="space-y-2">
        <Label htmlFor={id}>{label}</Label>
        <Input
          id={id}
          value={code}
          onChange={(e) => setCode(e.target.value)}
          inputMode={numeric ? 'numeric' : 'text'}
          autoComplete={numeric ? 'one-time-code' : 'off'}
          autoCapitalize="none"
          spellCheck={false}
          maxLength={32}
          autoFocus
          aria-invalid={error ? true : undefined}
          aria-describedby={error ? errorId : undefined}
        />
      </div>
      {error ? (
        <p id={errorId} role="alert" className="text-destructive text-sm">
          {error}
        </p>
      ) : null}
      <Button type="submit" className="w-full" disabled={pending || !trimmed}>
        {submitLabel}
      </Button>
    </form>
  )
}
