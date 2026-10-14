import { useId, type ReactNode } from 'react'
import { cn } from '@/lib/utils'

export function Section({
  title,
  description,
  actions,
  tone = 'default',
  headingLevel = 2,
  className,
  children,
}: {
  title?: string
  description?: ReactNode
  actions?: ReactNode
  tone?: 'default' | 'danger'
  headingLevel?: 2 | 3
  className?: string
  children: ReactNode
}) {
  const id = useId()
  const Heading = headingLevel === 3 ? 'h3' : 'h2'
  return (
    <section
      aria-labelledby={title ? id : undefined}
      data-tone={tone}
      className={cn(
        'bg-card text-card-foreground min-w-0 border p-4 md:p-5',
        tone === 'danger' && 'border-destructive/60',
        className,
      )}
    >
      {title || actions ? (
        <div className="mb-4 flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0 space-y-1">
            {title ? (
              <Heading id={id} className={cn('pixel-heading text-sm uppercase', tone === 'danger' && 'text-destructive')}>
                {title}
              </Heading>
            ) : null}
            {description ? <div className="text-muted-foreground text-sm">{description}</div> : null}
          </div>
          {actions ? <div className="flex flex-wrap gap-2">{actions}</div> : null}
        </div>
      ) : null}
      {children}
    </section>
  )
}
