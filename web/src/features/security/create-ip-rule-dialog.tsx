import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { IPRuleKind } from '@/api/types'
import { NativeSelect } from '@/components/native-select'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { errorMessage } from '@/lib/error-message'

const CIDR = /^[0-9a-fA-F:.]+\/\d{1,3}$/

export function CreateIpRuleDialog({ open, onOpenChange }: { open: boolean; onOpenChange(open: boolean): void }) {
  const { t } = useTranslation()
  const id = useId()
  const queryClient = useQueryClient()
  const [cidr, setCidr] = useState('')
  const [kind, setKind] = useState<IPRuleKind>('allow')
  const [comment, setComment] = useState('')
  const [expires, setExpires] = useState('')
  const trimmed = cidr.trim()
  const cidrInvalid = trimmed !== '' && !CIDR.test(trimmed)
  const complete = trimmed !== '' && !cidrInvalid

  const create = useMutation({
    mutationFn: () =>
      unwrap(
        api.POST('/security/ip-rules', {
          body: { cidr: trimmed, kind, comment: comment.trim(), expires_at: expires ? new Date(expires).getTime() : null },
        }),
      ),
    onSuccess: (rule) => {
      toast.success(t('security.ip_rules.created', { cidr: rule.cidr }))
      setCidr('')
      setComment('')
      setExpires('')
      setKind('allow')
      onOpenChange(false)
      void queryClient.invalidateQueries({ queryKey: ['security', 'ip-rules'] })
    },
  })

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) create.reset()
        onOpenChange(next)
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('security.ip_rules.create')}</DialogTitle>
          <DialogDescription>{t('security.ip_rules.create_description')}</DialogDescription>
        </DialogHeader>
        <form
          className="space-y-4"
          noValidate
          onSubmit={(e) => {
            e.preventDefault()
            if (complete) create.mutate()
          }}
        >
          <div className="space-y-2">
            <Label htmlFor={`${id}-cidr`}>{t('security.ip_rules.cidr')}</Label>
            <Input
              id={`${id}-cidr`}
              value={cidr}
              onChange={(e) => setCidr(e.target.value)}
              autoComplete="off"
              spellCheck={false}
              aria-invalid={cidrInvalid || undefined}
            />
            {cidrInvalid ? <p className="text-destructive text-sm">{t('security.ip_rules.cidr_invalid')}</p> : null}
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${id}-kind`}>{t('security.ip_rules.kind')}</Label>
            <NativeSelect id={`${id}-kind`} className="w-full" value={kind} onChange={(e) => setKind(e.target.value as IPRuleKind)}>
              <option value="allow">{t('security.ip_rules.kind_allow')}</option>
              <option value="deny">{t('security.ip_rules.kind_deny')}</option>
            </NativeSelect>
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${id}-comment`}>{t('security.ip_rules.comment')}</Label>
            <Input id={`${id}-comment`} value={comment} maxLength={200} onChange={(e) => setComment(e.target.value)} />
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${id}-expires`}>{t('security.ip_rules.expires')}</Label>
            <Input id={`${id}-expires`} type="datetime-local" value={expires} onChange={(e) => setExpires(e.target.value)} />
          </div>
          {create.error ? (
            <p role="alert" className="text-destructive text-sm">
              {errorMessage(create.error, t)}
            </p>
          ) : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" disabled={create.isPending || !complete}>
              {t('security.ip_rules.create')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
