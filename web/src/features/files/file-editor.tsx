import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowLeft } from 'lucide-react'
import { lazy, Suspense, useEffect, useState, type KeyboardEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { isApiError, isSudoCancelled } from '@/api/errors'
import type { FileContent, Server } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { FileName } from '@/components/file-name'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { useMe } from '@/features/auth/me'
import { errorMessage } from '@/lib/error-message'
import { visibleName } from '@/lib/file-name'
import { downloadUrl, fileContentKey, fileListsKey, readFile, writeFile } from './api'
import { editorLanguage } from './format'
import { baseName } from './path'
import { ServerSettingsLink } from './settings-link'

const CodeEditor = lazy(() => import('./code-editor'))

export interface FileEditorProps {
  server: Server
  path: string
  onClose(): void
  onDirtyChange?(dirty: boolean): void
}

export function FileEditor({ server, path, onClose, onDirtyChange }: FileEditorProps) {
  const { t } = useTranslation()
  const isAdmin = useMe()?.role === 'admin'
  const queryClient = useQueryClient()
  // The panel limits editor reads and saves (per account and in total): say so instead of "too many attempts".
  const message = (err: unknown) => (isApiError(err, 'rate_limited') ? t('files.editor.busy') : errorMessage(err, t))
  const notifyError = (err: unknown) => {
    if (!isSudoCancelled(err)) toast.error(message(err))
  }
  const contentKey = fileContentKey(server.id, path)
  const name = baseName(path)
  const file = useQuery({
    queryKey: contentKey,
    queryFn: () => readFile(server.id, path),
    // The loaded version (and mtime) is the base for conflict detection: never refresh it behind the user's back.
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

  // Route changes and reloads are asked by the files area (useBlocker in FilesTab, fed by onDirtyChange).

  const save = useMutation({
    // The version token is the stronger base (it changes with every save, also within one millisecond); the mtime
    // goes along for servers that answer without one.
    mutationFn: ({ content, base }: { content: string; base: Pick<FileContent, 'mtime' | 'version'> }) =>
      writeFile(server.id, { path, content, expected_mtime: base.mtime, ...(base.version ? { expected_version: base.version } : {}) }),
    onSuccess: (result, { content }) => {
      queryClient.setQueryData<FileContent>(contentKey, { path, content, mtime: result.mtime, size: result.size, version: result.version })
      // Text typed while the request ran stays a draft.
      setDraft((current) => (current === content ? null : current))
      toast.success(t('files.editor.saved'))
      void queryClient.invalidateQueries({ queryKey: fileListsKey(server.id) })
    },
    onError: (err) => (isApiError(err, 'file_modified') ? setConflict(true) : notifyError(err)),
  })

  function doSave() {
    if (!file.data || draft === null || !dirty || save.isPending) return
    save.mutate({ content: draft, base: file.data })
  }

  /** Ctrl/Cmd+S anywhere in the editor area, also while a button has the focus (CodeMirror handles its own). */
  function onKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (event.defaultPrevented || !(event.ctrlKey || event.metaKey) || event.key.toLowerCase() !== 's') return
    event.preventDefault()
    doSave()
  }

  async function overwrite() {
    setConflict(false)
    if (draft === null) return
    try {
      const current = await readFile(server.id, path)
      save.mutate({ content: draft, base: current })
    } catch (err) {
      notifyError(err)
    }
  }

  async function reload() {
    setConflict(false)
    try {
      await queryClient.fetchQuery({ queryKey: contentKey, queryFn: () => readFile(server.id, path), staleTime: 0 })
    } catch (err) {
      notifyError(err)
      return // keep the draft: nothing was reloaded
    }
    setDraft(null)
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
          {message(file.error)}
        </p>
      )
    }
    return (
      <Suspense fallback={<p className="text-muted-foreground text-sm">{t('common.loading')}</p>}>
        <CodeEditor
          key={editorKey}
          value={file.data.content}
          language={editorLanguage(name)}
          label={t('files.editor.label', { name: visibleName(name) })}
          onChange={setDraft}
          onSave={doSave}
        />
      </Suspense>
    )
  }

  return (
    <div className="space-y-3" onKeyDown={onKeyDown}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <Button type="button" variant="outline" size="sm" onClick={() => (dirty ? setConfirmClose(true) : onClose())}>
          <ArrowLeft aria-hidden="true" />
          {t('files.editor.back')}
        </Button>
        <p className="min-w-0 flex-1 truncate font-mono text-sm" title={visibleName(path)}>
          <FileName name={path} />
        </p>
        {dirty ? <Badge variant="outline">{t('files.editor.unsaved')}</Badge> : null}
        <Button type="button" size="sm" disabled={!dirty || save.isPending} onClick={doSave}>
          {save.isPending ? t('files.editor.saving') : t('common.save')}
        </Button>
      </div>
      {path === 'server.properties' ? (
        <div role="note" className="bevel-inset flex flex-wrap items-center gap-2 px-3 py-2 text-sm">
          <span>{t('files.editor.properties_hint')}</span>
          {isAdmin ? <ServerSettingsLink serverId={server.id}>{t('files.editor.open_settings')}</ServerSettingsLink> : null}
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
        description={t('files.editor.discard_description', { name: visibleName(name) })}
        confirmLabel={t('files.editor.discard')}
        destructive
        onConfirm={() => {
          setConfirmClose(false)
          setDraft(null)
          onClose()
        }}
      />
    </div>
  )
}
