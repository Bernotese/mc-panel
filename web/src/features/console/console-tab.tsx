import { useQueryClient } from '@tanstack/react-query'
import { Copy } from 'lucide-react'
import { useId, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { Server } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { playersKey } from '@/features/players/api'
import { errorMessage } from '@/lib/error-message'
import { sendConsoleCommand } from './api'
import { CommandInput } from './command-input'
import { entryText, visibleEntries } from './log-model'
import { LogView } from './log-view'
import { useConsoleStream, type ConnectionState } from './use-console-stream'

const CONNECTION_VARIANT: Record<ConnectionState, 'default' | 'secondary' | 'destructive' | 'outline'> = {
  connecting: 'outline',
  live: 'default',
  stopped: 'secondary',
  reconnecting: 'outline',
  unavailable: 'destructive',
  too_many: 'destructive',
  forbidden: 'destructive',
  not_found: 'destructive',
  unauthorized: 'destructive',
}

/** States in which the stream will not come back by itself: commands cannot work either. */
const LOCKED: readonly ConnectionState[] = ['forbidden', 'not_found']

export function ConsoleTab({ server }: { server: Server }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const rconId = useId()
  const stream = useConsoleStream(server.id)
  const [query, setQuery] = useState('')
  const [hideRcon, setHideRcon] = useState(true)
  const visible = useMemo(() => visibleEntries(stream.entries, { query, hideRcon }), [stream.entries, query, hideRcon])
  const running = server.status.state === 'running'
  const locked = LOCKED.includes(stream.connection)

  async function send(command: string) {
    const id = stream.addCommand(command)
    try {
      const response = await sendConsoleCommand(server.id, command)
      stream.finishCommand(id, true, response.output)
      // op, whitelist, ban, kick, … change what the Players tab shows.
      void queryClient.invalidateQueries({ queryKey: playersKey(server.id) })
    } catch (err) {
      stream.finishCommand(id, false, errorMessage(err, t))
    }
  }

  async function copy() {
    try {
      await navigator.clipboard.writeText(visible.map(entryText).join('\n'))
      toast.success(t('console.copied'))
    } catch {
      toast.error(t('console.copy_failed'))
    }
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-3">
        <span role="status">
          <Badge variant={CONNECTION_VARIANT[stream.connection]}>{t(`console.connection.${stream.connection}`)}</Badge>
        </span>
        <Input
          type="search"
          aria-label={t('console.filter')}
          placeholder={t('console.filter')}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          className="w-full sm:w-64"
        />
        <div className="flex items-center gap-2">
          <Switch id={rconId} checked={hideRcon} onCheckedChange={setHideRcon} />
          <Label htmlFor={rconId}>{t('console.hide_rcon')}</Label>
        </div>
        <Button type="button" variant="outline" size="sm" onClick={() => void copy()}>
          <Copy aria-hidden="true" />
          {t('console.copy')}
        </Button>
      </div>
      <LogView entries={visible} />
      <CommandInput
        disabled={!running || locked}
        lockedText={locked ? t(`console.connection.${stream.connection}`) : undefined}
        onSubmit={(command) => void send(command)}
      />
    </div>
  )
}
