import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useBlocker } from '@tanstack/react-router'
import { ArrowDownWideNarrow, ArrowUpNarrowWide, FilePlus, FolderPlus, Upload } from 'lucide-react'
import { useCallback, useEffect, useRef, useState, type DragEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { isApiError } from '@/api/errors'
import type { FileEntry, FileList as FileListing, Server } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { FileName } from '@/components/file-name'
import { NativeSelect } from '@/components/native-select'
import { Button } from '@/components/ui/button'
import { Section } from '@/components/section'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { errorMessage, useErrorToast } from '@/lib/error-message'
import { visibleName } from '@/lib/file-name'
import { useMediaQuery } from '@/lib/use-media-query'
import { cn } from '@/lib/utils'
import { deleteFile, fileListKey, fileListQueryOptions, fileListsKey, makeDirectory, renameFile, writeFile } from './api'
import { leaveTexts, type FilesBusy } from './busy'
import { ExtractDialog } from './extract-dialog'
import { FileEditor } from './file-editor'
import { FileList } from './file-list'
import { extractFailureReason, sortEntries, type SortKey } from './format'
import { JobStatus } from './job-status'
import { NameDialog } from './name-dialog'
import { useFileJob } from './use-file-job'
import { UploadList } from './upload-list'
import { useUploadQueue } from './upload-queue'
import { baseName, breadcrumbs, cleanPath, componentProblem, joinPath, parentPath, targetProblem } from './path'

type DialogState = { kind: 'mkdir' } | { kind: 'newfile' } | { kind: 'rename'; entry: FileEntry } | { kind: 'delete'; entry: FileEntry } | { kind: 'extract'; path: string }

const SORT_KEYS: SortKey[] = ['name', 'size', 'mtime']

/** Tailwind xl: list and editor side by side. */
export const WIDE_QUERY = '(min-width: 1280px)'

export interface FilesTabProps {
  server: Server
}

export function FilesTab({ server }: FilesTabProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const [dir, setDir] = useState('')
  const [editing, setEditing] = useState<string | null>(null)
  const [sortKey, setSortKey] = useState<SortKey>('name')
  const [descending, setDescending] = useState(false)
  const [dialog, setDialog] = useState<DialogState | null>(null)
  const list = useQuery(fileListQueryOptions(server.id, dir))
  const running = server.status.state === 'running'
  const dirLabel = dir ? visibleName(dir) : t('files.root')

  const refresh = useCallback(() => queryClient.invalidateQueries({ queryKey: fileListsKey(server.id) }), [queryClient, server.id])
  // A file the cached listing already shows is asked about before it is sent (review F2).
  const exists = useCallback(
    (path: string) =>
      queryClient.getQueryData<FileListing>(fileListKey(server.id, parentPath(path)))?.entries.some((e) => e.name === baseName(path) && e.type === 'file') ??
      false,
    [queryClient, server.id],
  )
  const uploads = useUploadQueue(server.id, { onUploaded: () => void refresh(), exists })
  const [editorDirty, setEditorDirty] = useState(false)
  const unsaved = editorDirty && editing !== null
  const wide = useMediaQuery(WIDE_QUERY)
  const [pendingOpen, setPendingOpen] = useState<string | null>(null)
  /** Opening another file would drop the editor's draft: ask first (two panes from xl keep the list clickable). */
  function openFile(path: string) {
    if (path === editing) return
    if (unsaved) setPendingOpen(path)
    else setEditing(path)
  }
  const pendingUploads = uploads.active
  const busy: FilesBusy | null = unsaved || pendingUploads ? { unsaved, uploads: pendingUploads } : null
  // The only guard of the files area: any navigation away (other area, other server, logout link) and reloads. Leaving cancels the uploads.
  const blocker = useBlocker({ shouldBlockFn: () => busy !== null, enableBeforeUnload: () => busy !== null, withResolver: true })
  const leave = leaveTexts(busy ?? { unsaved: true, uploads: false }, t)
  const jobQuery = useFileJob(server.id, true)
  const job = jobQuery.data?.job ?? null
  const lastState = useRef(job?.state)
  useEffect(() => {
    const previous = lastState.current
    lastState.current = job?.state
    if (previous !== 'running' || !job || job.state === 'running') return
    if (job.state === 'done') toast.success(t('files.extract.done', { name: visibleName(baseName(job.path)), dest: visibleName(job.dest) }))
    else {
      const reason = t(`files.extract.reason.${extractFailureReason(job.error)}`)
      toast.error(t('files.extract.failed', { name: visibleName(baseName(job.path)), reason }), { description: job.error || undefined })
    }
    void refresh()
  }, [job, refresh, t])
  const fileInput = useRef<HTMLInputElement>(null)
  const [dragging, setDragging] = useState(false)
  const conflict = uploads.items.find((i) => i.status === 'conflict')

  const [notice, setNotice] = useState<string | null>(null)

  // A drop beside the drop zone would make the browser navigate to the file and leave the panel.
  useEffect(() => {
    const stop = (event: globalThis.DragEvent) => {
      if (event.dataTransfer?.types.includes('Files')) event.preventDefault()
    }
    window.addEventListener('dragover', stop)
    window.addEventListener('drop', stop)
    return () => {
      window.removeEventListener('dragover', stop)
      window.removeEventListener('drop', stop)
    }
  }, [])

  // No data directory yet: there is nothing to create files in (review F10).
  const neverStarted = list.isError && isApiError(list.error, 'not_found') && dir === ''

  function onDrop(event: DragEvent<HTMLDivElement>) {
    event.preventDefault()
    setDragging(false)
    if (neverStarted) return
    // Folders cannot be uploaded (one file per request); name them instead of silently dropping them.
    const dropped: File[] = []
    let folders = 0
    const items = Array.from(event.dataTransfer.items ?? [])
    if (items.length > 0) {
      for (const item of items) {
        if (item.kind !== 'file') continue
        if (item.webkitGetAsEntry?.()?.isDirectory) {
          folders += 1
          continue
        }
        const file = item.getAsFile()
        if (file) dropped.push(file)
      }
    } else {
      dropped.push(...Array.from(event.dataTransfer.files))
    }
    setNotice(folders > 0 ? t('files.toolbar.folders_unsupported') : null)
    if (dropped.length > 0) uploads.add(dropped, dir)
  }

  const remove = useMutation({
    mutationFn: (entry: FileEntry) => deleteFile(server.id, joinPath(dir, entry.name)),
    onSuccess: (_data, entry) => {
      toast.success(t('files.done.delete', { name: visibleName(entry.name) }))
      setDialog(null)
      void refresh()
    },
    onError: notifyError,
  })

  async function createFolder(name: string) {
    await makeDirectory(server.id, joinPath(dir, name))
    toast.success(t('files.done.mkdir', { name: visibleName(name) }))
    setDialog(null)
    await refresh()
  }

  async function createFile(name: string) {
    await writeFile(server.id, { path: joinPath(dir, name), content: '', expected_mtime: null })
    toast.success(t('files.done.newfile', { name: visibleName(name) }))
    setDialog(null)
    await refresh()
    setEditing(joinPath(dir, name))
  }

  async function rename(entry: FileEntry, input: string) {
    const from = joinPath(dir, entry.name)
    // Send the normalised path (one trailing slash dropped), exactly what the service validates.
    const cleaned = cleanPath(input)
    const to = 'path' in cleaned ? cleaned.path : input
    if (to === from) {
      setDialog(null)
      return
    }
    await renameFile(server.id, from, to)
    toast.success(t('files.done.rename', { from: visibleName(from), to: visibleName(to) }))
    setDialog(null)
    await refresh()
  }

  function body() {
    if (list.isPending) return <p className="text-muted-foreground text-sm">{t('common.loading')}</p>
    if (list.isError) {
      if (isApiError(list.error, 'not_found') && dir === '') return <p className="text-muted-foreground text-sm">{t('files.never_started')}</p>
      if (isApiError(list.error, 'not_found')) {
        return (
          <div className="space-y-2">
            <p className="text-muted-foreground text-sm">{t('files.dir_gone')}</p>
            <Button type="button" variant="outline" size="sm" onClick={() => setDir('')}>
              {t('files.back_to_root')}
            </Button>
          </div>
        )
      }
      return (
        <p role="alert" className="text-destructive text-sm">
          {errorMessage(list.error, t)}
        </p>
      )
    }
    return (
      <div className="space-y-2">
        <FileList
          serverId={server.id}
          dir={dir}
          entries={sortEntries(list.data.entries, sortKey, descending)}
          onOpenDir={(name) => setDir(joinPath(dir, name))}
          onExtract={(entry) => setDialog({ kind: 'extract', path: joinPath(dir, entry.name) })}
          onOpenFile={(entry) => openFile(joinPath(dir, entry.name))}
          onRename={(entry) => setDialog({ kind: 'rename', entry })}
          onDelete={(entry) => setDialog({ kind: 'delete', entry })}
        />
        {list.data.truncated ? (
          <p className="text-muted-foreground text-sm">{t('files.truncated', { count: list.data.entries.length })}</p>
        ) : null}
      </div>
    )
  }

  /** Path, sorting and the listing; also the drop zone for uploads. */
  function browser() {
    return (
      <div
        className={cn('relative flex flex-col gap-3', dragging && 'ring-primary ring-2')}
        onDragOver={(event) => {
          if (!event.dataTransfer.types.includes('Files') || neverStarted) return
          event.preventDefault()
          setDragging(true)
        }}
        onDragLeave={(event) => {
          // Moving over a child fires dragleave on the zone too: only a real exit clears the highlight.
          if (event.currentTarget.contains(event.relatedTarget as Node | null)) return
          setDragging(false)
        }}
        onDrop={onDrop}
      >
        {dragging ? (
          <p className="bg-background/90 pointer-events-none absolute inset-0 z-10 flex items-center justify-center text-sm font-medium">
            {t('files.toolbar.drop', { dir: dirLabel })}
          </p>
        ) : null}
        <div className="flex flex-wrap items-center justify-between gap-2">
          <nav aria-label={t('files.breadcrumbs')} className="min-w-0">
            <ol className="flex flex-wrap items-center gap-1 text-sm">
              <li>
                {dir === '' ? (
                  <span aria-current="page" className="font-medium">
                    {t('files.root')}
                  </span>
                ) : (
                  <Button type="button" variant="link" className="h-auto p-0" onClick={() => setDir('')}>
                    {t('files.root')}
                  </Button>
                )}
              </li>
              {breadcrumbs(dir).map((crumb, i, all) => (
                <li key={crumb.path} className="flex items-center gap-1">
                  <span aria-hidden="true" className="text-muted-foreground">
                    /
                  </span>
                  {i === all.length - 1 ? (
                    <span aria-current="page" className="font-medium break-all">
                      <FileName name={crumb.name} />
                    </span>
                  ) : (
                    <Button type="button" variant="link" className="h-auto p-0" onClick={() => setDir(crumb.path)}>
                      <FileName name={crumb.name} />
                    </Button>
                  )}
                </li>
              ))}
            </ol>
          </nav>
          <div className="flex items-center gap-2">
            <label className="sr-only" htmlFor={`files-sort-${server.id}`}>
              {t('files.sort.label')}
            </label>
            <NativeSelect id={`files-sort-${server.id}`} value={sortKey} onChange={(e) => setSortKey(e.target.value as SortKey)}>
              {SORT_KEYS.map((key) => (
                <option key={key} value={key}>
                  {t(`files.sort.${key}`)}
                </option>
              ))}
            </NativeSelect>
            <Button
              type="button"
              variant="outline"
              size="icon-sm"
              aria-label={descending ? t('files.sort.descending') : t('files.sort.ascending')}
              onClick={() => setDescending((d) => !d)}
            >
              {descending ? <ArrowDownWideNarrow aria-hidden="true" /> : <ArrowUpNarrowWide aria-hidden="true" />}
            </Button>
          </div>
        </div>
        {notice ? (
          <p role="alert" className="text-destructive text-sm">
            {notice}
          </p>
        ) : null}
        {body()}
      </div>
    )
  }

  const toolbar = (
    <>
      <Button type="button" variant="outline" size="sm" onClick={() => setDialog({ kind: 'mkdir' })}>
        <FolderPlus aria-hidden="true" />
        {t('files.toolbar.new_folder')}
      </Button>
      <Button type="button" variant="outline" size="sm" onClick={() => setDialog({ kind: 'newfile' })}>
        <FilePlus aria-hidden="true" />
        {t('files.toolbar.new_file')}
      </Button>
      <Button type="button" size="sm" onClick={() => fileInput.current?.click()}>
        <Upload aria-hidden="true" />
        {t('files.toolbar.upload')}
      </Button>
      <input
        ref={fileInput}
        type="file"
        multiple
        className="sr-only"
        tabIndex={-1}
        aria-label={t('files.toolbar.upload_input')}
        onChange={(e) => {
          uploads.add(Array.from(e.target.files ?? []), dir)
          e.target.value = '' // the same file can be chosen again
        }}
      />
    </>
  )

  return (
    <div className="flex flex-col gap-6">
      {running ? (
        <p role="status" className="bevel-inset px-3 py-2 text-sm">
          {t('files.running_hint')}
        </p>
      ) : null}

      {job?.state === 'running' ? <JobStatus job={job} /> : null}

      <Section title={t('files.title')} actions={(editing === null || wide) && !neverStarted ? toolbar : undefined}>
        {editing === null ? (
          browser()
        ) : wide ? (
          <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)] gap-6">
            <div className="min-w-0">{browser()}</div>
            <div className="min-w-0 border-l pl-6">
              <FileEditor key={editing} server={server} path={editing} onClose={() => setEditing(null)} onDirtyChange={setEditorDirty} />
            </div>
          </div>
        ) : (
          <FileEditor key={editing} server={server} path={editing} onClose={() => setEditing(null)} onDirtyChange={setEditorDirty} />
        )}
      </Section>

      <UploadList items={uploads.items} onCancel={uploads.cancel} onClear={uploads.clear} />

      {conflict ? (
        <Dialog open onOpenChange={(open) => (open ? undefined : uploads.skip(conflict.id))}>
          <DialogContent className="sm:max-w-md">
            <DialogHeader>
              <DialogTitle>{t('files.uploads.conflict_title', { name: visibleName(conflict.name) })}</DialogTitle>
              <DialogDescription>{t('files.uploads.conflict_description')}</DialogDescription>
            </DialogHeader>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => uploads.skip(conflict.id)}>
                {t('files.uploads.skip')}
              </Button>
              <Button type="button" variant="destructive" onClick={() => uploads.overwrite(conflict.id)}>
                {t('files.uploads.overwrite')}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      ) : null}
      {dialog?.kind === 'extract' ? <ExtractDialog server={server} path={dialog.path} onClose={() => setDialog(null)} /> : null}
      {dialog?.kind === 'mkdir' ? (
        <NameDialog
          title={t('files.dialog.new_folder_title', { dir: dirLabel })}
          label={t('files.dialog.name')}
          initial=""
          submitLabel={t('files.dialog.create')}
          validate={(name) => {
            const p = componentProblem(name, dir)
            return p ? `files.validation.${p}` : null
          }}
          onClose={() => setDialog(null)}
          onSubmit={createFolder}
        />
      ) : null}
      {dialog?.kind === 'newfile' ? (
        <NameDialog
          title={t('files.dialog.new_file_title', { dir: dirLabel })}
          label={t('files.dialog.name')}
          initial=""
          submitLabel={t('files.dialog.create')}
          validate={(name) => {
            const p = componentProblem(name, dir)
            return p ? `files.validation.${p}` : null
          }}
          onClose={() => setDialog(null)}
          onSubmit={createFile}
        />
      ) : null}
      {dialog?.kind === 'rename' ? (
        <NameDialog
          title={t('files.dialog.rename_title', { name: visibleName(dialog.entry.name) })}
          label={t('files.dialog.target')}
          hint={t('files.dialog.target_hint')}
          initial={joinPath(dir, dialog.entry.name)}
          submitLabel={t('files.dialog.rename_submit')}
          validate={(to) => {
            const p = targetProblem(to)
            return p ? `files.validation.${p}` : null
          }}
          onClose={() => setDialog(null)}
          onSubmit={(to) => rename(dialog.entry, to)}
        />
      ) : null}
      <ConfirmDialog
        open={pendingOpen !== null}
        onOpenChange={(open) => (open ? undefined : setPendingOpen(null))}
        title={t('files.editor.discard_title')}
        description={t('files.editor.discard_description', { name: editing ? visibleName(baseName(editing)) : '' })}
        confirmLabel={t('files.editor.discard')}
        destructive
        onConfirm={() => {
          setEditing(pendingOpen)
          setEditorDirty(false)
          setPendingOpen(null)
        }}
      />
      <ConfirmDialog
        open={blocker.status === 'blocked'}
        onOpenChange={(open) => (open ? undefined : blocker.reset?.())}
        title={leave.title}
        description={leave.description}
        confirmLabel={leave.confirm}
        destructive
        onConfirm={() => blocker.proceed?.()}
      />
      {dialog?.kind === 'delete' ? (
        <ConfirmDialog
          open
          onOpenChange={(open) => (open ? undefined : setDialog(null))}
          title={t('files.dialog.delete_title', { name: visibleName(dialog.entry.name) })}
          description={dialog.entry.type === 'dir' ? t('files.dialog.delete_dir') : t('files.dialog.delete_file')}
          confirmLabel={t('files.dialog.delete_submit')}
          destructive
          pending={remove.isPending}
          onConfirm={() => remove.mutate(dialog.entry)}
        />
      ) : null}
    </div>
  )
}
