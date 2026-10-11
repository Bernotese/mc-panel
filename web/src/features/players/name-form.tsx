import { Plus } from 'lucide-react'
import { useId, useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { playerNameProblem } from './validation'

export function NameForm({ label, disabled, onAdd }: { label: string; disabled: boolean; onAdd(name: string): Promise<unknown> }) {
  const { t } = useTranslation()
  const id = useId()
  const messageId = `${id}-message`
  const [name, setName] = useState('')
  const [problem, setProblem] = useState<string | null>(null)

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const value = name.trim()
    const found = playerNameProblem(value)
    setProblem(found)
    if (found) return
    try {
      await onAdd(value)
      setName('')
    } catch {
      // usePlayerAction already showed the error as a toast; keep the input for a correction.
    }
  }

  return (
    <form onSubmit={(event) => void submit(event)} className="space-y-1">
      <Label htmlFor={id}>{label}</Label>
      <div className="flex gap-2">
        <Input
          id={id}
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder={t('players.name_placeholder')}
          autoComplete="off"
          autoCapitalize="off"
          spellCheck={false}
          disabled={disabled}
          aria-invalid={problem ? true : undefined}
          aria-describedby={problem ? messageId : undefined}
          className="max-w-xs"
        />
        <Button type="submit" variant="outline" disabled={disabled}>
          <Plus aria-hidden="true" />
          {t('players.add')}
        </Button>
      </div>
      {problem ? (
        <p id={messageId} className="text-destructive text-sm">
          {t(problem)}
        </p>
      ) : null}
    </form>
  )
}
