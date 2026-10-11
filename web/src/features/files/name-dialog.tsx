import { useId, useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { errorMessage } from '@/lib/error-message'

export interface NameDialogProps {
  title: string
  label: string
  initial: string
  hint?: string
  submitLabel: string
  /** Returns an i18n key or null. */
  validate(value: string): string | null
  onClose(): void
  /** Throws on failure; the error is shown inside the dialog. */
  onSubmit(value: string): Promise<void>
}

/** One field, local validation first, server errors (e.g. file_exists) inline instead of a toast. */
export function NameDialog({ title, label, initial, hint, submitLabel, validate, onClose, onSubmit }: NameDialogProps) {
  const { t } = useTranslation()
  const id = useId()
  const messageId = `${id}-message`
  const hintId = `${id}-hint`
  const [value, setValue] = useState(initial)
  const [problem, setProblem] = useState<string | null>(null)
  const [failure, setFailure] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (pending) return
    const found = validate(value)
    setProblem(found)
    setFailure(null)
    if (found) return
    setPending(true)
    try {
      await onSubmit(value)
    } catch (err) {
      setFailure(errorMessage(err, t))
    } finally {
      setPending(false)
    }
  }

  const message = problem ? t(problem) : failure
  const describedBy = [hint ? hintId : null, message ? messageId : null].filter(Boolean).join(' ') || undefined

  return (
    <Dialog open onOpenChange={(open) => (open ? undefined : onClose())}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={(event) => void submit(event)} className="space-y-4">
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
            {hint ? <DialogDescription id={hintId}>{hint}</DialogDescription> : null}
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor={id}>{label}</Label>
            <Input
              id={id}
              value={value}
              onChange={(e) => setValue(e.target.value)}
              autoComplete="off"
              autoCapitalize="off"
              spellCheck={false}
              aria-invalid={message ? true : undefined}
              aria-describedby={describedBy}
            />
            {message ? (
              <p id={messageId} role="alert" className="text-destructive text-sm">
                {message}
              </p>
            ) : null}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" disabled={pending}>
              {submitLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
