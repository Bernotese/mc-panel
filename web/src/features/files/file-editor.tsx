import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useBlocker } from '@tanstack/react-router'
import { ArrowLeft } from 'lucide-react'
import { lazy, Suspense, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { isApiError } from '@/api/errors'
import type { FileContent, Server } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { errorMessage, useErrorToast } from '@/lib/error-message'
import { downloadUrl, fileContentKey, fileListsKey, readFile, writeFile } from './api'
import { editorLanguage } from './format'
import { baseName } from './path'

const CodeEditor = lazy(() => import('./code-editor'))

export interface FileEditorProps {
  server: Server
  path: string
  onClose(): void
  onDirtyChange?(dirty: boolean): void
  onOpenSettings?(): void
}

export function FileEditor({ server, path, onClose, onDirtyChange, onOpenSettings }: FileEditorProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const contentKey = fileContentKey(server.id, path)
  const name = baseName(path)
  const file = useQuery({
    queryKey: contentKey,
    queryFn: () => readFile(server.id, path),
    // The loaded mtime is the base for conflict detection: never refresh it behind the user's back.
    staleTime: Infinity,
    gcTime: 0,
    refetchOnWindowFocus: false,
    retry: false,
  })
  const [draft, setDraft] = useState<string | null>(null)
  const [editorKey, setEditorKey] = useState(0)
  const [conflict, setConflict] = useState(false)
  const [confirmClose, setConfirmClose] = useState(false)
  const dirty = draft !== null && file.data !== undefined && draft !== file.data.content

  useEffect(() => {
    onDirtyChange?.(dirty)
  }, [dirty, onDirtyChange])
  useEffect(() => () => onDirtyChange?.(false), [onDirtyChange])

  // Route changes and reloads; tab switches are asked by the detail page (onDirtyChange).
  const blocker = useBlocker({ shouldBlockFn: () => dirty, enableBeforeUnload: () => dirty, withResolver: true })

  const save = useMutation({
    mutationFn: ({ content, mtime }: { content: string; mtime: number }) => writeFile(server.id, { path, content, expected_mtime: mtime }),
    onSuccess: (result, { content }) => {
      queryClient.setQueryData<FileContent>(contentKey, { path, content, mtime: result.mtime, size: result.size })
      // Text typed while the request ran stays a draft.
      setDraft((current) => (current === content ? null : current))
      toast.success(t('files.editor.saved'))
      void queryClient.invalidateQueries({ queryKey: fileListsKey(server.id) })
    },
    onError: (err) => (isApiError(err, 'file_modified') ? setConflict(true) : notifyError(err)),
  })

  function doSave() {
    if (!file.data || draft === null || !dirty || save.isPending) return
    save.mutate({ content: draft, mtime: file.data.mtime })
  }

  async function overwrite() {
    setConflict(false)
    if (draft === null) return
    try {
      const current = await readFile(server.id, path)
      save.mutate({ content: draft, mtime: current.mtime })
    } catch (err) {
      notifyError(err)
    }
  }

  async function reload() {
    setConflict(false)
    setDraft(null)
    await queryClient.refetchQueries({ queryKey: contentKey })
    setEditorKey((k) => k + 1)
  }

  function body() {
    if (file.isPending) return <p className="text-muted-foreground text-sm">{t('common.loading')}</p>
    if (file.isError) {
      if (isApiError(file.error, 'not_text')) {
        return (
          <div className="space-y-2">
            <p className="text-sm">{t('files.editor.not_text')}</p>
            <Button asChild variant="outline" size="sm">
              <a href={downloadUrl(server.id, path)} download>
                {t('files.editor.download_instead')}
              </a>
            </Button>
          </div>
        )
      }
      return (
        <p role="alert" className="text-destructive text-sm">
          {errorMessage(file.error, t)}
        </p>
      )
    }
    return (
      <Suspense fallback={<p className="text-muted-foreground text-sm">{t('common.loading')}</p>}>
        <CodeEditor
          key={editorKey}
          value={file.data.content}
          language={editorLanguage(name)}
          label={t('files.editor.label', { name })}
          onChange={setDraft}
          onSave={doSave}
        />
      </Suspense>
    )
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <Button type="button" variant="outline" size="sm" onClick={() => (dirty ? setConfirmClose(true) : onClose())}>
          <ArrowLeft aria-hidden="true" />
          {t('files.editor.back')}
        </Button>
        <p className="min-w-0 flex-1 truncate font-mono text-sm" title={path}>
          {path}
        </p>
        {dirty ? <Badge variant="outline">{t('files.editor.unsaved')}</Badge> : null}
        <Button type="button" size="sm" disabled={!dirty || save.isPending} onClick={doSave}>
          {save.isPending ? t('files.editor.saving') : t('common.save')}
        </Button>
      </div>
      {path === 'server.properties' ? (
        <div role="note" className="flex flex-wrap items-center gap-2 rounded-md border p-3 text-sm">
          <span>{t('files.editor.properties_hint')}</span>
          {onOpenSettings ? (
            <Button type="button" variant="link" className="h-auto p-0" onClick={onOpenSettings}>
              {t('files.editor.open_settings')}
            </Button>
          ) : null}
        </div>
      ) : null}
      {body()}

      <Dialog open={conflict} onOpenChange={setConflict}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t('files.editor.conflict_title')}</DialogTitle>
            <DialogDescription>{t('files.editor.conflict_description')}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setConflict(false)}>
              {t('common.cancel')}
            </Button>
            <Button type="button" variant="outline" onClick={() => void reload()}>
              {t('files.editor.reload')}
            </Button>
            <Button type="button" variant="destructive" onClick={() => void overwrite()}>
              {t('files.editor.overwrite')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <ConfirmDialog
        open={confirmClose}
        onOpenChange={setConfirmClose}
        title={t('files.editor.discard_title')}
        description={t('files.editor.discard_description', { name })}
        confirmLabel={t('files.editor.discard')}
        destructive
        onConfirm={() => {
          setConfirmClose(false)
          setDraft(null)
          onClose()
        }}
      />
      <ConfirmDialog
        open={blocker.status === 'blocked'}
        onOpenChange={(open) => (open ? undefined : blocker.reset?.())}
        title={t('files.editor.discard_title')}
        description={t('files.editor.discard_description', { name })}
        confirmLabel={t('files.editor.discard')}
        destructive
        onConfirm={() => blocker.proceed?.()}
      />
    </div>
  )
}
