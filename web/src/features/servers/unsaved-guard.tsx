import { useBlocker } from '@tanstack/react-router'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { createUnsavedRegistry, UnsavedContext } from '@/lib/unsaved-changes'

/**
 * Issue #154: asks before any navigation away (other area, other server, links) and before a reload or tab close
 * while a form inside reports unsaved changes (useReportUnsaved). Like the files area's guard.
 */
export function UnsavedGuard({ children }: { children: ReactNode }) {
  const { t } = useTranslation()
  const [registry] = useState(createUnsavedRegistry)
  const blocker = useBlocker({ shouldBlockFn: () => registry.any(), enableBeforeUnload: () => registry.any(), withResolver: true })
  return (
    <UnsavedContext.Provider value={registry}>
      {children}
      <ConfirmDialog
        open={blocker.status === 'blocked'}
        onOpenChange={(open) => (open ? undefined : blocker.reset?.())}
        title={t('servers.unsaved.leave_title')}
        description={t('servers.unsaved.leave_description')}
        confirmLabel={t('servers.unsaved.leave')}
        destructive
        onConfirm={() => blocker.proceed?.()}
      />
    </UnsavedContext.Provider>
  )
}
