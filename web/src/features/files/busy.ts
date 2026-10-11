import type { TFunction } from 'i18next'

/** What leaving the files tab would lose: unsaved editor changes and/or pending uploads (they are cancelled). */
export interface FilesBusy {
  unsaved: boolean
  uploads: boolean
}

/** Texts of the "leave the files tab?" confirmation. */
export function leaveTexts(busy: FilesBusy, t: TFunction): { title: string; description: string; confirm: string } {
  if (!busy.uploads) {
    return { title: t('files.editor.discard_title'), description: t('files.editor.discard_tab'), confirm: t('files.editor.discard') }
  }
  const description = busy.unsaved ? `${t('files.uploads.leave_description')} ${t('files.editor.discard_tab')}` : t('files.uploads.leave_description')
  return { title: t('files.uploads.leave_title'), description, confirm: t('files.uploads.leave') }
}
