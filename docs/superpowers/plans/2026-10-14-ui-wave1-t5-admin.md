# UI-Redesign – Welle 1 / T5: Admin + Auth (Implementation Plan)

> **For agentic workers:** Dieser Plan wird vom **Teammate T5** eines Claude-Code-Agent-Teams umgesetzt (CLAUDE.md, „Implementation with agent teams“). Arbeite im eigenen Worktree auf eigenem Branch, nur in den unten genannten Dateien. Steps use checkbox (`- [ ]`) syntax for tracking. Vor „fertig“: `make check` und `make test-vm SUITE=web` grün, Screenshots erzeugt und selbst angesehen.

**Goal:** Benutzer, Rechte, Audit, Security Center, Einstellungen, Profil, Login/2FA/Setup und den Sudo-Dialog auf das neue Design (M2 dunkel / Clean hell) umstellen: dichte Tabellen, Filter in einer Zeile, `Section` statt `Card`/`SectionCard`, `StatSlot`s, Token-Farben im Balkendiagramm, Auth-Panel mit Grasblock auf Pixelmuster.

**Architecture:** Reine Präsentationsänderungen in `web/src/features/{users,security,audit,settings,profile,auth,setup,sudo,webauthn}`. Daten-Hooks, Mutationen, API-Aufrufe und Texte bleiben; nur Layout, Container und Klassen ändern sich. Die Bausteine (`Section`, `StatSlot`, `PixelIcon`, `PageHeader`) und Utilities (`pixel-heading`, `pixel-pattern`, `bevel-inset`) kommen aus Welle 0; die shadcn-Komponenten (Button, Badge, Table/DataList, Dialog, Tabs) restylt T1 – T5 nutzt nur ihre unveränderte API.

**Tech Stack:** React 19, TypeScript, Tailwind v4, shadcn/ui (vendored), react-i18next, Vitest + Testing Library, Playwright (nur Screenshots).

**Spec:** `docs/superpowers/specs/2026-10-14-mc-panel-ui-redesign-design.md` (§3.4 Spacing, §3.6 Bausteine, §5.3 Admin- und Auth-Seiten, §6 Umsetzung). Welle-0-Plan: `docs/superpowers/plans/2026-10-14-ui-wave0-foundation.md` (Interfaces der Bausteine).

## Setup

```bash
cd /Users/jannes/Workspace/mc-panel
git fetch origin
git worktree add ../mc-panel-wt/feat-ui-admin -b feat/ui-admin origin/feat/ui-foundation
cd ../mc-panel-wt/feat-ui-admin/web && npm ci
```

Alle Pfade unten relativ zum Worktree-Root. Ändert der Lead `feat/ui-foundation` (z. B. neue Utility), `git fetch && git rebase origin/feat/ui-foundation`.

## Global Constraints

- **Dateibesitz:** nur `web/src/features/{users,security,audit,settings,profile,auth,setup,sudo,webauthn}/**` und neu `web/e2e/shots/admin.shots.ts`. Keine Änderungen an `web/src/routes/**` (T2), `web/src/components/**` (T1/Lead), `web/src/index.css`, `package.json` (Lead), anderen `features/*`, `web/e2e/*.spec.ts` (T2), Mocks.
- `web/src/features/profile/section-card.tsx` **bleibt bestehen** (andere Teammates importieren es noch); T5 benutzt es nur nicht mehr. Der Lead löscht es nach der Integration.
- **i18n:** keine bestehenden Schlüssel umbenennen/löschen. Neue Schlüssel nur unter den eigenen Namensräumen (`users.*`, `permissions.*`, `audit.*`, `security.*`, `settings.*`, `profile.*`, `login.*`, `second_factor.*`, `setup.*`, `sudo.*`), immer in `de.ts` **und** `en.ts`. Dieser Plan braucht keine neuen Schlüssel.
- **Zugängliche Namen bleiben:** alle Labels, Button-Namen, Überschriften, `role="status"`/`region`-Namen, die Unit-Tests und E2E nutzen – insbesondere `Benutzername`, `Passwort`, `Anmelden`, `Code aus der Authenticator-App`, `Bestätigen`, Dialog `Aktion bestätigen`, Button/Dialog `Benutzer anlegen`, Status `Kette gültig …`, Regionen `Fehlversuche`, `Aktive Jails`, `Status`, `Zeitzone für Backups`, `Monitoring & Alerts`, `Aktive Sitzungen`, `Alle Sitzungen (Admin)`, `Authenticator-App (TOTP)`, `Wiederherstellungscodes`, Überschrift `Rechte von alice`.
- **Spacing (Spec §3.4):** zwischen Sections `gap-6`, innerhalb `gap-4`, Formularfelder `gap-3`, Tabellenzeilen dicht. Formulare/Textseiten: `max-w-3xl`; Auth-Panel: `max-w-md` (Setup mit Tabs: `max-w-lg`).
- Silkscreen (`pixel-heading`, `font-heading`) nur für Überschriften/Labels, nie Fließtext, nie unter 12 px (`text-xs` = 12 px ist die Untergrenze).
- Keine externen Ressourcen, kein `dangerouslySetInnerHTML`, Farben nur über Tokens (`text-status-error`, `fill-chart-1`, …), keine Hex-Werte im TSX außer in `PixelIcon` (Welle 0).
- Mac: nur `make check`. Tests nur `make test-vm SUITE=web` (und `SUITE=shots` für Screenshots). Kein `SUITE=all` (macht der Lead).
- Commits: Conventional Commits (`feat(web): …`, `test(web): …`), jede Commit-Nachricht endet mit den Attribution-Zeilen aus der Session (`Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>` und `Claude-Session: …`).

## Review Focus

- **Regionen-Namen gehen beim Umbau verloren:** `StatSlot` ist kein `region`; die Security-Übersicht muss jeden Slot in `<section aria-label={label}>` packen, sonst brechen `overview-tab.test.tsx` und Screenreader-Navigation (Test in Task 3).
- **Überschriften-Hierarchie in den Einstellungen:** Seite `h1` → Bereich `h2` → Karte `h3`. Wer `SettingsCard` auf `Section` umstellt und `headingLevel` vergisst, erzeugt zwei `h2`-Ebenen (Test in Task 2).
- **Fehler-/Leerzustände der Tabellen:** Benutzer-, Audit- und Rechte-Seite zeigen bei Fehler `role="alert"` und bei leerer Liste den `empty`-Text weiter an – der Umbau der Filterzeile darf diese Zweige nicht verschieben oder verschlucken (Test in Task 4, Audit leer).
- **Login auf 390 px:** Das Panel darf bei schmalem Viewport nicht horizontal scrollen und der Theme-Umschalter darf das Panel nicht überdecken (Screenshot-Prüfung in Task 5, Code: `px-4`, Toggle `absolute` nur im Seitenrand).
- **Balkenfarben in Light Mode:** Erfolg/Fehler müssen sich auch ohne Farbe unterscheiden lassen (Legende mit Text + `<desc>`/sr-Tabelle bleiben) und die Token-Klassen müssen in beiden Modi existieren (Test in Task 3 prüft die Klassen).

---

## File Structure

| Datei | Änderung |
|---|---|
| `web/src/features/auth/auth-layout.tsx` | Zentriertes Panel `max-w-md`/`max-w-lg`, `PixelIcon grass`, `pixel-heading`, `pixel-pattern` |
| `web/src/features/auth/auth-layout.test.tsx` (neu) | h1, Marke, `main`, Breitenklasse |
| `web/src/features/sudo/sudo-dialog.tsx` | Titel mit Icon, Abstände |
| `web/src/features/settings/settings-card.tsx` | intern `Section headingLevel={3}` |
| `web/src/features/settings/settings-page.tsx` | einspaltig `max-w-3xl`, Bereichsüberschriften `pixel-heading` |
| `web/src/features/settings/settings-page.test.tsx` | + Test Überschriften-Hierarchie |
| `web/src/features/profile/{language,password,two-factor,recovery,passkeys,sessions,admin-sessions}-card.tsx` | `SectionCard` → `Section` |
| `web/src/features/profile/profile-page.tsx` | einspaltig `max-w-3xl`, `gap-6` |
| `web/src/features/security/overview-tab.tsx` | `StatSlot`s, `Section`s |
| `web/src/features/security/agent-status-card.tsx` | `Section` statt `Card` |
| `web/src/features/security/bar-chart.tsx` (+ Test) | Token-Farben, eckige Balken |
| `web/src/features/security/security-page.tsx`, `settings-tab.tsx` | Abstände, `max-w-3xl` |
| `web/src/features/users/users-page.tsx` | dichte Aktionen rechts |
| `web/src/features/users/permissions-page.tsx` | Warnungen als `Callout`-Zeilen, Abstände |
| `web/src/features/audit/audit-page.tsx` (+ Test) | Filter in einer Zeile |
| `web/e2e/shots/admin.shots.ts` (neu) | Screenshots aller T5-Seiten |

---

### Task 1: Auth-Panel und Sudo-Dialog

**Files:**
- Modify: `web/src/features/auth/auth-layout.tsx`, `web/src/features/sudo/sudo-dialog.tsx`
- Create: `web/src/features/auth/auth-layout.test.tsx`

**Interfaces:**
- Consumes: `PixelIcon({ kind: 'grass', size })` aus `@/components/pixel-icon`; Utilities `pixel-heading`, `pixel-pattern`, `bevel` (Welle 0); `ThemeToggle` (T1, API unverändert).
- Produces: `AuthLayout({ title, description?, wide?, children })` – **API unverändert**, genutzt von `LoginPage`, `SecondFactorPage`, `Setup2faPage`.

- [ ] **Step 1: Failing test**

`web/src/features/auth/auth-layout.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import '@/i18n'
import { AuthLayout } from './auth-layout'

describe('AuthLayout', () => {
  it('renders brand, an h1 title and the description inside main', () => {
    render(
      <AuthLayout title="Anmelden" description="Willkommen zurück">
        <p>Formular</p>
      </AuthLayout>,
    )
    const main = screen.getByRole('main')
    expect(main).toHaveAttribute('id', 'main')
    expect(screen.getByRole('heading', { level: 1, name: 'Anmelden' })).toBeInTheDocument()
    expect(screen.getByText('Willkommen zurück')).toBeInTheDocument()
    expect(screen.getByText('mc-panel')).toBeInTheDocument()
    expect(screen.getByText('Formular')).toBeInTheDocument()
  })

  it('uses the narrow panel by default and the wide one for setup', () => {
    const { rerender } = render(<AuthLayout title="A">x</AuthLayout>)
    expect(screen.getByRole('main')).toHaveClass('max-w-md')
    rerender(
      <AuthLayout title="A" wide>
        x
      </AuthLayout>,
    )
    expect(screen.getByRole('main')).toHaveClass('max-w-lg')
  })

  it('draws the pixel pattern on the page background', () => {
    const { container } = render(<AuthLayout title="A">x</AuthLayout>)
    expect(container.firstElementChild).toHaveClass('pixel-pattern')
  })
})
```

Vorher prüfen, wie andere Tests i18n laden (`grep -rn "from '@/i18n'" web/src --include=*.test.tsx | head -3`); falls Tests stattdessen `renderWithProviders` aus `@/test/render` nutzen, diesen verwenden und den `import '@/i18n'` weglassen.

- [ ] **Step 2: Test ausführen – soll fehlschlagen**

Run: `make test-vm SUITE=web`
Expected: FAIL in `auth-layout.test.tsx` (`max-w-sm` statt `max-w-md`, kein `pixel-pattern`).

- [ ] **Step 3: `auth-layout.tsx` umsetzen**

```tsx
// web/src/features/auth/auth-layout.tsx
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { PixelIcon } from '@/components/pixel-icon'
import { ThemeToggle } from '@/components/theme-toggle'
import { cn } from '@/lib/utils'

export function AuthLayout({ title, description, wide = false, children }: { title: string; description?: string; wide?: boolean; children: ReactNode }) {
  const { t } = useTranslation()
  return (
    <div className="pixel-pattern relative flex min-h-svh flex-col items-center justify-center px-4 py-12">
      <div className="absolute top-3 right-3">
        <ThemeToggle />
      </div>
      <main id="main" className={cn('w-full', wide ? 'max-w-lg' : 'max-w-md')}>
        <p className="mb-6 flex items-center justify-center gap-3">
          <PixelIcon kind="grass" size={32} />
          <span className="pixel-heading text-foreground text-lg">{t('auth.brand')}</span>
        </p>
        <div className="bg-card text-card-foreground bevel space-y-6 p-6 sm:p-8">
          <div className="space-y-2">
            <h1 className="pixel-heading text-xl">{title}</h1>
            {description ? <p className="text-muted-foreground text-sm">{description}</p> : null}
          </div>
          {children}
        </div>
      </main>
    </div>
  )
}
```

Die Marke ist ein `<p>` (kein zweites `h1`); `auth.brand` = „mc-panel“ wird in Silkscreen ohnehin in Versalien-Optik gezeigt, der Text bleibt klein geschrieben.

- [ ] **Step 4: Sudo-Dialog**

In `web/src/features/sudo/sudo-dialog.tsx` nur den Kopf ändern (Titeltext und Beschreibung bleiben, damit `getByRole('dialog', { name: 'Aktion bestätigen' })` weiter trifft):

```tsx
import { ShieldCheck } from 'lucide-react'
// …
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <ShieldCheck className="text-primary size-5 shrink-0" aria-hidden="true" />
            {t('sudo.title')}
          </DialogTitle>
          <DialogDescription>{t('sudo.description')}</DialogDescription>
        </DialogHeader>
        {open && me ? (
          <div className="pt-2">
            <SudoContent me={me} />
          </div>
        ) : null}
      </DialogContent>
```

Der Icon-Name bleibt `aria-hidden`, damit der Dialogname exakt `Aktion bestätigen` lautet.

- [ ] **Step 5: Tests grün**

Run: `make check && make test-vm SUITE=web`
Expected: PASS, inkl. `login-page.test.tsx`, `second-factor-page.test.tsx`, `setup-2fa-page.test.tsx`, `sudo-dialog.test.tsx`.

- [ ] **Step 6: Commit**

```bash
git add web/src/features/auth/auth-layout.tsx web/src/features/auth/auth-layout.test.tsx web/src/features/sudo/sudo-dialog.tsx
git commit -m "feat(web): restyle auth panel and sudo dialog for the redesign"
```

---

### Task 2: Einstellungen und Profil auf `Section`

**Files:**
- Modify: `web/src/features/settings/settings-card.tsx`, `web/src/features/settings/settings-page.tsx`, `web/src/features/settings/settings-page.test.tsx`
- Modify: `web/src/features/profile/{language-card,password-card,two-factor-card,recovery-card,passkeys-card,sessions-card,admin-sessions-card}.tsx`, `web/src/features/profile/profile-page.tsx`

**Interfaces:**
- Consumes: `Section({ title?, description?, actions?, tone?, headingLevel?, className?, children })` aus `@/components/section` (Welle 0).
- Produces: `SettingsCard({ title, description?, actions?, children })` – API unverändert, rendert jetzt eine `Section` mit `h3`.

- [ ] **Step 1: Failing test für die Überschriften-Hierarchie**

In `web/src/features/settings/settings-page.test.tsx` einen Test ergänzen (Render-Helfer und Login-Muster aus den bestehenden Tests der Datei übernehmen; dort wird `SettingsPage` bereits gerendert):

```tsx
  it('keeps h1 → h2 section → h3 card and limits the width', async () => {
    const { container } = await renderSettingsPage() // bestehenden Helfer/Aufruf der Datei verwenden
    expect(screen.getByRole('heading', { level: 1, name: 'Einstellungen' })).toBeInTheDocument()
    const status = await screen.findByRole('region', { name: 'Status' })
    expect(within(status).getByRole('heading', { level: 3, name: 'Status' })).toBeInTheDocument()
    expect(screen.getAllByRole('heading', { level: 2 }).length).toBeGreaterThanOrEqual(2)
    expect(container.querySelector('.max-w-3xl')).not.toBeNull()
  })
```

Falls die Datei keinen Helfer `renderSettingsPage` hat, den Render-Aufruf des Tests „Status“ (Zeile ~88) wörtlich kopieren. Den Titel der Seite (`settings.title`) vorher in `de.ts` nachsehen und im Test exakt verwenden.

- [ ] **Step 2: Test ausführen – soll fehlschlagen**

Run: `make test-vm SUITE=web`
Expected: FAIL – kein Element mit `.max-w-3xl`.

- [ ] **Step 3: `SettingsCard` auf `Section`**

```tsx
// web/src/features/settings/settings-card.tsx
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
```

- [ ] **Step 4: `settings-page.tsx` einspaltig**

Den `content`-Zweig und die beiden Bereiche ersetzen:

```tsx
  } else {
    content = (
      <div className="flex flex-col gap-6">
        <BaseDomainCard settings={settings.data} />
        <CurseForgeCard settings={settings.data} />
        <BackupTimezoneCard settings={settings.data} />
        <InstanceStatusCard settings={settings.data} />
      </div>
    )
  }

  return (
    <>
      <PageHeader title={t('settings.title')} description={t('settings.description')} />
      <div className="flex max-w-3xl flex-col gap-10">
        <section aria-labelledby={headingId} className="space-y-4">
          <h2 id={headingId} className="pixel-heading text-muted-foreground text-sm uppercase">
            {t('settings.minecraft.title')}
          </h2>
          {content}
        </section>
        {/* Loads on its own: a failure of the server settings does not hide it. */}
        <section aria-labelledby={monitoringHeadingId} className="space-y-4">
          <h2 id={monitoringHeadingId} className="pixel-heading text-muted-foreground text-sm uppercase">
            {t('settings.monitoring_section.title')}
          </h2>
          <MonitoringCard />
        </section>
      </div>
    </>
  )
```

- [ ] **Step 5: Profil-Karten von `SectionCard` auf `Section`**

In jeder der sieben Dateien:

```bash
cd web/src/features/profile
for f in language-card password-card two-factor-card recovery-card passkeys-card sessions-card admin-sessions-card; do
  sed -i '' -e "s#import { SectionCard } from './section-card'#import { Section } from '@/components/section'#" -e 's/<SectionCard/<Section/g' -e 's#</SectionCard>#</Section>#g' "$f.tsx"
done
grep -n "SectionCard" *.tsx
```

Expected: nur noch `section-card.tsx` selbst taucht auf. Danach die Importe in jeder Datei an die richtige alphabetische Stelle verschieben (Imports `@/components/...` stehen vor `@/features/...` und vor `./...`; `golangci`-unabhängig, aber ESLint/Prettier-Stil der Datei beibehalten). In `admin-sessions-card.tsx` `className="md:col-span-2"` entfernen (einspaltiges Layout).

- [ ] **Step 6: `profile-page.tsx` einspaltig**

```tsx
      <div className="flex max-w-3xl flex-col gap-6">
        <LanguageCard />
        <PasswordCard />
        <TwoFactorCard />
        <RecoveryCard />
        <PasskeysCard />
        <SessionsCard />
        {me?.role === 'admin' ? <AdminSessionsCard /> : null}
      </div>
```

- [ ] **Step 7: Tests grün**

Run: `make check && make test-vm SUITE=web`
Expected: PASS – alle `region`-Tests (`Status`, `Zeitzone für Backups`, `Monitoring & Alerts`, `Aktive Sitzungen`, `Alle Sitzungen (Admin)`, `Authenticator-App (TOTP)`, `Wiederherstellungscodes`) unverändert grün, neuer Hierarchie-Test grün.

- [ ] **Step 8: Commit**

```bash
git add web/src/features/settings web/src/features/profile
git commit -m "feat(web): move settings and profile to single-column sections"
```

---

### Task 3: Security Center

**Files:**
- Modify: `web/src/features/security/overview-tab.tsx`, `agent-status-card.tsx`, `bar-chart.tsx`, `bar-chart.test.tsx`, `security-page.tsx`, `settings-tab.tsx`
- Test: `web/src/features/security/overview-tab.test.tsx` (ergänzen)

**Interfaces:**
- Consumes: `StatSlot({ label, value, hint?, bar?, tone?, className? })`, `Section(...)` (Welle 0); Tailwind-Farben `fill-chart-1`, `bg-chart-1`, `fill-status-error`, `bg-status-error` (Welle 0, `@theme`).
- Produces: `LoginBarChart` – Props unverändert; `data-series="success"` nutzt `fill-chart-1`, `data-series="failure"` nutzt `fill-status-error`.

- [ ] **Step 1: Failing tests**

In `web/src/features/security/bar-chart.test.tsx` einen Test ergänzen (Daten-Fixture der Datei wiederverwenden; Name der vorhandenen Konstante vorher nachsehen, hier `DATA`):

```tsx
  it('uses the token colours for success and failure', () => {
    render(<LoginBarChart data={DATA} title="Logins" formatTick={String} labels={{ time: 'Zeit', success: 'Erfolg', failure: 'Fehler' }} />)
    const success = document.querySelector('rect[data-series="success"]')
    const failure = document.querySelector('rect[data-series="failure"]')
    expect(success).toHaveClass('fill-chart-1')
    expect(failure).toHaveClass('fill-status-error')
    expect(success).toHaveAttribute('rx', '0')
    const legend = screen.getByTestId('chart-legend')
    expect(legend.querySelector('.bg-chart-1')).not.toBeNull()
    expect(legend.querySelector('.bg-status-error')).not.toBeNull()
  })
```

Die Fixture muss mindestens einen Punkt mit `success > 0` **und** `failure > 0` enthalten; falls nicht, eine lokale Konstante `[{ t: 0, success: 2, failure: 1 }]` verwenden.

In `web/src/features/security/overview-tab.test.tsx` ergänzen (im ersten Test nach den bestehenden Region-Checks):

```tsx
    // Stat slots keep their region names and are sunken inventory slots.
    const failures = screen.getByRole('region', { name: 'Fehlversuche' })
    expect(failures.querySelector('.bevel-inset')).not.toBeNull()
    expect(screen.getByRole('heading', { level: 3, name: 'Agent' })).toBeInTheDocument()
```

Den exakten Agent-Titel (`security.agent.title`) vorher in `de.ts` nachsehen und einsetzen.

- [ ] **Step 2: Tests ausführen – sollen fehlschlagen**

Run: `make test-vm SUITE=web`
Expected: FAIL (`fill-[var(--chart-2)]` statt `fill-chart-1`, kein `.bevel-inset`).

- [ ] **Step 3: `bar-chart.tsx`**

- Erfolgs-Rechteck: `rx={0}`, `className="fill-chart-1"`.
- Fehler-Rechteck: `rx={0}`, `className="fill-status-error"`.
- Legende: `className="inline-block size-3 bg-chart-1"` bzw. `bg-status-error` (ohne `rounded-sm`).
- Achsenbeschriftung `text-[10px]` → `text-xs` **nicht** ändern (SVG-Koordinaten); bleibt.

- [ ] **Step 4: `agent-status-card.tsx` auf `Section`**

```tsx
import { Section } from '@/components/section'
// Card-Imports und useId entfernen
  return (
    <Section
      title={t('security.agent.title')}
      headingLevel={3}
      className="h-full"
      actions={
        agent.isPending ? null : (
          <Badge variant={reachable ? 'secondary' : 'outline'}>{reachable ? t('security.agent.reachable') : t('security.agent.unreachable')}</Badge>
        )
      }
    >
      <div className="space-y-2 text-sm">
        {agent.isPending ? <p className="text-muted-foreground">{t('common.loading')}</p> : null}
        {!agent.isPending && !reachable ? <p className="text-muted-foreground">{t('security.agent.unreachable_hint')}</p> : null}
        {agent.data ? (
          <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
            <dt className="text-muted-foreground">{t('security.agent.firewall')}</dt>
            <dd>{agent.data.firewall || '—'}</dd>
            <dt className="text-muted-foreground">{t('security.agent.version')}</dt>
            <dd>{agent.data.version || '—'}</dd>
            <dt className="text-muted-foreground">{t('security.agent.pending_sync')}</dt>
            <dd className="tabular-nums">{fmt.number(agent.data.pending_sync)}</dd>
          </dl>
        ) : null}
      </div>
    </Section>
  )
```

`agent-status-card.test.tsx` vorher lesen; nutzt er `region`/Überschrift, bleiben Name und Inhalt gleich.

- [ ] **Step 5: `overview-tab.tsx`**

`Stat` und `ListCard` ersetzen, `Card`-Imports entfernen, `StatSlot`/`Section` importieren:

```tsx
function Stat({ label, value }: { label: string; value: string }) {
  return (
    <section aria-label={label}>
      <StatSlot label={label} value={value} className="h-full py-3 [&>div:nth-child(2)]:text-3xl" />
    </section>
  )
}

function ListCard({ title, items, empty }: { title: string; items: ListItem[]; empty: string }) {
  return (
    <Section title={title} headingLevel={3}>
      {items.length === 0 ? (
        <p className="text-muted-foreground text-sm">{empty}</p>
      ) : (
        <ul aria-label={title} className="divide-y text-sm">
          {items.map((item) => (
            <li key={item.key} className="flex justify-between gap-3 py-1.5">
              <span className="min-w-0 break-all">{item.label}</span>
              <span className="text-muted-foreground font-pixel shrink-0 tabular-nums">{item.value}</span>
            </li>
          ))}
        </ul>
      )}
    </Section>
  )
}
```

Im Render: `space-y-6` bleibt; Stat-Raster `grid gap-3 sm:grid-cols-2 lg:grid-cols-3`; den Diagramm-`Card` durch `<Section>` (ohne Titel – das SVG hat `<title>`) ersetzen; Listen-Raster `grid gap-6 md:grid-cols-2`. `useId` wird nicht mehr gebraucht.

- [ ] **Step 6: `security-page.tsx` und `settings-tab.tsx`**

- `security-page.tsx`: `TabsContent … className="pt-6"` (statt `pt-4`).
- `settings-tab.tsx` Zeile ~83: `max-w-2xl space-y-4` → `max-w-3xl space-y-6`.
- `jails-tab.tsx`, `ip-rules-tab.tsx`, `notifiers-tab.tsx`, `attempts-tab.tsx`: nur prüfen (`grep -n "space-y-\|gap-" web/src/features/security/*-tab.tsx`), dass Abstände zwischen Toolbar und Tabelle `gap-4`/`space-y-4` und zwischen Blöcken `space-y-6` sind; abweichende Werte entsprechend setzen. Keine weiteren Änderungen.

- [ ] **Step 7: Tests grün**

Run: `make check && make test-vm SUITE=web`
Expected: PASS (inkl. `security-page.test.tsx`, `agent-status-card.test.tsx`, `overview-tab.test.tsx`, `bar-chart.test.tsx`).

- [ ] **Step 8: Commit**

```bash
git add web/src/features/security
git commit -m "feat(web): restyle security center with stat slots and token chart colours"
```

---

### Task 4: Benutzer, Rechte und Audit

**Files:**
- Modify: `web/src/features/users/users-page.tsx`, `web/src/features/users/permissions-page.tsx`, `web/src/features/audit/audit-page.tsx`
- Test: `web/src/features/audit/audit-page.test.tsx`, `web/src/features/users/permissions-page.test.tsx` (ergänzen)

**Interfaces:**
- Consumes: `DataList`/`Column<T>` (`{ id, header, cell, className? }`, T1 restylt, API unverändert), `Button` mit `size="xs"` (vorhanden), `PageHeader`.
- Produces: keine neuen Exporte.

- [ ] **Step 1: Failing tests**

`web/src/features/audit/audit-page.test.tsx` (Render-Muster der Datei übernehmen):

```tsx
  it('puts all filters into one toolbar row', async () => {
    await renderAuditPage() // vorhandenen Render-Aufruf der Datei verwenden
    const toolbar = await screen.findByRole('search', { name: 'Filter' })
    for (const label of ['Akteur', 'Aktion', 'Ergebnis', 'Von', 'Bis']) {
      expect(within(toolbar).getByLabelText(label)).toBeInTheDocument()
    }
    expect(toolbar).toHaveClass('flex-wrap')
  })

  it('shows the empty text when filters match nothing', async () => {
    const user = userEvent.setup()
    await renderAuditPage()
    await user.type(await screen.findByLabelText('Aktion'), 'does.not.exist')
    await user.click(screen.getByRole('button', { name: 'Filter anwenden' }))
    expect(await screen.findByText(/keine einträge/i)).toBeInTheDocument()
  })
```

Die exakten deutschen Labels (`audit.actor`, `audit.action`, `audit.result_label`, `audit.from`, `audit.to`, `common.apply_filters`, `audit.empty`) vorher in `web/src/i18n/de.ts` nachschlagen und in den Tests wörtlich verwenden. Der Name `Filter` des `search`-Landmarks kommt aus einem bestehenden Schlüssel, falls vorhanden (`grep -n "filters\b\|filter:" web/src/i18n/de.ts`); gibt es keinen, `audit.filters_label` neu anlegen (de: „Filter“, en: „Filters“). Filtert der Mock `/audit` nicht nach `action`, den zweiten Test streichen und stattdessen im ersten nur die Toolbar prüfen.

`web/src/features/users/permissions-page.test.tsx` (im Test, der eine `server.files`-Warnung auslöst; falls keiner existiert, nach dem Muster der Datei einen schreiben, der für alice die Checkbox „Dateien“ eines Servers anklickt):

```tsx
    const warning = await screen.findByRole('status')
    expect(warning).toHaveAttribute('data-tone', 'danger')
```

- [ ] **Step 2: Tests ausführen – sollen fehlschlagen**

Run: `make test-vm SUITE=web`
Expected: FAIL (kein `search`-Landmark, kein `data-tone`).

- [ ] **Step 3: Audit-Filter in einer Zeile**

In `audit-page.tsx` das `<form>` ersetzen (Felder, IDs und Handler bleiben):

```tsx
      <form
        role="search"
        aria-label={t('audit.filters_label')}
        className="mb-4 flex flex-wrap items-end gap-3"
        onSubmit={(e) => {
          e.preventDefault()
          setFilters(draft)
        }}
      >
        <div className="w-full space-y-1 sm:w-40">
          <Label htmlFor={`${id}-actor`}>{t('audit.actor')}</Label>
          <Input id={`${id}-actor`} value={draft.actor} onChange={(e) => update({ actor: e.target.value })} />
        </div>
        <div className="w-full space-y-1 sm:w-48">
          <Label htmlFor={`${id}-action`}>{t('audit.action')}</Label>
          <Input id={`${id}-action`} className="font-mono" value={draft.action} spellCheck={false} onChange={(e) => update({ action: e.target.value })} />
        </div>
        <div className="w-full space-y-1 sm:w-36">
          <Label htmlFor={`${id}-result`}>{t('audit.result_label')}</Label>
          <NativeSelect id={`${id}-result`} className="w-full" value={draft.result} onChange={(e) => update({ result: e.target.value as AuditResult | '' })}>
            <option value="">{t('audit.result_all')}</option>
            <option value="success">{t('audit.result.success')}</option>
            <option value="failure">{t('audit.result.failure')}</option>
            <option value="denied">{t('audit.result.denied')}</option>
          </NativeSelect>
        </div>
        <div className="w-full space-y-1 sm:w-40">
          <Label htmlFor={`${id}-from`}>{t('audit.from')}</Label>
          <Input id={`${id}-from`} type="date" value={draft.from} onChange={(e) => update({ from: e.target.value })} />
        </div>
        <div className="w-full space-y-1 sm:w-40">
          <Label htmlFor={`${id}-to`}>{t('audit.to')}</Label>
          <Input id={`${id}-to`} type="date" value={draft.to} onChange={(e) => update({ to: e.target.value })} />
        </div>
        <div className="flex gap-2">
          <Button type="submit" variant="secondary">
            {t('common.apply_filters')}
          </Button>
          <Button
            type="button"
            variant="ghost"
            onClick={() => {
              setDraft(EMPTY_FILTERS)
              setFilters(EMPTY_FILTERS)
            }}
          >
            {t('common.reset_filters')}
          </Button>
        </div>
      </form>
```

Spalten: `time` bekommt `className: 'whitespace-nowrap'`, `hash` `className: 'font-mono'`, `ip` bleibt Mono. `load more`-Button: `className="mt-4"` bleibt.

Falls `audit.filters_label` neu ist: in `de.ts` unter `audit` `filters_label: 'Filter',`, in `en.ts` `filters_label: 'Filters',`.

- [ ] **Step 4: Benutzer – dichte Aktionen rechts**

In `users-page.tsx` die Aktionsspalte:

```tsx
    {
      id: 'actions',
      header: t('users.col.actions'),
      className: 'text-right',
      cell: (u) => (
        <div className="flex flex-wrap justify-end gap-1.5">
          <Button asChild size="xs" variant="outline">
            <Link to="/users/$id/permissions" params={{ id: String(u.id) }} aria-label={t('users.permissions_for', { name: u.username })}>
              {t('users.permissions')}
            </Link>
          </Button>
          <Button
            type="button"
            size="xs"
            variant="outline"
            aria-label={t('users.reset_for', { name: u.username })}
            disabled={!u.totp_enabled && u.passkey_count === 0}
            onClick={() => setResetting(u)}
          >
            {t('users.reset_2fa')}
          </Button>
          <Button
            type="button"
            size="xs"
            variant="ghost"
            className="text-destructive"
            aria-label={t('users.delete_for', { name: u.username })}
            disabled={u.id === me?.id}
            onClick={() => setDeleting(u)}
          >
            {t('common.delete')}
          </Button>
        </div>
      ),
    },
```

Spalte `last_login`: `className: 'whitespace-nowrap tabular-nums'`. Der Benutzername: `className="font-medium"` bleibt.

Vorher in `web/src/components/data-list.tsx` prüfen, dass `className` auf Kopf **und** Zelle angewendet wird (Desktop-Tabelle); im mobilen Karten-Layout ist `text-right` harmlos.

- [ ] **Step 5: Rechte – Warnungen und Abstände**

In `permissions-page.tsx` die drei Warnungen durch eine lokale Komponente ersetzen (Datei-lokal, nicht exportieren):

```tsx
function Warning({ children }: { children: string }) {
  return (
    <p role="status" data-tone="danger" className="bevel-inset text-foreground border-destructive mb-4 border-l-4 px-3 py-2 text-sm">
      {children}
    </p>
  )
}
// …
      {filesWarning ? <Warning>{t('permissions.files_warning')}</Warning> : null}
      {pluginsWarning ? <Warning>{t('permissions.plugins_warning')}</Warning> : null}
      {backupsWarning ? <Warning>{t('permissions.backups_warning')}</Warning> : null}
```

Die Berechtigungs-Spalten bekommen `className: 'text-center'` und die Checkbox `className="accent-primary size-4 align-middle"`.

- [ ] **Step 6: Tests grün**

Run: `make check && make test-vm SUITE=web`
Expected: PASS (`users-page.test.tsx`, `permissions-page.test.tsx`, `audit-page.test.tsx`, `audit-filters.test.ts`).

- [ ] **Step 7: Commit**

```bash
git add web/src/features/users web/src/features/audit web/src/i18n/de.ts web/src/i18n/en.ts
git commit -m "feat(web): dense user, permission and audit tables with one-row filters"
```

(`de.ts`/`en.ts` nur, falls `audit.filters_label` neu angelegt wurde.)

---

### Task 5: Screenshots und Abschluss

**Files:**
- Create: `web/e2e/shots/admin.shots.ts`

**Interfaces:**
- Consumes: `shoot(page, name)`, `signInMock(page, user?)` aus `./shoot` (Welle 0). **Navigation:** Links in `getByRole('navigation', { name: 'Hauptnavigation' })` mit den Namen aus `nav.*` (`Benutzer`, `Security Center`, `Audit-Log`, `Einstellungen`, `Profil`) – T2 behält diese zugänglichen Namen in der Icon-Leiste.

- [ ] **Step 1: `admin.shots.ts`**

```ts
import { expect, test } from '@playwright/test'
import { shoot, signInMock } from './shoot'

async function openNav(page: import('@playwright/test').Page, name: string) {
  await page.getByRole('navigation', { name: 'Hauptnavigation' }).getByRole('link', { name, exact: true }).click()
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
}

test('admin: auth screens', async ({ page }) => {
  await page.goto('/')
  await page.getByLabel('Benutzername').fill('admin')
  await page.getByLabel('Passwort').fill('correct-horse-battery')
  await page.getByRole('button', { name: 'Anmelden' }).click()
  await expect(page).toHaveURL(/\/login\/2fa$/)
  await shoot(page, 'admin-2fa')
})

test('admin: setup-2fa for a user without second factor', async ({ page }) => {
  await signInMock(page, 'alice')
  test.skip(!page.url().endsWith('/setup-2fa'), 'alice already has a second factor in the mock')
  await shoot(page, 'admin-setup-2fa')
})

test('admin: pages', async ({ page }) => {
  await signInMock(page)
  await openNav(page, 'Benutzer')
  await shoot(page, 'admin-users')
  await page.getByRole('link', { name: /Rechte für alice|alice/ }).first().click()
  await expect(page.getByRole('heading', { name: 'Rechte von alice' })).toBeVisible()
  await shoot(page, 'admin-permissions')
  await openNav(page, 'Security Center')
  await shoot(page, 'admin-security')
  await openNav(page, 'Audit-Log')
  await shoot(page, 'admin-audit')
  await openNav(page, 'Einstellungen')
  await shoot(page, 'admin-settings')
  await openNav(page, 'Profil')
  await shoot(page, 'admin-profile')
  await openNav(page, 'Benutzer')
  await page.getByRole('button', { name: 'Benutzer anlegen' }).click()
  await shoot(page, 'admin-create-user-dialog')
})
```

Den Link-Namen „Rechte für alice“ vorher aus `users.permissions_for` in `de.ts` übernehmen und das Regex durch den exakten Namen ersetzen. Funktioniert `openNav` auf dem Integrationsstand noch nicht (T2 noch nicht gemergt, alte Sidebar hat dieselben Link-Namen in `nav` „Hauptnavigation“ – sollte funktionieren), das im Report vermerken.

- [ ] **Step 2: Screenshots erzeugen und ansehen**

Run: `make check && make test-vm SUITE=shots`
Expected: Dateien `web/screenshots/admin-*-{dark,light}-{desktop,mobile}.png` (plus `foundation-*`). Mit dem Read-Tool mindestens ansehen: `admin-2fa-dark-mobile`, `admin-2fa-light-desktop`, `admin-security-dark-desktop`, `admin-audit-light-desktop`, `admin-users-dark-mobile`, `admin-settings-light-desktop`. Prüfen:
- Login/2FA: Grasblock + Marke über dem Panel, Pixelmuster dahinter, keine horizontale Scrollleiste bei 390 px, Theme-Toggle überdeckt das Panel nicht.
- Security: vier Slots vertieft, Balken grün/rot, eckig.
- Audit: Filter auf Desktop in **einer** Zeile.
- Einstellungen/Profil: eine Spalte, max. ca. 768 px breit, 24 px zwischen Sections.
Abweichungen beheben, erneut `SUITE=shots`.

- [ ] **Step 3: Gesamtprüfung**

Run: `make check && make test-vm SUITE=web && make test-vm SUITE=lint`
Expected: alles grün.

- [ ] **Step 4: Commit und Push**

```bash
git add web/e2e/shots/admin.shots.ts
git commit -m "test(web): add admin screenshots for visual review"
git push -u origin feat/ui-admin
```

- [ ] **Step 5: Report an den Lead**

Kurz melden: Branch `feat/ui-admin`, Commits, Testergebnis, Pfade der sechs angesehenen Screenshots, neue i18n-Schlüssel (falls `audit.filters_label`), offene Punkte (z. B. Navigation in Screenshots abhängig von T2). Keinen PR öffnen – der Lead integriert T5 in `feat/ui-pages`.
