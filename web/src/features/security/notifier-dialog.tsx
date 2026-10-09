import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { Notifier, NotifierKind } from '@/api/types'
import { NativeSelect } from '@/components/native-select'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { errorMessage } from '@/lib/error-message'
import { notifiersKey } from './security-key'

const HTTP_URL = /^https?:\/\/\S+$/

interface UpdateNotifierBody {
  name?: string
  enabled?: boolean
  url?: string
  topic?: string
  token?: string
}

/** Mounted per dialog opening (keyed), so the initial state comes straight from props. */
function NotifierForm({ notifier, onClose }: { notifier: Notifier | null; onClose(): void }) {
  const { t } = useTranslation()
  const id = useId()
  const queryClient = useQueryClient()
  const editing = notifier !== null
  const [kind, setKind] = useState<NotifierKind>(notifier?.kind ?? 'discord')
  const [name, setName] = useState(notifier?.name ?? '')
  const [enabled, setEnabled] = useState(notifier?.enabled ?? true)
  const [url, setUrl] = useState('')
  const [topic, setTopic] = useState('')
  const [token, setToken] = useState('')
  const trimmedName = name.trim()
  const urlInvalid = url !== '' && !HTTP_URL.test(url)
  const complete =
    trimmedName.length >= 1 && trimmedName.length <= 64 && !urlInvalid && (editing || (url !== '' && (kind === 'discord' || topic.trim() !== '')))

  const submit = useMutation({
    mutationFn: () => {
      if (notifier) {
        const body: UpdateNotifierBody = { name: trimmedName, enabled }
        if (url) body.url = url
        if (topic.trim()) body.topic = topic.trim()
        if (token) body.token = token
        return unwrap(api.PATCH('/security/notifiers/{id}', { params: { path: { id: notifier.id } }, body }))
      }
      return unwrap(
        api.POST('/security/notifiers', {
          body: kind === 'ntfy' ? { kind, name: trimmedName, enabled, url, topic: topic.trim(), ...(token ? { token } : {}) } : { kind, name: trimmedName, enabled, url },
        }),
      )
    },
    onSuccess: (saved) => {
      toast.success(editing ? t('security.notifiers.updated', { name: saved.name }) : t('security.notifiers.created', { name: saved.name }))
      void queryClient.invalidateQueries({ queryKey: notifiersKey })
      onClose()
    },
  })

  return (
    <form
      className="space-y-4"
      noValidate
      onSubmit={(e) => {
        e.preventDefault()
        if (complete) submit.mutate()
      }}
    >
      <div className="space-y-2">
        <Label htmlFor={`${id}-kind`}>{t('security.notifiers.kind')}</Label>
        <NativeSelect id={`${id}-kind`} className="w-full" value={kind} disabled={editing} onChange={(e) => setKind(e.target.value as NotifierKind)}>
          <option value="discord">{t('security.notifiers.kind_discord')}</option>
          <option value="ntfy">{t('security.notifiers.kind_ntfy')}</option>
        </NativeSelect>
      </div>
      <div className="space-y-2">
        <Label htmlFor={`${id}-name`}>{t('security.notifiers.name')}</Label>
        <Input id={`${id}-name`} value={name} maxLength={64} onChange={(e) => setName(e.target.value)} />
      </div>
      <div className="flex items-center gap-2">
        <Switch id={`${id}-enabled`} checked={enabled} onCheckedChange={setEnabled} />
        <Label htmlFor={`${id}-enabled`}>{t('security.notifiers.enabled')}</Label>
      </div>
      <div className="space-y-2">
        <Label htmlFor={`${id}-url`}>{t(`security.notifiers.url_${kind}`)}</Label>
        <Input
          id={`${id}-url`}
          type="url"
          inputMode="url"
          value={url}
          autoComplete="off"
          spellCheck={false}
          aria-invalid={urlInvalid || undefined}
          onChange={(e) => setUrl(e.target.value)}
        />
        {urlInvalid ? <p className="text-destructive text-sm">{t('security.notifiers.url_invalid')}</p> : null}
        {editing ? <p className="text-muted-foreground text-sm">{t('security.notifiers.url_keep')}</p> : null}
      </div>
      {kind === 'ntfy' ? (
        <>
          <div className="space-y-2">
            <Label htmlFor={`${id}-topic`}>{t('security.notifiers.topic')}</Label>
            <Input id={`${id}-topic`} value={topic} autoComplete="off" spellCheck={false} onChange={(e) => setTopic(e.target.value)} />
            {!editing ? <p className="text-muted-foreground text-sm">{t('security.notifiers.topic_required')}</p> : null}
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${id}-token`}>{t('security.notifiers.token')}</Label>
            <Input id={`${id}-token`} type="password" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value)} />
          </div>
        </>
      ) : null}
      {submit.error ? (
        <p role="alert" className="text-destructive text-sm">
          {errorMessage(submit.error, t)}
        </p>
      ) : null}
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onClose}>
          {t('common.cancel')}
        </Button>
        <Button type="submit" disabled={submit.isPending || !complete}>
          {editing ? t('common.save') : t('common.create')}
        </Button>
      </DialogFooter>
    </form>
  )
}

export function NotifierDialog({ open, onOpenChange, notifier }: { open: boolean; onOpenChange(open: boolean): void; notifier: Notifier | null }) {
  const { t } = useTranslation()
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{notifier ? t('security.notifiers.edit_title') : t('security.notifiers.create')}</DialogTitle>
          <DialogDescription>{t('security.notifiers.create_description')}</DialogDescription>
        </DialogHeader>
        {open ? <NotifierForm key={notifier?.id ?? 'new'} notifier={notifier} onClose={() => onOpenChange(false)} /> : null}
      </DialogContent>
    </Dialog>
  )
}
