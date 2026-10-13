import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { FieldRow } from '@/features/servers/field-row'
import { normalizeNote, validateNote } from './model'

export function CreateBackupDialog({ pending, onClose, onSubmit }: { pending: boolean; onClose(): void; onSubmit(note: string): void }) {
  const { t } = useTranslation()
  const id = useId()
  const [text, setText] = useState('')
  const note = normalizeNote(text)
  const error = validateNote(note)
  return (
    <Dialog open onOpenChange={(open) => (open ? undefined : onClose())}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('backups.create.title')}</DialogTitle>
          <DialogDescription>{t('backups.create.description')}</DialogDescription>
        </DialogHeader>
        <form
          className="space-y-4"
          noValidate
          onSubmit={(e) => {
            e.preventDefault()
            // Enter while the request runs must not start a second backup (the tab also guards synchronously).
            if (error === null && !pending) onSubmit(note)
          }}
        >
          <FieldRow id={`${id}-note`} label={t('backups.create.note_label')} hint={t('backups.create.note_hint')} error={error ?? undefined}>
            {(c) => <Input {...c} value={text} autoComplete="off" onChange={(e) => setText(e.target.value)} />}
          </FieldRow>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" disabled={error !== null || pending}>
              {t('backups.create.submit')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
