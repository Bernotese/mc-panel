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
    <section className="bg-card border">
      <h2>
        <button
          type="button"
          className="hover:bg-accent focus-visible:ring-ring pixel-heading flex w-full items-center justify-between gap-2 px-4 py-3 text-left text-sm uppercase md:px-5 outline-none focus-visible:ring-2"
          aria-expanded={expanded}
          aria-controls={expanded ? contentId : undefined}
          onClick={() => setOpen(!expanded)}
        >
          {title}
          <ChevronDown className={cn('size-4 shrink-0 transition-transform', expanded && 'rotate-180')} aria-hidden="true" />
        </button>
      </h2>
      {description ? <p className="text-muted-foreground px-4 pb-3 text-sm md:px-5">{description}</p> : null}
      {expanded ? (
        <div id={contentId} className="flex flex-col gap-4 border-t p-4 md:p-5">
          {children}
        </div>
      ) : null}
    </section>
  )
}
