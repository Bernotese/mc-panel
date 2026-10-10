import { useId, type ReactNode } from 'react'
import { Card, CardContent, CardDescription, CardHeader } from '@/components/ui/card'

/** Card with an h3 heading: the settings page groups cards under h2 sections. */
export function SettingsCard({ title, description, actions, children }: { title: string; description?: string; actions?: ReactNode; children: ReactNode }) {
  const id = useId()
  return (
    <section aria-labelledby={id} className="min-w-0">
      <Card className="h-full">
        <CardHeader>
          <div className="flex flex-wrap items-start justify-between gap-2">
            <div className="space-y-1">
              <h3 id={id} className="leading-none font-semibold">
                {title}
              </h3>
              {description ? <CardDescription>{description}</CardDescription> : null}
            </div>
            {actions}
          </div>
        </CardHeader>
        <CardContent className="space-y-4 text-sm">{children}</CardContent>
      </Card>
    </section>
  )
}
