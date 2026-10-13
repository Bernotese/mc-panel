import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

export interface RestoreDialogProps {
  serverName: string
  /** Formatted snapshot time for the title. */
  when: string
  pending: boolean
  onClose(): void
  onConfirm(): void
}

/** A restore replaces the whole data directory (spec §5.5): the server name must be typed, like when deleting a server. */
export function RestoreDialog({ serverName, when, pending, onClose, onConfirm }: RestoreDialogProps) {
  const { t } = useTranslation()
  const id = useId()
  const [typed, setTyped] = useState('')
  const matches = typed.trim() === serverName
  return (
    <Dialog open onOpenChange={(open) => (open ? undefined : onClose())}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('backups.restore.title', { when })}</DialogTitle>
          <DialogDescription>{t('backups.restore.description')}</DialogDescription>
        </DialogHeader>
        <form
          className="space-y-4"
          noValidate
          onSubmit={(e) => {
            e.preventDefault()
            if (matches && !pending) onConfirm()
          }}
        >
          <div className="space-y-2">
            <Label htmlFor={`${id}-confirm`}>{t('backups.restore.confirm_label', { name: serverName })}</Label>
            <Input
              id={`${id}-confirm`}
              value={typed}
              autoCapitalize="none"
              autoComplete="off"
              spellCheck={false}
              onChange={(e) => setTyped(e.target.value)}
            />
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" variant="destructive" disabled={!matches || pending}>
              {t('backups.restore.submit')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
