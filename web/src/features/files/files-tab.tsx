import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowDownWideNarrow, ArrowUpNarrowWide, FilePlus, FolderPlus } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { isApiError } from '@/api/errors'
import type { FileEntry, Server } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { NativeSelect } from '@/components/native-select'
import { Button } from '@/components/ui/button'
import { SectionCard } from '@/features/profile/section-card'
import { errorMessage, useErrorToast } from '@/lib/error-message'
import { deleteFile, fileListQueryOptions, fileListsKey, makeDirectory, renameFile, writeFile } from './api'
import { FileList } from './file-list'
import { sortEntries, type SortKey } from './format'
import { NameDialog } from './name-dialog'
import { breadcrumbs, componentProblem, joinPath, targetProblem } from './path'

type DialogState = { kind: 'mkdir' } | { kind: 'newfile' } | { kind: 'rename'; entry: FileEntry } | { kind: 'delete'; entry: FileEntry }

const SORT_KEYS: SortKey[] = ['name', 'size', 'mtime']

export interface FilesTabProps {
  server: Server
  /** Admins: link from the server.properties hint to the settings tab (Task 15). */
  onOpenSettings?: () => void
  /** Reports unsaved editor changes to the detail page (Task 15). */
  onDirtyChange?: (dirty: boolean) => void
}

export function FilesTab({ server }: FilesTabProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const [dir, setDir] = useState('')
  const [sortKey, setSortKey] = useState<SortKey>('name')
  const [descending, setDescending] = useState(false)
  const [dialog, setDialog] = useState<DialogState | null>(null)
  const list = useQuery(fileListQueryOptions(server.id, dir))
  const running = server.status.state === 'running'
  const dirLabel = dir || t('files.root')

  const refresh = () => queryClient.invalidateQueries({ queryKey: fileListsKey(server.id) })

  const remove = useMutation({
    mutationFn: (entry: FileEntry) => deleteFile(server.id, joinPath(dir, entry.name)),
    onSuccess: (_data, entry) => {
      toast.success(t('files.done.delete', { name: entry.name }))
      setDialog(null)
      void refresh()
    },
    onError: notifyError,
  })

  async function createFolder(name: string) {
    await makeDirectory(server.id, joinPath(dir, name))
    toast.success(t('files.done.mkdir', { name }))
    setDialog(null)
    await refresh()
  }

  async function createFile(name: string) {
    await writeFile(server.id, { path: joinPath(dir, name), content: '', expected_mtime: null })
    toast.success(t('files.done.newfile', { name }))
    setDialog(null)
    await refresh()
  }

  async function rename(entry: FileEntry, to: string) {
    const from = joinPath(dir, entry.name)
    await renameFile(server.id, from, to)
    toast.success(t('files.done.rename', { from, to }))
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
          onRename={(entry) => setDialog({ kind: 'rename', entry })}
          onDelete={(entry) => setDialog({ kind: 'delete', entry })}
        />
        {list.data.truncated ? (
          <p className="text-muted-foreground text-sm">{t('files.truncated', { count: list.data.entries.length })}</p>
        ) : null}
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
    </>
  )

  return (
    <div className="space-y-4">
      {running ? (
        <p role="status" className="rounded-md border p-3 text-sm">
          {t('files.running_hint')}
        </p>
      ) : null}

      <SectionCard title={t('files.title')} actions={toolbar}>
        <div className="space-y-3">
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
                        {crumb.name}
                      </span>
                    ) : (
                      <Button type="button" variant="link" className="h-auto p-0" onClick={() => setDir(crumb.path)}>
                        {crumb.name}
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
          {body()}
        </div>
      </SectionCard>

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
          title={t('files.dialog.rename_title', { name: dialog.entry.name })}
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
      {dialog?.kind === 'delete' ? (
        <ConfirmDialog
          open
          onOpenChange={(open) => (open ? undefined : setDialog(null))}
          title={t('files.dialog.delete_title', { name: dialog.entry.name })}
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
