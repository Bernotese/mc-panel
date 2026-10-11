import { useVirtualizer } from '@tanstack/react-virtual'
import { ArrowDown } from 'lucide-react'
import { useEffect, useRef, useState, type UIEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import type { CommandEntry, LineEntry, LogEntry } from './log-model'
import { LogText } from './log-text'

const ROW_ESTIMATE_PX = 20
/** Distance from the end that still counts as "at the bottom" (keeps auto-scroll on). */
const BOTTOM_SLACK_PX = 32

export function LogView({ entries }: { entries: readonly LogEntry[] }) {
  const { t } = useTranslation()
  const scrollRef = useRef<HTMLDivElement>(null)
  const [atBottom, setAtBottom] = useState(true)
  const [seenKey, setSeenKey] = useState<string | undefined>(undefined)
  const lastKey = entries.at(-1)?.key
  // eslint-disable-next-line react-hooks/incompatible-library -- useVirtualizer returns new functions on purpose; this component is not memoized
  const virtualizer = useVirtualizer({
    count: entries.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => ROW_ESTIMATE_PX,
    overscan: 20,
    getItemKey: (index) => entries[index].key,
  })

  useEffect(() => {
    if (atBottom && entries.length > 0) virtualizer.scrollToIndex(entries.length - 1, { align: 'end' })
  }, [atBottom, lastKey, entries.length, virtualizer])

  function onScroll(event: UIEvent<HTMLDivElement>) {
    const el = event.currentTarget
    const bottom = el.scrollHeight - el.scrollTop - el.clientHeight <= BOTTOM_SLACK_PX
    if (bottom === atBottom) return
    setAtBottom(bottom)
    if (!bottom) setSeenKey(lastKey)
  }

  const showJump = !atBottom && lastKey !== seenKey

  return (
    <div className="relative">
      <div
        ref={scrollRef}
        data-console-log=""
        role="log"
        aria-live="off"
        aria-label={t('console.log_label')}
        tabIndex={0}
        onScroll={onScroll}
        className="h-[60vh] min-h-64 overflow-y-auto rounded-md bg-zinc-950 p-2 font-mono text-xs leading-5 text-zinc-100 sm:text-sm"
      >
        {entries.length === 0 ? (
          <p className="text-zinc-400">{t('console.empty')}</p>
        ) : (
          <div className="relative w-full" style={{ height: virtualizer.getTotalSize() }}>
            {virtualizer.getVirtualItems().map((item) => {
              const entry = entries[item.index]
              return (
                <div
                  key={item.key}
                  data-index={item.index}
                  ref={virtualizer.measureElement}
                  className="absolute top-0 left-0 w-full"
                  style={{ transform: `translateY(${item.start}px)` }}
                >
                  {entry.kind === 'line' ? <LineRow entry={entry} /> : <CommandRow entry={entry} />}
                </div>
              )
            })}
          </div>
        )}
      </div>
      {showJump ? (
        <Button type="button" size="sm" className="absolute right-4 bottom-4 shadow" onClick={() => setAtBottom(true)}>
          <ArrowDown aria-hidden="true" />
          {t('console.jump_to_bottom')}
        </Button>
      ) : null}
    </div>
  )
}

function LineRow({ entry }: { entry: LineEntry }) {
  return (
    <div
      data-level={entry.level}
      className={cn(
        'px-1 break-words whitespace-pre-wrap',
        entry.level === 'warn' && 'border-l-2 border-yellow-500 bg-yellow-500/10',
        entry.level === 'error' && 'border-l-2 border-red-500 bg-red-500/10',
      )}
    >
      <LogText segments={entry.segments} />
    </div>
  )
}

function CommandRow({ entry }: { entry: CommandEntry }) {
  const { t } = useTranslation()
  return (
    <div data-command="" className="my-1 rounded-sm border border-zinc-700 bg-zinc-900 px-2 py-1">
      <p className="break-words whitespace-pre-wrap text-sky-300">› {entry.command}</p>
      {entry.status === 'pending' ? (
        <p className="text-zinc-400">{t('console.command_pending')}</p>
      ) : entry.status === 'error' ? (
        <p className="break-words whitespace-pre-wrap text-red-300">{entry.output}</p>
      ) : entry.output ? (
        <p className="break-words whitespace-pre-wrap">
          <LogText segments={entry.segments} />
        </p>
      ) : (
        <p className="text-zinc-400">{t('console.command_no_output')}</p>
      )}
    </div>
  )
}
