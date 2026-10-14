import type { ReactNode } from 'react'
import { Section } from '@/components/section'

/** Card with an h3 heading: the settings page groups cards under h2 sections. */
export function SettingsCard({ title, description, actions, children }: { title: string; description?: string; actions?: ReactNode; children: ReactNode }) {
  return (
    <Section title={title} description={description} actions={actions} headingLevel={3}>
      <div className="space-y-4 text-sm">{children}</div>
    </Section>
  )
}
