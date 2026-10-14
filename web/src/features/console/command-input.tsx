import { SendHorizontal } from 'lucide-react'
import { useId, useState, type FormEvent, type KeyboardEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { commandProblem, type CommandProblem } from './command'
import { emptyHistory, historyDown, historyUp, pushHistory, type HistoryState } from './history'

/** lockedText replaces the "server stopped" placeholder and hint while disabled for another reason. */
export function CommandInput({
  disabled,
  lockedText,
  onSubmit,
}: {
  disabled: boolean
  lockedText?: string
  onSubmit(command: string): void
}) {
  const { t } = useTranslation()
  const id = useId()
  const [value, setValue] = useState('')
  const [history, setHistory] = useState<HistoryState>(emptyHistory)
  const [problem, setProblem] = useState<CommandProblem | null>(null)
  const hintId = `${id}-hint`
  const errorId = `${id}-error`

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const command = value.trim()
    const found = commandProblem(command)
    setProblem(found)
    if (found) return
    setHistory((h) => pushHistory(h, command))
    setValue('')
    onSubmit(command)
  }

  function onKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key !== 'ArrowUp' && event.key !== 'ArrowDown') return
    event.preventDefault()
    const [next, text] = event.key === 'ArrowUp' ? historyUp(history, value) : historyDown(history, value)
    setHistory(next)
    setValue(text)
  }

  return (
    <form onSubmit={submit} className="space-y-1">
      <div className="flex gap-2">
        <Label htmlFor={id} className="sr-only">
          {t('console.command')}
        </Label>
        <div className="console-scope border-console-border focus-within:ring-ring flex min-w-0 flex-1 items-center gap-2 border px-3 focus-within:ring-2">
          <span aria-hidden="true" className="text-mc-a font-mono">
            &gt;
          </span>
          <Input
            id={id}
            value={value}
            onChange={(e) => setValue(e.target.value)}
            onKeyDown={onKeyDown}
            disabled={disabled}
            autoComplete="off"
            autoCapitalize="off"
            spellCheck={false}
            placeholder={disabled ? (lockedText ?? t('console.stopped_placeholder')) : t('console.command_placeholder')}
            aria-invalid={problem ? true : undefined}
            aria-describedby={problem ? `${errorId} ${hintId}` : hintId}
            className="text-console-foreground placeholder:text-mc-8 h-9 border-0 bg-transparent! px-0 font-mono shadow-none! focus-visible:ring-0"
          />
        </div>
        <Button type="submit" disabled={disabled}>
          <SendHorizontal aria-hidden="true" />
          {t('console.send')}
        </Button>
      </div>
      {problem ? (
        <p id={errorId} role="alert" className="text-destructive text-sm">
          {t(`console.validation.${problem}`)}
        </p>
      ) : null}
      <p id={hintId} className="text-muted-foreground text-xs">
        {disabled ? (lockedText ?? t('console.stopped_hint')) : t('console.stop_hint')}
      </p>
    </form>
  )
}
