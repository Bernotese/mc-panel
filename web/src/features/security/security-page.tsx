// web/src/features/security/security-page.tsx
import { useTranslation } from 'react-i18next'
import { PageHeader } from '@/components/page-header'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { AttemptsTab } from './attempts-tab'
import { IpRulesTab } from './ip-rules-tab'
import { JailsTab } from './jails-tab'
import { NotifiersTab } from './notifiers-tab'
import { OverviewTab } from './overview-tab'
import { SettingsTab } from './settings-tab'

const TABS = [
  { id: 'overview', Component: OverviewTab },
  { id: 'attempts', Component: AttemptsTab },
  { id: 'jails', Component: JailsTab },
  { id: 'ip_rules', Component: IpRulesTab },
  { id: 'settings', Component: SettingsTab },
  { id: 'notifiers', Component: NotifiersTab },
] as const

export function SecurityPage() {
  const { t } = useTranslation()
  return (
    <>
      <PageHeader title={t('security.title')} description={t('security.description')} />
      <Tabs defaultValue="overview">
        {/* Horizontally scrollable tab bar on phones instead of wrapping six triggers. */}
        <div className="-mx-4 overflow-x-auto px-4 md:mx-0 md:px-0">
          <TabsList className="w-max">
            {TABS.map(({ id }) => (
              <TabsTrigger key={id} value={id}>
                {t(`security.tabs.${id}`)}
              </TabsTrigger>
            ))}
          </TabsList>
        </div>
        {TABS.map(({ id, Component }) => (
          <TabsContent key={id} value={id} className="pt-4">
            <Component />
          </TabsContent>
        ))}
      </Tabs>
    </>
  )
}
