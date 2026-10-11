import { useQueryClient } from '@tanstack/react-query'
import { useId, useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import type { FileJobResponse, Server } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { errorMessage } from '@/lib/error-message'
import { extractArchive, fileJobKey } from './api'
import { baseName, joinPath, parentPath, stripZip, targetProblem } from './path'

/** Extracting only while the server is stopped (spec §6.3); the panel checks again (409 instance_running). */
export function ExtractDialog({ server, path, onClose }: { server: Server; path: string; onClose(): void }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const id = useId()
  const name = baseName(path)
  const [dest, setDest] = useState(joinPath(parentPath(path), stripZip(name)))
  const [message, setMessage] = useState<string | null>(null)
  const [pending, setPending] = useState(false)
  const stopped = server.status.state === 'stopped'

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!stopped || pending) return
    const problem = targetProblem(dest)
    if (problem) {
      setMessage(t(`files.validation.${problem}`))
      return
    }
    setMessage(null)
    setPending(true)
    try {
      const job = await extractArchive(server.id, path, dest)
      queryClient.setQueryData<FileJobResponse>(fileJobKey(server.id), { job }) // starts the 2-s polling
      onClose()
    } catch (err) {
      setMessage(errorMessage(err, t))
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => (open ? undefined : onClose())}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={(event) => void submit(event)} className="space-y-4">
          <DialogHeader>
            <DialogTitle>{t('files.extract.title', { name })}</DialogTitle>
            <DialogDescription>{t('files.extract.dest_hint')}</DialogDescription>
          </DialogHeader>
          {!stopped ? (
            <p role="status" className="rounded-md border p-3 text-sm">
              {t('files.extract.stopped_only')}
            </p>
          ) : null}
          <div className="space-y-2">
            <Label htmlFor={id}>{t('files.extract.dest')}</Label>
            <Input id={id} value={dest} onChange={(e) => setDest(e.target.value)} autoComplete="off" spellCheck={false} disabled={!stopped} />
            {message ? (
              <p role="alert" className="text-destructive text-sm">
                {message}
              </p>
            ) : null}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" disabled={!stopped || pending}>
              {t('files.extract.submit')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
