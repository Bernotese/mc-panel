import { ChevronDown } from 'lucide-react'
import { useId, useState, type ReactNode } from 'react'
import { cn } from '@/lib/utils'

/** Collapsed by default; forceOpen keeps it open (e.g. after a submit with errors inside). */
export function CollapsibleSection({
  title,
  description,
  forceOpen = false,
  children,
}: {
  title: string
  description?: string
  forceOpen?: boolean
  children: ReactNode
}) {
  const contentId = useId()
  const [open, setOpen] = useState(false)
  const expanded = open || forceOpen
  return (
    <section className="rounded-md border">
      <h2 className="text-base font-semibold">
        <button
          type="button"
          className="hover:bg-muted/50 focus-visible:ring-ring flex w-full items-center justify-between gap-2 rounded-md px-4 py-3 text-left outline-none focus-visible:ring-2"
          aria-expanded={expanded}
          aria-controls={expanded ? contentId : undefined}
          onClick={() => setOpen(!expanded)}
        >
          {title}
          <ChevronDown className={cn('size-4 shrink-0 transition-transform', expanded && 'rotate-180')} aria-hidden="true" />
        </button>
      </h2>
      {description ? <p className="text-muted-foreground px-4 pb-3 text-sm">{description}</p> : null}
      {expanded ? (
        <div id={contentId} className="space-y-4 border-t p-4">
          {children}
        </div>
      ) : null}
    </section>
  )
}
