import { useId, useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { reasonProblem } from './validation'

export interface BanDialogProps {
  name: string
  open: boolean
  pending: boolean
  onOpenChange(open: boolean): void
  onConfirm(reason: string | undefined): void
}

export function BanDialog({ name, open, pending, onOpenChange, onConfirm }: BanDialogProps) {
  const { t } = useTranslation()
  const id = useId()
  const messageId = `${id}-message`
  const [reason, setReason] = useState('')
  const [problem, setProblem] = useState<string | null>(null)

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (pending) return
    const value = reason.trim()
    const found = value ? reasonProblem(value) : null
    setProblem(found)
    if (found) return
    onConfirm(value || undefined)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={submit} className="space-y-4">
          <DialogHeader>
            <DialogTitle>{t('players.ban_dialog.title', { name })}</DialogTitle>
            <DialogDescription>{t('players.ban_dialog.description')}</DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor={id}>{t('players.ban_dialog.reason')}</Label>
            <Input
              id={id}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              autoComplete="off"
              aria-invalid={problem ? true : undefined}
              aria-describedby={problem ? messageId : undefined}
            />
            {problem ? (
              <p id={messageId} className="text-destructive text-sm">
                {t(problem)}
              </p>
            ) : null}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" variant="destructive" disabled={pending}>
              {t('players.ban_dialog.submit')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
