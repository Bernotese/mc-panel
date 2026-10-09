# Stream E – Frontend – Implementierungsplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Das komplette React-Frontend von mc-panel (Login mit 2FA, erzwungenes 2FA-Setup, Dashboard, Benutzer- und Rechteverwaltung, Security Center, Audit-Log, Profil) gegen den OpenAPI-Vertrag bauen – vollständig lauffähig gegen MSW-Mocks, bereit für das echte Backend in Phase 2.

**Architecture:** Ein typisierter `openapi-fetch`-Client mit einer Middleware kapselt alles Sitzungsspezifische (CSRF-Token, Session-Rotation, 401-Umleitung, globaler Sudo-Dialog mit genau einem Wiederholungsversuch). React Query hält Serverzustand inkl. des `Me`-Objekts, das die Middleware nach jeder Stufenänderung aktualisiert; TanStack-Router-Guards entscheiden anhand von `Me.stage`/`Me.role`. Seiten liegen als kleine Feature-Module unter `src/features/*`, Route-Dateien unter `src/routes/*` sind dünne Hüllen. MSW simuliert alle 57 Operationen mit In-Memory-Zustand für Dev (`npm run dev:mock`) und Tests.

**Tech Stack:** React 19 + TypeScript + Vite, TanStack Router (file-based) + TanStack Query v5, Tailwind v4, shadcn/ui (+ `sheet`), openapi-typescript + openapi-fetch, i18next/react-i18next, qrcode, @simplewebauthn/browser, sonner, lucide-react, MSW 2, Vitest + Testing Library + user-event, Playwright.

**Spec:** `docs/superpowers/specs/2026-10-08-mc-panel-foundation-design.md` (insbesondere §4 Login-Ablauf, §4.5 Sudo, §4.6 CSRF, §7.4 Security Center, §8 Frontend)

## Global Constraints

Es gelten alle Constraints aus `docs/superpowers/plans/2026-10-08-foundation-00-overview.md` und die Regeln aus dem Phase-0-Plan. Frontend-spezifisch, verbindlich:

- Eigentum: nur `web/**` (außer `web/go.mod`). Keine Backend-Dateien, kein `api/openapi.yaml` ändern.
- **Keine neuen npm-Abhängigkeiten.** `package.json` ändert sich nur durch `msw init` (`msw.workerDirectory`) und neue Skripte (`dev:mock`, `e2e`, `e2e:real`). Neue shadcn-Komponente nur `sheet` (nutzt dieselbe Radix-Dialog-Basis wie `dialog`).
- API-Präfix `/api`; Client-`baseUrl` = `${window.location.origin}/api`; `credentials: 'same-origin'`.
- CSRF-Header exakt `X-CSRF-Token`; Wert ausschließlich aus `Me.csrf_token`, nur im Speicher (nie `localStorage`).
- Fehlerformat `{"error":"<code>","message":"<text>","request_id"?}`; Codes exakt aus dem `Error`-Enum (17 Codes).
- Sudo-Gültigkeit 10 min (Backend); Frontend wiederholt eine `sudo_required`-Anfrage **höchstens einmal**.
- Theme: Klassen-Strategie (`<html class="dark">`), `localStorage`-Schlüssel exakt `mcpanel.theme`, Werte `light|dark|system`, Standard `system`, angewendet in `main.tsx` vor dem ersten Render – kein Inline-Script.
- CSP: `script-src 'self'`, `img-src 'self' data:` → QR-Codes als `data:`-URL in `<img>`; keine externen CDNs, keine Webfonts von Dritten.
- Sprachen: `de` (Standard), `en`; nach Login `Me.locale`, vorher `navigator.languages` (erste passende), sonst `de`. Datum/Zeit nur über `Intl` mit der aktiven Sprache.
- Breakpoint: `md` = 768 px (`DESKTOP_QUERY = '(min-width: 768px)'`); darunter Karten statt Tabellen und Sheet statt Sidebar; Mindestbreite 360 px.
- MSW: `VITE_MSW=1` (oder `npm run dev:mock` = `vite --mode mock` mit `web/.env.mock`) aktiviert den Browser-Worker; Tests nutzen `msw/node` mit `onUnhandledRequest: 'error'`. `mockServiceWorker.js` wird nie in `internal/webui/dist` ausgeliefert.
- Mock-Konten: Passwort `correct-horse-battery`, TOTP-Code `123456`, Benutzer `admin` (TOTP+Passkey), `alice` (user, ohne 2FA), `newadmin` (admin, ohne 2FA → Setup), `bob` (user, TOTP, Sprache `en`).
- Rechte-Semantik (Stream D): Jedes Recht auf einem Server schließt `server.view` ein – die Matrix setzt „Ansehen“ automatisch mit und entfernt beim Abwählen von „Ansehen“ alle Rechte des Servers.
- Agent-Status darf in Dev `reachable: false` (oder `503 agent_unavailable`) liefern; die UI zeigt das als neutralen Hinweis, nie als Absturz.
- Tests: `cd web && npx vitest run <pfad>`; Typprüfung `npm run typecheck`; nach neuen Route-Dateien zuerst `npx vite build` (erzeugt `src/routeTree.gen.ts`).
- Imports von Typen immer mit `import type` / `type`-Modifier (`verbatimModuleSyntax`); keine ungenutzten Imports (`noUnusedLocals`).
- Commits: Conventional Commits mit zweitem `-m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"`.

## Review Focus

1. **Sudo-Wiederholungsschleife:** Liefert das Backend nach erfolgreicher Bestätigung erneut `sudo_required` (Uhrenabweichung, Rotation verpasst), darf weder der Dialog endlos wieder aufgehen noch die Anfrage mehrfach gesendet werden; parallele Anfragen teilen sich einen Dialog. → Task 5 (`retries a sudo_required request at most once`), Task 6 (`shares the pending promise`), Task 8 (`shows a single dialog for concurrent sudo requests`).
2. **CSRF-Token nach Session-Rotation veraltet:** Login, 2FA, Sudo, TOTP-Aktivierung, Passkey-Registrierung und Passwortwechsel erzeugen eine neue Session; ohne Nachladen von `/auth/me` scheitert die nächste Mutation mit `csrf_invalid`. Gleichzeitig darf ein falscher 2FA-Code (`401 invalid_code`) nicht zum Logout führen. → Task 5 (`refetches Me after an endpoint that rotates the session`, `recovers exactly once from a stale CSRF token`, `redirects on 401 unauthorized but not on wrong passwords or codes`).
3. **Veraltetes `Me` nach Stufenwechsel:** Nach 2FA muss der Cache `stage: 'full'` enthalten, bevor navigiert wird, sonst schickt der `_app`-Guard zurück auf `/login/2fa` (Schleife); umgekehrt dürfen die Recovery-Codes beim Setup nicht verschwinden, nur weil `Me` inzwischen `full` ist. → Task 10 (`lands on / with a fresh full-stage Me after a valid TOTP code`, `keeps the recovery codes on screen after Me turned full`).
4. **Mobile Darstellung (360 px):** Tabellen mit 6–8 Spalten sind auf dem Handy unbenutzbar, die Sidebar verdeckt den Inhalt. → Task 2 (`collapses into labelled cards on a 360px viewport`), Task 14 (Login-Versuche als Karten), Task 20 (`uses a sheet instead of the sidebar on phones`), Task 21 (Playwright-Smoke mit 360×740).
5. **WebAuthn nicht unterstützt:** In Browsern ohne `PublicKeyCredential` (ältere WebViews, manche Firmen-Policies) dürfen keine Passkey-Buttons erscheinen, die beim Klick werfen; TOTP/Recovery muss nutzbar bleiben. → Task 8 (`offers passkeys only when the browser supports WebAuthn`), Task 9 (`explains that the browser lacks passkey support`), Task 10 (`hides the passkey tab when WebAuthn is unsupported`), Task 19 (Passkey-Karte ohne „hinzufügen“).

## Dateistruktur

```
web/
  index.html                          HTML-Hülle (lang, color-scheme, kein Inline-Script)
  vite.config.ts                      + Plugin stripMswWorker (Worker nie im Embed)
  playwright.config.ts                Projekte "mock" (MSW-Dev-Server :5174) und "real" (E2E_BASE_URL)
  .env.mock                           VITE_MSW=1 für `vite --mode mock`
  public/mockServiceWorker.js         generiert durch `npx msw init public`
  e2e/
    totp.ts                           RFC-6238-TOTP mit node:crypto (keine Abhängigkeit)
    totp.spec.ts                      RFC-Testvektoren
    smoke.spec.ts                     Login+TOTP, Security Center, Mobile-Navigation gegen MSW
    login-totp.spec.ts                frischer Admin: erzwungenes TOTP-Setup + Re-Login (echtes Backend)
  src/
    main.tsx                          Theme anwenden, MSW optional starten, Router/Query bereitstellen
    router.ts                         createAppRouter, RouterContext, Register-Deklaration
    routeTree.gen.ts                  generiert (TanStack Router Plugin)
    vite-env.d.ts                     ImportMetaEnv.VITE_MSW
    api/
      schema.d.ts                     generiert (npm run gen:api)
      types.ts                        Aliase auf components['schemas'][…] + ALL_PERMISSIONS
      csrf.ts                         In-Memory-CSRF-Token
      handlers.ts                     injizierbare Callbacks (onUnauthorized, onSetupRequired, requestSudo, onMe)
      errors.ts                       ApiError, toApiError, unwrap, unwrapVoid
      client.ts                       openapi-fetch-Client + Middleware
    app/
      query-client.ts                 QueryClient-Defaults
      wire-api.ts                     verbindet Middleware mit Router, Query-Cache, Sudo-Store
    i18n/
      index.ts                        i18next-Init, detectLocale, setLocale
      de.ts / en.ts                   Ressourcen (en ist per Typ an de gebunden)
    lib/
      format.ts                       Intl-Formatierung + useFormat
      theme.ts                        Theme-Präferenz (mcpanel.theme)
      use-media-query.ts              useMediaQuery, DESKTOP_QUERY
      download.ts                     downloadText (Recovery-Codes als .txt)
      error-message.ts                ApiError → Text, useErrorToast, isWebAuthnCancelled
      user-agent.ts                   describeUserAgent (Sitzungslisten)
      utils.ts                        cn (shadcn)
    components/
      ui/*                            shadcn (button input label card dialog table tabs badge … sheet)
      app-toaster.tsx                 sonner-Toaster mit Theme
      theme-toggle.tsx                Hell/Dunkel/System-Umschalter
      native-select.tsx               zugängliches natives <select>
      confirm-dialog.tsx              Bestätigungsdialog
      page-header.tsx                 Seitentitel + Aktionen
      data-list.tsx                   Tabelle (Desktop) / Karten (Mobil)
      not-found-page.tsx              404
      code-form.tsx                   Code-Eingabe (TOTP/Recovery/Sudo)
    features/
      auth/        me.ts guards.ts route-guards.ts auth-layout.tsx login-page.tsx second-factor-page.tsx use-logout.ts logout-button.tsx
      setup/       recovery-codes-panel.tsx totp-setup-card.tsx passkey-setup-card.tsx setup-2fa-page.tsx
      sudo/        sudo-store.ts sudo-dialog.tsx
      webauthn/    webauthn.ts
      layout/      nav.ts app-layout.tsx
      dashboard/   servers.ts server-card.tsx create-server-dialog.tsx dashboard-page.tsx
      users/       users-page.tsx create-user-dialog.tsx permission-matrix.ts permissions-page.tsx
      security/    bar-chart.tsx overview-tab.tsx agent-status-card.tsx attempts-tab.tsx jails-tab.tsx create-jail-dialog.tsx
                   ip-rules-tab.tsx create-ip-rule-dialog.tsx countries.ts settings-tab.tsx notifiers-tab.tsx notifier-dialog.tsx security-page.tsx
      audit/       audit-filters.ts audit-page.tsx
      profile/     section-card.tsx language-card.tsx password-card.tsx session-columns.tsx sessions-card.tsx admin-sessions-card.tsx
                   two-factor-card.tsx recovery-card.tsx passkeys-card.tsx profile-page.tsx
    mocks/
      db.ts                           In-Memory-Zustand, Seeds, Helfer (loginAs …)
      handlers/util.ts                guard (Session→CSRF→Stage→Rolle→Sudo), op, paginate …
      handlers/{auth,me,admin,users,servers,audit,security}.ts   57 Operationen
      handlers.ts                     operations + handlers
      node.ts / browser.ts            setupServer / setupWorker
    routes/
      __root.tsx                      Outlet + SudoDialog + Toaster
      login/index.tsx  login/2fa.tsx  setup-2fa.tsx
      _app.tsx                        Guard (stage full) + AppLayout
      _app/index.tsx  _app/users/index.tsx  _app/users/$id.permissions.tsx  _app/security.tsx  _app/audit.tsx  _app/profile.tsx
    test/
      setup.ts                        jest-dom, Polyfills, MSW-Lebenszyklus, Resets
      media.ts                        steuerbares matchMedia (Viewport, Dark-Preference)
      call-api.ts                     roher fetch-Helfer für Mock-Tests
      render.tsx                      renderWithProviders (Memory-Router, QueryClient, SudoDialog, Toaster)
```

Gelöscht werden: `src/routes/index.tsx`, `src/routes/index.test.tsx` (Phase-0-Platzhalter, ersetzt durch `_app/index.tsx`), `src/App.tsx`/`App.css`/`assets` falls noch vorhanden, `public/vite.svg`.

---

### Task 1: Test-Infrastruktur, i18n-Ressourcen, Formatierung

**Files:**
- Create: `web/src/test/media.ts`, `web/src/i18n/index.ts`, `web/src/i18n/de.ts`, `web/src/i18n/en.ts`, `web/src/lib/format.ts`, `web/src/api/types.ts`
- Modify: `web/src/test/setup.ts`
- Test: `web/src/i18n/i18n.test.ts`, `web/src/lib/format.test.ts`

**Interfaces:**
- Consumes: Phase-0-Scaffold (`vite.config.ts` mit `setupFiles: ['./src/test/setup.ts']`, i18next, react-i18next)
- Produces:
```ts
// api/types.ts – Aliase auf generierte Schemas (vollständige Liste in Step 5)
export type Me, User, Server, Session, … ; export const ALL_PERMISSIONS: readonly Permission[]
// test/media.ts
export function installMatchMedia(): void
export function setViewport(next: 'desktop' | 'mobile'): void   // desktop = 1280 px, mobile = 360 px
export function setPrefersDark(next: boolean): void
// i18n/index.ts
export const SUPPORTED_LOCALES: readonly Locale[]
export function detectLocale(languages?: readonly string[]): Locale
export const i18nReady: Promise<unknown>
export function setLocale(locale: Locale): void     // i18n + <html lang>
export default i18n
// i18n/de.ts / en.ts
export const de: { … }                              // alle UI-Texte (vollständig, siehe unten)
export type Messages = typeof de
export const en: Messages
// lib/format.ts
export function formatDateTime(ms: number, locale: string): string
export function formatDate(ms: number, locale: string): string
export function formatTime(ms: number, locale: string): string
export function formatRelative(ms: number, locale: string, now?: number): string
export function formatNumber(n: number, locale: string): string
export function useFormat(): { locale: string; dateTime(ms: number): string; date(ms: number): string; time(ms: number): string; relative(ms: number): string; number(n: number): string }
```

Hinweis: Die Ressourcendateien enthalten bereits **alle** Texte des Streams. Der Schlüsselscan-Test (Step 1) prüft ab jetzt nach jeder Task, dass jeder statische `t('…')`-Schlüssel im Code existiert; dynamische Schlüssel sind in `DYNAMIC_KEYS` aufgezählt. Benötigt eine spätere Task einen neuen Text, wird er in **beiden** Dateien ergänzt (TS erzwingt Gleichheit).

- [ ] **Step 1: Failing Tests**

`web/src/i18n/i18n.test.ts`:
```ts
import { describe, expect, it } from 'vitest'
import { de } from './de'
import { en } from './en'
import i18n, { detectLocale } from './index'

function keys(obj: object, prefix = ''): string[] {
  return Object.entries(obj).flatMap(([k, v]) => (typeof v === 'string' ? [`${prefix}${k}`] : keys(v as object, `${prefix}${k}.`)))
}

const ERROR_CODES = [
  'bad_request',
  'validation_failed',
  'unauthorized',
  'invalid_credentials',
  'invalid_code',
  'forbidden',
  'sudo_required',
  'setup_2fa_required',
  'csrf_invalid',
  'not_found',
  'conflict',
  'payload_too_large',
  'rate_limited',
  'jailed',
  'ip_blocked',
  'agent_unavailable',
  'internal',
]

/** Keys built at runtime (template literals) – kept in sync with the components that use them. */
const DYNAMIC_KEYS = [
  ...['view', 'power', 'console', 'files', 'plugins', 'backups'].map((p) => `permission.server.${p}`),
  ...['light', 'dark', 'system'].map((p) => `theme.${p}`),
  ...['start', 'stop', 'restart'].flatMap((a) => [`dashboard.power.${a}`, `dashboard.power_noun.${a}`]),
  ...['admin', 'user'].map((r) => `users.role.${r}`),
  ...['24h', '7d', '30d'].map((r) => `security.overview.ranges.${r}`),
  ...['password', 'totp', 'webauthn', 'recovery', 'sudo'].map((s) => `security.attempts.stage.${s}`),
  ...['allow', 'deny'].map((k) => `security.ip_rules.kind_${k}`),
  ...['discord', 'ntfy'].flatMap((k) => [`security.notifiers.kind_${k}`, `security.notifiers.url_${k}`]),
  ...['success', 'failure', 'denied'].map((r) => `audit.result.${r}`),
  ...['user', 'system', 'cli'].map((a) => `audit.actor_type.${a}`),
  ...['overview', 'attempts', 'jails', 'ip_rules', 'settings', 'notifiers'].map((tab) => `security.tabs.${tab}`),
  ...['de', 'en'].map((l) => `profile.language.${l}`),
  ...['totp', 'webauthn', 'recovery'].map((m) => `second_factor.tab_${m}`),
  ...['dashboard', 'users', 'security', 'audit', 'profile'].map((k) => `nav.${k}`),
]

describe('i18n resources', () => {
  it('has identical key sets for de and en', () => {
    expect(keys(en).sort()).toEqual(keys(de).sort())
  })

  it('translates every API error code', () => {
    for (const code of ERROR_CODES) expect(i18n.exists(`errors.${code}`), code).toBe(true)
  })

  it('defines every dynamic key', () => {
    expect(DYNAMIC_KEYS.filter((k) => !i18n.exists(k))).toEqual([])
  })

  it('defines every static t() key used in the source', () => {
    const sources = import.meta.glob(['/src/**/*.{ts,tsx}', '!/src/**/*.test.{ts,tsx}', '!/src/i18n/**', '!/src/routeTree.gen.ts'], {
      query: '?raw',
      import: 'default',
      eager: true,
    }) as Record<string, string>
    const missing: string[] = []
    for (const [file, code] of Object.entries(sources)) {
      for (const match of code.matchAll(/\bt\(\s*'([A-Za-z0-9_.]+)'/g)) {
        if (!i18n.exists(match[1])) missing.push(`${file}: ${match[1]}`)
      }
    }
    expect(missing).toEqual([])
  })

  it('detects the browser language and falls back to German', () => {
    expect(detectLocale(['en-US', 'de'])).toBe('en')
    expect(detectLocale(['fr-FR', 'de-AT'])).toBe('de')
    expect(detectLocale(['fr'])).toBe('de')
    expect(detectLocale([])).toBe('de')
  })
})
```

`web/src/lib/format.test.ts`:
```ts
import { describe, expect, it } from 'vitest'
import { formatDate, formatDateTime, formatNumber, formatRelative } from './format'

// Noon UTC stays on 8 Oct 2026 in every timezone from UTC-11 to UTC+11.
const NOON = Date.UTC(2026, 9, 8, 12, 0, 0)

describe('format', () => {
  it('formats dates per locale', () => {
    expect(formatDate(NOON, 'de')).toBe('08.10.2026')
    expect(formatDate(NOON, 'en')).toBe('Oct 8, 2026')
    expect(formatDateTime(NOON, 'de')).toContain('08.10.2026')
  })

  it('formats relative times', () => {
    expect(formatRelative(NOON - 5 * 60_000, 'de', NOON)).toBe('vor 5 Minuten')
    expect(formatRelative(NOON - 5 * 60_000, 'en', NOON)).toBe('5 minutes ago')
    expect(formatRelative(NOON + 2 * 3_600_000, 'de', NOON)).toBe('in 2 Stunden')
    expect(formatRelative(NOON - 3 * 86_400_000, 'en', NOON)).toBe('3 days ago')
  })

  it('formats numbers per locale', () => {
    expect(formatNumber(12345, 'de')).toBe('12.345')
    expect(formatNumber(12345, 'en')).toBe('12,345')
  })
})
```

- [ ] **Step 2: Tests laufen lassen → FAIL**

Run: `cd web && npx vitest run src/i18n src/lib/format.test.ts`
Expected: FAIL – `Failed to resolve import "./de"` bzw. `"./format"`.

- [ ] **Step 3: `web/src/i18n/de.ts`**

```ts
export const de = {
  common: {
    cancel: 'Abbrechen',
    save: 'Speichern',
    delete: 'Löschen',
    create: 'Anlegen',
    edit: 'Bearbeiten',
    loading: 'Lädt …',
    load_more: 'Mehr laden',
    apply_filters: 'Filtern',
    reset_filters: 'Filter zurücksetzen',
    yes: 'Ja',
    no: 'Nein',
    retry: 'Erneut versuchen',
  },
  theme: {
    toggle: 'Farbschema wechseln (aktuell: {{current}})',
    light: 'Hell',
    dark: 'Dunkel',
    system: 'System',
  },
  not_found: {
    title: 'Seite nicht gefunden',
    description: 'Diese Adresse gibt es nicht.',
    home: 'Zur Übersicht',
  },
  errors: {
    bad_request: 'Ungültige Anfrage',
    validation_failed: 'Eingaben ungültig',
    unauthorized: 'Bitte melde dich erneut an.',
    invalid_credentials: 'Benutzername oder Passwort falsch.',
    invalid_code: 'Der Code ist ungültig.',
    forbidden: 'Dafür fehlt dir die Berechtigung.',
    sudo_required: 'Diese Aktion muss bestätigt werden.',
    setup_2fa_required: 'Bitte richte zuerst die Zwei-Faktor-Anmeldung ein.',
    csrf_invalid: 'Sitzung abgelaufen. Bitte lade die Seite neu.',
    not_found: 'Nicht gefunden.',
    conflict: 'Konflikt mit dem aktuellen Zustand',
    payload_too_large: 'Die Anfrage ist zu groß.',
    rate_limited: 'Zu viele Versuche. Bitte später erneut versuchen.',
    rate_limited_retry: 'Zu viele Versuche. Bitte in {{seconds}} s erneut versuchen.',
    jailed: 'Deine IP-Adresse ist vorübergehend gesperrt.',
    ip_blocked: 'Zugriff von deiner IP-Adresse ist nicht erlaubt.',
    agent_unavailable: 'Der Agent ist nicht erreichbar.',
    internal: 'Interner Serverfehler.',
    network: 'Server nicht erreichbar. Bitte Verbindung prüfen.',
    unknown: 'Unerwarteter Fehler (HTTP {{status}}).',
  },
  webauthn: {
    cancelled: 'Vorgang abgebrochen oder Zeit abgelaufen.',
  },
  sudo: {
    title: 'Aktion bestätigen',
    description: 'Bestätige diese sensible Aktion mit deinem zweiten Faktor. Die Bestätigung gilt 10 Minuten.',
    code_label: 'Code aus der Authenticator-App',
    confirm: 'Bestätigen',
    tab_totp: 'Authenticator-App',
    tab_passkey: 'Passkey',
    passkey_hint: 'Bestätige mit einem registrierten Passkey.',
    passkey_button: 'Mit Passkey bestätigen',
    no_method: 'Für dein Konto ist keine Bestätigungsmethode verfügbar, die dieser Browser unterstützt.',
  },
  auth: {
    brand: 'mc-panel',
    logout: 'Abmelden',
    other_account: 'Mit anderem Konto anmelden',
  },
  login: {
    title: 'Anmelden',
    description: 'Melde dich mit deinem mc-panel-Konto an.',
    username: 'Benutzername',
    password: 'Passwort',
    submit: 'Anmelden',
    submitting: 'Anmeldung läuft …',
  },
  second_factor: {
    title: 'Zweiter Faktor',
    description: 'Angemeldet als {{username}}. Bestätige die Anmeldung.',
    tab_totp: 'Authenticator-App',
    tab_webauthn: 'Passkey',
    tab_recovery: 'Wiederherstellungscode',
    totp_label: 'Code aus der Authenticator-App',
    recovery_label: 'Einer deiner Wiederherstellungscodes',
    verify: 'Bestätigen',
    passkey_hint: 'Verwende einen registrierten Passkey (z. B. Touch ID oder Sicherheitsschlüssel).',
    passkey_button: 'Mit Passkey anmelden',
    no_method: 'Keine passende Anmeldemethode verfügbar. Wende dich an einen Administrator.',
  },
  setup: {
    title: 'Zwei-Faktor-Anmeldung einrichten',
    description_admin: 'Als Administrator musst du einen zweiten Faktor einrichten, bevor du das Panel nutzen kannst.',
    description_user: 'Für dein Konto ist ein zweiter Faktor vorgeschrieben.',
    tab_totp: 'Authenticator-App',
    tab_passkey: 'Passkey',
    codes_title: 'Fast geschafft',
    finish: 'Weiter zum Panel',
  },
  totp_setup: {
    intro: 'Verknüpfe eine Authenticator-App (z. B. Aegis, 2FAS oder Google Authenticator) mit deinem Konto.',
    start: 'Einrichtung starten',
    scan: 'Scanne den QR-Code mit deiner Authenticator-App.',
    qr_alt: 'QR-Code für die Authenticator-App',
    manual: 'Oder gib diesen Schlüssel manuell ein:',
    code_label: 'Code aus der Authenticator-App',
    confirm: 'Aktivieren',
  },
  passkey: {
    intro: 'Registriere einen Passkey (Touch ID, Windows Hello oder Sicherheitsschlüssel).',
    name_label: 'Name des Passkeys',
    default_name: 'Mein Passkey',
    register: 'Passkey registrieren',
    unsupported: 'Dieser Browser unterstützt keine Passkeys.',
  },
  recovery: {
    title: 'Wiederherstellungscodes',
    description: 'Jeder Code funktioniert genau einmal, falls du keinen Zugriff auf deinen zweiten Faktor hast. Die Codes werden nur jetzt angezeigt.',
    list_label: 'Wiederherstellungscodes',
    copy: 'Kopieren',
    copied: 'Codes in die Zwischenablage kopiert.',
    copy_failed: 'Kopieren fehlgeschlagen.',
    download: 'Als Textdatei herunterladen',
    file_header: 'mc-panel – Wiederherstellungscodes (jeder Code ist einmal gültig)',
    confirm: 'Ich habe die Codes sicher gespeichert.',
  },
  nav: {
    label: 'Hauptnavigation',
    dashboard: 'Übersicht',
    users: 'Benutzer',
    security: 'Security Center',
    audit: 'Audit-Log',
    profile: 'Profil',
    open_menu: 'Menü öffnen',
    menu_description: 'Navigation zu allen Bereichen des Panels',
    skip: 'Zum Inhalt springen',
    signed_in_as: 'Angemeldet als {{username}}',
  },
  permission: {
    server: {
      view: 'Ansehen',
      power: 'Starten/Stoppen',
      console: 'Konsole',
      files: 'Dateien',
      plugins: 'Plugins',
      backups: 'Backups',
    },
  },
  dashboard: {
    title: 'Server',
    description: 'Deine Minecraft-Server. Die Instanzverwaltung folgt in einem späteren Teilprojekt.',
    empty: 'Keine Server verfügbar.',
    create_server: 'Server anlegen',
    create_description: 'Legt einen Stub-Server an, damit Rechte vergeben werden können.',
    name_label: 'Name',
    name_hint: '2–32 Zeichen: Kleinbuchstaben, Ziffern, Bindestrich.',
    name_invalid: 'Der Name entspricht nicht den Regeln.',
    created: 'Server „{{name}}“ angelegt.',
    deleted: 'Server „{{name}}“ gelöscht.',
    delete_for: '{{name}} löschen',
    delete_title: 'Server „{{name}}“ löschen?',
    delete_description: 'Alle Rechte auf diesen Server werden entfernt.',
    stub_hint: 'Stub-Server (noch ohne Instanz)',
    permissions_label: 'Deine Rechte',
    power_group: 'Steuerung für {{name}}',
    no_power: 'Keine Berechtigung für Start/Stopp.',
    power: {
      start: 'Starten',
      stop: 'Stoppen',
      restart: 'Neu starten',
    },
    power_noun: {
      start: 'Start',
      stop: 'Stopp',
      restart: 'Neustart',
    },
    power_requested: '{{action}} für „{{name}}“ angefordert.',
  },
  users: {
    title: 'Benutzer',
    description: 'Konten verwalten, Rollen und Rechte vergeben.',
    caption: 'Benutzerliste',
    create: 'Benutzer anlegen',
    create_description: 'Neue Konten haben keine Rechte, bis du sie vergibst.',
    username: 'Benutzername',
    username_hint: '3–32 Zeichen: Buchstaben, Ziffern, Punkt, Unterstrich, Bindestrich.',
    username_invalid: 'Der Benutzername entspricht nicht den Regeln.',
    password: 'Initiales Passwort',
    password_hint: 'Mindestens 12 Zeichen.',
    password_invalid: 'Das Passwort muss 12 bis 256 Zeichen lang sein.',
    role_label: 'Rolle',
    role: {
      admin: 'Administrator',
      user: 'Benutzer',
    },
    role_for: 'Rolle von {{name}}',
    role_changed: 'Rolle von {{name}} geändert: {{role}}',
    created: 'Benutzer {{name}} angelegt.',
    deleted: 'Benutzer {{name}} gelöscht.',
    you: '(du)',
    never: 'noch nie',
    col: {
      username: 'Benutzername',
      role: 'Rolle',
      twofa: '2FA',
      last_login: 'Letzte Anmeldung',
      actions: 'Aktionen',
    },
    twofa_none: 'keine',
    twofa_totp: 'TOTP',
    twofa_passkeys: 'Passkeys: {{n}}',
    permissions: 'Rechte',
    permissions_for: 'Rechte von {{name}}',
    reset_2fa: '2FA zurücksetzen',
    reset_for: '2FA von {{name}} zurücksetzen',
    reset_title: '2FA von {{name}} zurücksetzen?',
    reset_description: 'TOTP, Passkeys und Wiederherstellungscodes werden gelöscht. Das Konto muss 2FA neu einrichten.',
    reset_done: '2FA von {{name}} zurückgesetzt.',
    delete_for: '{{name}} löschen',
    delete_title: 'Benutzer {{name}} löschen?',
    delete_description: 'Das Konto und alle Sitzungen werden entfernt. Das kann nicht rückgängig gemacht werden.',
  },
  permissions: {
    title: 'Rechte von {{name}}',
    description: 'Rechte pro Server. Jedes Recht schließt „Ansehen“ ein.',
    back: 'Zurück zu den Benutzern',
    caption: 'Rechte-Matrix',
    col_server: 'Server',
    checkbox: '{{server}}: {{permission}}',
    saved: 'Rechte gespeichert.',
    admin_note: 'Administratoren haben automatisch alle Rechte auf allen Servern.',
    no_servers: 'Noch keine Server angelegt.',
    unsaved: 'Ungespeicherte Änderungen',
  },
  security: {
    title: 'Security Center',
    description: 'Login-Versuche, Sperren, IP-Regeln und Benachrichtigungen.',
    tabs: {
      overview: 'Übersicht',
      attempts: 'Login-Versuche',
      jails: 'Jails',
      ip_rules: 'IP-Regeln',
      settings: 'Einstellungen',
      notifiers: 'Benachrichtigungen',
    },
    overview: {
      range_label: 'Zeitraum',
      ranges: {
        '24h': '24 Stunden',
        '7d': '7 Tage',
        '30d': '30 Tage',
      },
      chart_title: 'Login-Versuche ({{range}})',
      success: 'Erfolgreich',
      failure: 'Fehlgeschlagen',
      time: 'Zeit',
      failures_total: 'Fehlversuche',
      active_jails: 'Aktive Jails',
      top_ips: 'Top-IPs (Fehlversuche)',
      top_countries: 'Top-Länder (Fehlversuche)',
      no_countries: 'Keine Länderdaten (GeoIP nicht verfügbar).',
      no_data: 'Keine Daten im Zeitraum.',
      new_devices: 'Logins von neuen Geräten/IPs',
    },
    agent: {
      title: 'Agent',
      reachable: 'Erreichbar',
      unreachable: 'Nicht erreichbar',
      unreachable_hint: 'Sperren wirken weiterhin in der App; die Firewall wird nachsynchronisiert, sobald der Agent antwortet.',
      firewall: 'Firewall',
      version: 'Version',
      pending_sync: 'Ausstehende Synchronisationen',
    },
    attempts: {
      caption: 'Login-Versuche',
      result_label: 'Ergebnis',
      result_all: 'Alle',
      result_success: 'Erfolgreich',
      result_failure: 'Fehlgeschlagen',
      ip_label: 'IP-Adresse',
      col: {
        time: 'Zeit',
        username: 'Benutzer',
        ip: 'IP',
        stage: 'Schritt',
        result: 'Ergebnis',
        reason: 'Grund',
        user_agent: 'User-Agent',
      },
      stage: {
        password: 'Passwort',
        totp: 'TOTP',
        webauthn: 'Passkey',
        recovery: 'Wiederherstellung',
        sudo: 'Sudo',
      },
      empty: 'Keine Login-Versuche gefunden.',
    },
    jails: {
      caption: 'Jails',
      only_active: 'Nur aktive',
      create: 'IP sperren',
      create_description: 'Sperrt eine IP-Adresse manuell (IPv6 wird auf /64 normalisiert).',
      ip: 'IP-Adresse',
      duration: 'Dauer (Minuten)',
      duration_invalid: 'Die Dauer muss zwischen 1 und 10080 Minuten liegen.',
      reason: 'Grund',
      created: '{{ip}} gesperrt.',
      lift: 'Aufheben',
      lift_for: 'Sperre für {{ip}} aufheben',
      lifted: 'Sperre für {{ip}} aufgehoben.',
      col: {
        ip: 'IP',
        reason: 'Grund',
        level: 'Stufe',
        since: 'Seit',
        until: 'Bis',
        status: 'Status',
        firewall: 'Firewall',
      },
      status_active: 'Aktiv',
      status_lifted: 'Aufgehoben von {{by}}',
      status_expired: 'Abgelaufen',
      manual: 'manuell',
      synced: 'synchronisiert',
      pending: 'ausstehend',
      empty: 'Keine Jails.',
    },
    ip_rules: {
      caption: 'IP-Regeln',
      create: 'Regel hinzufügen',
      create_description: 'CIDR-Notation, z. B. 203.0.113.0/24 oder 2001:db8::/48.',
      cidr: 'CIDR',
      cidr_invalid: 'Ungültige CIDR-Notation.',
      kind: 'Art',
      kind_allow: 'Erlauben',
      kind_deny: 'Sperren',
      comment: 'Kommentar',
      expires: 'Läuft ab (optional)',
      never: 'nie',
      created: 'Regel {{cidr}} hinzugefügt.',
      deleted: 'Regel {{cidr}} gelöscht.',
      delete_for: 'Regel {{cidr}} löschen',
      delete_title: 'Regel {{cidr}} löschen?',
      delete_description: 'Die Regel wird sofort entfernt.',
      col: {
        cidr: 'CIDR',
        kind: 'Art',
        comment: 'Kommentar',
        expires: 'Ablauf',
        created_by: 'Angelegt von',
        created_at: 'Angelegt',
      },
      empty: 'Keine IP-Regeln.',
    },
    settings: {
      enforce_2fa: 'Zwei-Faktor-Anmeldung für alle Benutzer erzwingen',
      enforce_2fa_hint: 'Administratoren müssen 2FA immer einrichten.',
      allowlist_only: 'Admin-Zugang nur aus der Allowlist',
      allowlist_only_hint: 'Administratoren können sich nur aus Netzen mit „Erlauben“-Regel anmelden. Änderungen, die dich aussperren würden, lehnt der Server ab.',
      countries: 'Erlaubte Länder',
      countries_hint: 'ISO-Ländercodes, durch Komma getrennt (z. B. DE, AT, CH). Leer = alle Länder.',
      countries_invalid: 'Ungültige Ländercodes: {{codes}}',
      geoip_missing: 'Keine GeoIP-Datenbank gefunden – der Länderfilter ist deaktiviert.',
      saved: 'Einstellungen gespeichert.',
    },
    notifiers: {
      caption: 'Benachrichtigungen',
      create: 'Benachrichtigung hinzufügen',
      edit_title: 'Benachrichtigung bearbeiten',
      create_description: 'Discord-Webhook oder ntfy-Topic für Sicherheitsereignisse.',
      kind: 'Dienst',
      kind_discord: 'Discord',
      kind_ntfy: 'ntfy',
      name: 'Name',
      enabled: 'Aktiv',
      enabled_for: '{{name}} aktiv',
      url_discord: 'Webhook-URL',
      url_ntfy: 'Server-URL',
      url_keep: 'Leer lassen, um die gespeicherte URL zu behalten.',
      topic: 'Topic',
      token: 'Zugangstoken (optional)',
      test: 'Test senden',
      test_for: 'Testnachricht an {{name}} senden',
      test_sent: 'Testnachricht an {{name}} gesendet.',
      created: 'Benachrichtigung {{name}} angelegt.',
      updated: 'Benachrichtigung {{name}} gespeichert.',
      deleted: 'Benachrichtigung {{name}} gelöscht.',
      edit_for: '{{name}} bearbeiten',
      delete_for: '{{name}} löschen',
      delete_title: '{{name}} löschen?',
      delete_description: 'Es werden keine Ereignisse mehr an dieses Ziel gesendet.',
      url_invalid: 'Bitte eine http(s)-URL angeben.',
      topic_required: 'ntfy benötigt ein Topic.',
      name_required: 'Bitte einen Namen angeben (max. 64 Zeichen).',
      empty: 'Keine Benachrichtigungen eingerichtet.',
      col: {
        name: 'Name',
        kind: 'Dienst',
        target: 'Ziel',
        enabled: 'Aktiv',
        actions: 'Aktionen',
      },
    },
  },
  audit: {
    title: 'Audit-Log',
    description: 'Alle sicherheitsrelevanten Aktionen, manipulationssicher verkettet.',
    caption: 'Audit-Einträge',
    actor: 'Akteur',
    action: 'Aktion',
    result_label: 'Ergebnis',
    result_all: 'Alle',
    from: 'Von',
    to: 'Bis',
    result: {
      success: 'Erfolg',
      failure: 'Fehler',
      denied: 'Abgelehnt',
    },
    actor_type: {
      user: 'Benutzer',
      system: 'System',
      cli: 'CLI',
    },
    col: {
      time: 'Zeit',
      actor: 'Akteur',
      action: 'Aktion',
      target: 'Ziel',
      ip: 'IP',
      result: 'Ergebnis',
      details: 'Details',
      hash: 'Hash',
    },
    chain_ok: 'Kette gültig ({{checked}} Einträge geprüft)',
    chain_broken: 'Kette ungültig ab Eintrag #{{id}}',
    chain_checking: 'Kette wird geprüft …',
    chain_error: 'Kettenprüfung fehlgeschlagen',
    verify_again: 'Erneut prüfen',
    empty: 'Keine Einträge gefunden.',
  },
  profile: {
    title: 'Profil',
    description: 'Konto, Sicherheit und Sitzungen.',
    language: {
      title: 'Sprache',
      label: 'Sprache der Oberfläche',
      de: 'Deutsch',
      en: 'English',
      saved: 'Sprache gespeichert.',
    },
    password: {
      title: 'Passwort ändern',
      current: 'Aktuelles Passwort',
      new: 'Neues Passwort',
      confirm: 'Neues Passwort wiederholen',
      hint: 'Mindestens 12 Zeichen. Alle anderen Sitzungen werden abgemeldet.',
      too_short: 'Das neue Passwort muss 12 bis 256 Zeichen lang sein.',
      mismatch: 'Die Passwörter stimmen nicht überein.',
      wrong_current: 'Das aktuelle Passwort ist falsch.',
      submit: 'Passwort ändern',
      changed: 'Passwort geändert. Andere Sitzungen wurden abgemeldet.',
    },
    totp: {
      title: 'Authenticator-App (TOTP)',
      enabled: 'Aktiv',
      disabled: 'Nicht eingerichtet',
      setup: 'Einrichten',
      setup_title: 'Authenticator-App einrichten',
      setup_description: 'Scanne den QR-Code und bestätige mit einem Code.',
      disable: 'Deaktivieren',
      disable_title: 'Authenticator-App deaktivieren?',
      disable_description: 'Danach kannst du dich nur noch per Passkey oder Wiederherstellungscode anmelden.',
      disabled_done: 'Authenticator-App deaktiviert.',
      enabled_done: 'Authenticator-App aktiviert.',
    },
    recovery: {
      title: 'Wiederherstellungscodes',
      remaining: 'Verbleibende Codes: {{remaining}}',
      regenerate: 'Neue Codes erzeugen',
      regenerate_title: 'Neue Wiederherstellungscodes erzeugen?',
      regenerate_description: 'Alle bisherigen Codes werden sofort ungültig.',
      new_codes_title: 'Neue Wiederherstellungscodes',
      done: 'Fertig',
    },
    passkeys: {
      title: 'Passkeys',
      caption: 'Registrierte Passkeys',
      add: 'Passkey hinzufügen',
      add_description: 'Der Browser fragt nach Touch ID, Windows Hello oder deinem Sicherheitsschlüssel.',
      added: 'Passkey {{name}} hinzugefügt.',
      deleted: 'Passkey {{name}} entfernt.',
      delete_for: '{{name}} entfernen',
      delete_title: 'Passkey {{name}} entfernen?',
      delete_description: 'Der Passkey kann danach nicht mehr zur Anmeldung verwendet werden.',
      col: {
        name: 'Name',
        created: 'Hinzugefügt',
        last_used: 'Zuletzt verwendet',
        actions: 'Aktionen',
      },
      never: 'nie',
      empty: 'Keine Passkeys registriert.',
    },
    sessions: {
      title: 'Aktive Sitzungen',
      caption: 'Deine Sitzungen',
      all_title: 'Alle Sitzungen (Admin)',
      all_caption: 'Sitzungen aller Benutzer',
      current: 'Diese Sitzung',
      revoke: 'Abmelden',
      revoke_for: 'Sitzung {{device}} abmelden',
      revoked: 'Sitzung abgemeldet.',
      revoke_others: 'Alle anderen abmelden',
      revoke_others_title: 'Alle anderen Sitzungen abmelden?',
      revoke_others_description: 'Alle Geräte außer diesem werden abgemeldet.',
      revoked_others: 'Andere Sitzungen abgemeldet.',
      device: '{{browser}} auf {{os}}',
      unknown: 'Unbekannt',
      col: {
        user: 'Benutzer',
        device: 'Gerät',
        location: 'IP / Land',
        last_seen: 'Zuletzt aktiv',
        created: 'Angemeldet seit',
        actions: 'Aktionen',
      },
      empty: 'Keine Sitzungen.',
    },
  },
}

export type Messages = typeof de
```

- [ ] **Step 4: `web/src/i18n/en.ts`**

```ts
import type { Messages } from './de'

export const en: Messages = {
  common: {
    cancel: 'Cancel',
    save: 'Save',
    delete: 'Delete',
    create: 'Create',
    edit: 'Edit',
    loading: 'Loading …',
    load_more: 'Load more',
    apply_filters: 'Filter',
    reset_filters: 'Reset filters',
    yes: 'Yes',
    no: 'No',
    retry: 'Try again',
  },
  theme: {
    toggle: 'Change color scheme (current: {{current}})',
    light: 'Light',
    dark: 'Dark',
    system: 'System',
  },
  not_found: {
    title: 'Page not found',
    description: 'This address does not exist.',
    home: 'Back to overview',
  },
  errors: {
    bad_request: 'Invalid request',
    validation_failed: 'Invalid input',
    unauthorized: 'Please sign in again.',
    invalid_credentials: 'Wrong username or password.',
    invalid_code: 'The code is invalid.',
    forbidden: 'You do not have permission for this.',
    sudo_required: 'This action needs to be confirmed.',
    setup_2fa_required: 'Please set up two-factor authentication first.',
    csrf_invalid: 'Session expired. Please reload the page.',
    not_found: 'Not found.',
    conflict: 'Conflicts with the current state',
    payload_too_large: 'The request is too large.',
    rate_limited: 'Too many attempts. Please try again later.',
    rate_limited_retry: 'Too many attempts. Please try again in {{seconds}} s.',
    jailed: 'Your IP address is temporarily blocked.',
    ip_blocked: 'Access from your IP address is not allowed.',
    agent_unavailable: 'The agent is not reachable.',
    internal: 'Internal server error.',
    network: 'Server unreachable. Please check your connection.',
    unknown: 'Unexpected error (HTTP {{status}}).',
  },
  webauthn: {
    cancelled: 'Cancelled or timed out.',
  },
  sudo: {
    title: 'Confirm action',
    description: 'Confirm this sensitive action with your second factor. The confirmation lasts 10 minutes.',
    code_label: 'Code from your authenticator app',
    confirm: 'Confirm',
    tab_totp: 'Authenticator app',
    tab_passkey: 'Passkey',
    passkey_hint: 'Confirm with a registered passkey.',
    passkey_button: 'Confirm with passkey',
    no_method: 'Your account has no confirmation method that this browser supports.',
  },
  auth: {
    brand: 'mc-panel',
    logout: 'Sign out',
    other_account: 'Sign in with another account',
  },
  login: {
    title: 'Sign in',
    description: 'Sign in with your mc-panel account.',
    username: 'Username',
    password: 'Password',
    submit: 'Sign in',
    submitting: 'Signing in …',
  },
  second_factor: {
    title: 'Second factor',
    description: 'Signed in as {{username}}. Confirm the sign-in.',
    tab_totp: 'Authenticator app',
    tab_webauthn: 'Passkey',
    tab_recovery: 'Recovery code',
    totp_label: 'Code from your authenticator app',
    recovery_label: 'One of your recovery codes',
    verify: 'Confirm',
    passkey_hint: 'Use a registered passkey (e.g. Touch ID or a security key).',
    passkey_button: 'Sign in with passkey',
    no_method: 'No suitable sign-in method available. Please contact an administrator.',
  },
  setup: {
    title: 'Set up two-factor authentication',
    description_admin: 'As an administrator you must set up a second factor before using the panel.',
    description_user: 'Your account requires a second factor.',
    tab_totp: 'Authenticator app',
    tab_passkey: 'Passkey',
    codes_title: 'Almost done',
    finish: 'Continue to the panel',
  },
  totp_setup: {
    intro: 'Link an authenticator app (e.g. Aegis, 2FAS or Google Authenticator) to your account.',
    start: 'Start setup',
    scan: 'Scan the QR code with your authenticator app.',
    qr_alt: 'QR code for the authenticator app',
    manual: 'Or enter this key manually:',
    code_label: 'Code from your authenticator app',
    confirm: 'Enable',
  },
  passkey: {
    intro: 'Register a passkey (Touch ID, Windows Hello or a security key).',
    name_label: 'Passkey name',
    default_name: 'My passkey',
    register: 'Register passkey',
    unsupported: 'This browser does not support passkeys.',
  },
  recovery: {
    title: 'Recovery codes',
    description: 'Each code works exactly once if you lose access to your second factor. The codes are shown only now.',
    list_label: 'Recovery codes',
    copy: 'Copy',
    copied: 'Codes copied to the clipboard.',
    copy_failed: 'Copying failed.',
    download: 'Download as text file',
    file_header: 'mc-panel – recovery codes (each code is valid once)',
    confirm: 'I have stored the codes safely.',
  },
  nav: {
    label: 'Main navigation',
    dashboard: 'Overview',
    users: 'Users',
    security: 'Security Center',
    audit: 'Audit log',
    profile: 'Profile',
    open_menu: 'Open menu',
    menu_description: 'Navigation to all areas of the panel',
    skip: 'Skip to content',
    signed_in_as: 'Signed in as {{username}}',
  },
  permission: {
    server: {
      view: 'View',
      power: 'Start/stop',
      console: 'Console',
      files: 'Files',
      plugins: 'Plugins',
      backups: 'Backups',
    },
  },
  dashboard: {
    title: 'Servers',
    description: 'Your Minecraft servers. Instance management follows in a later sub-project.',
    empty: 'No servers available.',
    create_server: 'Create server',
    create_description: 'Creates a stub server so that permissions can be granted.',
    name_label: 'Name',
    name_hint: '2–32 characters: lowercase letters, digits, hyphen.',
    name_invalid: 'The name does not match the rules.',
    created: 'Server “{{name}}” created.',
    deleted: 'Server “{{name}}” deleted.',
    delete_for: 'Delete {{name}}',
    delete_title: 'Delete server “{{name}}”?',
    delete_description: 'All permissions on this server will be removed.',
    stub_hint: 'Stub server (no instance yet)',
    permissions_label: 'Your permissions',
    power_group: 'Controls for {{name}}',
    no_power: 'No permission to start/stop.',
    power: {
      start: 'Start',
      stop: 'Stop',
      restart: 'Restart',
    },
    power_noun: {
      start: 'Start',
      stop: 'Stop',
      restart: 'Restart',
    },
    power_requested: '{{action}} requested for “{{name}}”.',
  },
  users: {
    title: 'Users',
    description: 'Manage accounts, roles and permissions.',
    caption: 'User list',
    create: 'Create user',
    create_description: 'New accounts have no permissions until you grant them.',
    username: 'Username',
    username_hint: '3–32 characters: letters, digits, dot, underscore, hyphen.',
    username_invalid: 'The username does not match the rules.',
    password: 'Initial password',
    password_hint: 'At least 12 characters.',
    password_invalid: 'The password must be 12 to 256 characters long.',
    role_label: 'Role',
    role: {
      admin: 'Administrator',
      user: 'User',
    },
    role_for: 'Role of {{name}}',
    role_changed: 'Role of {{name}} changed: {{role}}',
    created: 'User {{name}} created.',
    deleted: 'User {{name}} deleted.',
    you: '(you)',
    never: 'never',
    col: {
      username: 'Username',
      role: 'Role',
      twofa: '2FA',
      last_login: 'Last sign-in',
      actions: 'Actions',
    },
    twofa_none: 'none',
    twofa_totp: 'TOTP',
    twofa_passkeys: 'Passkeys: {{n}}',
    permissions: 'Permissions',
    permissions_for: 'Permissions of {{name}}',
    reset_2fa: 'Reset 2FA',
    reset_for: 'Reset 2FA of {{name}}',
    reset_title: 'Reset 2FA of {{name}}?',
    reset_description: 'TOTP, passkeys and recovery codes will be deleted. The account must set up 2FA again.',
    reset_done: '2FA of {{name}} reset.',
    delete_for: 'Delete {{name}}',
    delete_title: 'Delete user {{name}}?',
    delete_description: 'The account and all its sessions will be removed. This cannot be undone.',
  },
  permissions: {
    title: 'Permissions of {{name}}',
    description: 'Permissions per server. Every permission includes “View”.',
    back: 'Back to users',
    caption: 'Permission matrix',
    col_server: 'Server',
    checkbox: '{{server}}: {{permission}}',
    saved: 'Permissions saved.',
    admin_note: 'Administrators automatically hold every permission on every server.',
    no_servers: 'No servers created yet.',
    unsaved: 'Unsaved changes',
  },
  security: {
    title: 'Security Center',
    description: 'Sign-in attempts, blocks, IP rules and notifications.',
    tabs: {
      overview: 'Overview',
      attempts: 'Sign-in attempts',
      jails: 'Jails',
      ip_rules: 'IP rules',
      settings: 'Settings',
      notifiers: 'Notifications',
    },
    overview: {
      range_label: 'Time range',
      ranges: {
        '24h': '24 hours',
        '7d': '7 days',
        '30d': '30 days',
      },
      chart_title: 'Sign-in attempts ({{range}})',
      success: 'Successful',
      failure: 'Failed',
      time: 'Time',
      failures_total: 'Failed attempts',
      active_jails: 'Active jails',
      top_ips: 'Top IPs (failures)',
      top_countries: 'Top countries (failures)',
      no_countries: 'No country data (GeoIP unavailable).',
      no_data: 'No data in this range.',
      new_devices: 'Sign-ins from new devices/IPs',
    },
    agent: {
      title: 'Agent',
      reachable: 'Reachable',
      unreachable: 'Unreachable',
      unreachable_hint: 'Blocks stay active inside the app; the firewall is synchronised as soon as the agent responds.',
      firewall: 'Firewall',
      version: 'Version',
      pending_sync: 'Pending synchronisations',
    },
    attempts: {
      caption: 'Sign-in attempts',
      result_label: 'Result',
      result_all: 'All',
      result_success: 'Successful',
      result_failure: 'Failed',
      ip_label: 'IP address',
      col: {
        time: 'Time',
        username: 'User',
        ip: 'IP',
        stage: 'Step',
        result: 'Result',
        reason: 'Reason',
        user_agent: 'User agent',
      },
      stage: {
        password: 'Password',
        totp: 'TOTP',
        webauthn: 'Passkey',
        recovery: 'Recovery',
        sudo: 'Sudo',
      },
      empty: 'No sign-in attempts found.',
    },
    jails: {
      caption: 'Jails',
      only_active: 'Active only',
      create: 'Block IP',
      create_description: 'Blocks an IP address manually (IPv6 is normalised to /64).',
      ip: 'IP address',
      duration: 'Duration (minutes)',
      duration_invalid: 'The duration must be between 1 and 10080 minutes.',
      reason: 'Reason',
      created: '{{ip}} blocked.',
      lift: 'Lift',
      lift_for: 'Lift block for {{ip}}',
      lifted: 'Block for {{ip}} lifted.',
      col: {
        ip: 'IP',
        reason: 'Reason',
        level: 'Level',
        since: 'Since',
        until: 'Until',
        status: 'Status',
        firewall: 'Firewall',
      },
      status_active: 'Active',
      status_lifted: 'Lifted by {{by}}',
      status_expired: 'Expired',
      manual: 'manual',
      synced: 'synchronised',
      pending: 'pending',
      empty: 'No jails.',
    },
    ip_rules: {
      caption: 'IP rules',
      create: 'Add rule',
      create_description: 'CIDR notation, e.g. 203.0.113.0/24 or 2001:db8::/48.',
      cidr: 'CIDR',
      cidr_invalid: 'Invalid CIDR notation.',
      kind: 'Kind',
      kind_allow: 'Allow',
      kind_deny: 'Deny',
      comment: 'Comment',
      expires: 'Expires (optional)',
      never: 'never',
      created: 'Rule {{cidr}} added.',
      deleted: 'Rule {{cidr}} deleted.',
      delete_for: 'Delete rule {{cidr}}',
      delete_title: 'Delete rule {{cidr}}?',
      delete_description: 'The rule is removed immediately.',
      col: {
        cidr: 'CIDR',
        kind: 'Kind',
        comment: 'Comment',
        expires: 'Expires',
        created_by: 'Created by',
        created_at: 'Created',
      },
      empty: 'No IP rules.',
    },
    settings: {
      enforce_2fa: 'Require two-factor authentication for all users',
      enforce_2fa_hint: 'Administrators always have to set up 2FA.',
      allowlist_only: 'Admin access only from the allowlist',
      allowlist_only_hint: 'Administrators can only sign in from networks with an “allow” rule. The server rejects changes that would lock you out.',
      countries: 'Allowed countries',
      countries_hint: 'ISO country codes separated by commas (e.g. DE, AT, CH). Empty = all countries.',
      countries_invalid: 'Invalid country codes: {{codes}}',
      geoip_missing: 'No GeoIP database found – the country filter is disabled.',
      saved: 'Settings saved.',
    },
    notifiers: {
      caption: 'Notifications',
      create: 'Add notification',
      edit_title: 'Edit notification',
      create_description: 'Discord webhook or ntfy topic for security events.',
      kind: 'Service',
      kind_discord: 'Discord',
      kind_ntfy: 'ntfy',
      name: 'Name',
      enabled: 'Enabled',
      enabled_for: '{{name}} enabled',
      url_discord: 'Webhook URL',
      url_ntfy: 'Server URL',
      url_keep: 'Leave empty to keep the stored URL.',
      topic: 'Topic',
      token: 'Access token (optional)',
      test: 'Send test',
      test_for: 'Send a test message to {{name}}',
      test_sent: 'Test message sent to {{name}}.',
      created: 'Notification {{name}} created.',
      updated: 'Notification {{name}} saved.',
      deleted: 'Notification {{name}} deleted.',
      edit_for: 'Edit {{name}}',
      delete_for: 'Delete {{name}}',
      delete_title: 'Delete {{name}}?',
      delete_description: 'No more events will be sent to this target.',
      url_invalid: 'Please enter an http(s) URL.',
      topic_required: 'ntfy needs a topic.',
      name_required: 'Please enter a name (max. 64 characters).',
      empty: 'No notifications configured.',
      col: {
        name: 'Name',
        kind: 'Service',
        target: 'Target',
        enabled: 'Enabled',
        actions: 'Actions',
      },
    },
  },
  audit: {
    title: 'Audit log',
    description: 'All security-relevant actions, chained to detect tampering.',
    caption: 'Audit entries',
    actor: 'Actor',
    action: 'Action',
    result_label: 'Result',
    result_all: 'All',
    from: 'From',
    to: 'To',
    result: {
      success: 'Success',
      failure: 'Failure',
      denied: 'Denied',
    },
    actor_type: {
      user: 'User',
      system: 'System',
      cli: 'CLI',
    },
    col: {
      time: 'Time',
      actor: 'Actor',
      action: 'Action',
      target: 'Target',
      ip: 'IP',
      result: 'Result',
      details: 'Details',
      hash: 'Hash',
    },
    chain_ok: 'Chain valid ({{checked}} entries checked)',
    chain_broken: 'Chain broken at entry #{{id}}',
    chain_checking: 'Checking chain …',
    chain_error: 'Chain check failed',
    verify_again: 'Check again',
    empty: 'No entries found.',
  },
  profile: {
    title: 'Profile',
    description: 'Account, security and sessions.',
    language: {
      title: 'Language',
      label: 'Interface language',
      de: 'Deutsch',
      en: 'English',
      saved: 'Language saved.',
    },
    password: {
      title: 'Change password',
      current: 'Current password',
      new: 'New password',
      confirm: 'Repeat new password',
      hint: 'At least 12 characters. All other sessions will be signed out.',
      too_short: 'The new password must be 12 to 256 characters long.',
      mismatch: 'The passwords do not match.',
      wrong_current: 'The current password is wrong.',
      submit: 'Change password',
      changed: 'Password changed. Other sessions have been signed out.',
    },
    totp: {
      title: 'Authenticator app (TOTP)',
      enabled: 'Enabled',
      disabled: 'Not set up',
      setup: 'Set up',
      setup_title: 'Set up authenticator app',
      setup_description: 'Scan the QR code and confirm with a code.',
      disable: 'Disable',
      disable_title: 'Disable the authenticator app?',
      disable_description: 'Afterwards you can only sign in with a passkey or a recovery code.',
      disabled_done: 'Authenticator app disabled.',
      enabled_done: 'Authenticator app enabled.',
    },
    recovery: {
      title: 'Recovery codes',
      remaining: 'Codes left: {{remaining}}',
      regenerate: 'Generate new codes',
      regenerate_title: 'Generate new recovery codes?',
      regenerate_description: 'All previous codes become invalid immediately.',
      new_codes_title: 'New recovery codes',
      done: 'Done',
    },
    passkeys: {
      title: 'Passkeys',
      caption: 'Registered passkeys',
      add: 'Add passkey',
      add_description: 'Your browser will ask for Touch ID, Windows Hello or your security key.',
      added: 'Passkey {{name}} added.',
      deleted: 'Passkey {{name}} removed.',
      delete_for: 'Remove {{name}}',
      delete_title: 'Remove passkey {{name}}?',
      delete_description: 'The passkey can no longer be used to sign in.',
      col: {
        name: 'Name',
        created: 'Added',
        last_used: 'Last used',
        actions: 'Actions',
      },
      never: 'never',
      empty: 'No passkeys registered.',
    },
    sessions: {
      title: 'Active sessions',
      caption: 'Your sessions',
      all_title: 'All sessions (admin)',
      all_caption: 'Sessions of all users',
      current: 'This session',
      revoke: 'Sign out',
      revoke_for: 'Sign out session {{device}}',
      revoked: 'Session signed out.',
      revoke_others: 'Sign out all others',
      revoke_others_title: 'Sign out all other sessions?',
      revoke_others_description: 'All devices except this one will be signed out.',
      revoked_others: 'Other sessions signed out.',
      device: '{{browser}} on {{os}}',
      unknown: 'Unknown',
      col: {
        user: 'User',
        device: 'Device',
        location: 'IP / country',
        last_seen: 'Last active',
        created: 'Signed in since',
        actions: 'Actions',
      },
      empty: 'No sessions.',
    },
  },
}
```

- [ ] **Step 5: `web/src/i18n/index.ts` und `web/src/api/types.ts`**

```ts
import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import type { Locale } from '@/api/types'
import { de } from './de'
import { en } from './en'

export const SUPPORTED_LOCALES: readonly Locale[] = ['de', 'en']

/** First supported browser language wins; German is the default. */
export function detectLocale(languages: readonly string[] = typeof navigator === 'undefined' ? [] : navigator.languages): Locale {
  for (const language of languages) {
    const base = language.toLowerCase().split('-')[0]
    if (base === 'de' || base === 'en') return base
  }
  return 'de'
}

export const i18nReady = i18n.use(initReactI18next).init({
  resources: { de: { translation: de }, en: { translation: en } },
  lng: detectLocale(),
  fallbackLng: 'de',
  supportedLngs: [...SUPPORTED_LOCALES],
  interpolation: { escapeValue: false },
})

export function setLocale(locale: Locale): void {
  if (i18n.language !== locale) void i18n.changeLanguage(locale)
  document.documentElement.lang = locale
}

export default i18n
```

`web/src/api/types.ts` (Typ-Aliase auf das generierte Schema; wird ab hier von i18n, Mocks und Client genutzt):

```ts
import type { components } from './schema'

type Schemas = components['schemas']

export type ErrorBody = Schemas['Error']
export type ErrorCode = Schemas['Error']['error']
export type Role = Schemas['Role']
export type Stage = Schemas['Stage']
export type Locale = Schemas['Locale']
export type Permission = Schemas['Permission']
export type SecondFactor = Schemas['SecondFactor']
export type Me = Schemas['Me']
export type Passkey = Schemas['Passkey']
export type PasskeyCreated = Schemas['PasskeyCreated']
export type TotpSetupResponse = Schemas['TotpSetupResponse']
export type RecoveryCodesResponse = Schemas['RecoveryCodesResponse']
export type SudoResponse = Schemas['SudoResponse']
export type WebAuthnOptions = Schemas['WebAuthnOptions']
export type Session = Schemas['Session']
export type User = Schemas['User']
export type Server = Schemas['Server']
export type PermissionGrant = Schemas['PermissionGrant']
export type UserPermissions = Schemas['UserPermissions']
export type AuditEntry = Schemas['AuditEntry']
export type AuditPage = Schemas['AuditPage']
export type AuditVerify = Schemas['AuditVerify']
export type AuditResult = Schemas['AuditEntry']['result']
export type SeriesPoint = Schemas['SeriesPoint']
export type NewDeviceLogin = Schemas['NewDeviceLogin']
export type SecurityOverview = Schemas['SecurityOverview']
export type SecurityRange = Schemas['SecurityOverview']['range']
export type LoginAttempt = Schemas['LoginAttempt']
export type AttemptStage = Schemas['LoginAttempt']['stage']
export type LoginAttemptPage = Schemas['LoginAttemptPage']
export type Jail = Schemas['Jail']
export type IPRule = Schemas['IPRule']
export type IPRuleKind = Schemas['IPRule']['kind']
export type SecuritySettings = Schemas['SecuritySettings']
export type SecuritySettingsResponse = Schemas['SecuritySettingsResponse']
export type Notifier = Schemas['Notifier']
export type NotifierKind = Schemas['Notifier']['kind']
export type AgentStatus = Schemas['AgentStatus']
export type PowerAction = Schemas['PowerRequest']['action']

export const ALL_PERMISSIONS: readonly Permission[] = [
  'server.view',
  'server.power',
  'server.console',
  'server.files',
  'server.plugins',
  'server.backups',
]
```

Vorher sicherstellen, dass das Schema aktuell ist: `cd web && npm run gen:api` (Phase-0-Skript).

- [ ] **Step 6: `web/src/lib/format.ts`**

```ts
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

export function formatDateTime(ms: number, locale: string): string {
  return new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(ms))
}

export function formatDate(ms: number, locale: string): string {
  return new Intl.DateTimeFormat(locale, { dateStyle: 'medium' }).format(new Date(ms))
}

export function formatTime(ms: number, locale: string): string {
  return new Intl.DateTimeFormat(locale, { hour: '2-digit', minute: '2-digit' }).format(new Date(ms))
}

export function formatRelative(ms: number, locale: string, now: number = Date.now()): string {
  const diff = ms - now
  const abs = Math.abs(diff)
  const rtf = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' })
  if (abs < 60_000) return rtf.format(Math.round(diff / 1000), 'second')
  if (abs < 3_600_000) return rtf.format(Math.round(diff / 60_000), 'minute')
  if (abs < 86_400_000) return rtf.format(Math.round(diff / 3_600_000), 'hour')
  return rtf.format(Math.round(diff / 86_400_000), 'day')
}

export function formatNumber(n: number, locale: string): string {
  return new Intl.NumberFormat(locale).format(n)
}

export function useFormat() {
  const { i18n } = useTranslation()
  const locale = i18n.language
  return useMemo(
    () => ({
      locale,
      dateTime: (ms: number) => formatDateTime(ms, locale),
      date: (ms: number) => formatDate(ms, locale),
      time: (ms: number) => formatTime(ms, locale),
      relative: (ms: number) => formatRelative(ms, locale),
      number: (n: number) => formatNumber(n, locale),
    }),
    [locale],
  )
}
```

- [ ] **Step 7: `web/src/test/media.ts` und `web/src/test/setup.ts`**

```ts
// web/src/test/media.ts
type Viewport = 'desktop' | 'mobile'

const WIDTH: Record<Viewport, number> = { desktop: 1280, mobile: 360 }
let viewport: Viewport = 'desktop'
let prefersDark = false

interface Listener {
  query: string
  callback: (event: MediaQueryListEvent) => void
}
const listeners = new Set<Listener>()

function evaluate(query: string): boolean {
  if (query.includes('prefers-color-scheme: dark')) return prefersDark
  const min = /min-width:\s*(\d+)px/.exec(query)
  if (min) return WIDTH[viewport] >= Number(min[1])
  const max = /max-width:\s*(\d+)px/.exec(query)
  if (max) return WIDTH[viewport] <= Number(max[1])
  return false
}

function notify(): void {
  for (const l of [...listeners]) l.callback({ matches: evaluate(l.query), media: l.query } as MediaQueryListEvent)
}

/** jsdom has no matchMedia; this one is controllable per test (default: 1280px desktop, light). */
export function installMatchMedia(): void {
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    writable: true,
    value: (query: string): MediaQueryList => {
      const add = (_type: string, callback: (event: MediaQueryListEvent) => void) => {
        listeners.add({ query, callback })
      }
      const remove = (_type: string, callback: (event: MediaQueryListEvent) => void) => {
        for (const l of listeners) if (l.callback === callback && l.query === query) listeners.delete(l)
      }
      return {
        get matches() {
          return evaluate(query)
        },
        media: query,
        onchange: null,
        addEventListener: add,
        removeEventListener: remove,
        addListener: (callback: (event: MediaQueryListEvent) => void) => add('change', callback),
        removeListener: (callback: (event: MediaQueryListEvent) => void) => remove('change', callback),
        dispatchEvent: () => false,
      } as unknown as MediaQueryList
    },
  })
}

export function setViewport(next: Viewport): void {
  viewport = next
  notify()
}

export function setPrefersDark(next: boolean): void {
  prefersDark = next
  notify()
}
```

`web/src/test/setup.ts` (komplette Datei; MSW kommt in Task 3 dazu):
```ts
import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach } from 'vitest'
import i18n, { i18nReady } from '@/i18n'
import { installMatchMedia, setPrefersDark, setViewport } from './media'

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= ResizeObserverStub as unknown as typeof ResizeObserver
Element.prototype.scrollIntoView ??= function scrollIntoView() {}
Element.prototype.hasPointerCapture ??= () => false
Element.prototype.releasePointerCapture ??= () => {}

installMatchMedia()
await i18nReady
await i18n.changeLanguage('de')

afterEach(async () => {
  cleanup()
  setViewport('desktop')
  setPrefersDark(false)
  localStorage.clear()
  await i18n.changeLanguage('de')
})
```

- [ ] **Step 8: Tests laufen lassen → PASS**

Run: `cd web && npx vitest run src/i18n src/lib/format.test.ts`
Expected: PASS (i18n 5, format 3). Der Phase-0-Test `src/routes/index.test.tsx` bleibt grün.

- [ ] **Step 9: Typecheck, Commit**

Run: `cd web && npm run typecheck`
Expected: keine Fehler (insbesondere: `en` erfüllt `Messages` exakt).

```bash
git add web/src/i18n web/src/lib/format.ts web/src/lib/format.test.ts web/src/test web/src/api/types.ts
git commit -m "feat(web): add test infrastructure, de/en resources and Intl formatting" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Theme, Media-Query und UI-Bausteine

**Files:**
- Create: `web/src/lib/theme.ts`, `web/src/lib/use-media-query.ts`, `web/src/lib/download.ts`, `web/src/components/theme-toggle.tsx`, `web/src/components/app-toaster.tsx`, `web/src/components/native-select.tsx`, `web/src/components/confirm-dialog.tsx`, `web/src/components/page-header.tsx`, `web/src/components/data-list.tsx`, `web/src/components/not-found-page.tsx`
- Modify: `web/src/index.css` (nur prüfen/ergänzen: Dark-Variant)
- Test: `web/src/lib/theme.test.ts`, `web/src/components/data-list.test.tsx`

**Interfaces:**
- Consumes: `installMatchMedia`, `setViewport`, `setPrefersDark` (Task 1, `src/test/media.ts`); shadcn `Button`, `Card*`, `Dialog*`, `Table*` aus `@/components/ui/*`; `cn` aus `@/lib/utils`; `lucide-react` (von `shadcn init` installiert)
- Produces:
```ts
// lib/theme.ts
export type ThemePreference = 'light' | 'dark' | 'system'
export const THEME_STORAGE_KEY = 'mcpanel.theme'
export function getStoredTheme(): ThemePreference
export function resolveTheme(pref: ThemePreference): 'light' | 'dark'
export function applyTheme(pref?: ThemePreference): void
export function setTheme(pref: ThemePreference): void
export function nextTheme(pref: ThemePreference): ThemePreference
export function watchSystemTheme(): () => void
export function useThemePreference(): ThemePreference
// lib/use-media-query.ts
export const DESKTOP_QUERY = '(min-width: 768px)'
export function useMediaQuery(query: string): boolean
// lib/download.ts
export function downloadText(filename: string, content: string): void
// components
export function ThemeToggle(): JSX.Element
export function AppToaster(): JSX.Element
export function NativeSelect(props: SelectHTMLAttributes<HTMLSelectElement>): JSX.Element
export interface ConfirmDialogProps { open: boolean; onOpenChange(open: boolean): void; title: string; description: string; confirmLabel: string; destructive?: boolean; pending?: boolean; onConfirm(): void }
export function ConfirmDialog(props: ConfirmDialogProps): JSX.Element
export function PageHeader(props: { title: string; description?: string; actions?: ReactNode }): JSX.Element
export interface Column<T> { id: string; header: string; cell(row: T): ReactNode; className?: string }
export function DataList<T>(props: { items: T[]; columns: Column<T>[]; rowKey(row: T): string | number; caption: string; empty: string }): JSX.Element
export function NotFoundPage(): JSX.Element
```

- [ ] **Step 1: Failing Tests schreiben**

`web/src/lib/theme.test.ts`:
```ts
import { describe, expect, it } from 'vitest'
import { setPrefersDark } from '@/test/media'
import { applyTheme, getStoredTheme, nextTheme, resolveTheme, setTheme, THEME_STORAGE_KEY, watchSystemTheme } from './theme'

describe('theme', () => {
  it('defaults to system and ignores garbage in localStorage', () => {
    expect(getStoredTheme()).toBe('system')
    localStorage.setItem(THEME_STORAGE_KEY, 'neon')
    expect(getStoredTheme()).toBe('system')
  })

  it('resolves system preference via prefers-color-scheme', () => {
    setPrefersDark(true)
    expect(resolveTheme('system')).toBe('dark')
    setPrefersDark(false)
    expect(resolveTheme('system')).toBe('light')
    expect(resolveTheme('dark')).toBe('dark')
  })

  it('persists the choice under mcpanel.theme and toggles the dark class', () => {
    setTheme('dark')
    expect(localStorage.getItem('mcpanel.theme')).toBe('dark')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    setTheme('light')
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })

  it('follows the OS while set to system', () => {
    setTheme('system')
    const stop = watchSystemTheme()
    setPrefersDark(true)
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    setPrefersDark(false)
    expect(document.documentElement.classList.contains('dark')).toBe(false)
    stop()
    applyTheme('light')
  })

  it('cycles light → dark → system → light', () => {
    expect(nextTheme('light')).toBe('dark')
    expect(nextTheme('dark')).toBe('system')
    expect(nextTheme('system')).toBe('light')
  })
})
```

`web/src/components/data-list.test.tsx`:
```tsx
import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { setViewport } from '@/test/media'
import { DataList, type Column } from './data-list'

interface Row { id: number; name: string; ip: string }
const rows: Row[] = [
  { id: 1, name: 'admin', ip: '203.0.113.5' },
  { id: 2, name: 'alice', ip: '198.51.100.23' },
]
const columns: Column<Row>[] = [
  { id: 'name', header: 'Name', cell: (r) => r.name },
  { id: 'ip', header: 'IP', cell: (r) => r.ip },
]

describe('DataList', () => {
  it('renders a table with caption on desktop', () => {
    render(<DataList items={rows} columns={columns} rowKey={(r) => r.id} caption="Benutzer" empty="Leer" />)
    const table = screen.getByRole('table', { name: 'Benutzer' })
    expect(within(table).getAllByRole('row')).toHaveLength(3)
    expect(within(table).getByRole('columnheader', { name: 'IP' })).toBeInTheDocument()
  })

  it('collapses into labelled cards on a 360px viewport', () => {
    setViewport('mobile')
    render(<DataList items={rows} columns={columns} rowKey={(r) => r.id} caption="Benutzer" empty="Leer" />)
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
    const list = screen.getByRole('list', { name: 'Benutzer' })
    const cards = within(list).getAllByRole('listitem')
    expect(cards).toHaveLength(2)
    expect(within(cards[1]).getByText('IP')).toBeInTheDocument()
    expect(within(cards[1]).getByText('198.51.100.23')).toBeInTheDocument()
  })

  it('shows the empty text when there are no rows', () => {
    render(<DataList items={[]} columns={columns} rowKey={(r: Row) => r.id} caption="Benutzer" empty="Keine Einträge" />)
    expect(screen.getByText('Keine Einträge')).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Tests laufen lassen → FAIL**

Run: `cd web && npx vitest run src/lib/theme.test.ts src/components/data-list.test.tsx`
Expected: FAIL – `Failed to resolve import "./theme"` bzw. `"./data-list"`.

- [ ] **Step 3: `web/src/lib/theme.ts`**

```ts
import { useSyncExternalStore } from 'react'

export type ThemePreference = 'light' | 'dark' | 'system'
export const THEME_STORAGE_KEY = 'mcpanel.theme'
const DARK_QUERY = '(prefers-color-scheme: dark)'
const listeners = new Set<() => void>()

export function getStoredTheme(): ThemePreference {
  try {
    const value = localStorage.getItem(THEME_STORAGE_KEY)
    return value === 'light' || value === 'dark' || value === 'system' ? value : 'system'
  } catch {
    return 'system'
  }
}

export function resolveTheme(pref: ThemePreference): 'light' | 'dark' {
  if (pref !== 'system') return pref
  return window.matchMedia(DARK_QUERY).matches ? 'dark' : 'light'
}

/** Applies the class strategy on <html>. Called in main.tsx before the first render (no inline script, CSP). */
export function applyTheme(pref: ThemePreference = getStoredTheme()): void {
  const resolved = resolveTheme(pref)
  const root = document.documentElement
  root.classList.toggle('dark', resolved === 'dark')
  root.style.colorScheme = resolved
}

export function setTheme(pref: ThemePreference): void {
  try {
    localStorage.setItem(THEME_STORAGE_KEY, pref)
  } catch {
    // storage unavailable (private mode) – the choice only lasts for this page view
  }
  applyTheme(pref)
  listeners.forEach((l) => l())
}

export function nextTheme(pref: ThemePreference): ThemePreference {
  if (pref === 'light') return 'dark'
  if (pref === 'dark') return 'system'
  return 'light'
}

export function watchSystemTheme(): () => void {
  const mql = window.matchMedia(DARK_QUERY)
  const onChange = () => {
    if (getStoredTheme() === 'system') applyTheme('system')
  }
  mql.addEventListener('change', onChange)
  return () => mql.removeEventListener('change', onChange)
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export function useThemePreference(): ThemePreference {
  return useSyncExternalStore(subscribe, getStoredTheme, () => 'system')
}
```

- [ ] **Step 4: `web/src/lib/use-media-query.ts` und `web/src/lib/download.ts`**

```ts
// web/src/lib/use-media-query.ts
import { useCallback, useSyncExternalStore } from 'react'

/** md breakpoint of Tailwind; below it tables collapse into cards and the sidebar becomes a sheet. */
export const DESKTOP_QUERY = '(min-width: 768px)'

export function useMediaQuery(query: string): boolean {
  const subscribe = useCallback(
    (onChange: () => void) => {
      const mql = window.matchMedia(query)
      mql.addEventListener('change', onChange)
      return () => mql.removeEventListener('change', onChange)
    },
    [query],
  )
  return useSyncExternalStore(subscribe, () => window.matchMedia(query).matches, () => false)
}
```

```ts
// web/src/lib/download.ts
export function downloadText(filename: string, content: string): void {
  const blob = new Blob([content], { type: 'text/plain;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename
  anchor.rel = 'noopener'
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  URL.revokeObjectURL(url)
}
```

- [ ] **Step 5: Komponenten schreiben**

`web/src/components/data-list.tsx`:
```tsx
import { Fragment, type ReactNode } from 'react'
import { Card, CardContent } from '@/components/ui/card'
import { Table, TableBody, TableCaption, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { DESKTOP_QUERY, useMediaQuery } from '@/lib/use-media-query'

export interface Column<T> {
  id: string
  header: string
  cell(row: T): ReactNode
  className?: string
}

interface DataListProps<T> {
  items: T[]
  columns: Column<T>[]
  rowKey(row: T): string | number
  caption: string
  empty: string
}

/** Table on desktop, one card per row below the md breakpoint (usable from 360px width). */
export function DataList<T>({ items, columns, rowKey, caption, empty }: DataListProps<T>) {
  const desktop = useMediaQuery(DESKTOP_QUERY)
  if (items.length === 0) {
    return <p className="text-muted-foreground py-6 text-center text-sm">{empty}</p>
  }
  if (desktop) {
    return (
      <Table>
        <TableCaption className="sr-only">{caption}</TableCaption>
        <TableHeader>
          <TableRow>
            {columns.map((c) => (
              <TableHead key={c.id} scope="col" className={c.className}>
                {c.header}
              </TableHead>
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {items.map((item) => (
            <TableRow key={rowKey(item)}>
              {columns.map((c) => (
                <TableCell key={c.id} className={c.className}>
                  {c.cell(item)}
                </TableCell>
              ))}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    )
  }
  return (
    <ul aria-label={caption} className="space-y-3">
      {items.map((item) => (
        <li key={rowKey(item)}>
          <Card className="py-3">
            <CardContent className="px-4">
              <dl className="grid grid-cols-[minmax(6rem,auto)_1fr] gap-x-3 gap-y-1.5 text-sm">
                {columns.map((c) => (
                  <Fragment key={c.id}>
                    <dt className="text-muted-foreground">{c.header}</dt>
                    <dd className="min-w-0 break-words">{c.cell(item)}</dd>
                  </Fragment>
                ))}
              </dl>
            </CardContent>
          </Card>
        </li>
      ))}
    </ul>
  )
}
```

`web/src/components/theme-toggle.tsx`:
```tsx
import { Monitor, Moon, Sun } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { nextTheme, setTheme, useThemePreference } from '@/lib/theme'

export function ThemeToggle() {
  const { t } = useTranslation()
  const pref = useThemePreference()
  const Icon = pref === 'dark' ? Moon : pref === 'light' ? Sun : Monitor
  return (
    <Button
      type="button"
      variant="ghost"
      size="icon"
      aria-label={t('theme.toggle', { current: t(`theme.${pref}`) })}
      title={t('theme.toggle', { current: t(`theme.${pref}`) })}
      onClick={() => setTheme(nextTheme(pref))}
    >
      <Icon className="size-4" aria-hidden="true" />
    </Button>
  )
}
```

`web/src/components/app-toaster.tsx` (eigener Wrapper statt `components/ui/sonner.tsx`, weil dieser `next-themes` erwartet):
```tsx
import { Toaster } from 'sonner'
import { useThemePreference } from '@/lib/theme'

export function AppToaster() {
  const pref = useThemePreference()
  return <Toaster theme={pref} richColors closeButton position="top-right" />
}
```

`web/src/components/native-select.tsx` (native `<select>`: mobil bedienbar, in jsdom testbar):
```tsx
import type { SelectHTMLAttributes } from 'react'
import { cn } from '@/lib/utils'

export function NativeSelect({ className, ...props }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select
      className={cn(
        'border-input bg-background focus-visible:border-ring focus-visible:ring-ring/50 h-9 rounded-md border px-3 py-1 text-sm shadow-xs outline-none focus-visible:ring-[3px] disabled:cursor-not-allowed disabled:opacity-50',
        className,
      )}
      {...props}
    />
  )
}
```

`web/src/components/confirm-dialog.tsx`:
```tsx
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'

export interface ConfirmDialogProps {
  open: boolean
  onOpenChange(open: boolean): void
  title: string
  description: string
  confirmLabel: string
  destructive?: boolean
  pending?: boolean
  onConfirm(): void
}

export function ConfirmDialog({ open, onOpenChange, title, description, confirmLabel, destructive, pending, onConfirm }: ConfirmDialogProps) {
  const { t } = useTranslation()
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
            {t('common.cancel')}
          </Button>
          <Button type="button" variant={destructive ? 'destructive' : 'default'} disabled={pending} onClick={onConfirm}>
            {confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
```

`web/src/components/page-header.tsx`:
```tsx
import type { ReactNode } from 'react'

export function PageHeader({ title, description, actions }: { title: string; description?: string; actions?: ReactNode }) {
  return (
    <div className="mb-6 flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
      <div className="min-w-0">
        <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
        {description ? <p className="text-muted-foreground mt-1 text-sm">{description}</p> : null}
      </div>
      {actions ? <div className="flex flex-wrap gap-2">{actions}</div> : null}
    </div>
  )
}
```

`web/src/components/not-found-page.tsx`:
```tsx
import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

export function NotFoundPage() {
  const { t } = useTranslation()
  return (
    <main className="flex min-h-[50svh] flex-col items-center justify-center gap-3 p-6 text-center">
      <h1 className="text-2xl font-semibold">{t('not_found.title')}</h1>
      <p className="text-muted-foreground">{t('not_found.description')}</p>
      <Link to="/" className="text-primary underline underline-offset-4">
        {t('not_found.home')}
      </Link>
    </main>
  )
}
```

- [ ] **Step 6: Dark-Variant in `web/src/index.css` prüfen**

Run: `cd web && grep -n "custom-variant dark" src/index.css`
Expected: eine Zeile `@custom-variant dark (&:is(.dark *));` (legt `shadcn init` an). Fehlt sie, direkt unter `@import "tailwindcss";` einfügen:
```css
@custom-variant dark (&:is(.dark *));
```

- [ ] **Step 7: Tests laufen lassen → PASS**

Run: `cd web && npx vitest run src/lib/theme.test.ts src/components/data-list.test.tsx`
Expected: PASS (8 Tests).

- [ ] **Step 8: Gesamte Suite + Typecheck**

Run: `cd web && npx vitest run && npm run typecheck`
Expected: alle Tests grün (inkl. i18n-Schlüsselscan aus Task 1), keine TS-Fehler.

- [ ] **Step 9: Commit**

```bash
git add web/src/lib web/src/components web/src/index.css
git commit -m "feat(web): add theme handling, media query hook and shared UI building blocks" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: MSW-Mock – In-Memory-DB, Auth-, Me- und Admin-Handler

**Files:**
- Create: `web/src/mocks/db.ts`, `web/src/mocks/handlers/util.ts`, `web/src/mocks/handlers/auth.ts`, `web/src/mocks/handlers/me.ts`, `web/src/mocks/handlers/admin.ts`, `web/src/mocks/handlers.ts`, `web/src/mocks/node.ts`, `web/src/test/call-api.ts`
- Modify: `web/src/test/setup.ts` (MSW-Server-Lebenszyklus, DB-Reset)
- Test: `web/src/mocks/handlers/auth.test.ts`

**Interfaces:**
- Consumes: Typ-Aliase und `ALL_PERMISSIONS` aus `src/api/types.ts` (Task 1); `installMatchMedia`, `setViewport`, `setPrefersDark` (Task 1)
- Produces:
```ts
// mocks/db.ts
export const MOCK_PASSWORD = 'correct-horse-battery'; MOCK_TOTP_CODE = '123456'; MOCK_TOTP_SECRET = 'JBSWY3DPEHPK3PXP'
export const MOCK_RECOVERY_CODES: string[]; MOCK_CLIENT_IP = '198.51.100.7'; WEBAUTHN_CHALLENGE: string; SUDO_MS; HOUR; DAY
export interface MockUser, MockSession, MockServer, MockDb
export const db: MockDb
export function resetDb(now?: number): void
export function nextId(): number
export function findUser(id: number): MockUser | undefined
export function findUserByName(name: string): MockUser | undefined
export function currentSession(): MockSession | undefined
export function hasSecondFactor(u: MockUser): boolean
export function toMe(u: MockUser, s: MockSession): Me
export function toUser(u: MockUser): User
export function toSession(s: MockSession): Session
export function startSession(u: MockUser, stage: Stage): MockSession
export function rotateSession(s: MockSession): void
export function stageAfterPassword(u: MockUser): Stage
export function recordAudit(actor: MockUser | null, action: string, target: string, result?: AuditResult, details?: Record<string, unknown>): void
export function recordAttempt(username: string, stage: AttemptStage, success: boolean, reason: string): void
export function generateRecoveryCodes(): string[]
export function normalizeCode(code: string): string
export function ipKeyOf(ip: string): string
export function loginAs(username: string, opts?: { stage?: Stage; sudo?: boolean }): Me
// mocks/handlers/util.ts
export const API = '*/api'
export interface MockOperation { operationId: string; handler: HttpHandler }
export function op(operationId: string, handler: HttpHandler): MockOperation
export interface Authed { session: MockSession; user: MockUser }
export function guard(request: Request, opts?: { stages?: Stage[]; admin?: boolean; sudo?: boolean }): Authed | ErrorResponse
export function apiError(status: number, code: ErrorCode, message?: string): ErrorResponse
// mocks/handlers.ts – wird in Task 4 vervollständigt
export const operations: MockOperation[]
export const handlers: HttpHandler[]
// mocks/node.ts
export const server: SetupServerApi
// test/call-api.ts
export function callApi(method: string, path: string, opts?: { body?: unknown; csrf?: string }): Promise<{ status: number; body: unknown }>
export function field(body: unknown, key: string): unknown
```

Mock-Konten (alle mit Passwort `correct-horse-battery`, TOTP-Code immer `123456`):

| Benutzer | Rolle | 2FA | Ergebnis nach Passwort |
|---|---|---|---|
| `admin` | admin | TOTP + 1 Passkey + 10 Recovery-Codes | `preauth` |
| `alice` | user | keine | `full` (Rechte: survival view+power, creative view) |
| `newadmin` | admin | keine | `setup` |
| `bob` | user (Sprache `en`) | TOTP | `preauth` |

- [ ] **Step 1: Schema-Aliase prüfen**

Run: `cd web && grep -c "^export type" src/api/types.ts`
Expected: `39` (Datei aus Task 1, unverändert).

- [ ] **Step 2: Failing Test `web/src/test/call-api.ts` + `web/src/mocks/handlers/auth.test.ts`**

`web/src/test/call-api.ts` (Test-Helfer, bewusst ohne openapi-fetch, um die Mocks unabhängig vom Client zu prüfen):
```ts
export async function callApi(
  method: string,
  path: string,
  opts: { body?: unknown; csrf?: string } = {},
): Promise<{ status: number; body: unknown }> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  if (opts.csrf) headers['X-CSRF-Token'] = opts.csrf
  const res = await fetch(`${window.location.origin}/api${path}`, {
    method,
    headers,
    body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
  })
  const text = await res.text()
  return { status: res.status, body: text ? (JSON.parse(text) as unknown) : null }
}

export function field(body: unknown, key: string): unknown {
  return typeof body === 'object' && body !== null ? (body as Record<string, unknown>)[key] : undefined
}
```

`web/src/mocks/handlers/auth.test.ts`:
```ts
import { describe, expect, it } from 'vitest'
import { callApi, field } from '@/test/call-api'
import { db, findUserByName, loginAs, MOCK_PASSWORD, MOCK_TOTP_CODE } from '../db'

describe('mock auth flow', () => {
  it('rejects a wrong password and records the attempt', async () => {
    const before = db.attempts.length
    const res = await callApi('POST', '/auth/login', { body: { username: 'admin', password: 'wrong-password-123' } })
    expect(res.status).toBe(401)
    expect(field(res.body, 'error')).toBe('invalid_credentials')
    expect(db.attempts).toHaveLength(before + 1)
    expect(db.attempts[0].success).toBe(false)
  })

  it('walks admin through password and TOTP and rotates the CSRF token', async () => {
    const login = await callApi('POST', '/auth/login', { body: { username: 'admin', password: MOCK_PASSWORD } })
    expect(login.status).toBe(200)
    expect(field(login.body, 'stage')).toBe('preauth')
    expect(field(login.body, 'second_factors')).toEqual(['totp', 'webauthn', 'recovery'])
    const preCsrf = String(field(login.body, 'csrf_token'))

    const noCsrf = await callApi('POST', '/auth/2fa/totp', { body: { code: MOCK_TOTP_CODE } })
    expect(field(noCsrf.body, 'error')).toBe('csrf_invalid')
    const wrong = await callApi('POST', '/auth/2fa/totp', { body: { code: '000000' }, csrf: preCsrf })
    expect(wrong.status).toBe(401)
    expect(field(wrong.body, 'error')).toBe('invalid_code')

    const full = await callApi('POST', '/auth/2fa/totp', { body: { code: MOCK_TOTP_CODE }, csrf: preCsrf })
    expect(field(full.body, 'stage')).toBe('full')
    expect(field(full.body, 'csrf_token')).not.toBe(preCsrf)
  })

  it('forces a fresh admin into the setup stage until TOTP is enabled', async () => {
    const login = await callApi('POST', '/auth/login', { body: { username: 'newadmin', password: MOCK_PASSWORD } })
    expect(field(login.body, 'stage')).toBe('setup')
    const csrf = String(field(login.body, 'csrf_token'))

    const blocked = await callApi('GET', '/me/passkeys')
    expect(field(blocked.body, 'error')).toBe('setup_2fa_required')

    const setup = await callApi('POST', '/me/totp/setup', { csrf })
    expect(field(setup.body, 'secret')).toBe('JBSWY3DPEHPK3PXP')
    const enable = await callApi('POST', '/me/totp/enable', { body: { code: MOCK_TOTP_CODE }, csrf })
    expect(field(enable.body, 'codes')).toHaveLength(10)

    const me = await callApi('GET', '/auth/me')
    expect(field(me.body, 'stage')).toBe('full')
    expect(field(me.body, 'csrf_token')).not.toBe(csrf)
  })

  it('requires sudo for sensitive actions and rotates the token on sudo', async () => {
    const me = loginAs('admin')
    const denied = await callApi('DELETE', '/me/totp', { csrf: me.csrf_token })
    expect(field(denied.body, 'error')).toBe('sudo_required')

    const sudo = await callApi('POST', '/auth/sudo/totp', { body: { code: MOCK_TOTP_CODE }, csrf: me.csrf_token })
    expect(Number(field(sudo.body, 'sudo_until'))).toBeGreaterThan(Date.now())

    const stale = await callApi('DELETE', '/me/totp', { csrf: me.csrf_token })
    expect(field(stale.body, 'error')).toBe('csrf_invalid')

    const fresh = await callApi('GET', '/auth/me')
    const ok = await callApi('DELETE', '/me/totp', { csrf: String(field(fresh.body, 'csrf_token')) })
    expect(ok.status).toBe(204)
    expect(findUserByName('admin')?.totpEnabled).toBe(false)
  })

  it('refuses to remove the last second factor of an admin', async () => {
    const me = loginAs('admin', { sudo: true })
    const admin = findUserByName('admin')
    if (!admin) throw new Error('seed missing')
    admin.passkeys = []
    const res = await callApi('DELETE', '/me/totp', { csrf: me.csrf_token })
    expect(res.status).toBe(409)
    expect(field(res.body, 'error')).toBe('conflict')
  })

  it('lists own sessions with the current one flagged and revokes the others', async () => {
    const me = loginAs('admin')
    const list = await callApi('GET', '/me/sessions')
    const sessions = list.body as Array<{ current: boolean }>
    expect(sessions).toHaveLength(3)
    expect(sessions.filter((s) => s.current)).toHaveLength(1)
    const revoke = await callApi('POST', '/me/sessions/revoke-others', { csrf: me.csrf_token })
    expect(revoke.status).toBe(204)
    expect((await callApi('GET', '/me/sessions')).body).toHaveLength(1)
  })

  it('lets admins see every session but users not', async () => {
    loginAs('alice')
    expect((await callApi('GET', '/admin/sessions')).status).toBe(403)
    loginAs('admin')
    const all = (await callApi('GET', '/admin/sessions')).body as Array<{ username: string }>
    expect(all.map((s) => s.username)).toEqual(expect.arrayContaining(['admin', 'alice']))
  })
})
```

- [ ] **Step 3: Test laufen lassen → FAIL**

Run: `cd web && npx vitest run src/mocks/handlers/auth.test.ts`
Expected: FAIL – `Failed to resolve import "../db"`.

- [ ] **Step 4: `web/src/mocks/db.ts`**

```ts
import type {
  AgentStatus,
  AttemptStage,
  AuditEntry,
  AuditResult,
  IPRule,
  Jail,
  Locale,
  LoginAttempt,
  Me,
  NewDeviceLogin,
  Notifier,
  Passkey,
  PermissionGrant,
  Role,
  SecondFactor,
  SecuritySettings,
  Session,
  Stage,
  User,
} from '@/api/types'

/** Seeded credentials for MSW mode (local UI development and tests only). */
export const MOCK_PASSWORD = 'correct-horse-battery'
export const MOCK_TOTP_CODE = '123456'
export const MOCK_TOTP_SECRET = 'JBSWY3DPEHPK3PXP'
export const MOCK_RECOVERY_CODES = [
  'abcdefghjk',
  'mnpqrstuvw',
  'xyz2345678',
  'a2b3c4d5e6',
  'f7g8h9jkmn',
  'pqrstuvwxy',
  'z23456789a',
  'bcdefghjkm',
  'npqrstuvwx',
  'yz23456789',
]
export const MOCK_CLIENT_IP = '198.51.100.7'
export const WEBAUTHN_CHALLENGE = 'bW9jay1jaGFsbGVuZ2U'
export const SUDO_MS = 10 * 60 * 1000
const MINUTE = 60_000
export const HOUR = 60 * MINUTE
export const DAY = 24 * HOUR
const RECOVERY_ALPHABET = 'abcdefghjkmnpqrstuvwxyz23456789'
const UA_MAC_SAFARI =
  'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15'
const UA_IPHONE =
  'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1'
const UA_WIN_FIREFOX = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:131.0) Gecko/20100101 Firefox/131.0'
const UA_LINUX_CHROME =
  'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36'

export interface MockUser {
  id: number
  username: string
  password: string
  role: Role
  locale: Locale
  totpEnabled: boolean
  totpPending: boolean
  passkeys: Passkey[]
  recoveryCodes: string[]
  createdAt: number
  lastLoginAt: number | null
}

export interface MockSession {
  id: number
  userId: number
  stage: Stage
  csrf: string
  sudoUntil: number
  ip: string
  country: string
  userAgent: string
  createdAt: number
  lastSeenAt: number
}

export interface MockServer {
  id: number
  name: string
}

export interface MockDb {
  users: MockUser[]
  sessions: MockSession[]
  currentSessionId: number | null
  servers: MockServer[]
  grants: Map<number, PermissionGrant[]>
  audit: AuditEntry[]
  attempts: LoginAttempt[]
  newDeviceLogins: NewDeviceLogin[]
  jails: Jail[]
  ipRules: IPRule[]
  settings: SecuritySettings
  geoipAvailable: boolean
  notifiers: Notifier[]
  agent: AgentStatus
  nextId: number
}

function fakeHash(id: number): string {
  return ((id * 2654435761) >>> 0).toString(16).padStart(8, '0').repeat(8)
}

function seedAttempts(now: number): LoginAttempt[] {
  const ips = ['203.0.113.5', '203.0.113.9', '198.51.100.23', '2001:db8::1f', '192.0.2.44']
  const countries: Record<string, string> = {
    '203.0.113.5': 'CN',
    '203.0.113.9': 'RU',
    '198.51.100.23': 'DE',
    '2001:db8::1f': 'US',
    '192.0.2.44': 'NL',
  }
  const names = ['admin', 'root', 'alice', 'minecraft']
  const out: LoginAttempt[] = []
  for (let i = 0; i < 150; i++) {
    const ip = ips[i % ips.length]
    const success = i % 4 === 0
    out.push({
      id: 1000 + 150 - i,
      ts: now - i * 4 * HOUR - (i % 7) * MINUTE,
      ip,
      username: success ? 'admin' : names[i % names.length],
      stage: success && i % 8 === 0 ? 'totp' : 'password',
      success,
      reason: success ? 'ok' : 'invalid_credentials',
      country: countries[ip],
      user_agent: i % 3 === 0 ? 'python-requests/2.32.3' : UA_MAC_SAFARI,
    })
  }
  return out
}

function seedAudit(now: number): AuditEntry[] {
  const pattern: Array<[string, string, AuditResult]> = [
    ['auth.login', 'admin', 'success'],
    ['user.create', 'user:alice', 'success'],
    ['permissions.set', 'user:alice', 'success'],
    ['auth.login', 'root', 'failure'],
    ['security.jail.create', '203.0.113.5/32', 'success'],
    ['server.power', 'server:survival', 'success'],
    ['user.delete', 'user:carol', 'denied'],
  ]
  return Array.from({ length: 60 }, (_, i): AuditEntry => {
    const [action, target, result] = pattern[i % pattern.length]
    const id = i + 1
    const system = action === 'security.jail.create'
    return {
      id,
      ts: now - (60 - i) * 6 * HOUR,
      actor_type: system ? 'system' : 'user',
      actor_id: system ? null : 1,
      actor_name: system ? 'system' : 'admin',
      action,
      target,
      ip: system ? '' : MOCK_CLIENT_IP,
      result,
      details: system ? { reason: '5 failed logins in 10 min' } : {},
      hash: fakeHash(id),
    }
  })
}

function seed(now: number): MockDb {
  return {
    users: [
      {
        id: 1,
        username: 'admin',
        password: MOCK_PASSWORD,
        role: 'admin',
        locale: 'de',
        totpEnabled: true,
        totpPending: false,
        passkeys: [{ id: 1, name: 'MacBook Touch ID', created_at: now - 20 * DAY, last_used_at: now - 2 * DAY }],
        recoveryCodes: [...MOCK_RECOVERY_CODES],
        createdAt: now - 30 * DAY,
        lastLoginAt: now - DAY,
      },
      {
        id: 2,
        username: 'alice',
        password: MOCK_PASSWORD,
        role: 'user',
        locale: 'de',
        totpEnabled: false,
        totpPending: false,
        passkeys: [],
        recoveryCodes: [],
        createdAt: now - 10 * DAY,
        lastLoginAt: now - 3 * DAY,
      },
      {
        id: 3,
        username: 'newadmin',
        password: MOCK_PASSWORD,
        role: 'admin',
        locale: 'de',
        totpEnabled: false,
        totpPending: false,
        passkeys: [],
        recoveryCodes: [],
        createdAt: now - HOUR,
        lastLoginAt: null,
      },
      {
        id: 4,
        username: 'bob',
        password: MOCK_PASSWORD,
        role: 'user',
        locale: 'en',
        totpEnabled: true,
        totpPending: false,
        passkeys: [],
        recoveryCodes: MOCK_RECOVERY_CODES.slice(0, 5),
        createdAt: now - 5 * DAY,
        lastLoginAt: now - 4 * DAY,
      },
    ],
    sessions: [
      { id: 101, userId: 1, stage: 'full', csrf: 'seed-101', sudoUntil: 0, ip: '198.51.100.23', country: 'DE', userAgent: UA_IPHONE, createdAt: now - 3 * HOUR, lastSeenAt: now - 20 * MINUTE },
      { id: 102, userId: 1, stage: 'full', csrf: 'seed-102', sudoUntil: 0, ip: '203.0.113.200', country: 'AT', userAgent: UA_WIN_FIREFOX, createdAt: now - 8 * HOUR, lastSeenAt: now - 2 * HOUR },
      { id: 103, userId: 2, stage: 'full', csrf: 'seed-103', sudoUntil: 0, ip: '192.0.2.10', country: 'DE', userAgent: UA_LINUX_CHROME, createdAt: now - DAY, lastSeenAt: now - 5 * HOUR },
    ],
    currentSessionId: null,
    servers: [
      { id: 1, name: 'survival' },
      { id: 2, name: 'creative' },
      { id: 3, name: 'lobby' },
    ],
    grants: new Map<number, PermissionGrant[]>([
      [
        2,
        [
          { server_id: 1, permissions: ['server.view', 'server.power'] },
          { server_id: 2, permissions: ['server.view'] },
        ],
      ],
    ]),
    audit: seedAudit(now),
    attempts: seedAttempts(now),
    newDeviceLogins: [
      { ts: now - 2 * HOUR, username: 'admin', ip: '203.0.113.200', country: 'AT', user_agent: UA_WIN_FIREFOX },
      { ts: now - 3 * DAY, username: 'alice', ip: '192.0.2.10', country: 'DE', user_agent: UA_LINUX_CHROME },
    ],
    jails: [
      { id: 1, ip_key: '203.0.113.5/32', reason: '5 failed logins in 10 min', level: 1, manual: false, created_at: now - 5 * MINUTE, expires_at: now + 10 * MINUTE, lifted_at: null, lifted_by: null, firewall_synced: true },
      { id: 2, ip_key: '2001:db8::/64', reason: 'repeat offender', level: 3, manual: false, created_at: now - HOUR, expires_at: now + HOUR, lifted_at: null, lifted_by: null, firewall_synced: false },
      { id: 3, ip_key: '192.0.2.44/32', reason: '5 failed logins in 10 min', level: 1, manual: false, created_at: now - 3 * DAY, expires_at: now - 3 * DAY + 15 * MINUTE, lifted_at: now - 3 * DAY + 5 * MINUTE, lifted_by: 'admin', firewall_synced: true },
    ],
    ipRules: [
      { id: 1, cidr: '198.51.100.0/24', kind: 'allow', comment: 'Home network', expires_at: null, created_by: 'admin', created_at: now - 25 * DAY },
      { id: 2, cidr: '192.0.2.0/24', kind: 'deny', comment: 'Scanner', expires_at: now + 7 * DAY, created_by: 'admin', created_at: now - 2 * DAY },
    ],
    settings: { enforce_2fa_for_users: false, admin_allowlist_only: false, allowed_countries: [] },
    geoipAvailable: true,
    notifiers: [
      { id: 1, kind: 'discord', name: 'Discord #alerts', enabled: true, target_masked: 'https://discord.com/api/webhooks/…/****', created_at: now - 20 * DAY },
      { id: 2, kind: 'ntfy', name: 'ntfy Handy', enabled: false, target_masked: 'https://ntfy.sh/mc-…', created_at: now - 4 * DAY },
    ],
    agent: { reachable: true, firewall: 'noop', version: '0.1.0-dev', pending_sync: 1 },
    nextId: 10_000,
  }
}

export const db: MockDb = seed(Date.now())

export function resetDb(now: number = Date.now()): void {
  Object.assign(db, seed(now))
}

export function nextId(): number {
  db.nextId += 1
  return db.nextId
}

function newToken(prefix: string): string {
  return `${prefix}-${nextId()}-${Math.random().toString(36).slice(2, 10)}`
}

export function findUser(id: number): MockUser | undefined {
  return db.users.find((u) => u.id === id)
}

export function findUserByName(name: string): MockUser | undefined {
  return db.users.find((u) => u.username === name)
}

export function currentSession(): MockSession | undefined {
  return db.sessions.find((s) => s.id === db.currentSessionId)
}

export function hasSecondFactor(u: MockUser): boolean {
  return u.totpEnabled || u.passkeys.length > 0
}

function secondFactorsOf(u: MockUser): SecondFactor[] {
  const factors: SecondFactor[] = []
  if (u.totpEnabled) factors.push('totp')
  if (u.passkeys.length > 0) factors.push('webauthn')
  if (u.recoveryCodes.length > 0 && hasSecondFactor(u)) factors.push('recovery')
  return factors
}

export function toMe(u: MockUser, s: MockSession): Me {
  return {
    id: u.id,
    username: u.username,
    role: u.role,
    stage: s.stage,
    locale: u.locale,
    totp_enabled: u.totpEnabled,
    passkey_count: u.passkeys.length,
    recovery_codes_remaining: u.recoveryCodes.length,
    second_factors: secondFactorsOf(u),
    sudo_until: s.sudoUntil,
    csrf_token: s.csrf,
  }
}

export function toUser(u: MockUser): User {
  return {
    id: u.id,
    username: u.username,
    role: u.role,
    totp_enabled: u.totpEnabled,
    passkey_count: u.passkeys.length,
    created_at: u.createdAt,
    last_login_at: u.lastLoginAt,
  }
}

export function toSession(s: MockSession): Session {
  return {
    id: s.id,
    user_id: s.userId,
    username: findUser(s.userId)?.username ?? '?',
    current: s.id === db.currentSessionId,
    ip: s.ip,
    country: s.country,
    user_agent: s.userAgent,
    created_at: s.createdAt,
    last_seen_at: s.lastSeenAt,
    expires_at: s.createdAt + 12 * HOUR,
  }
}

/** Replaces the browser's current session (models the __Host-sid cookie). */
export function startSession(u: MockUser, stage: Stage): MockSession {
  const now = Date.now()
  db.sessions = db.sessions.filter((s) => s.id !== db.currentSessionId)
  const session: MockSession = {
    id: nextId(),
    userId: u.id,
    stage,
    csrf: newToken('csrf'),
    sudoUntil: 0,
    ip: MOCK_CLIENT_IP,
    country: 'DE',
    userAgent: navigator.userAgent,
    createdAt: now,
    lastSeenAt: now,
  }
  db.sessions.push(session)
  db.currentSessionId = session.id
  return session
}

/** New session id + CSRF token after every privilege change (session fixation protection). */
export function rotateSession(s: MockSession): void {
  s.id = nextId()
  s.csrf = newToken('csrf')
  db.currentSessionId = s.id
}

export function stageAfterPassword(u: MockUser): Stage {
  if (hasSecondFactor(u)) return 'preauth'
  if (u.role === 'admin' || db.settings.enforce_2fa_for_users) return 'setup'
  return 'full'
}

export function recordAudit(
  actor: MockUser | null,
  action: string,
  target: string,
  result: AuditResult = 'success',
  details: Record<string, unknown> = {},
): void {
  const id = (db.audit[db.audit.length - 1]?.id ?? 0) + 1
  db.audit.push({
    id,
    ts: Date.now(),
    actor_type: actor ? 'user' : 'system',
    actor_id: actor?.id ?? null,
    actor_name: actor?.username ?? 'system',
    action,
    target,
    ip: actor ? MOCK_CLIENT_IP : '',
    result,
    details,
    hash: fakeHash(id),
  })
}

export function recordAttempt(username: string, stage: AttemptStage, success: boolean, reason: string): void {
  const id = Math.max(0, ...db.attempts.map((a) => a.id)) + 1
  db.attempts.unshift({
    id,
    ts: Date.now(),
    ip: MOCK_CLIENT_IP,
    username,
    stage,
    success,
    reason,
    country: 'DE',
    user_agent: navigator.userAgent,
  })
}

export function generateRecoveryCodes(): string[] {
  return Array.from({ length: 10 }, () =>
    Array.from({ length: 10 }, () => RECOVERY_ALPHABET[Math.floor(Math.random() * RECOVERY_ALPHABET.length)]).join(''),
  )
}

export function normalizeCode(code: string): string {
  return code.toLowerCase().replace(/[\s-]/g, '')
}

/** Mock-only approximation of httpx.IPKey: IPv4 → /32, IPv6 → /64 (no real prefix math). */
export function ipKeyOf(ip: string): string {
  return ip.includes(':') ? `${ip}/64` : `${ip}/32`
}

/** Test/dev helper: puts the "browser" into an authenticated session without HTTP. */
export function loginAs(username: string, opts: { stage?: Stage; sudo?: boolean } = {}): Me {
  const user = findUserByName(username)
  if (!user) throw new Error(`unknown mock user ${username}`)
  const session = startSession(user, opts.stage ?? 'full')
  if (opts.sudo) session.sudoUntil = Date.now() + SUDO_MS
  return toMe(user, session)
}
```

- [ ] **Step 5: `web/src/mocks/handlers/util.ts`**

```ts
import { HttpResponse, type HttpHandler } from 'msw'
import type { ErrorCode, Permission, Stage } from '@/api/types'
import { ALL_PERMISSIONS } from '@/api/types'
import { currentSession, db, findUser, type MockSession, type MockUser } from '../db'

/** Wildcard origin: matches in jsdom (http://localhost:3000) and in the browser worker. */
export const API = '*/api'
export const ALL_STAGES: Stage[] = ['preauth', 'setup', 'full']

export interface MockOperation {
  operationId: string
  handler: HttpHandler
}

export function op(operationId: string, handler: HttpHandler): MockOperation {
  return { operationId, handler }
}

export function apiError(status: number, code: ErrorCode, message: string = code) {
  return HttpResponse.json({ error: code, message }, { status })
}

export type ErrorResponse = ReturnType<typeof apiError>

export interface Authed {
  session: MockSession
  user: MockUser
}

export interface GuardOptions {
  stages?: Stage[]
  admin?: boolean
  sudo?: boolean
}

/** Mirrors the panel middleware order: session → CSRF → stage → role → sudo. */
export function guard(request: Request, opts: GuardOptions = {}): Authed | ErrorResponse {
  const session = currentSession()
  const user = session ? findUser(session.userId) : undefined
  if (!session || !user) return apiError(401, 'unauthorized', 'authentication required')
  if (request.method !== 'GET' && request.headers.get('X-CSRF-Token') !== session.csrf) {
    return apiError(403, 'csrf_invalid', 'missing or invalid CSRF token')
  }
  const stages = opts.stages ?? ['full']
  if (!stages.includes(session.stage)) {
    return session.stage === 'setup'
      ? apiError(403, 'setup_2fa_required', 'two-factor setup required')
      : apiError(401, 'unauthorized', 'authentication incomplete')
  }
  if (opts.admin && user.role !== 'admin') return apiError(403, 'forbidden', 'admin role required')
  if (opts.sudo && !(session.sudoUntil > Date.now())) return apiError(403, 'sudo_required', 're-authentication required')
  session.lastSeenAt = Date.now()
  return { session, user }
}

export async function readJson<T>(request: Request): Promise<T | null> {
  try {
    return (await request.json()) as T
  } catch {
    return null
  }
}

export function idParam(params: Record<string, string | readonly string[] | undefined>): number {
  const raw = params.id
  const value = Number(typeof raw === 'string' ? raw : raw?.[0])
  return Number.isInteger(value) && value > 0 ? value : -1
}

export function noContent() {
  return new HttpResponse(null, { status: 204 })
}

export function paginate<T>(items: T[], url: URL): { items: T[]; next_cursor: string | null } {
  const offset = Math.max(0, Number(url.searchParams.get('cursor') ?? '0') || 0)
  const limit = Math.min(200, Math.max(1, Number(url.searchParams.get('limit') ?? '50') || 50))
  const next = offset + limit < items.length ? String(offset + limit) : null
  return { items: items.slice(offset, offset + limit), next_cursor: next }
}

export function serverPermissions(user: MockUser, serverId: number): Permission[] {
  if (user.role === 'admin') return [...ALL_PERMISSIONS]
  return db.grants.get(user.id)?.find((g) => g.server_id === serverId)?.permissions ?? []
}
```

- [ ] **Step 6: `web/src/mocks/handlers/auth.ts`** (11 Operationen inkl. `getHealth`)

```ts
import { http, HttpResponse } from 'msw'
import type { AttemptStage, Me, SudoResponse } from '@/api/types'
import {
  db,
  findUserByName,
  MOCK_TOTP_CODE,
  normalizeCode,
  recordAttempt,
  recordAudit,
  rotateSession,
  startSession,
  stageAfterPassword,
  SUDO_MS,
  toMe,
  WEBAUTHN_CHALLENGE,
} from '../db'
import { ALL_STAGES, API, apiError, guard, noContent, op, readJson, type Authed, type MockOperation } from './util'

function requestOptions() {
  return {
    publicKey: {
      challenge: WEBAUTHN_CHALLENGE,
      timeout: 60_000,
      rpId: 'localhost',
      allowCredentials: [],
      userVerification: 'preferred',
    },
  }
}

function hasCredential(body: { credential?: unknown } | null): boolean {
  const credential = body?.credential
  return typeof credential === 'object' && credential !== null
}

function completeLogin(g: Authed, method: AttemptStage) {
  recordAttempt(g.user.username, method, true, 'ok')
  g.session.stage = 'full'
  rotateSession(g.session)
  g.user.lastLoginAt = Date.now()
  recordAudit(g.user, 'auth.login', g.user.username, 'success', { method })
  return HttpResponse.json<Me>(toMe(g.user, g.session))
}

function grantSudo(g: Authed) {
  recordAttempt(g.user.username, 'sudo', true, 'ok')
  g.session.sudoUntil = Date.now() + SUDO_MS
  rotateSession(g.session)
  recordAudit(g.user, 'auth.sudo', g.user.username)
  return HttpResponse.json<SudoResponse>({ sudo_until: g.session.sudoUntil })
}

export const authOperations: MockOperation[] = [
  op('getHealth', http.get(`${API}/health`, () => HttpResponse.json({ status: 'ok' as const }))),

  op(
    'login',
    http.post(`${API}/auth/login`, async ({ request }) => {
      const body = await readJson<{ username?: string; password?: string }>(request)
      if (!body?.username || !body.password) return apiError(400, 'bad_request', 'username and password required')
      const user = findUserByName(body.username)
      if (!user || user.password !== body.password) {
        recordAttempt(body.username, 'password', false, 'invalid_credentials')
        return apiError(401, 'invalid_credentials', 'invalid username or password')
      }
      recordAttempt(user.username, 'password', true, 'ok')
      const session = startSession(user, stageAfterPassword(user))
      if (session.stage === 'full') {
        user.lastLoginAt = Date.now()
        recordAudit(user, 'auth.login', user.username)
      }
      return HttpResponse.json<Me>(toMe(user, session))
    }),
  ),

  op(
    'getMe',
    http.get(`${API}/auth/me`, ({ request }) => {
      const g = guard(request, { stages: ALL_STAGES })
      if (g instanceof Response) return g
      return HttpResponse.json<Me>(toMe(g.user, g.session))
    }),
  ),

  op(
    'logout',
    http.post(`${API}/auth/logout`, ({ request }) => {
      const g = guard(request, { stages: ALL_STAGES })
      if (g instanceof Response) return g
      db.sessions = db.sessions.filter((s) => s.id !== g.session.id)
      db.currentSessionId = null
      return noContent()
    }),
  ),

  op(
    'verifyTotp',
    http.post(`${API}/auth/2fa/totp`, async ({ request }) => {
      const g = guard(request, { stages: ['preauth'] })
      if (g instanceof Response) return g
      const body = await readJson<{ code?: string }>(request)
      if (!g.user.totpEnabled || body?.code !== MOCK_TOTP_CODE) {
        recordAttempt(g.user.username, 'totp', false, 'invalid_code')
        return apiError(401, 'invalid_code', 'invalid code')
      }
      return completeLogin(g, 'totp')
    }),
  ),

  op(
    'verifyRecovery',
    http.post(`${API}/auth/2fa/recovery`, async ({ request }) => {
      const g = guard(request, { stages: ['preauth'] })
      if (g instanceof Response) return g
      const body = await readJson<{ code?: string }>(request)
      const code = normalizeCode(body?.code ?? '')
      const index = g.user.recoveryCodes.findIndex((c) => normalizeCode(c) === code)
      if (!code || index < 0) {
        recordAttempt(g.user.username, 'recovery', false, 'invalid_code')
        return apiError(401, 'invalid_code', 'invalid recovery code')
      }
      g.user.recoveryCodes.splice(index, 1)
      return completeLogin(g, 'recovery')
    }),
  ),

  op(
    'beginWebAuthnLogin',
    http.post(`${API}/auth/2fa/webauthn/begin`, ({ request }) => {
      const g = guard(request, { stages: ['preauth'] })
      if (g instanceof Response) return g
      if (g.user.passkeys.length === 0) return apiError(409, 'conflict', 'no passkeys registered')
      return HttpResponse.json(requestOptions())
    }),
  ),

  op(
    'finishWebAuthnLogin',
    http.post(`${API}/auth/2fa/webauthn/finish`, async ({ request }) => {
      const g = guard(request, { stages: ['preauth'] })
      if (g instanceof Response) return g
      const body = await readJson<{ credential?: unknown }>(request)
      if (!hasCredential(body)) {
        recordAttempt(g.user.username, 'webauthn', false, 'invalid_code')
        return apiError(401, 'invalid_code', 'assertion rejected')
      }
      g.user.passkeys[0].last_used_at = Date.now()
      return completeLogin(g, 'webauthn')
    }),
  ),

  op(
    'sudoTotp',
    http.post(`${API}/auth/sudo/totp`, async ({ request }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      const body = await readJson<{ code?: string }>(request)
      if (!g.user.totpEnabled || body?.code !== MOCK_TOTP_CODE) {
        recordAttempt(g.user.username, 'sudo', false, 'invalid_code')
        return apiError(401, 'invalid_code', 'invalid code')
      }
      return grantSudo(g)
    }),
  ),

  op(
    'beginSudoWebAuthn',
    http.post(`${API}/auth/sudo/webauthn/begin`, ({ request }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      if (g.user.passkeys.length === 0) return apiError(409, 'conflict', 'no passkeys registered')
      return HttpResponse.json(requestOptions())
    }),
  ),

  op(
    'finishSudoWebAuthn',
    http.post(`${API}/auth/sudo/webauthn/finish`, async ({ request }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      const body = await readJson<{ credential?: unknown }>(request)
      if (!hasCredential(body)) return apiError(401, 'invalid_code', 'assertion rejected')
      return grantSudo(g)
    }),
  ),
]
```

- [ ] **Step 7: `web/src/mocks/handlers/me.ts`** (13 Operationen)

```ts
import { http, HttpResponse } from 'msw'
import type { Locale, Me, Passkey, PasskeyCreated, RecoveryCodesResponse, Session, TotpSetupResponse } from '@/api/types'
import {
  db,
  generateRecoveryCodes,
  hasSecondFactor,
  MOCK_TOTP_CODE,
  MOCK_TOTP_SECRET,
  nextId,
  recordAudit,
  rotateSession,
  toMe,
  toSession,
  WEBAUTHN_CHALLENGE,
} from '../db'
import { API, apiError, guard, idParam, noContent, op, readJson, type Authed, type MockOperation } from './util'

/** Setup-stage users may add a first factor without sudo; full-stage users with 2FA need sudo. */
function needsSudoForNewFactor(g: Authed): boolean {
  return g.session.stage === 'full' && hasSecondFactor(g.user) && !(g.session.sudoUntil > Date.now())
}

function finishFirstFactor(g: Authed): string[] {
  const codes = g.user.recoveryCodes.length === 0 ? generateRecoveryCodes() : []
  if (codes.length > 0) g.user.recoveryCodes = codes
  if (g.session.stage === 'setup') g.session.stage = 'full'
  rotateSession(g.session)
  return codes
}

export const meOperations: MockOperation[] = [
  op(
    'updateMe',
    http.patch(`${API}/me`, async ({ request }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      const body = await readJson<{ locale?: Locale }>(request)
      if (body?.locale !== 'de' && body?.locale !== 'en') return apiError(400, 'bad_request', 'locale must be de or en')
      g.user.locale = body.locale
      return HttpResponse.json<Me>(toMe(g.user, g.session))
    }),
  ),

  op(
    'changePassword',
    http.post(`${API}/me/password`, async ({ request }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      const body = await readJson<{ current_password?: string; new_password?: string }>(request)
      if (body?.current_password !== g.user.password) return apiError(401, 'invalid_credentials', 'current password is wrong')
      const next = body.new_password ?? ''
      if (next.length < 12 || next.length > 256) return apiError(400, 'validation_failed', 'password must be 12-256 characters')
      g.user.password = next
      db.sessions = db.sessions.filter((s) => s.userId !== g.user.id || s.id === g.session.id)
      rotateSession(g.session)
      recordAudit(g.user, 'auth.password.change', g.user.username)
      return noContent()
    }),
  ),

  op(
    'setupTotp',
    http.post(`${API}/me/totp/setup`, ({ request }) => {
      const g = guard(request, { stages: ['setup', 'full'] })
      if (g instanceof Response) return g
      if (needsSudoForNewFactor(g)) return apiError(403, 'sudo_required', 're-authentication required')
      if (g.user.totpEnabled) return apiError(409, 'conflict', 'TOTP already enabled')
      g.user.totpPending = true
      const label = encodeURIComponent(`mc-panel:${g.user.username}`)
      return HttpResponse.json<TotpSetupResponse>({
        secret: MOCK_TOTP_SECRET,
        otpauth_url: `otpauth://totp/${label}?secret=${MOCK_TOTP_SECRET}&issuer=mc-panel&algorithm=SHA1&digits=6&period=30`,
      })
    }),
  ),

  op(
    'enableTotp',
    http.post(`${API}/me/totp/enable`, async ({ request }) => {
      const g = guard(request, { stages: ['setup', 'full'] })
      if (g instanceof Response) return g
      if (!g.user.totpPending) return apiError(409, 'conflict', 'no TOTP setup pending')
      const body = await readJson<{ code?: string }>(request)
      if (body?.code !== MOCK_TOTP_CODE) return apiError(401, 'invalid_code', 'invalid code')
      g.user.totpEnabled = true
      g.user.totpPending = false
      const codes = finishFirstFactor(g)
      recordAudit(g.user, 'auth.totp.enable', g.user.username)
      return HttpResponse.json<RecoveryCodesResponse>({ codes })
    }),
  ),

  op(
    'disableTotp',
    http.delete(`${API}/me/totp`, ({ request }) => {
      const g = guard(request, { sudo: true })
      if (g instanceof Response) return g
      if (!g.user.totpEnabled) return apiError(409, 'conflict', 'TOTP is not enabled')
      const lastFactor = g.user.passkeys.length === 0
      if (lastFactor && (g.user.role === 'admin' || db.settings.enforce_2fa_for_users)) {
        return apiError(409, 'conflict', 'cannot remove the last second factor')
      }
      g.user.totpEnabled = false
      recordAudit(g.user, 'auth.totp.disable', g.user.username)
      return noContent()
    }),
  ),

  op(
    'beginPasskeyRegistration',
    http.post(`${API}/me/passkeys/begin`, ({ request }) => {
      const g = guard(request, { stages: ['setup', 'full'] })
      if (g instanceof Response) return g
      if (needsSudoForNewFactor(g)) return apiError(403, 'sudo_required', 're-authentication required')
      return HttpResponse.json({
        publicKey: {
          challenge: WEBAUTHN_CHALLENGE,
          rp: { name: 'mc-panel', id: 'localhost' },
          user: { id: `dXNlci0${g.user.id}`, name: g.user.username, displayName: g.user.username },
          pubKeyCredParams: [
            { type: 'public-key', alg: -7 },
            { type: 'public-key', alg: -257 },
          ],
          timeout: 60_000,
          attestation: 'none',
          excludeCredentials: [],
          authenticatorSelection: { residentKey: 'preferred', userVerification: 'preferred' },
        },
      })
    }),
  ),

  op(
    'finishPasskeyRegistration',
    http.post(`${API}/me/passkeys/finish`, async ({ request }) => {
      const g = guard(request, { stages: ['setup', 'full'] })
      if (g instanceof Response) return g
      const body = await readJson<{ name?: string; credential?: unknown }>(request)
      const name = body?.name?.trim() ?? ''
      if (name.length < 1 || name.length > 64) return apiError(400, 'validation_failed', 'name must be 1-64 characters')
      if (typeof body?.credential !== 'object' || body.credential === null) return apiError(400, 'bad_request', 'credential missing')
      const passkey: Passkey = { id: nextId(), name, created_at: Date.now(), last_used_at: null }
      g.user.passkeys.push(passkey)
      const codes = finishFirstFactor(g)
      recordAudit(g.user, 'auth.passkey.add', g.user.username, 'success', { name })
      return HttpResponse.json<PasskeyCreated>({ passkey, recovery_codes: codes }, { status: 201 })
    }),
  ),

  op(
    'listPasskeys',
    http.get(`${API}/me/passkeys`, ({ request }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      return HttpResponse.json<Passkey[]>(g.user.passkeys)
    }),
  ),

  op(
    'deletePasskey',
    http.delete(`${API}/me/passkeys/:id`, ({ request, params }) => {
      const g = guard(request, { sudo: true })
      if (g instanceof Response) return g
      const id = idParam(params)
      const passkey = g.user.passkeys.find((p) => p.id === id)
      if (!passkey) return apiError(404, 'not_found', 'passkey not found')
      const lastFactor = !g.user.totpEnabled && g.user.passkeys.length === 1
      if (lastFactor && (g.user.role === 'admin' || db.settings.enforce_2fa_for_users)) {
        return apiError(409, 'conflict', 'cannot remove the last second factor')
      }
      g.user.passkeys = g.user.passkeys.filter((p) => p.id !== id)
      recordAudit(g.user, 'auth.passkey.delete', g.user.username, 'success', { name: passkey.name })
      return noContent()
    }),
  ),

  op(
    'regenerateRecoveryCodes',
    http.post(`${API}/me/recovery-codes`, ({ request }) => {
      const g = guard(request, { sudo: true })
      if (g instanceof Response) return g
      g.user.recoveryCodes = generateRecoveryCodes()
      recordAudit(g.user, 'auth.recovery.regenerate', g.user.username)
      return HttpResponse.json<RecoveryCodesResponse>({ codes: [...g.user.recoveryCodes] })
    }),
  ),

  op(
    'listMySessions',
    http.get(`${API}/me/sessions`, ({ request }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      return HttpResponse.json<Session[]>(db.sessions.filter((s) => s.userId === g.user.id).map(toSession))
    }),
  ),

  op(
    'revokeMySession',
    http.delete(`${API}/me/sessions/:id`, ({ request, params }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      const id = idParam(params)
      if (!db.sessions.some((s) => s.id === id && s.userId === g.user.id)) return apiError(404, 'not_found', 'session not found')
      db.sessions = db.sessions.filter((s) => s.id !== id)
      if (id === db.currentSessionId) db.currentSessionId = null
      return noContent()
    }),
  ),

  op(
    'revokeOtherSessions',
    http.post(`${API}/me/sessions/revoke-others`, ({ request }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      db.sessions = db.sessions.filter((s) => s.userId !== g.user.id || s.id === g.session.id)
      return noContent()
    }),
  ),
]
```

- [ ] **Step 8: `web/src/mocks/handlers/admin.ts`** (2 Operationen)

```ts
import { http, HttpResponse } from 'msw'
import type { Session } from '@/api/types'
import { db, recordAudit, toSession } from '../db'
import { API, apiError, guard, idParam, noContent, op, type MockOperation } from './util'

export const adminOperations: MockOperation[] = [
  op(
    'listAllSessions',
    http.get(`${API}/admin/sessions`, ({ request }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      return HttpResponse.json<Session[]>(db.sessions.map(toSession))
    }),
  ),
  op(
    'revokeAnySession',
    http.delete(`${API}/admin/sessions/:id`, ({ request, params }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const id = idParam(params)
      const target = db.sessions.find((s) => s.id === id)
      if (!target) return apiError(404, 'not_found', 'session not found')
      db.sessions = db.sessions.filter((s) => s.id !== id)
      if (id === db.currentSessionId) db.currentSessionId = null
      recordAudit(g.user, 'session.revoke', `session:${id}`)
      return noContent()
    }),
  ),
]
```

- [ ] **Step 9: `web/src/mocks/handlers.ts`, `web/src/mocks/node.ts`**

```ts
// web/src/mocks/handlers.ts
import type { HttpHandler } from 'msw'
import { adminOperations } from './handlers/admin'
import { authOperations } from './handlers/auth'
import { meOperations } from './handlers/me'
import type { MockOperation } from './handlers/util'

export const operations: MockOperation[] = [...authOperations, ...meOperations, ...adminOperations]
export const handlers: HttpHandler[] = operations.map((o) => o.handler)
```

```ts
// web/src/mocks/node.ts
import { setupServer } from 'msw/node'
import { handlers } from './handlers'

export const server = setupServer(...handlers)
```

- [ ] **Step 10: `web/src/test/setup.ts` erweitern** (komplette Datei)

```ts
import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterAll, afterEach, beforeAll } from 'vitest'
import i18n, { i18nReady } from '@/i18n'
import { resetDb } from '@/mocks/db'
import { server } from '@/mocks/node'
import { installMatchMedia, setPrefersDark, setViewport } from './media'

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= ResizeObserverStub as unknown as typeof ResizeObserver
Element.prototype.scrollIntoView ??= function scrollIntoView() {}
Element.prototype.hasPointerCapture ??= () => false
Element.prototype.releasePointerCapture ??= () => {}

installMatchMedia()
await i18nReady
await i18n.changeLanguage('de')

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
afterEach(async () => {
  cleanup()
  server.resetHandlers()
  resetDb()
  setViewport('desktop')
  setPrefersDark(false)
  localStorage.clear()
  await i18n.changeLanguage('de')
})
afterAll(() => server.close())
```

- [ ] **Step 11: Tests laufen lassen → PASS**

Run: `cd web && npx vitest run src/mocks/handlers/auth.test.ts`
Expected: PASS (7 Tests).

- [ ] **Step 12: Gesamte Suite + Typecheck**

Run: `cd web && npx vitest run && npm run typecheck`
Expected: grün.

- [ ] **Step 13: Commit**

```bash
git add web/src/mocks web/src/test
git commit -m "feat(web): add in-memory MSW mock with auth, profile and admin session handlers" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: MSW-Mock – Benutzer, Server, Audit, Security; Browser-Worker

**Files:**
- Create: `web/src/mocks/handlers/users.ts`, `web/src/mocks/handlers/servers.ts`, `web/src/mocks/handlers/audit.ts`, `web/src/mocks/handlers/security.ts`, `web/src/mocks/browser.ts`, `web/.env.mock`, `web/public/mockServiceWorker.js` (generiert)
- Modify: `web/src/mocks/handlers.ts`, `web/vite.config.ts`, `web/src/vite-env.d.ts`, `web/package.json` (nur `msw.workerDirectory` durch `msw init` und Skripte `dev:mock`, `e2e`, `e2e:real`)
- Test: `web/src/mocks/handlers.test.ts`

**Interfaces:**
- Consumes: alles aus Task 3 (`db`, `guard`, `op`, `paginate`, `serverPermissions`, `callApi`, `field`)
- Produces: `operations` mit genau 57 Einträgen (eine pro `operationId` aus `api/openapi.yaml`); `worker` (`msw/browser`); `.env.mock` mit `VITE_MSW=1`; npm-Skripte `dev:mock` (`vite --mode mock`), `e2e`, `e2e:real`; Vite-Plugin `stripMswWorker`, das `mockServiceWorker.js` aus dem Produktions-Build entfernt.

- [ ] **Step 1: Failing Test `web/src/mocks/handlers.test.ts`**

```ts
import { describe, expect, it } from 'vitest'
import openapiYaml from '../../../api/openapi.yaml?raw'
import { callApi, field } from '@/test/call-api'
import { db, loginAs } from './db'
import { operations } from './handlers'

describe('mock coverage', () => {
  it('mocks every operationId of api/openapi.yaml exactly once', () => {
    const ids = [...openapiYaml.matchAll(/operationId:\s*(\w+)/g)].map((m) => m[1]).sort()
    expect(ids).toHaveLength(57)
    expect(operations.map((o) => o.operationId).sort()).toEqual(ids)
  })
})

describe('mock behaviour', () => {
  it('shows a restricted user only the servers they hold permissions on', async () => {
    loginAs('alice')
    const list = (await callApi('GET', '/servers')).body as Array<{ name: string; permissions: string[] }>
    expect(list.map((s) => s.name)).toEqual(['survival', 'creative'])
    expect(list[0].permissions).toContain('server.power')
    expect((await callApi('GET', '/servers/3')).status).toBe(403)
  })

  it('creates users only with sudo and rejects duplicate names', async () => {
    const me = loginAs('admin')
    const body = { username: 'carol', password: 'long-enough-password', role: 'user' }
    expect(field((await callApi('POST', '/users', { body, csrf: me.csrf_token })).body, 'error')).toBe('sudo_required')
    const sudo = loginAs('admin', { sudo: true })
    expect((await callApi('POST', '/users', { body, csrf: sudo.csrf_token })).status).toBe(201)
    expect((await callApi('POST', '/users', { body, csrf: sudo.csrf_token })).status).toBe(409)
  })

  it('refuses to demote the last admin', async () => {
    const me = loginAs('admin', { sudo: true })
    db.users = db.users.filter((u) => u.username !== 'newadmin')
    const res = await callApi('PATCH', '/users/1', { body: { role: 'user' }, csrf: me.csrf_token })
    expect(res.status).toBe(409)
  })

  it('pages login attempts with an opaque cursor and filters by result', async () => {
    loginAs('admin')
    const first = await callApi('GET', '/security/attempts?limit=50')
    expect(field(first.body, 'items')).toHaveLength(50)
    expect(field(first.body, 'next_cursor')).toBe('50')
    const last = await callApi('GET', '/security/attempts?limit=50&cursor=100')
    expect(field(last.body, 'next_cursor')).toBeNull()
    const failures = (await callApi('GET', '/security/attempts?success=false')).body as { items: Array<{ success: boolean }> }
    expect(failures.items.every((a) => !a.success)).toBe(true)
  })

  it('validates country codes in security settings', async () => {
    const me = loginAs('admin', { sudo: true })
    const res = await callApi('PUT', '/security/settings', {
      body: { enforce_2fa_for_users: false, admin_allowlist_only: false, allowed_countries: ['Germany'] },
      csrf: me.csrf_token,
    })
    expect(field(res.body, 'error')).toBe('validation_failed')
  })

  it('filters the audit log by result and pages it', async () => {
    loginAs('admin')
    const page = (await callApi('GET', '/audit?result=denied&limit=5')).body as {
      items: Array<{ result: string }>
      next_cursor: string | null
    }
    expect(page.items).toHaveLength(5)
    expect(page.items.every((e) => e.result === 'denied')).toBe(true)
    expect(page.next_cursor).toBe('5')
  })

  it('buckets the overview series by range', async () => {
    loginAs('admin')
    expect(field((await callApi('GET', '/security/overview?range=24h')).body, 'series')).toHaveLength(24)
    expect(field((await callApi('GET', '/security/overview?range=7d')).body, 'series')).toHaveLength(7)
    expect(field((await callApi('GET', '/security/overview?range=30d')).body, 'series')).toHaveLength(30)
  })
})
```

- [ ] **Step 2: Test laufen lassen → FAIL**

Run: `cd web && npx vitest run src/mocks/handlers.test.ts`
Expected: FAIL – Coverage-Test meldet fehlende operationIds (`listUsers`, `listServers`, …), Verhaltens-Tests scheitern an `onUnhandledRequest: 'error'`.

- [ ] **Step 3: `web/src/mocks/handlers/users.ts`** (8 Operationen)

```ts
import { http, HttpResponse } from 'msw'
import type { Permission, PermissionGrant, Role, User, UserPermissions } from '@/api/types'
import { ALL_PERMISSIONS } from '@/api/types'
import { db, findUser, nextId, recordAudit, toUser, type MockUser } from '../db'
import { API, apiError, guard, idParam, noContent, op, readJson, type MockOperation } from './util'

const USERNAME = /^[a-zA-Z0-9_.-]{3,32}$/

function isRole(value: unknown): value is Role {
  return value === 'admin' || value === 'user'
}

function withView(perms: Permission[]): Permission[] {
  const set = new Set<Permission>(perms)
  if (set.size > 0) set.add('server.view')
  return ALL_PERMISSIONS.filter((p) => set.has(p))
}

function adminCount(): number {
  return db.users.filter((u) => u.role === 'admin').length
}

export const userOperations: MockOperation[] = [
  op(
    'listUsers',
    http.get(`${API}/users`, ({ request }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      return HttpResponse.json<User[]>(db.users.map(toUser))
    }),
  ),

  op(
    'createUser',
    http.post(`${API}/users`, async ({ request }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const body = await readJson<{ username?: string; password?: string; role?: string }>(request)
      const username = body?.username ?? ''
      const password = body?.password ?? ''
      if (!USERNAME.test(username)) return apiError(400, 'validation_failed', 'username must match ^[a-zA-Z0-9_.-]{3,32}$')
      if (password.length < 12 || password.length > 256) return apiError(400, 'validation_failed', 'password must be 12-256 characters')
      if (!isRole(body?.role)) return apiError(400, 'validation_failed', 'invalid role')
      if (db.users.some((u) => u.username.toLowerCase() === username.toLowerCase())) return apiError(409, 'conflict', 'username taken')
      const user: MockUser = {
        id: nextId(),
        username,
        password,
        role: body.role,
        locale: 'de',
        totpEnabled: false,
        totpPending: false,
        passkeys: [],
        recoveryCodes: [],
        createdAt: Date.now(),
        lastLoginAt: null,
      }
      db.users.push(user)
      recordAudit(g.user, 'user.create', `user:${username}`)
      return HttpResponse.json<User>(toUser(user), { status: 201 })
    }),
  ),

  op(
    'getUser',
    http.get(`${API}/users/:id`, ({ request, params }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      const user = findUser(idParam(params))
      if (!user) return apiError(404, 'not_found', 'user not found')
      return HttpResponse.json<User>(toUser(user))
    }),
  ),

  op(
    'updateUser',
    http.patch(`${API}/users/:id`, async ({ request, params }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const user = findUser(idParam(params))
      if (!user) return apiError(404, 'not_found', 'user not found')
      const body = await readJson<{ role?: string; password?: string }>(request)
      if (body?.role !== undefined && !isRole(body.role)) return apiError(400, 'validation_failed', 'invalid role')
      if (body?.password !== undefined && (body.password.length < 12 || body.password.length > 256)) {
        return apiError(400, 'validation_failed', 'password must be 12-256 characters')
      }
      if (body?.role === 'user' && user.role === 'admin' && adminCount() === 1) return apiError(409, 'conflict', 'cannot demote the last admin')
      if (isRole(body?.role)) user.role = body.role
      if (body?.password !== undefined) user.password = body.password
      recordAudit(g.user, 'user.update', `user:${user.username}`, 'success', { role: user.role })
      return HttpResponse.json<User>(toUser(user))
    }),
  ),

  op(
    'deleteUser',
    http.delete(`${API}/users/:id`, ({ request, params }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const user = findUser(idParam(params))
      if (!user) return apiError(404, 'not_found', 'user not found')
      if (user.id === g.user.id) return apiError(409, 'conflict', 'cannot delete yourself')
      if (user.role === 'admin' && adminCount() === 1) return apiError(409, 'conflict', 'cannot delete the last admin')
      db.users = db.users.filter((u) => u.id !== user.id)
      db.sessions = db.sessions.filter((s) => s.userId !== user.id)
      db.grants.delete(user.id)
      recordAudit(g.user, 'user.delete', `user:${user.username}`)
      return noContent()
    }),
  ),

  op(
    'resetUser2fa',
    http.post(`${API}/users/:id/reset-2fa`, ({ request, params }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const user = findUser(idParam(params))
      if (!user) return apiError(404, 'not_found', 'user not found')
      user.totpEnabled = false
      user.totpPending = false
      user.passkeys = []
      user.recoveryCodes = []
      recordAudit(g.user, 'user.reset_2fa', `user:${user.username}`)
      return noContent()
    }),
  ),

  op(
    'getUserPermissions',
    http.get(`${API}/users/:id/permissions`, ({ request, params }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      const user = findUser(idParam(params))
      if (!user) return apiError(404, 'not_found', 'user not found')
      return HttpResponse.json<UserPermissions>({ grants: db.grants.get(user.id) ?? [] })
    }),
  ),

  op(
    'setUserPermissions',
    http.put(`${API}/users/:id/permissions`, async ({ request, params }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const user = findUser(idParam(params))
      if (!user) return apiError(404, 'not_found', 'user not found')
      const body = await readJson<{ grants?: PermissionGrant[] }>(request)
      if (!Array.isArray(body?.grants)) return apiError(400, 'bad_request', 'grants required')
      for (const grant of body.grants) {
        if (!db.servers.some((s) => s.id === grant.server_id)) return apiError(400, 'validation_failed', `unknown server ${grant.server_id}`)
        if (!grant.permissions.every((p: Permission) => ALL_PERMISSIONS.includes(p))) return apiError(400, 'validation_failed', 'unknown permission')
      }
      // Stream D contract: any permission on a server implies server.view.
      const grants = body.grants
        .filter((gr) => gr.permissions.length > 0)
        .map((gr) => ({ server_id: gr.server_id, permissions: withView(gr.permissions) }))
      db.grants.set(user.id, grants)
      recordAudit(g.user, 'permissions.set', `user:${user.username}`, 'success', { grants })
      return HttpResponse.json<UserPermissions>({ grants })
    }),
  ),
]
```

- [ ] **Step 4: `web/src/mocks/handlers/servers.ts`** (5 Operationen)

```ts
import { http, HttpResponse } from 'msw'
import type { PowerAction, Server } from '@/api/types'
import { ALL_PERMISSIONS } from '@/api/types'
import { db, nextId, recordAudit } from '../db'
import { API, apiError, guard, idParam, noContent, op, readJson, serverPermissions, type MockOperation } from './util'

const SERVER_NAME = /^[a-z0-9][a-z0-9-]{1,31}$/
const ACTIONS: PowerAction[] = ['start', 'stop', 'restart']

export const serverOperations: MockOperation[] = [
  op(
    'listServers',
    http.get(`${API}/servers`, ({ request }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      const visible = db.servers
        .map((s) => ({ id: s.id, name: s.name, permissions: serverPermissions(g.user, s.id) }))
        .filter((s) => s.permissions.length > 0)
      return HttpResponse.json<Server[]>(visible)
    }),
  ),

  op(
    'createServer',
    http.post(`${API}/servers`, async ({ request }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const body = await readJson<{ name?: string }>(request)
      const name = body?.name ?? ''
      if (!SERVER_NAME.test(name)) return apiError(400, 'validation_failed', 'name must match ^[a-z0-9][a-z0-9-]{1,31}$')
      if (db.servers.some((s) => s.name === name)) return apiError(409, 'conflict', 'server name taken')
      const server = { id: nextId(), name }
      db.servers.push(server)
      recordAudit(g.user, 'server.create', `server:${name}`)
      return HttpResponse.json<Server>({ ...server, permissions: [...ALL_PERMISSIONS] }, { status: 201 })
    }),
  ),

  op(
    'getServer',
    http.get(`${API}/servers/:id`, ({ request, params }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      const id = idParam(params)
      const permissions = serverPermissions(g.user, id)
      if (!permissions.includes('server.view')) return apiError(403, 'forbidden', 'missing permission server.view')
      const server = db.servers.find((s) => s.id === id)
      if (!server) return apiError(404, 'not_found', 'server not found')
      return HttpResponse.json<Server>({ ...server, permissions })
    }),
  ),

  op(
    'deleteServer',
    http.delete(`${API}/servers/:id`, ({ request, params }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const id = idParam(params)
      const server = db.servers.find((s) => s.id === id)
      if (!server) return apiError(404, 'not_found', 'server not found')
      db.servers = db.servers.filter((s) => s.id !== id)
      for (const [userId, grants] of db.grants) db.grants.set(userId, grants.filter((gr) => gr.server_id !== id))
      recordAudit(g.user, 'server.delete', `server:${server.name}`)
      return noContent()
    }),
  ),

  op(
    'serverPower',
    http.post(`${API}/servers/:id/power`, async ({ request, params }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      const id = idParam(params)
      if (!serverPermissions(g.user, id).includes('server.power')) return apiError(403, 'forbidden', 'missing permission server.power')
      const server = db.servers.find((s) => s.id === id)
      if (!server) return apiError(404, 'not_found', 'server not found')
      const body = await readJson<{ action?: PowerAction }>(request)
      if (!body?.action || !ACTIONS.includes(body.action)) return apiError(400, 'validation_failed', 'action must be start, stop or restart')
      recordAudit(g.user, 'server.power', `server:${server.name}`, 'success', { action: body.action })
      return HttpResponse.json({ status: 'accepted' as const }, { status: 202 })
    }),
  ),
]
```

- [ ] **Step 5: `web/src/mocks/handlers/audit.ts`** (2 Operationen)

```ts
import { http, HttpResponse } from 'msw'
import type { AuditPage, AuditVerify } from '@/api/types'
import { db } from '../db'
import { API, guard, op, paginate, type MockOperation } from './util'

export const auditOperations: MockOperation[] = [
  op(
    'listAudit',
    http.get(`${API}/audit`, ({ request }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      const url = new URL(request.url)
      const actor = url.searchParams.get('actor')?.toLowerCase() ?? ''
      const action = url.searchParams.get('action') ?? ''
      const result = url.searchParams.get('result') ?? ''
      const from = Number(url.searchParams.get('from') ?? '0') || 0
      const to = Number(url.searchParams.get('to') ?? '0') || Number.MAX_SAFE_INTEGER
      const entries = [...db.audit]
        .reverse()
        .filter((e) => !actor || e.actor_name.toLowerCase().includes(actor))
        .filter((e) => !action || e.action.startsWith(action))
        .filter((e) => !result || e.result === result)
        .filter((e) => e.ts >= from && e.ts <= to)
      return HttpResponse.json<AuditPage>(paginate(entries, url))
    }),
  ),
  op(
    'verifyAudit',
    http.get(`${API}/audit/verify`, ({ request }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      return HttpResponse.json<AuditVerify>({ ok: true, checked: db.audit.length, first_bad_id: null })
    }),
  ),
]
```

- [ ] **Step 6: `web/src/mocks/handlers/security.ts`** (16 Operationen)

```ts
import { http, HttpResponse } from 'msw'
import type {
  AgentStatus,
  IPRule,
  Jail,
  LoginAttemptPage,
  Notifier,
  NotifierKind,
  SecurityOverview,
  SecurityRange,
  SecuritySettingsResponse,
} from '@/api/types'
import { db, DAY, HOUR, ipKeyOf, MOCK_CLIENT_IP, nextId, recordAudit } from '../db'
import { API, apiError, guard, idParam, noContent, op, paginate, readJson, type MockOperation } from './util'

const RANGES: Record<SecurityRange, { buckets: number; size: number }> = {
  '24h': { buckets: 24, size: HOUR },
  '7d': { buckets: 7, size: DAY },
  '30d': { buckets: 30, size: DAY },
}
const COUNTRY = /^[A-Z]{2}$/
const CIDR = /^[0-9a-fA-F:.]+\/\d{1,3}$/
const IPV4 = /^(\d{1,3}\.){3}\d{1,3}$/

function isActive(j: Jail, now: number): boolean {
  return j.lifted_at === null && j.expires_at > now
}

function settingsResponse(): SecuritySettingsResponse {
  return { ...db.settings, allowed_countries: [...db.settings.allowed_countries], geoip_available: db.geoipAvailable }
}

function maskTarget(kind: NotifierKind, url: string, topic: string): string {
  let origin = url
  try {
    origin = new URL(url).origin
  } catch {
    // keep raw value
  }
  return kind === 'discord' ? `${origin}/api/webhooks/…/****` : `${origin}/${topic.slice(0, 3)}…`
}

function validUrl(url: unknown): url is string {
  return typeof url === 'string' && /^https?:\/\/\S+$/.test(url)
}

function topCounts(keys: string[], limit: number): Array<[string, number]> {
  const counts = new Map<string, number>()
  for (const key of keys) counts.set(key, (counts.get(key) ?? 0) + 1)
  return [...counts.entries()].sort((a, b) => b[1] - a[1]).slice(0, limit)
}

export const securityOperations: MockOperation[] = [
  op(
    'getSecurityOverview',
    http.get(`${API}/security/overview`, ({ request }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      const raw = new URL(request.url).searchParams.get('range') ?? '24h'
      if (raw !== '24h' && raw !== '7d' && raw !== '30d') return apiError(400, 'bad_request', 'range must be 24h, 7d or 30d')
      const { buckets, size } = RANGES[raw]
      const now = Date.now()
      const start = now - buckets * size
      const inRange = db.attempts.filter((a) => a.ts >= start && a.ts <= now)
      const series = Array.from({ length: buckets }, (_, i) => {
        const t = start + i * size
        const slice = inRange.filter((a) => a.ts >= t && a.ts < t + size)
        return { t, success: slice.filter((a) => a.success).length, failure: slice.filter((a) => !a.success).length }
      })
      const failures = inRange.filter((a) => !a.success)
      const countryOf = new Map(failures.map((a) => [ipKeyOf(a.ip), a.country]))
      const overview: SecurityOverview = {
        range: raw,
        series,
        top_ips: topCounts(failures.map((a) => ipKeyOf(a.ip)), 5).map(([ip_key, n]) => ({ ip_key, country: countryOf.get(ip_key) ?? '', failures: n })),
        top_countries: db.geoipAvailable
          ? topCounts(failures.map((a) => a.country).filter(Boolean), 5).map(([country, n]) => ({ country, failures: n }))
          : [],
        active_jails: db.jails.filter((j) => isActive(j, now)).length,
        failures_total: failures.length,
        new_device_logins: db.newDeviceLogins.filter((d) => d.ts >= start),
      }
      return HttpResponse.json(overview)
    }),
  ),

  op(
    'listLoginAttempts',
    http.get(`${API}/security/attempts`, ({ request }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      const url = new URL(request.url)
      const success = url.searchParams.get('success')
      const ip = url.searchParams.get('ip') ?? ''
      const items = [...db.attempts]
        .sort((a, b) => b.ts - a.ts)
        .filter((a) => success === null || String(a.success) === success)
        .filter((a) => !ip || a.ip.includes(ip))
      return HttpResponse.json<LoginAttemptPage>(paginate(items, url))
    }),
  ),

  op(
    'listJails',
    http.get(`${API}/security/jails`, ({ request }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      const activeOnly = new URL(request.url).searchParams.get('active') !== 'false'
      const now = Date.now()
      const jails = [...db.jails].sort((a, b) => b.created_at - a.created_at).filter((j) => !activeOnly || isActive(j, now))
      return HttpResponse.json<Jail[]>(jails)
    }),
  ),

  op(
    'createJail',
    http.post(`${API}/security/jails`, async ({ request }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const body = await readJson<{ ip?: string; duration_minutes?: number; reason?: string }>(request)
      const ip = body?.ip?.trim() ?? ''
      const minutes = body?.duration_minutes ?? 0
      const reason = body?.reason ?? ''
      if (!(IPV4.test(ip) || ip.includes(':'))) return apiError(400, 'validation_failed', 'invalid IP address')
      if (!Number.isInteger(minutes) || minutes < 1 || minutes > 10080) return apiError(400, 'validation_failed', 'duration must be 1-10080 minutes')
      if (reason.length > 200) return apiError(400, 'validation_failed', 'reason too long')
      if (ip === MOCK_CLIENT_IP) return apiError(409, 'conflict', 'refusing to jail your own IP')
      const now = Date.now()
      const jail: Jail = {
        id: nextId(),
        ip_key: ipKeyOf(ip),
        reason,
        level: 1,
        manual: true,
        created_at: now,
        expires_at: now + minutes * 60_000,
        lifted_at: null,
        lifted_by: null,
        firewall_synced: false,
      }
      db.jails.push(jail)
      recordAudit(g.user, 'security.jail.create', jail.ip_key, 'success', { minutes })
      return HttpResponse.json<Jail>(jail, { status: 201 })
    }),
  ),

  op(
    'liftJail',
    http.delete(`${API}/security/jails/:id`, ({ request, params }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const jail = db.jails.find((j) => j.id === idParam(params) && isActive(j, Date.now()))
      if (!jail) return apiError(404, 'not_found', 'jail not found')
      jail.lifted_at = Date.now()
      jail.lifted_by = g.user.username
      recordAudit(g.user, 'security.jail.lift', jail.ip_key)
      return noContent()
    }),
  ),

  op(
    'listIPRules',
    http.get(`${API}/security/ip-rules`, ({ request }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      return HttpResponse.json<IPRule[]>(db.ipRules)
    }),
  ),

  op(
    'createIPRule',
    http.post(`${API}/security/ip-rules`, async ({ request }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const body = await readJson<{ cidr?: string; kind?: string; comment?: string; expires_at?: number | null }>(request)
      const cidr = body?.cidr?.trim() ?? ''
      if (!CIDR.test(cidr)) return apiError(400, 'validation_failed', 'invalid CIDR')
      if (body?.kind !== 'allow' && body?.kind !== 'deny') return apiError(400, 'validation_failed', 'kind must be allow or deny')
      if ((body.comment ?? '').length > 200) return apiError(400, 'validation_failed', 'comment too long')
      if (db.ipRules.some((r) => r.cidr === cidr)) return apiError(409, 'conflict', 'rule already exists')
      if (body.kind === 'deny' && cidr === `${MOCK_CLIENT_IP}/32`) return apiError(409, 'conflict', 'rule would lock you out')
      const rule: IPRule = {
        id: nextId(),
        cidr,
        kind: body.kind,
        comment: body.comment ?? '',
        expires_at: body.expires_at ?? null,
        created_by: g.user.username,
        created_at: Date.now(),
      }
      db.ipRules.push(rule)
      recordAudit(g.user, 'security.ip_rule.create', cidr, 'success', { kind: rule.kind })
      return HttpResponse.json<IPRule>(rule, { status: 201 })
    }),
  ),

  op(
    'deleteIPRule',
    http.delete(`${API}/security/ip-rules/:id`, ({ request, params }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const rule = db.ipRules.find((r) => r.id === idParam(params))
      if (!rule) return apiError(404, 'not_found', 'rule not found')
      const allowRules = db.ipRules.filter((r) => r.kind === 'allow')
      if (db.settings.admin_allowlist_only && rule.kind === 'allow' && allowRules.length === 1) {
        return apiError(409, 'conflict', 'removing the last allow rule would lock you out')
      }
      db.ipRules = db.ipRules.filter((r) => r.id !== rule.id)
      recordAudit(g.user, 'security.ip_rule.delete', rule.cidr)
      return noContent()
    }),
  ),

  op(
    'getSecuritySettings',
    http.get(`${API}/security/settings`, ({ request }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      return HttpResponse.json<SecuritySettingsResponse>(settingsResponse())
    }),
  ),

  op(
    'updateSecuritySettings',
    http.put(`${API}/security/settings`, async ({ request }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const body = await readJson<{ enforce_2fa_for_users?: boolean; admin_allowlist_only?: boolean; allowed_countries?: string[] }>(request)
      if (typeof body?.enforce_2fa_for_users !== 'boolean' || typeof body.admin_allowlist_only !== 'boolean' || !Array.isArray(body.allowed_countries)) {
        return apiError(400, 'bad_request', 'invalid settings')
      }
      if (!body.allowed_countries.every((c) => COUNTRY.test(c))) return apiError(400, 'validation_failed', 'countries must be ISO 3166-1 alpha-2')
      if (body.admin_allowlist_only && !db.ipRules.some((r) => r.kind === 'allow')) {
        return apiError(409, 'conflict', 'allowlist-only without allow rules would lock you out')
      }
      db.settings = {
        enforce_2fa_for_users: body.enforce_2fa_for_users,
        admin_allowlist_only: body.admin_allowlist_only,
        allowed_countries: body.allowed_countries,
      }
      recordAudit(g.user, 'security.settings.update', 'settings', 'success', { ...db.settings })
      return HttpResponse.json<SecuritySettingsResponse>(settingsResponse())
    }),
  ),

  op(
    'listNotifiers',
    http.get(`${API}/security/notifiers`, ({ request }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      return HttpResponse.json<Notifier[]>(db.notifiers)
    }),
  ),

  op(
    'createNotifier',
    http.post(`${API}/security/notifiers`, async ({ request }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const body = await readJson<{ kind?: string; name?: string; enabled?: boolean; url?: string; topic?: string; token?: string }>(request)
      if (body?.kind !== 'discord' && body?.kind !== 'ntfy') return apiError(400, 'validation_failed', 'kind must be discord or ntfy')
      const name = body.name?.trim() ?? ''
      if (name.length < 1 || name.length > 64) return apiError(400, 'validation_failed', 'name must be 1-64 characters')
      if (!validUrl(body.url)) return apiError(400, 'validation_failed', 'url must be http(s)')
      if (body.kind === 'ntfy' && !body.topic) return apiError(400, 'validation_failed', 'ntfy needs a topic')
      if (db.notifiers.some((n) => n.name === name)) return apiError(409, 'conflict', 'name taken')
      const notifier: Notifier = {
        id: nextId(),
        kind: body.kind,
        name,
        enabled: body.enabled ?? true,
        target_masked: maskTarget(body.kind, body.url, body.topic ?? ''),
        created_at: Date.now(),
      }
      db.notifiers.push(notifier)
      recordAudit(g.user, 'security.notifier.create', name)
      return HttpResponse.json<Notifier>(notifier, { status: 201 })
    }),
  ),

  op(
    'updateNotifier',
    http.patch(`${API}/security/notifiers/:id`, async ({ request, params }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const notifier = db.notifiers.find((n) => n.id === idParam(params))
      if (!notifier) return apiError(404, 'not_found', 'notifier not found')
      const body = await readJson<{ name?: string; enabled?: boolean; url?: string; topic?: string; token?: string }>(request)
      if (body?.name !== undefined && (body.name.trim().length < 1 || body.name.trim().length > 64)) {
        return apiError(400, 'validation_failed', 'name must be 1-64 characters')
      }
      if (body?.url !== undefined && !validUrl(body.url)) return apiError(400, 'validation_failed', 'url must be http(s)')
      const newName = body?.name?.trim()
      if (newName !== undefined && db.notifiers.some((n) => n.id !== notifier.id && n.name === newName)) {
        return apiError(400, 'validation_failed', 'name already in use')
      }
      if (newName !== undefined) notifier.name = newName
      if (typeof body?.enabled === 'boolean') notifier.enabled = body.enabled
      if (body?.url !== undefined) notifier.target_masked = maskTarget(notifier.kind, body.url, body.topic ?? '')
      recordAudit(g.user, 'security.notifier.update', notifier.name)
      return HttpResponse.json<Notifier>(notifier)
    }),
  ),

  op(
    'deleteNotifier',
    http.delete(`${API}/security/notifiers/:id`, ({ request, params }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const notifier = db.notifiers.find((n) => n.id === idParam(params))
      if (!notifier) return apiError(404, 'not_found', 'notifier not found')
      db.notifiers = db.notifiers.filter((n) => n.id !== notifier.id)
      recordAudit(g.user, 'security.notifier.delete', notifier.name)
      return noContent()
    }),
  ),

  op(
    'testNotifier',
    http.post(`${API}/security/notifiers/:id/test`, ({ request, params }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      const notifier = db.notifiers.find((n) => n.id === idParam(params))
      if (!notifier) return apiError(404, 'not_found', 'notifier not found')
      // Mock rule for UI work: disabled notifiers simulate a failed delivery (contract: 400 validation_failed + message).
      if (!notifier.enabled) return apiError(400, 'validation_failed', 'delivery failed: HTTP 404 from target')
      return noContent()
    }),
  ),

  op(
    'getAgentStatus',
    http.get(`${API}/security/agent`, ({ request }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      return HttpResponse.json<AgentStatus>(db.agent)
    }),
  ),
]
```

- [ ] **Step 7: `web/src/mocks/handlers.ts` vervollständigen und `web/src/mocks/browser.ts` anlegen**

```ts
// web/src/mocks/handlers.ts
import type { HttpHandler } from 'msw'
import { adminOperations } from './handlers/admin'
import { auditOperations } from './handlers/audit'
import { authOperations } from './handlers/auth'
import { meOperations } from './handlers/me'
import { securityOperations } from './handlers/security'
import { serverOperations } from './handlers/servers'
import { userOperations } from './handlers/users'
import type { MockOperation } from './handlers/util'

export const operations: MockOperation[] = [
  ...authOperations,
  ...meOperations,
  ...adminOperations,
  ...userOperations,
  ...serverOperations,
  ...auditOperations,
  ...securityOperations,
]
export const handlers: HttpHandler[] = operations.map((o) => o.handler)
```

```ts
// web/src/mocks/browser.ts
import { setupWorker } from 'msw/browser'
import { handlers } from './handlers'

export const worker = setupWorker(...handlers)
```

- [ ] **Step 8: Tests laufen lassen → PASS**

Run: `cd web && npx vitest run src/mocks`
Expected: PASS (Coverage-Test mit 57 operationIds + alle Verhaltens-Tests aus Task 3 und 4).

- [ ] **Step 9: Browser-Worker, Env-Datei, Skripte**

```bash
cd web
npx msw init public --save
printf 'VITE_MSW=1\n' > .env.mock
npm pkg set scripts.dev:mock="vite --mode mock" scripts.e2e="playwright test --project=mock" scripts.e2e:real="playwright test --project=real"
```
Expected: `public/mockServiceWorker.js` existiert; `package.json` enthält `"msw": {"workerDirectory": ["public"]}` und die drei neuen Skripte; keine neue Abhängigkeit.

`web/src/vite-env.d.ts` (komplette Datei):
```ts
/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_MSW?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
```

- [ ] **Step 10: `web/vite.config.ts` – Worker nie mit ausliefern** (komplette Datei)

```ts
/// <reference types="vitest/config" />
import fs from 'node:fs'
import path from 'node:path'
import tailwindcss from '@tailwindcss/vite'
import { tanstackRouter } from '@tanstack/router-plugin/vite'
import react from '@vitejs/plugin-react'
import { defineConfig, type Plugin } from 'vite'

const outDir = path.resolve(__dirname, '../internal/webui/dist')

/** The MSW service worker lives in public/ for `npm run dev:mock`, but must never be embedded into the Go binary. */
function stripMswWorker(): Plugin {
  return {
    name: 'mcpanel-strip-msw-worker',
    apply: 'build',
    closeBundle() {
      fs.rmSync(path.join(outDir, 'mockServiceWorker.js'), { force: true })
    },
  }
}

export default defineConfig({
  plugins: [tanstackRouter({ target: 'react', autoCodeSplitting: true }), react(), tailwindcss(), stripMswWorker()],
  resolve: { alias: { '@': path.resolve(__dirname, './src') } },
  server: {
    port: 5173,
    strictPort: true,
    proxy: { '/api': { target: 'http://127.0.0.1:8080', changeOrigin: false } },
  },
  build: { outDir, emptyOutDir: true, sourcemap: false },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    exclude: ['e2e/**', 'node_modules/**'],
  },
})
```

- [ ] **Step 11: Build prüfen – kein Worker im Embed**

Run: `make web-build && test ! -f internal/webui/dist/mockServiceWorker.js && echo "worker stripped"`
Expected: Build grün (inkl. `tsc -b`), Ausgabe `worker stripped`.

- [ ] **Step 12: Mock-Dev-Server manuell prüfen**

Run: `cd web && npm run dev:mock` und `http://localhost:5173` im Browser öffnen.
Expected: Browser-Konsole zeigt `[MSW] Mocking enabled.` erst ab Task 8 (Bootstrap); bis dahin lädt die Phase-0-Seite ohne Fehler. Server mit `Ctrl+C` beenden.

- [ ] **Step 13: Commit**

```bash
git add web/src/mocks web/src/vite-env.d.ts web/vite.config.ts web/.env.mock web/public/mockServiceWorker.js web/package.json web/package-lock.json
git commit -m "feat(web): mock all 57 API operations with MSW and keep the worker out of production builds" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 5: API-Client – CSRF-Store, Fehler, Middleware (401, Sudo-Retry, Setup)

**Files:**
- Create: `web/src/api/csrf.ts`, `web/src/api/errors.ts`, `web/src/api/handlers.ts`, `web/src/api/client.ts`, `web/src/lib/error-message.ts`
- Modify: `web/src/test/setup.ts`
- Test: `web/src/api/errors.test.ts`, `web/src/api/client.test.ts`, `web/src/lib/error-message.test.ts`

**Interfaces:**
- Consumes: `paths` aus `src/api/schema.d.ts`; `Me`, `ErrorCode` (Task 3); MSW-`server`, `db`, `loginAs`, `currentSession`, `MOCK_*` (Task 3/4); `i18n` (Task 1)
- Produces:
```ts
// api/csrf.ts
export function getCsrfToken(): string
export function setCsrfToken(token: string): void
// api/handlers.ts
export interface ApiHandlers { onUnauthorized(): void; onSetupRequired(): void; requestSudo(): Promise<boolean>; onMe(me: Me): void }
export function configureApiHandlers(overrides: Partial<ApiHandlers>): void
export function resetApiHandlers(): void
export function apiHandlers(): ApiHandlers
// api/errors.ts
export type ApiErrorCode = ErrorCode | 'unknown'
export class ApiError extends Error { readonly status: number; readonly code: ApiErrorCode; readonly requestId?: string; readonly retryAfterSeconds?: number }
export function toApiError(response: Response, body: unknown): ApiError
export function isApiError(err: unknown, code?: ApiErrorCode): err is ApiError
export function unwrap<T>(p: Promise<{ data?: T; error?: unknown; response: Response }>): Promise<T>
export function unwrapVoid(p: Promise<{ error?: unknown; response: Response }>): Promise<void>
// api/client.ts
export type ClientPaths      // paths ohne Pflicht-Header X-CSRF-Token (setzt die Middleware)
export const ME_PATHS: ReadonlySet<string>
export const ROTATING_PATHS: ReadonlySet<string>
export function createApiClient(baseUrl?: string): ApiClient
export type ApiClient
export const api: ApiClient
// lib/error-message.ts
export function isWebAuthnCancelled(err: unknown): boolean
export function errorMessage(err: unknown, t: TFunction): string
export function useErrorToast(): (err: unknown) => void
```

Verhalten der Middleware (verbindlich):
1. Jede Nicht-GET/HEAD-Anfrage bekommt `X-CSRF-Token` aus dem Store. `credentials: 'same-origin'`.
2. Erfolgreiche Antworten von `ME_PATHS` (`/auth/login`, `/auth/me`, `/auth/2fa/totp`, `/auth/2fa/recovery`, `/auth/2fa/webauthn/finish`, `PATCH /me`) → Token übernehmen + `onMe(me)`.
3. Erfolgreiche Antworten von `ROTATING_PATHS` (`/auth/sudo/totp`, `/auth/sudo/webauthn/finish`, `/me/totp/enable`, `/me/passkeys/finish`, `/me/password`) → `GET /auth/me` direkt nachladen (Token + `onMe`), **bevor** der Aufrufer die Antwort sieht.
4. `401 unauthorized` → Token leeren, `onUnauthorized()`. `401 invalid_credentials`/`invalid_code` lösen **keine** Umleitung aus.
5. `403 setup_2fa_required` → `onSetupRequired()`.
6. `403 csrf_invalid` → Me nachladen, Anfrage **einmal** wiederholen.
7. `403 sudo_required` (nicht für `/auth/*`) → `await requestSudo()`; bei `true` Anfrage **einmal** mit frischem Token wiederholen, bei `false` Originalantwort zurückgeben.

- [ ] **Step 1: Failing Tests**

`web/src/api/errors.test.ts`:
```ts
import { describe, expect, it } from 'vitest'
import { ApiError, isApiError, toApiError, unwrap, unwrapVoid } from './errors'

describe('toApiError', () => {
  it('parses the JSON error format', () => {
    const err = toApiError(new Response(null, { status: 409 }), { error: 'conflict', message: 'username taken', request_id: 'r-1' })
    expect(err).toBeInstanceOf(ApiError)
    expect(err).toMatchObject({ status: 409, code: 'conflict', message: 'username taken', requestId: 'r-1' })
  })

  it('falls back to unknown for non-JSON bodies', () => {
    const err = toApiError(new Response(null, { status: 502 }), 'Bad Gateway')
    expect(err).toMatchObject({ status: 502, code: 'unknown', message: 'Bad Gateway' })
  })

  it('reads Retry-After seconds', () => {
    const res = new Response(null, { status: 429, headers: { 'Retry-After': '30' } })
    expect(toApiError(res, { error: 'rate_limited', message: 'slow down' }).retryAfterSeconds).toBe(30)
  })

  it('narrows by code', () => {
    const err = toApiError(new Response(null, { status: 403 }), { error: 'sudo_required', message: 'x' })
    expect(isApiError(err, 'sudo_required')).toBe(true)
    expect(isApiError(err, 'forbidden')).toBe(false)
    expect(isApiError(new Error('x'))).toBe(false)
  })
})

describe('unwrap', () => {
  it('returns data on success and throws ApiError otherwise', async () => {
    await expect(unwrap(Promise.resolve({ data: 1, response: new Response(null, { status: 200 }) }))).resolves.toBe(1)
    await expect(
      unwrap(Promise.resolve({ error: { error: 'not_found', message: 'nope' }, response: new Response(null, { status: 404 }) })),
    ).rejects.toMatchObject({ code: 'not_found' })
    await expect(unwrapVoid(Promise.resolve({ error: undefined, response: new Response(null, { status: 500 }) }))).rejects.toMatchObject({
      status: 500,
      code: 'unknown',
    })
  })
})
```

`web/src/api/client.test.ts`:
```ts
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import { currentSession, db, findUserByName, loginAs, MOCK_PASSWORD, MOCK_TOTP_CODE } from '@/mocks/db'
import { server } from '@/mocks/node'
import { api } from './client'
import { getCsrfToken, setCsrfToken } from './csrf'
import { unwrap, unwrapVoid } from './errors'
import { configureApiHandlers } from './handlers'

describe('api client', () => {
  it('stores the CSRF token from Me responses and sends it on mutations', async () => {
    const onMe = vi.fn()
    configureApiHandlers({ onMe })
    const pre = await unwrap(api.POST('/auth/login', { body: { username: 'admin', password: MOCK_PASSWORD } }))
    expect(getCsrfToken()).toBe(pre.csrf_token)
    const full = await unwrap(api.POST('/auth/2fa/totp', { body: { code: MOCK_TOTP_CODE } }))
    expect(full.stage).toBe('full')
    expect(getCsrfToken()).toBe(full.csrf_token)
    expect(onMe).toHaveBeenCalledTimes(2)
  })

  it('does not send the CSRF header on GET', async () => {
    let seen: string | null = 'unset'
    server.use(
      http.get('*/api/servers', ({ request }) => {
        seen = request.headers.get('X-CSRF-Token')
        return HttpResponse.json([])
      }),
    )
    setCsrfToken('token-1')
    await unwrap(api.GET('/servers'))
    expect(seen).toBeNull()
  })

  it('refetches Me after an endpoint that rotates the session', async () => {
    const me = loginAs('admin')
    setCsrfToken(me.csrf_token)
    const onMe = vi.fn()
    configureApiHandlers({ onMe })
    const res = await unwrap(api.POST('/auth/sudo/totp', { body: { code: MOCK_TOTP_CODE } }))
    expect(getCsrfToken()).not.toBe(me.csrf_token)
    expect(getCsrfToken()).toBe(currentSession()?.csrf)
    expect(onMe).toHaveBeenCalledWith(expect.objectContaining({ sudo_until: res.sudo_until }))
  })

  it('recovers exactly once from a stale CSRF token', async () => {
    loginAs('admin')
    setCsrfToken('stale-token')
    await unwrapVoid(api.POST('/me/sessions/revoke-others'))
    expect(getCsrfToken()).toBe(currentSession()?.csrf)
    expect(db.sessions.filter((s) => s.userId === 1)).toHaveLength(1)
  })

  it('redirects on 401 unauthorized but not on wrong passwords or codes', async () => {
    const onUnauthorized = vi.fn()
    configureApiHandlers({ onUnauthorized })
    await expect(unwrap(api.POST('/auth/login', { body: { username: 'admin', password: 'wrong-password!' } }))).rejects.toMatchObject({
      status: 401,
      code: 'invalid_credentials',
    })
    const pre = loginAs('admin', { stage: 'preauth' })
    setCsrfToken(pre.csrf_token)
    await expect(unwrap(api.POST('/auth/2fa/totp', { body: { code: '000000' } }))).rejects.toMatchObject({ code: 'invalid_code' })
    expect(onUnauthorized).not.toHaveBeenCalled()

    db.currentSessionId = null
    await expect(unwrap(api.GET('/me/sessions'))).rejects.toMatchObject({ code: 'unauthorized' })
    expect(onUnauthorized).toHaveBeenCalledTimes(1)
    expect(getCsrfToken()).toBe('')
  })

  it('asks for sudo and retries the original request with the rotated token', async () => {
    const me = loginAs('admin')
    setCsrfToken(me.csrf_token)
    const requestSudo = vi.fn(async () => {
      await unwrap(api.POST('/auth/sudo/totp', { body: { code: MOCK_TOTP_CODE } }))
      return true
    })
    configureApiHandlers({ requestSudo })
    await unwrapVoid(api.DELETE('/me/totp'))
    expect(requestSudo).toHaveBeenCalledTimes(1)
    expect(findUserByName('admin')?.totpEnabled).toBe(false)
  })

  it('returns sudo_required when the dialog is cancelled', async () => {
    const me = loginAs('admin')
    setCsrfToken(me.csrf_token)
    configureApiHandlers({ requestSudo: async () => false })
    await expect(unwrapVoid(api.DELETE('/me/totp'))).rejects.toMatchObject({ status: 403, code: 'sudo_required' })
    expect(findUserByName('admin')?.totpEnabled).toBe(true)
  })

  it('retries a sudo_required request at most once (no dialog loop)', async () => {
    const me = loginAs('admin')
    setCsrfToken(me.csrf_token)
    let hits = 0
    server.use(
      http.post('*/api/users', () => {
        hits += 1
        return HttpResponse.json({ error: 'sudo_required', message: 'still no sudo' }, { status: 403 })
      }),
    )
    const requestSudo = vi.fn(async () => true)
    configureApiHandlers({ requestSudo })
    await expect(
      unwrap(api.POST('/users', { body: { username: 'carol', password: 'long-enough-password', role: 'user' } })),
    ).rejects.toMatchObject({ code: 'sudo_required' })
    expect(requestSudo).toHaveBeenCalledTimes(1)
    expect(hits).toBe(2)
  })

  it('reports setup_2fa_required', async () => {
    const me = loginAs('newadmin', { stage: 'setup' })
    setCsrfToken(me.csrf_token)
    const onSetupRequired = vi.fn()
    configureApiHandlers({ onSetupRequired })
    await expect(unwrap(api.GET('/me/passkeys'))).rejects.toMatchObject({ code: 'setup_2fa_required' })
    expect(onSetupRequired).toHaveBeenCalledTimes(1)
  })
})
```

`web/src/lib/error-message.test.ts`:
```ts
import { describe, expect, it } from 'vitest'
import { ApiError } from '@/api/errors'
import i18n from '@/i18n'
import { errorMessage, isWebAuthnCancelled } from './error-message'

const t = i18n.t.bind(i18n)

describe('errorMessage', () => {
  it('translates known codes', () => {
    expect(errorMessage(new ApiError({ status: 401, code: 'invalid_code', message: 'invalid code' }), t)).toBe('Der Code ist ungültig.')
  })

  it('appends the server detail for validation and conflict errors', () => {
    const err = new ApiError({ status: 409, code: 'conflict', message: 'username taken' })
    expect(errorMessage(err, t)).toBe('Konflikt mit dem aktuellen Zustand (username taken)')
  })

  it('uses Retry-After for rate limits', () => {
    const err = new ApiError({ status: 429, code: 'rate_limited', message: 'slow', retryAfterSeconds: 30 })
    expect(errorMessage(err, t)).toBe('Zu viele Versuche. Bitte in 30 s erneut versuchen.')
  })

  it('falls back to a network message for non-API errors', () => {
    expect(errorMessage(new TypeError('Failed to fetch'), t)).toBe('Server nicht erreichbar. Bitte Verbindung prüfen.')
  })

  it('recognises cancelled WebAuthn ceremonies', () => {
    const cancelled = Object.assign(new Error('The operation either timed out or was not allowed.'), { name: 'NotAllowedError' })
    expect(isWebAuthnCancelled(cancelled)).toBe(true)
    expect(errorMessage(cancelled, t)).toBe('Vorgang abgebrochen oder Zeit abgelaufen.')
  })
})
```

- [ ] **Step 2: Tests laufen lassen → FAIL**

Run: `cd web && npx vitest run src/api src/lib/error-message.test.ts`
Expected: FAIL – Module `./errors`, `./client`, `./error-message` fehlen.

- [ ] **Step 3: `web/src/api/csrf.ts` und `web/src/api/handlers.ts`**

```ts
// web/src/api/csrf.ts
/** Synchronizer token of the current session. Updated from every Me response; never persisted. */
let csrfToken = ''

export function getCsrfToken(): string {
  return csrfToken
}

export function setCsrfToken(token: string): void {
  csrfToken = token
}
```

```ts
// web/src/api/handlers.ts
import type { Me } from './types'

export interface ApiHandlers {
  onUnauthorized(): void
  onSetupRequired(): void
  requestSudo(): Promise<boolean>
  onMe(me: Me): void
}

const defaults: ApiHandlers = {
  onUnauthorized: () => undefined,
  onSetupRequired: () => undefined,
  requestSudo: () => Promise.resolve(false),
  onMe: () => undefined,
}

let current: ApiHandlers = defaults

export function configureApiHandlers(overrides: Partial<ApiHandlers>): void {
  current = { ...current, ...overrides }
}

export function resetApiHandlers(): void {
  current = defaults
}

export function apiHandlers(): ApiHandlers {
  return current
}
```

- [ ] **Step 4: `web/src/api/errors.ts`**

```ts
import type { ErrorCode } from './types'

export type ApiErrorCode = ErrorCode | 'unknown'

interface ApiErrorInit {
  status: number
  code: ApiErrorCode
  message: string
  requestId?: string
  retryAfterSeconds?: number
}

export class ApiError extends Error {
  readonly status: number
  readonly code: ApiErrorCode
  readonly requestId?: string
  readonly retryAfterSeconds?: number

  constructor(init: ApiErrorInit) {
    super(init.message)
    this.name = 'ApiError'
    this.status = init.status
    this.code = init.code
    this.requestId = init.requestId
    this.retryAfterSeconds = init.retryAfterSeconds
  }
}

const KNOWN_CODES: ReadonlySet<string> = new Set<ErrorCode>([
  'bad_request',
  'validation_failed',
  'unauthorized',
  'invalid_credentials',
  'invalid_code',
  'forbidden',
  'sudo_required',
  'setup_2fa_required',
  'csrf_invalid',
  'not_found',
  'conflict',
  'payload_too_large',
  'rate_limited',
  'jailed',
  'ip_blocked',
  'agent_unavailable',
  'internal',
])

function isErrorCode(value: unknown): value is ErrorCode {
  return typeof value === 'string' && KNOWN_CODES.has(value)
}

export function toApiError(response: Response, body: unknown): ApiError {
  const retryHeader = response.headers.get('Retry-After')
  const retryAfterSeconds = retryHeader && /^\d+$/.test(retryHeader) ? Number(retryHeader) : undefined
  if (typeof body === 'object' && body !== null) {
    const b = body as { error?: unknown; message?: unknown; request_id?: unknown }
    if (isErrorCode(b.error)) {
      return new ApiError({
        status: response.status,
        code: b.error,
        message: typeof b.message === 'string' ? b.message : b.error,
        requestId: typeof b.request_id === 'string' ? b.request_id : undefined,
        retryAfterSeconds,
      })
    }
  }
  return new ApiError({
    status: response.status,
    code: 'unknown',
    message: typeof body === 'string' && body ? body : `HTTP ${response.status}`,
    retryAfterSeconds,
  })
}

export function isApiError(err: unknown, code?: ApiErrorCode): err is ApiError {
  return err instanceof ApiError && (code === undefined || err.code === code)
}

export async function unwrap<T>(promise: Promise<{ data?: T; error?: unknown; response: Response }>): Promise<T> {
  const { data, error, response } = await promise
  if (!response.ok) throw toApiError(response, error)
  return data as T
}

export async function unwrapVoid(promise: Promise<{ error?: unknown; response: Response }>): Promise<void> {
  const { error, response } = await promise
  if (!response.ok) throw toApiError(response, error)
}
```

- [ ] **Step 5: `web/src/api/client.ts`**

```ts
import createClient, { type Middleware } from 'openapi-fetch'
import { getCsrfToken, setCsrfToken } from './csrf'
import { apiHandlers } from './handlers'
import type { paths } from './schema'
import type { Me } from './types'

/** The contract marks X-CSRF-Token as required header; the middleware sets it, so callers never pass it. */
type StripCsrfHeader<Op> = Op extends { parameters: infer P }
  ? Omit<Op, 'parameters'> & { parameters: Omit<P, 'header'> & { header?: never } }
  : Op

export type ClientPaths = {
  [Path in keyof paths]: {
    [Method in keyof paths[Path]]: Method extends 'parameters' ? paths[Path][Method] : StripCsrfHeader<paths[Path][Method]>
  }
}

export const ME_PATHS: ReadonlySet<string> = new Set([
  '/auth/login',
  '/auth/me',
  '/auth/2fa/totp',
  '/auth/2fa/recovery',
  '/auth/2fa/webauthn/finish',
  '/me',
])

/** Endpoints after which the backend rotates the session (new id + CSRF token) without returning Me. */
export const ROTATING_PATHS: ReadonlySet<string> = new Set([
  '/auth/sudo/totp',
  '/auth/sudo/webauthn/finish',
  '/me/totp/enable',
  '/me/passkeys/finish',
  '/me/password',
])

function isMe(value: unknown): value is Me {
  return (
    typeof value === 'object' &&
    value !== null &&
    typeof (value as { csrf_token?: unknown }).csrf_token === 'string' &&
    typeof (value as { stage?: unknown }).stage === 'string'
  )
}

async function readErrorCode(response: Response): Promise<string | null> {
  try {
    const body: unknown = await response.clone().json()
    const code = typeof body === 'object' && body !== null ? (body as { error?: unknown }).error : undefined
    return typeof code === 'string' ? code : null
  } catch {
    return null
  }
}

export function createApiClient(baseUrl: string = `${window.location.origin}/api`) {
  // Late-bound so that MSW (tests) can patch globalThis.fetch after this module was imported.
  const send = (request: Request): Promise<Response> => globalThis.fetch(request)
  const client = createClient<ClientPaths>({ baseUrl, credentials: 'same-origin', fetch: send })
  const retryCopies = new WeakMap<Request, Request>()

  async function captureMe(response: Response): Promise<void> {
    try {
      const body: unknown = await response.clone().json()
      if (isMe(body)) {
        setCsrfToken(body.csrf_token)
        apiHandlers().onMe(body)
      }
    } catch {
      // not JSON – nothing to capture
    }
  }

  async function refreshMe(): Promise<void> {
    const response = await send(new Request(`${baseUrl}/auth/me`, { credentials: 'same-origin', headers: { Accept: 'application/json' } }))
    if (response.ok) await captureMe(response)
  }

  async function afterSuccess(response: Response, schemaPath: string): Promise<void> {
    if (ME_PATHS.has(schemaPath)) await captureMe(response)
    if (ROTATING_PATHS.has(schemaPath)) await refreshMe()
  }

  async function retryOnce(copy: Request, schemaPath: string): Promise<Response> {
    const token = getCsrfToken()
    if (token) copy.headers.set('X-CSRF-Token', token)
    const response = await send(copy)
    if (response.ok) {
      await afterSuccess(response, schemaPath)
    } else if (response.status === 401 && (await readErrorCode(response)) === 'unauthorized') {
      setCsrfToken('')
      apiHandlers().onUnauthorized()
    }
    return response
  }

  const middleware: Middleware = {
    onRequest({ request }) {
      if (request.method !== 'GET' && request.method !== 'HEAD') {
        const token = getCsrfToken()
        if (token) request.headers.set('X-CSRF-Token', token)
      }
      retryCopies.set(request, request.clone())
      return request
    },
    async onResponse({ request, response, schemaPath }) {
      const copy = retryCopies.get(request)
      retryCopies.delete(request)
      if (response.ok) {
        await afterSuccess(response, schemaPath)
        return response
      }
      const code = await readErrorCode(response)
      if (response.status === 401 && code === 'unauthorized') {
        setCsrfToken('')
        apiHandlers().onUnauthorized()
        return response
      }
      if (response.status !== 403 || !copy) return response
      if (code === 'setup_2fa_required') {
        apiHandlers().onSetupRequired()
        return response
      }
      if (code === 'csrf_invalid') {
        await refreshMe()
        return retryOnce(copy, schemaPath)
      }
      if (code === 'sudo_required' && !schemaPath.startsWith('/auth/')) {
        const confirmed = await apiHandlers().requestSudo()
        return confirmed ? retryOnce(copy, schemaPath) : response
      }
      return response
    },
  }
  client.use(middleware)
  return client
}

export type ApiClient = ReturnType<typeof createApiClient>

export const api: ApiClient = createApiClient()
```

- [ ] **Step 6: `web/src/lib/error-message.ts`**

```ts
import type { TFunction } from 'i18next'
import { useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { isApiError } from '@/api/errors'

const DETAIL_CODES: ReadonlySet<string> = new Set(['bad_request', 'validation_failed', 'conflict'])

/** NotAllowedError/AbortError: user cancelled the browser dialog or it timed out. */
export function isWebAuthnCancelled(err: unknown): boolean {
  if (typeof err !== 'object' || err === null) return false
  const { name, code } = err as { name?: unknown; code?: unknown }
  return name === 'NotAllowedError' || name === 'AbortError' || code === 'ERROR_CEREMONY_ABORTED'
}

export function errorMessage(err: unknown, t: TFunction): string {
  if (isWebAuthnCancelled(err)) return t('webauthn.cancelled')
  if (isApiError(err)) {
    if (err.code === 'unknown') return t('errors.unknown', { status: err.status })
    if (err.code === 'rate_limited' && err.retryAfterSeconds) return t('errors.rate_limited_retry', { seconds: err.retryAfterSeconds })
    const base = t(`errors.${err.code}`)
    return DETAIL_CODES.has(err.code) && err.message && err.message !== err.code ? `${base} (${err.message})` : base
  }
  return t('errors.network')
}

export function useErrorToast(): (err: unknown) => void {
  const { t } = useTranslation()
  return useCallback((err: unknown) => {
    toast.error(errorMessage(err, t))
  }, [t])
}
```

- [ ] **Step 7: `web/src/test/setup.ts` – Handler und Token zwischen Tests zurücksetzen** (komplette Datei)

```ts
import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterAll, afterEach, beforeAll } from 'vitest'
import { setCsrfToken } from '@/api/csrf'
import { resetApiHandlers } from '@/api/handlers'
import i18n, { i18nReady } from '@/i18n'
import { resetDb } from '@/mocks/db'
import { server } from '@/mocks/node'
import { installMatchMedia, setPrefersDark, setViewport } from './media'

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= ResizeObserverStub as unknown as typeof ResizeObserver
Element.prototype.scrollIntoView ??= function scrollIntoView() {}
Element.prototype.hasPointerCapture ??= () => false
Element.prototype.releasePointerCapture ??= () => {}

installMatchMedia()
await i18nReady
await i18n.changeLanguage('de')

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
afterEach(async () => {
  cleanup()
  server.resetHandlers()
  resetDb()
  resetApiHandlers()
  setCsrfToken('')
  setViewport('desktop')
  setPrefersDark(false)
  localStorage.clear()
  await i18n.changeLanguage('de')
})
afterAll(() => server.close())
```

- [ ] **Step 8: Tests laufen lassen → PASS**

Run: `cd web && npx vitest run src/api src/lib/error-message.test.ts`
Expected: PASS (errors 5, client 9, error-message 5).

- [ ] **Step 9: Gesamte Suite + Typecheck**

Run: `cd web && npx vitest run && npm run typecheck`
Expected: grün. Insbesondere kompiliert `api.POST('/me/sessions/revoke-others')` **ohne** `params.header` – Beleg, dass `ClientPaths` den Pflicht-Header entfernt.

- [ ] **Step 10: Commit**

```bash
git add web/src/api web/src/lib/error-message.ts web/src/lib/error-message.test.ts web/src/test/setup.ts
git commit -m "feat(web): add typed API client with CSRF handling, session rotation and one-shot sudo retry" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: WebAuthn-Helfer und Sudo-Store

**Files:**
- Create: `web/src/features/webauthn/webauthn.ts`, `web/src/features/sudo/sudo-store.ts`
- Modify: `web/src/test/setup.ts`
- Test: `web/src/features/webauthn/webauthn.test.ts`, `web/src/features/sudo/sudo-store.test.ts`

**Interfaces:**
- Consumes: `api`, `unwrap` (Task 5); `@simplewebauthn/browser` (`browserSupportsWebAuthn`, `startAuthentication`, `startRegistration`)
- Produces:
```ts
// features/webauthn/webauthn.ts
export function isWebAuthnSupported(): boolean
export function extractPublicKey<T>(options: WebAuthnOptions): T
export function loginWithPasskey(): Promise<Me>
export function sudoWithPasskey(): Promise<number>          // sudo_until
export function registerPasskey(name: string): Promise<PasskeyCreated>
// features/sudo/sudo-store.ts
export function requestSudo(): Promise<boolean>              // teilt sich einen offenen Dialog
export function settleSudo(confirmed: boolean): void
export function isSudoOpen(): boolean
export function useSudoOpen(): boolean
export function resetSudoStore(): void
```

- [ ] **Step 1: Failing Tests**

`web/src/features/webauthn/webauthn.test.ts`:
```ts
import { browserSupportsWebAuthn, startAuthentication, startRegistration } from '@simplewebauthn/browser'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { setCsrfToken } from '@/api/csrf'
import { loginAs, WEBAUTHN_CHALLENGE } from '@/mocks/db'
import { extractPublicKey, isWebAuthnSupported, loginWithPasskey, registerPasskey, sudoWithPasskey } from './webauthn'

vi.mock('@simplewebauthn/browser', () => ({
  browserSupportsWebAuthn: vi.fn(() => true),
  startAuthentication: vi.fn(),
  startRegistration: vi.fn(),
}))

const assertion = { id: 'cred-1', rawId: 'cred-1', type: 'public-key', response: {}, clientExtensionResults: {} }

beforeEach(() => {
  vi.mocked(browserSupportsWebAuthn).mockReturnValue(true)
  vi.mocked(startAuthentication).mockResolvedValue(assertion as unknown as Awaited<ReturnType<typeof startAuthentication>>)
  vi.mocked(startRegistration).mockResolvedValue(assertion as unknown as Awaited<ReturnType<typeof startRegistration>>)
})

describe('webauthn helpers', () => {
  it('feature-detects support and never throws', () => {
    expect(isWebAuthnSupported()).toBe(true)
    vi.mocked(browserSupportsWebAuthn).mockImplementation(() => {
      throw new Error('no window.PublicKeyCredential')
    })
    expect(isWebAuthnSupported()).toBe(false)
  })

  it('unwraps the go-webauthn {publicKey} envelope', () => {
    expect(extractPublicKey<{ challenge: string }>({ publicKey: { challenge: 'abc' } })).toEqual({ challenge: 'abc' })
    expect(() => extractPublicKey({ foo: 1 })).toThrow('publicKey')
  })

  it('logs in with a passkey and passes optionsJSON = publicKey', async () => {
    setCsrfToken(loginAs('admin', { stage: 'preauth' }).csrf_token)
    const me = await loginWithPasskey()
    expect(me.stage).toBe('full')
    expect(vi.mocked(startAuthentication).mock.calls[0][0].optionsJSON).toMatchObject({ challenge: WEBAUTHN_CHALLENGE })
  })

  it('confirms sudo with a passkey', async () => {
    setCsrfToken(loginAs('admin').csrf_token)
    expect(await sudoWithPasskey()).toBeGreaterThan(Date.now())
  })

  it('registers a named passkey and returns first-time recovery codes', async () => {
    setCsrfToken(loginAs('newadmin', { stage: 'setup' }).csrf_token)
    const created = await registerPasskey('YubiKey 5C')
    expect(created.passkey.name).toBe('YubiKey 5C')
    expect(created.recovery_codes).toHaveLength(10)
    expect(vi.mocked(startRegistration).mock.calls[0][0].optionsJSON).toMatchObject({ rp: { id: 'localhost' } })
  })
})
```

`web/src/features/sudo/sudo-store.test.ts`:
```ts
import { describe, expect, it } from 'vitest'
import { isSudoOpen, requestSudo, settleSudo } from './sudo-store'

describe('sudo store', () => {
  it('opens once and shares the pending promise between concurrent requests', async () => {
    const a = requestSudo()
    const b = requestSudo()
    expect(a).toBe(b)
    expect(isSudoOpen()).toBe(true)
    settleSudo(true)
    await expect(a).resolves.toBe(true)
    expect(isSudoOpen()).toBe(false)
  })

  it('resolves false on cancel and allows a new request afterwards', async () => {
    const first = requestSudo()
    settleSudo(false)
    await expect(first).resolves.toBe(false)
    const second = requestSudo()
    expect(second).not.toBe(first)
    settleSudo(true)
    await expect(second).resolves.toBe(true)
  })

  it('ignores settle calls without a pending request', () => {
    expect(() => settleSudo(true)).not.toThrow()
    expect(isSudoOpen()).toBe(false)
  })
})
```

- [ ] **Step 2: Tests laufen lassen → FAIL**

Run: `cd web && npx vitest run src/features/webauthn src/features/sudo`
Expected: FAIL – Module fehlen.

- [ ] **Step 3: `web/src/features/webauthn/webauthn.ts`**

```ts
import { browserSupportsWebAuthn, startAuthentication, startRegistration } from '@simplewebauthn/browser'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { Me, PasskeyCreated, WebAuthnOptions } from '@/api/types'

type RegistrationOptionsJSON = Parameters<typeof startRegistration>[0]['optionsJSON']
type AuthenticationOptionsJSON = Parameters<typeof startAuthentication>[0]['optionsJSON']
type CredentialJSON = Record<string, unknown>

export function isWebAuthnSupported(): boolean {
  try {
    return browserSupportsWebAuthn()
  } catch {
    return false
  }
}

/** go-webauthn answers with CredentialCreationOptions/CredentialRequestOptions: { publicKey: {...} }. */
export function extractPublicKey<T>(options: WebAuthnOptions): T {
  const publicKey = (options as { publicKey?: unknown }).publicKey
  if (typeof publicKey !== 'object' || publicKey === null) throw new Error('WebAuthn options without publicKey')
  return publicKey as T
}

function asCredential(value: object): CredentialJSON {
  return value as unknown as CredentialJSON
}

export async function loginWithPasskey(): Promise<Me> {
  const options = await unwrap(api.POST('/auth/2fa/webauthn/begin'))
  const credential = await startAuthentication({ optionsJSON: extractPublicKey<AuthenticationOptionsJSON>(options) })
  return unwrap(api.POST('/auth/2fa/webauthn/finish', { body: { credential: asCredential(credential) } }))
}

export async function sudoWithPasskey(): Promise<number> {
  const options = await unwrap(api.POST('/auth/sudo/webauthn/begin'))
  const credential = await startAuthentication({ optionsJSON: extractPublicKey<AuthenticationOptionsJSON>(options) })
  const result = await unwrap(api.POST('/auth/sudo/webauthn/finish', { body: { credential: asCredential(credential) } }))
  return result.sudo_until
}

export async function registerPasskey(name: string): Promise<PasskeyCreated> {
  const options = await unwrap(api.POST('/me/passkeys/begin'))
  const credential = await startRegistration({ optionsJSON: extractPublicKey<RegistrationOptionsJSON>(options) })
  return unwrap(api.POST('/me/passkeys/finish', { body: { name, credential: asCredential(credential) } }))
}
```

- [ ] **Step 4: `web/src/features/sudo/sudo-store.ts`**

```ts
import { useSyncExternalStore } from 'react'

let pending: Promise<boolean> | null = null
let resolvePending: ((confirmed: boolean) => void) | null = null
const listeners = new Set<() => void>()

function emit(): void {
  listeners.forEach((l) => l())
}

/** Opens the global sudo dialog. Concurrent callers share one dialog and one answer. */
export function requestSudo(): Promise<boolean> {
  if (pending) return pending
  pending = new Promise<boolean>((resolve) => {
    resolvePending = resolve
  })
  emit()
  return pending
}

export function settleSudo(confirmed: boolean): void {
  const resolve = resolvePending
  if (!resolve) return
  pending = null
  resolvePending = null
  emit()
  resolve(confirmed)
}

export function isSudoOpen(): boolean {
  return pending !== null
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export function useSudoOpen(): boolean {
  return useSyncExternalStore(subscribe, isSudoOpen, () => false)
}

export function resetSudoStore(): void {
  settleSudo(false)
}
```

- [ ] **Step 5: `web/src/test/setup.ts` – Sudo-Store zurücksetzen**

In `web/src/test/setup.ts` den Import ergänzen und im `afterEach` nach `resetApiHandlers()` aufrufen. Komplette Datei:

```ts
import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterAll, afterEach, beforeAll } from 'vitest'
import { setCsrfToken } from '@/api/csrf'
import { resetApiHandlers } from '@/api/handlers'
import { resetSudoStore } from '@/features/sudo/sudo-store'
import i18n, { i18nReady } from '@/i18n'
import { resetDb } from '@/mocks/db'
import { server } from '@/mocks/node'
import { installMatchMedia, setPrefersDark, setViewport } from './media'

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= ResizeObserverStub as unknown as typeof ResizeObserver
Element.prototype.scrollIntoView ??= function scrollIntoView() {}
Element.prototype.hasPointerCapture ??= () => false
Element.prototype.releasePointerCapture ??= () => {}

installMatchMedia()
await i18nReady
await i18n.changeLanguage('de')

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
afterEach(async () => {
  cleanup()
  server.resetHandlers()
  resetDb()
  resetApiHandlers()
  resetSudoStore()
  setCsrfToken('')
  setViewport('desktop')
  setPrefersDark(false)
  localStorage.clear()
  await i18n.changeLanguage('de')
})
afterAll(() => server.close())
```

- [ ] **Step 6: Tests laufen lassen → PASS**

Run: `cd web && npx vitest run src/features/webauthn src/features/sudo`
Expected: PASS (8 Tests).

- [ ] **Step 7: Gesamte Suite + Typecheck, Commit**

Run: `cd web && npx vitest run && npm run typecheck`
Expected: grün.

```bash
git add web/src/features/webauthn web/src/features/sudo web/src/test/setup.ts
git commit -m "feat(web): add WebAuthn helpers and shared sudo request store" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Auth-Zustand – Me-Cache, Guards, API-Verdrahtung, Test-Renderer

**Files:**
- Create: `web/src/features/auth/me.ts`, `web/src/features/auth/guards.ts`, `web/src/app/wire-api.ts`, `web/src/test/render.tsx`
- Test: `web/src/features/auth/me.test.ts`, `web/src/features/auth/guards.test.ts`, `web/src/app/wire-api.test.ts`

**Interfaces:**
- Consumes: `api`, `unwrap`, `isApiError`, `setCsrfToken`, `configureApiHandlers`, `apiHandlers` (Task 5); `requestSudo`, `settleSudo`, `isSudoOpen` (Task 6); `setLocale` (Task 1); `AppToaster` (Task 2)
- Produces:
```ts
// features/auth/me.ts
export const meKey: readonly ['me']
export function fetchMe(): Promise<Me | null>
export const meQueryOptions                         // queryOptions({ queryKey: meKey, queryFn: fetchMe, staleTime: 60_000 })
export function useMe(): Me | null                  // liest nur den Cache (enabled: false); Guards laden
export function applyMe(queryClient: QueryClient, me: Me): void   // Token + Cache + Sprache
export function loadMe(queryClient: QueryClient): Promise<Me | null>
export function refreshMe(queryClient: QueryClient): Promise<Me | null>
export function clearSession(queryClient: QueryClient): void
// features/auth/guards.ts
export type AuthPath = '/' | '/login' | '/login/2fa' | '/setup-2fa'
export function pathForStage(stage: Stage): AuthPath
export function appGuard(me: Me | null): AuthPath | null
export function adminGuard(me: Me | null): AuthPath | null
export function loginGuard(me: Me | null): AuthPath | null
export function secondFactorGuard(me: Me | null): AuthPath | null
export function setupGuard(me: Me | null): AuthPath | null
// app/wire-api.ts
export interface AppNavigator { push(path: string): void; currentPath(): string }
export function routerNavigator(router: AnyRouter): AppNavigator
export function wireApi(queryClient: QueryClient, nav: AppNavigator): void
// test/render.tsx
export function createTestQueryClient(): QueryClient
export function renderWithProviders(ui: ReactNode, opts?: { path?: string; me?: Me | null; queryClient?: QueryClient }): Promise<RenderResult & { router: TestRouter; queryClient: QueryClient }>
```

- [ ] **Step 1: Failing Tests**

`web/src/features/auth/guards.test.ts`:
```ts
import { describe, expect, it } from 'vitest'
import type { Me } from '@/api/types'
import { adminGuard, appGuard, loginGuard, pathForStage, secondFactorGuard, setupGuard } from './guards'

function me(stage: Me['stage'], role: Me['role'] = 'admin'): Me {
  return {
    id: 1,
    username: 'x',
    role,
    stage,
    locale: 'de',
    totp_enabled: true,
    passkey_count: 0,
    recovery_codes_remaining: 10,
    second_factors: ['totp'],
    sudo_until: 0,
    csrf_token: 't',
  }
}

describe('guards', () => {
  it('maps stages to their page', () => {
    expect(pathForStage('preauth')).toBe('/login/2fa')
    expect(pathForStage('setup')).toBe('/setup-2fa')
    expect(pathForStage('full')).toBe('/')
  })

  it('protects the app area', () => {
    expect(appGuard(null)).toBe('/login')
    expect(appGuard(me('preauth'))).toBe('/login/2fa')
    expect(appGuard(me('setup'))).toBe('/setup-2fa')
    expect(appGuard(me('full'))).toBeNull()
  })

  it('keeps users out of admin pages', () => {
    expect(adminGuard(me('full', 'user'))).toBe('/')
    expect(adminGuard(me('full', 'admin'))).toBeNull()
    expect(adminGuard(me('setup', 'admin'))).toBe('/setup-2fa')
  })

  it('routes the auth pages by stage', () => {
    expect(loginGuard(null)).toBeNull()
    expect(loginGuard(me('preauth'))).toBeNull()
    expect(loginGuard(me('full'))).toBe('/')
    expect(secondFactorGuard(null)).toBe('/login')
    expect(secondFactorGuard(me('preauth'))).toBeNull()
    expect(secondFactorGuard(me('full'))).toBe('/')
    expect(setupGuard(me('setup'))).toBeNull()
    expect(setupGuard(me('preauth'))).toBe('/login/2fa')
    expect(setupGuard(me('full'))).toBe('/')
  })
})
```

`web/src/features/auth/me.test.ts`:
```ts
import { QueryClient } from '@tanstack/react-query'
import { describe, expect, it } from 'vitest'
import { getCsrfToken } from '@/api/csrf'
import i18n from '@/i18n'
import { loginAs } from '@/mocks/db'
import { applyMe, clearSession, fetchMe, loadMe, meKey, refreshMe } from './me'

describe('me cache', () => {
  it('applyMe stores the token, caches Me and switches the language', () => {
    const qc = new QueryClient()
    const me = loginAs('bob')
    applyMe(qc, me)
    expect(getCsrfToken()).toBe(me.csrf_token)
    expect(qc.getQueryData(meKey)).toEqual(me)
    expect(i18n.language).toBe('en')
    expect(document.documentElement.lang).toBe('en')
  })

  it('fetchMe resolves null without a session', async () => {
    expect(await fetchMe()).toBeNull()
  })

  it('loadMe uses the cache while refreshMe bypasses it', async () => {
    const qc = new QueryClient()
    loginAs('admin')
    expect((await loadMe(qc))?.username).toBe('admin')
    loginAs('alice')
    expect((await loadMe(qc))?.username).toBe('admin')
    expect((await refreshMe(qc))?.username).toBe('alice')
  })

  it('clearSession forgets Me, the token and all other cached data', () => {
    const qc = new QueryClient()
    applyMe(qc, loginAs('admin'))
    qc.setQueryData(['users'], [])
    clearSession(qc)
    expect(qc.getQueryData(meKey)).toBeNull()
    expect(qc.getQueryData(['users'])).toBeUndefined()
    expect(getCsrfToken()).toBe('')
  })
})
```

`web/src/app/wire-api.test.ts`:
```ts
import { QueryClient } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vitest'
import { apiHandlers } from '@/api/handlers'
import { meKey } from '@/features/auth/me'
import { isSudoOpen, settleSudo } from '@/features/sudo/sudo-store'
import { loginAs } from '@/mocks/db'
import { wireApi } from './wire-api'

function fakeNavigator(path: string) {
  return { push: vi.fn(), currentPath: () => path }
}

describe('wireApi', () => {
  it('sends unauthenticated users to /login and drops cached data', () => {
    const qc = new QueryClient()
    qc.setQueryData(meKey, loginAs('admin'))
    qc.setQueryData(['users'], [])
    const nav = fakeNavigator('/users')
    wireApi(qc, nav)
    apiHandlers().onUnauthorized()
    expect(qc.getQueryData(meKey)).toBeNull()
    expect(qc.getQueryData(['users'])).toBeUndefined()
    expect(nav.push).toHaveBeenCalledWith('/login')
  })

  it('does not navigate again when already on a login page', () => {
    const nav = fakeNavigator('/login/2fa')
    wireApi(new QueryClient(), nav)
    apiHandlers().onUnauthorized()
    expect(nav.push).not.toHaveBeenCalled()
  })

  it('sends setup-stage users to /setup-2fa', () => {
    const nav = fakeNavigator('/users')
    wireApi(new QueryClient(), nav)
    apiHandlers().onSetupRequired()
    expect(nav.push).toHaveBeenCalledWith('/setup-2fa')
  })

  it('applies every Me the client sees', () => {
    const qc = new QueryClient()
    wireApi(qc, fakeNavigator('/'))
    const me = loginAs('admin')
    apiHandlers().onMe(me)
    expect(qc.getQueryData(meKey)).toEqual(me)
  })

  it('routes sudo requests to the sudo store', async () => {
    wireApi(new QueryClient(), fakeNavigator('/'))
    const answer = apiHandlers().requestSudo()
    expect(isSudoOpen()).toBe(true)
    settleSudo(true)
    await expect(answer).resolves.toBe(true)
  })
})
```

- [ ] **Step 2: Tests laufen lassen → FAIL**

Run: `cd web && npx vitest run src/features/auth src/app`
Expected: FAIL – Module `./guards`, `./me`, `./wire-api` fehlen.

- [ ] **Step 3: `web/src/features/auth/me.ts`**

```ts
import { queryOptions, useQuery, type QueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import { setCsrfToken } from '@/api/csrf'
import { isApiError, unwrap } from '@/api/errors'
import type { Me } from '@/api/types'
import { setLocale } from '@/i18n'

export const meKey = ['me'] as const

export async function fetchMe(): Promise<Me | null> {
  try {
    return await unwrap(api.GET('/auth/me'))
  } catch (err) {
    if (isApiError(err) && err.status === 401) return null
    throw err
  }
}

export const meQueryOptions = queryOptions({ queryKey: meKey, queryFn: fetchMe, staleTime: 60_000 })

/** Reads the cached Me only. Route guards load it; the API middleware keeps it fresh after every stage change. */
export function useMe(): Me | null {
  return useQuery({ ...meQueryOptions, enabled: false }).data ?? null
}

export function applyMe(queryClient: QueryClient, me: Me): void {
  setCsrfToken(me.csrf_token)
  queryClient.setQueryData(meKey, me)
  setLocale(me.locale)
}

export function loadMe(queryClient: QueryClient): Promise<Me | null> {
  return queryClient.ensureQueryData(meQueryOptions)
}

export function refreshMe(queryClient: QueryClient): Promise<Me | null> {
  return queryClient.fetchQuery({ ...meQueryOptions, staleTime: 0 })
}

export function clearSession(queryClient: QueryClient): void {
  setCsrfToken('')
  queryClient.setQueryData(meKey, null)
  queryClient.removeQueries({ predicate: (q) => q.queryKey[0] !== meKey[0] })
}
```

- [ ] **Step 4: `web/src/features/auth/guards.ts`**

```ts
import type { Me, Stage } from '@/api/types'

export type AuthPath = '/' | '/login' | '/login/2fa' | '/setup-2fa'

export function pathForStage(stage: Stage): AuthPath {
  if (stage === 'preauth') return '/login/2fa'
  if (stage === 'setup') return '/setup-2fa'
  return '/'
}

export function appGuard(me: Me | null): AuthPath | null {
  if (!me) return '/login'
  return me.stage === 'full' ? null : pathForStage(me.stage)
}

export function adminGuard(me: Me | null): AuthPath | null {
  const base = appGuard(me)
  if (base || !me) return base ?? '/login'
  return me.role === 'admin' ? null : '/'
}

/** The login page stays reachable in preauth/setup so a user can switch accounts. */
export function loginGuard(me: Me | null): AuthPath | null {
  return me?.stage === 'full' ? '/' : null
}

export function secondFactorGuard(me: Me | null): AuthPath | null {
  if (!me) return '/login'
  return me.stage === 'preauth' ? null : pathForStage(me.stage)
}

export function setupGuard(me: Me | null): AuthPath | null {
  if (!me) return '/login'
  return me.stage === 'setup' ? null : pathForStage(me.stage)
}
```

- [ ] **Step 5: `web/src/app/wire-api.ts`**

```ts
import type { QueryClient } from '@tanstack/react-query'
import type { AnyRouter } from '@tanstack/react-router'
import { configureApiHandlers } from '@/api/handlers'
import { applyMe, clearSession } from '@/features/auth/me'
import { requestSudo } from '@/features/sudo/sudo-store'

export interface AppNavigator {
  push(path: string): void
  currentPath(): string
}

export function routerNavigator(router: AnyRouter): AppNavigator {
  return {
    push: (path) => router.history.push(path),
    currentPath: () => router.history.location.pathname,
  }
}

/** Connects the framework-free API middleware with React Query, the router and the sudo dialog. */
export function wireApi(queryClient: QueryClient, nav: AppNavigator): void {
  configureApiHandlers({
    onMe: (me) => applyMe(queryClient, me),
    onUnauthorized: () => {
      clearSession(queryClient)
      if (!nav.currentPath().startsWith('/login')) nav.push('/login')
    },
    onSetupRequired: () => {
      if (nav.currentPath() !== '/setup-2fa') nav.push('/setup-2fa')
    },
    requestSudo,
  })
}
```

- [ ] **Step 6: `web/src/test/render.tsx`**

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, createRootRoute, createRoute, createRouter, Outlet, RouterProvider } from '@tanstack/react-router'
import { render, type RenderResult } from '@testing-library/react'
import type { ReactNode } from 'react'
import type { Me } from '@/api/types'
import { routerNavigator, wireApi } from '@/app/wire-api'
import { AppToaster } from '@/components/app-toaster'
import { applyMe } from '@/features/auth/me'

export function createTestQueryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity }, mutations: { retry: false } } })
}

function createTestRouter(ui: ReactNode, path: string) {
  const rootRoute = createRootRoute({
    component: () => (
      <>
        <Outlet />
        <AppToaster />
      </>
    ),
  })
  const indexRoute = createRoute({ getParentRoute: () => rootRoute, path: '/', component: () => <>{ui}</> })
  const splatRoute = createRoute({ getParentRoute: () => rootRoute, path: '$', component: () => <>{ui}</> })
  return createRouter({
    routeTree: rootRoute.addChildren([indexRoute, splatRoute]),
    history: createMemoryHistory({ initialEntries: [path] }),
  })
}

export type TestRouter = ReturnType<typeof createTestRouter>

export interface RenderOptions {
  path?: string
  me?: Me | null
  queryClient?: QueryClient
}

/** Renders a page inside a memory router that matches every path; assert navigation via router.state.location. */
export async function renderWithProviders(
  ui: ReactNode,
  { path = '/', me = null, queryClient = createTestQueryClient() }: RenderOptions = {},
): Promise<RenderResult & { router: TestRouter; queryClient: QueryClient }> {
  if (me) applyMe(queryClient, me)
  const router = createTestRouter(ui, path)
  wireApi(queryClient, routerNavigator(router))
  await router.load()
  const result = render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return { ...result, router, queryClient }
}
```

- [ ] **Step 7: Tests laufen lassen → PASS**

Run: `cd web && npx vitest run src/features/auth src/app`
Expected: PASS (guards 4, me 4, wire-api 5).

- [ ] **Step 8: Gesamte Suite + Typecheck, Commit**

Run: `cd web && npx vitest run && npm run typecheck`
Expected: grün.

```bash
git add web/src/features/auth web/src/app web/src/test/render.tsx
git commit -m "feat(web): add Me cache, stage guards and API wiring for router and sudo" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Globaler Sudo-Dialog und App-Bootstrap

**Files:**
- Create: `web/src/components/code-form.tsx`, `web/src/features/sudo/sudo-dialog.tsx`, `web/src/app/query-client.ts`, `web/src/router.ts`
- Modify: `web/src/routes/__root.tsx`, `web/src/main.tsx`, `web/index.html`, `web/src/test/render.tsx`
- Test: `web/src/features/sudo/sudo-dialog.test.tsx`

**Interfaces:**
- Consumes: `useMe`, `applyMe` (Task 7); `settleSudo`, `useSudoOpen`, `requestSudo` (Task 6); `sudoWithPasskey`, `isWebAuthnSupported` (Task 6); `api`, `unwrap`, `errorMessage` (Task 5); `applyTheme`, `watchSystemTheme`, `AppToaster`, `NotFoundPage` (Task 2); `i18nReady` (Task 1); `wireApi`, `routerNavigator` (Task 7)
- Produces:
```ts
export interface CodeFormProps { label: string; submitLabel: string; numeric?: boolean; pending?: boolean; error?: string | null; onSubmit(code: string): void }
export function CodeForm(props: CodeFormProps): JSX.Element
export function SudoDialog(): JSX.Element
export function createQueryClient(): QueryClient
export interface RouterContext { queryClient: QueryClient }
export function createAppRouter(queryClient: QueryClient): AppRouter   // registriert in @tanstack/react-router Register
```

- [ ] **Step 1: `web/src/test/render.tsx` – SudoDialog im Test-Root** (komplette Datei)

```tsx
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, createRootRoute, createRoute, createRouter, Outlet, RouterProvider } from '@tanstack/react-router'
import { render, type RenderResult } from '@testing-library/react'
import type { ReactNode } from 'react'
import type { Me } from '@/api/types'
import { routerNavigator, wireApi } from '@/app/wire-api'
import { AppToaster } from '@/components/app-toaster'
import { applyMe } from '@/features/auth/me'
import { SudoDialog } from '@/features/sudo/sudo-dialog'

export function createTestQueryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity }, mutations: { retry: false } } })
}

function createTestRouter(ui: ReactNode, path: string) {
  const rootRoute = createRootRoute({
    component: () => (
      <>
        <Outlet />
        <SudoDialog />
        <AppToaster />
      </>
    ),
  })
  const indexRoute = createRoute({ getParentRoute: () => rootRoute, path: '/', component: () => <>{ui}</> })
  const splatRoute = createRoute({ getParentRoute: () => rootRoute, path: '$', component: () => <>{ui}</> })
  return createRouter({
    routeTree: rootRoute.addChildren([indexRoute, splatRoute]),
    history: createMemoryHistory({ initialEntries: [path] }),
  })
}

export type TestRouter = ReturnType<typeof createTestRouter>

export interface RenderOptions {
  path?: string
  me?: Me | null
  queryClient?: QueryClient
}

/** Renders a page inside a memory router that matches every path; assert navigation via router.state.location. */
export async function renderWithProviders(
  ui: ReactNode,
  { path = '/', me = null, queryClient = createTestQueryClient() }: RenderOptions = {},
): Promise<RenderResult & { router: TestRouter; queryClient: QueryClient }> {
  if (me) applyMe(queryClient, me)
  const router = createTestRouter(ui, path)
  wireApi(queryClient, routerNavigator(router))
  await router.load()
  const result = render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return { ...result, router, queryClient }
}
```

- [ ] **Step 2: Failing Test `web/src/features/sudo/sudo-dialog.test.tsx`**

```tsx
import { browserSupportsWebAuthn, startAuthentication } from '@simplewebauthn/browser'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { loginAs, MOCK_TOTP_CODE } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { requestSudo } from './sudo-store'

vi.mock('@simplewebauthn/browser', () => ({
  browserSupportsWebAuthn: vi.fn(() => true),
  startAuthentication: vi.fn(),
  startRegistration: vi.fn(),
}))

beforeEach(() => {
  vi.mocked(browserSupportsWebAuthn).mockReturnValue(true)
})

function open(): Promise<boolean> {
  let answer: Promise<boolean> = Promise.resolve(false)
  act(() => {
    answer = requestSudo()
  })
  return answer
}

describe('SudoDialog', () => {
  it('confirms with a TOTP code, shows wrong codes inline and resolves the request', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<p>page</p>, { me: loginAs('admin') })
    const answer = open()
    const dialog = await screen.findByRole('dialog', { name: 'Aktion bestätigen' })
    const input = within(dialog).getByLabelText('Code aus der Authenticator-App')
    await user.type(input, '000000')
    await user.click(within(dialog).getByRole('button', { name: 'Bestätigen' }))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('Der Code ist ungültig.')
    await user.clear(input)
    await user.type(input, MOCK_TOTP_CODE)
    await user.click(within(dialog).getByRole('button', { name: 'Bestätigen' }))
    await expect(answer).resolves.toBe(true)
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('resolves false when cancelled', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<p>page</p>, { me: loginAs('admin') })
    const answer = open()
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: 'Abbrechen' }))
    await expect(answer).resolves.toBe(false)
  })

  it('offers passkeys only when the browser supports WebAuthn', async () => {
    vi.mocked(browserSupportsWebAuthn).mockReturnValue(false)
    await renderWithProviders(<p>page</p>, { me: loginAs('admin') })
    open()
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).queryByRole('tab', { name: 'Passkey' })).not.toBeInTheDocument()
    expect(within(dialog).getByLabelText('Code aus der Authenticator-App')).toBeInTheDocument()
  })

  it('confirms with a passkey', async () => {
    vi.mocked(startAuthentication).mockResolvedValue({
      id: 'c',
      rawId: 'c',
      type: 'public-key',
      response: {},
      clientExtensionResults: {},
    } as unknown as Awaited<ReturnType<typeof startAuthentication>>)
    const user = userEvent.setup()
    await renderWithProviders(<p>page</p>, { me: loginAs('admin') })
    const answer = open()
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('tab', { name: 'Passkey' }))
    await user.click(within(dialog).getByRole('button', { name: 'Mit Passkey bestätigen' }))
    await expect(answer).resolves.toBe(true)
  })

  it('shows a single dialog for concurrent sudo requests', async () => {
    await renderWithProviders(<p>page</p>, { me: loginAs('admin') })
    const a = open()
    const b = open()
    expect(a).toBe(b)
    expect(await screen.findAllByRole('dialog')).toHaveLength(1)
  })
})
```

- [ ] **Step 3: Test laufen lassen → FAIL**

Run: `cd web && npx vitest run src/features/sudo/sudo-dialog.test.tsx`
Expected: FAIL – `Failed to resolve import "@/features/sudo/sudo-dialog"`.

- [ ] **Step 4: `web/src/components/code-form.tsx`**

```tsx
import { useId, useState } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

export interface CodeFormProps {
  label: string
  submitLabel: string
  numeric?: boolean
  pending?: boolean
  error?: string | null
  onSubmit(code: string): void
}

export function CodeForm({ label, submitLabel, numeric = false, pending = false, error = null, onSubmit }: CodeFormProps) {
  const id = useId()
  const errorId = `${id}-error`
  const [code, setCode] = useState('')
  const trimmed = code.trim()
  return (
    <form
      className="space-y-4"
      noValidate
      onSubmit={(e) => {
        e.preventDefault()
        if (trimmed) onSubmit(trimmed)
      }}
    >
      <div className="space-y-2">
        <Label htmlFor={id}>{label}</Label>
        <Input
          id={id}
          value={code}
          onChange={(e) => setCode(e.target.value)}
          inputMode={numeric ? 'numeric' : 'text'}
          autoComplete={numeric ? 'one-time-code' : 'off'}
          autoCapitalize="none"
          spellCheck={false}
          maxLength={32}
          autoFocus
          aria-invalid={error ? true : undefined}
          aria-describedby={error ? errorId : undefined}
        />
      </div>
      {error ? (
        <p id={errorId} role="alert" className="text-destructive text-sm">
          {error}
        </p>
      ) : null}
      <Button type="submit" className="w-full" disabled={pending || !trimmed}>
        {submitLabel}
      </Button>
    </form>
  )
}
```

- [ ] **Step 5: `web/src/features/sudo/sudo-dialog.tsx`**

```tsx
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { Me } from '@/api/types'
import { CodeForm } from '@/components/code-form'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useMe } from '@/features/auth/me'
import { isWebAuthnSupported, sudoWithPasskey } from '@/features/webauthn/webauthn'
import { errorMessage } from '@/lib/error-message'
import { settleSudo, useSudoOpen } from './sudo-store'

/** Mounted fresh for every sudo request, so no state survives between requests. */
function SudoContent({ me }: { me: Me }) {
  const { t } = useTranslation()
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)
  const totp = me.totp_enabled
  const passkey = me.passkey_count > 0 && isWebAuthnSupported()

  async function run(action: () => Promise<unknown>) {
    setPending(true)
    setError(null)
    try {
      await action()
      settleSudo(true)
    } catch (err) {
      setError(errorMessage(err, t))
      setPending(false)
    }
  }

  const totpForm = (
    <CodeForm
      label={t('sudo.code_label')}
      submitLabel={t('sudo.confirm')}
      numeric
      pending={pending}
      error={error}
      onSubmit={(code) => void run(() => unwrap(api.POST('/auth/sudo/totp', { body: { code } })))}
    />
  )
  const passkeyPanel = (
    <div className="space-y-3">
      <p className="text-muted-foreground text-sm">{t('sudo.passkey_hint')}</p>
      {error ? (
        <p role="alert" className="text-destructive text-sm">
          {error}
        </p>
      ) : null}
      <Button type="button" className="w-full" disabled={pending} onClick={() => void run(sudoWithPasskey)}>
        {t('sudo.passkey_button')}
      </Button>
    </div>
  )

  let body = (
    <p role="alert" className="text-sm">
      {t('sudo.no_method')}
    </p>
  )
  if (totp && passkey) {
    body = (
      <Tabs defaultValue="totp">
        <TabsList className="grid w-full grid-cols-2">
          <TabsTrigger value="totp">{t('sudo.tab_totp')}</TabsTrigger>
          <TabsTrigger value="passkey">{t('sudo.tab_passkey')}</TabsTrigger>
        </TabsList>
        <TabsContent value="totp" className="pt-3">
          {totpForm}
        </TabsContent>
        <TabsContent value="passkey" className="pt-3">
          {passkeyPanel}
        </TabsContent>
      </Tabs>
    )
  } else if (totp) {
    body = totpForm
  } else if (passkey) {
    body = passkeyPanel
  }

  return (
    <div className="space-y-4">
      {body}
      <Button type="button" variant="outline" className="w-full" onClick={() => settleSudo(false)}>
        {t('common.cancel')}
      </Button>
    </div>
  )
}

/** Global confirmation dialog; opened by the API middleware on 403 sudo_required. */
export function SudoDialog() {
  const { t } = useTranslation()
  const open = useSudoOpen()
  const me = useMe()
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) settleSudo(false)
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('sudo.title')}</DialogTitle>
          <DialogDescription>{t('sudo.description')}</DialogDescription>
        </DialogHeader>
        {open && me ? <SudoContent me={me} /> : null}
      </DialogContent>
    </Dialog>
  )
}
```

- [ ] **Step 6: Test laufen lassen → PASS**

Run: `cd web && npx vitest run src/features/sudo`
Expected: PASS (sudo-store 3, sudo-dialog 5).

- [ ] **Step 7: Bootstrap – `web/src/app/query-client.ts`, `web/src/router.ts`**

```ts
// web/src/app/query-client.ts
import { QueryClient } from '@tanstack/react-query'
import { ApiError } from '@/api/errors'

export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        // 4xx never get better by retrying; 5xx/network get two more attempts.
        retry: (failureCount, error) => !(error instanceof ApiError && error.status < 500) && failureCount < 2,
        refetchOnWindowFocus: false,
        staleTime: 15_000,
      },
      mutations: { retry: false },
    },
  })
}
```

```ts
// web/src/router.ts
import type { QueryClient } from '@tanstack/react-query'
import { createRouter } from '@tanstack/react-router'
import { routeTree } from './routeTree.gen'

export interface RouterContext {
  queryClient: QueryClient
}

export function createAppRouter(queryClient: QueryClient) {
  return createRouter({ routeTree, context: { queryClient }, defaultPreload: 'intent', defaultPreloadStaleTime: 0, scrollRestoration: true })
}

declare module '@tanstack/react-router' {
  interface Register {
    router: ReturnType<typeof createAppRouter>
  }
}
```

- [ ] **Step 8: `web/src/routes/__root.tsx`, `web/src/main.tsx`, `web/index.html`** (komplette Dateien)

```tsx
// web/src/routes/__root.tsx
import { createRootRouteWithContext, Outlet } from '@tanstack/react-router'
import { AppToaster } from '@/components/app-toaster'
import { NotFoundPage } from '@/components/not-found-page'
import { SudoDialog } from '@/features/sudo/sudo-dialog'
import type { RouterContext } from '@/router'

export const Route = createRootRouteWithContext<RouterContext>()({
  component: RootLayout,
  notFoundComponent: NotFoundPage,
})

function RootLayout() {
  return (
    <>
      <Outlet />
      <SudoDialog />
      <AppToaster />
    </>
  )
}
```

```tsx
// web/src/main.tsx
import { QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider } from '@tanstack/react-router'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { createQueryClient } from './app/query-client'
import { routerNavigator, wireApi } from './app/wire-api'
import { i18nReady } from './i18n'
import { applyTheme, watchSystemTheme } from './lib/theme'
import { createAppRouter } from './router'
import './index.css'

// Theme before the first paint – no inline script in index.html (CSP script-src 'self').
applyTheme()
watchSystemTheme()

async function enableMocking(): Promise<void> {
  if (import.meta.env.VITE_MSW !== '1') return
  const { worker } = await import('./mocks/browser')
  await worker.start({ onUnhandledRequest: 'bypass' })
}

const queryClient = createQueryClient()
const router = createAppRouter(queryClient)
wireApi(queryClient, routerNavigator(router))

void Promise.all([enableMocking(), i18nReady]).then(() => {
  const root = document.getElementById('root')
  if (!root) throw new Error('#root missing in index.html')
  createRoot(root).render(
    <StrictMode>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </StrictMode>,
  )
})
```

```html
<!-- web/index.html -->
<!doctype html>
<html lang="de">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <meta name="color-scheme" content="light dark" />
    <title>mc-panel</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

Außerdem `web/public/vite.svg` löschen, falls vom Vite-Template noch vorhanden (wird nicht mehr referenziert): `rm -f web/public/vite.svg`.

- [ ] **Step 9: Route-Baum erzeugen, Typecheck, Suite**

Run: `cd web && npx vite build && npm run typecheck && npx vitest run`
Expected: `src/routeTree.gen.ts` aktualisiert, keine TS-Fehler, alle Tests grün.

- [ ] **Step 10: Manuell im Mock-Modus prüfen**

Run: `cd web && npm run dev:mock`, `http://localhost:5173` öffnen.
Expected: Konsole `[MSW] Mocking enabled.`; Phase-0-Startseite „mc-panel“ wird angezeigt; `<html>` hat je nach System `class="dark"`. Mit `Ctrl+C` beenden.

- [ ] **Step 11: Commit**

```bash
git add web/src web/index.html web/public
git commit -m "feat(web): add global sudo dialog and bootstrap router, query client and MSW toggle" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 9: Setup-Bausteine – Recovery-Codes, TOTP-Einrichtung, Passkey-Registrierung

**Files:**
- Create: `web/src/features/setup/recovery-codes-panel.tsx`, `web/src/features/setup/totp-setup-card.tsx`, `web/src/features/setup/passkey-setup-card.tsx`
- Test: `web/src/features/setup/recovery-codes-panel.test.tsx`, `web/src/features/setup/totp-setup-card.test.tsx`, `web/src/features/setup/passkey-setup-card.test.tsx`

**Interfaces:**
- Consumes: `api`, `unwrap` (Task 5); `errorMessage` (Task 5); `CodeForm` (Task 8); `registerPasskey`, `isWebAuthnSupported` (Task 6); `downloadText` (Task 2); `renderWithProviders` (Task 7/8); `qrcode` (`QRCode.toDataURL`)
- Produces:
```ts
export interface RecoveryCodesPanelProps { codes: string[]; doneLabel: string; onDone(): void }
export function RecoveryCodesPanel(props: RecoveryCodesPanelProps): JSX.Element
export function TotpSetupCard(props: { onEnabled(codes: string[]): void }): JSX.Element      // codes = [] wenn schon Codes existierten
export function PasskeySetupCard(props: { onRegistered(created: PasskeyCreated): void }): JSX.Element
```
Diese Komponenten werden vom erzwungenen Setup (Task 10) **und** vom Profil (Task 19) verwendet. Erfordert der Server Sudo (2FA schon aktiv), öffnet die Middleware den Sudo-Dialog automatisch.

- [ ] **Step 1: Failing Tests**

`web/src/features/setup/recovery-codes-panel.test.tsx`:
```tsx
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { RecoveryCodesPanel } from './recovery-codes-panel'

const codes = ['aaaaaaaaaa', 'bbbbbbbbbb']

beforeEach(() => {
  Object.assign(URL, { createObjectURL: vi.fn(() => 'blob:codes'), revokeObjectURL: vi.fn() })
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)
})
afterEach(() => vi.restoreAllMocks())

describe('RecoveryCodesPanel', () => {
  it('lists every code', () => {
    render(<RecoveryCodesPanel codes={codes} doneLabel="Weiter" onDone={() => undefined} />)
    const list = screen.getByRole('list', { name: 'Wiederherstellungscodes' })
    expect(within(list).getAllByRole('listitem').map((li) => li.textContent)).toEqual(codes)
  })

  it('only continues after the user confirmed storing the codes', async () => {
    const user = userEvent.setup()
    const onDone = vi.fn()
    render(<RecoveryCodesPanel codes={codes} doneLabel="Weiter" onDone={onDone} />)
    const done = screen.getByRole('button', { name: 'Weiter' })
    expect(done).toBeDisabled()
    await user.click(screen.getByRole('checkbox', { name: 'Ich habe die Codes sicher gespeichert.' }))
    await user.click(done)
    expect(onDone).toHaveBeenCalledTimes(1)
  })

  it('copies the codes to the clipboard', async () => {
    const user = userEvent.setup()
    render(<RecoveryCodesPanel codes={codes} doneLabel="Weiter" onDone={() => undefined} />)
    await user.click(screen.getByRole('button', { name: 'Kopieren' }))
    expect(await navigator.clipboard.readText()).toBe('aaaaaaaaaa\nbbbbbbbbbb')
  })

  it('downloads the codes as a text file', async () => {
    const user = userEvent.setup()
    render(<RecoveryCodesPanel codes={codes} doneLabel="Weiter" onDone={() => undefined} />)
    await user.click(screen.getByRole('button', { name: 'Als Textdatei herunterladen' }))
    expect(URL.createObjectURL).toHaveBeenCalledWith(expect.any(Blob))
    expect(HTMLAnchorElement.prototype.click).toHaveBeenCalledTimes(1)
  })
})
```

`web/src/features/setup/totp-setup-card.test.tsx`:
```tsx
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { loginAs, MOCK_TOTP_CODE, MOCK_TOTP_SECRET } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { TotpSetupCard } from './totp-setup-card'

vi.mock('qrcode', () => ({ default: { toDataURL: vi.fn(async () => 'data:image/png;base64,QR') } }))

describe('TotpSetupCard', () => {
  it('shows QR code and secret, rejects wrong codes and reports the recovery codes', async () => {
    const user = userEvent.setup()
    const onEnabled = vi.fn()
    await renderWithProviders(<TotpSetupCard onEnabled={onEnabled} />, { me: loginAs('newadmin', { stage: 'setup' }) })
    await user.click(screen.getByRole('button', { name: 'Einrichtung starten' }))
    const qr = await screen.findByRole('img', { name: 'QR-Code für die Authenticator-App' })
    expect(qr).toHaveAttribute('src', 'data:image/png;base64,QR')
    expect(screen.getByTestId('totp-secret')).toHaveTextContent(MOCK_TOTP_SECRET)

    const input = screen.getByLabelText('Code aus der Authenticator-App')
    await user.type(input, '000000')
    await user.click(screen.getByRole('button', { name: 'Aktivieren' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Der Code ist ungültig.')

    await user.clear(input)
    await user.type(input, MOCK_TOTP_CODE)
    await user.click(screen.getByRole('button', { name: 'Aktivieren' }))
    await vi.waitFor(() => expect(onEnabled).toHaveBeenCalledTimes(1))
    expect(onEnabled.mock.calls[0][0]).toHaveLength(10)
  })

  it('shows the 409 conflict when TOTP is already enabled', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<TotpSetupCard onEnabled={() => undefined} />, { me: loginAs('bob', { sudo: true }) })
    await user.click(screen.getByRole('button', { name: 'Einrichtung starten' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Konflikt mit dem aktuellen Zustand (TOTP already enabled)')
  })
})
```

`web/src/features/setup/passkey-setup-card.test.tsx`:
```tsx
import { browserSupportsWebAuthn, startRegistration } from '@simplewebauthn/browser'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { loginAs } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { PasskeySetupCard } from './passkey-setup-card'

vi.mock('@simplewebauthn/browser', () => ({
  browserSupportsWebAuthn: vi.fn(() => true),
  startAuthentication: vi.fn(),
  startRegistration: vi.fn(),
}))

beforeEach(() => {
  vi.mocked(browserSupportsWebAuthn).mockReturnValue(true)
  vi.mocked(startRegistration).mockResolvedValue({
    id: 'c',
    rawId: 'c',
    type: 'public-key',
    response: {},
    clientExtensionResults: {},
  } as unknown as Awaited<ReturnType<typeof startRegistration>>)
})

describe('PasskeySetupCard', () => {
  it('registers a named passkey', async () => {
    const user = userEvent.setup()
    const onRegistered = vi.fn()
    await renderWithProviders(<PasskeySetupCard onRegistered={onRegistered} />, { me: loginAs('newadmin', { stage: 'setup' }) })
    const name = screen.getByLabelText('Name des Passkeys')
    expect(name).toHaveValue('Mein Passkey')
    await user.clear(name)
    await user.type(name, 'YubiKey 5C')
    await user.click(screen.getByRole('button', { name: 'Passkey registrieren' }))
    await vi.waitFor(() => expect(onRegistered).toHaveBeenCalledTimes(1))
    expect(onRegistered.mock.calls[0][0]).toMatchObject({ passkey: { name: 'YubiKey 5C' } })
  })

  it('shows a readable message when the user cancels the browser dialog', async () => {
    vi.mocked(startRegistration).mockRejectedValue(Object.assign(new Error('cancelled'), { name: 'NotAllowedError' }))
    const user = userEvent.setup()
    await renderWithProviders(<PasskeySetupCard onRegistered={() => undefined} />, { me: loginAs('newadmin', { stage: 'setup' }) })
    await user.click(screen.getByRole('button', { name: 'Passkey registrieren' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Vorgang abgebrochen oder Zeit abgelaufen.')
  })

  it('explains that the browser lacks passkey support', async () => {
    vi.mocked(browserSupportsWebAuthn).mockReturnValue(false)
    await renderWithProviders(<PasskeySetupCard onRegistered={() => undefined} />, { me: loginAs('newadmin', { stage: 'setup' }) })
    expect(screen.getByText('Dieser Browser unterstützt keine Passkeys.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Passkey registrieren' })).not.toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Tests laufen lassen → FAIL**

Run: `cd web && npx vitest run src/features/setup`
Expected: FAIL – Module `./recovery-codes-panel`, `./totp-setup-card`, `./passkey-setup-card` fehlen.

- [ ] **Step 3: `web/src/features/setup/recovery-codes-panel.tsx`**

```tsx
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { downloadText } from '@/lib/download'

export interface RecoveryCodesPanelProps {
  codes: string[]
  doneLabel: string
  onDone(): void
}

export function RecoveryCodesPanel({ codes, doneLabel, onDone }: RecoveryCodesPanelProps) {
  const { t } = useTranslation()
  const titleId = useId()
  const checkboxId = useId()
  const [confirmed, setConfirmed] = useState(false)
  const text = codes.join('\n')

  async function copy() {
    try {
      await navigator.clipboard.writeText(text)
      toast.success(t('recovery.copied'))
    } catch {
      toast.error(t('recovery.copy_failed'))
    }
  }

  return (
    <section aria-labelledby={titleId} className="space-y-4">
      <h2 id={titleId} className="text-lg font-semibold">
        {t('recovery.title')}
      </h2>
      <p className="text-muted-foreground text-sm">{t('recovery.description')}</p>
      <ol aria-label={t('recovery.list_label')} className="bg-muted grid grid-cols-1 gap-2 rounded-md p-4 font-mono text-sm sm:grid-cols-2">
        {codes.map((code) => (
          <li key={code} className="select-all">
            {code}
          </li>
        ))}
      </ol>
      <div className="flex flex-wrap gap-2">
        <Button type="button" variant="outline" onClick={() => void copy()}>
          {t('recovery.copy')}
        </Button>
        <Button
          type="button"
          variant="outline"
          onClick={() => downloadText('mc-panel-recovery-codes.txt', `${t('recovery.file_header')}\n\n${text}\n`)}
        >
          {t('recovery.download')}
        </Button>
      </div>
      <div className="flex items-start gap-2">
        <input
          id={checkboxId}
          type="checkbox"
          className="accent-primary mt-0.5 size-4"
          checked={confirmed}
          onChange={(e) => setConfirmed(e.target.checked)}
        />
        <label htmlFor={checkboxId} className="text-sm">
          {t('recovery.confirm')}
        </label>
      </div>
      <Button type="button" className="w-full" disabled={!confirmed} onClick={onDone}>
        {doneLabel}
      </Button>
    </section>
  )
}
```

- [ ] **Step 4: `web/src/features/setup/totp-setup-card.tsx`**

```tsx
import { useMutation, useQuery } from '@tanstack/react-query'
import QRCode from 'qrcode'
import { useTranslation } from 'react-i18next'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import { CodeForm } from '@/components/code-form'
import { Button } from '@/components/ui/button'
import { errorMessage } from '@/lib/error-message'

export function TotpSetupCard({ onEnabled }: { onEnabled(codes: string[]): void }) {
  const { t } = useTranslation()
  const setup = useMutation({ mutationFn: () => unwrap(api.POST('/me/totp/setup')) })
  const otpauthUrl = setup.data?.otpauth_url ?? null
  // QR as data: URL in a plain <img> – allowed by CSP img-src 'self' data:, no canvas/script injection.
  const qr = useQuery({
    queryKey: ['totp-qr', otpauthUrl],
    queryFn: () => QRCode.toDataURL(otpauthUrl ?? '', { margin: 1, width: 224, errorCorrectionLevel: 'M' }),
    enabled: otpauthUrl !== null,
    staleTime: Infinity,
    gcTime: 0,
  })
  const enable = useMutation({
    mutationFn: (code: string) => unwrap(api.POST('/me/totp/enable', { body: { code } })),
    onSuccess: (res) => onEnabled(res.codes),
  })

  if (!setup.data) {
    return (
      <div className="space-y-4">
        <p className="text-muted-foreground text-sm">{t('totp_setup.intro')}</p>
        {setup.error ? (
          <p role="alert" className="text-destructive text-sm">
            {errorMessage(setup.error, t)}
          </p>
        ) : null}
        <Button type="button" className="w-full" disabled={setup.isPending} onClick={() => setup.mutate()}>
          {t('totp_setup.start')}
        </Button>
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <p className="text-sm">{t('totp_setup.scan')}</p>
      {qr.data ? (
        <img src={qr.data} alt={t('totp_setup.qr_alt')} width={224} height={224} className="mx-auto rounded-md bg-white p-2" />
      ) : null}
      <p className="text-muted-foreground text-sm">{t('totp_setup.manual')}</p>
      <code data-testid="totp-secret" className="bg-muted block rounded-md p-2 font-mono text-sm break-all">
        {setup.data.secret}
      </code>
      <CodeForm
        label={t('totp_setup.code_label')}
        submitLabel={t('totp_setup.confirm')}
        numeric
        pending={enable.isPending}
        error={enable.error ? errorMessage(enable.error, t) : null}
        onSubmit={(code) => enable.mutate(code)}
      />
    </div>
  )
}
```

- [ ] **Step 5: `web/src/features/setup/passkey-setup-card.tsx`**

```tsx
import { useMutation } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { PasskeyCreated } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { isWebAuthnSupported, registerPasskey } from '@/features/webauthn/webauthn'
import { errorMessage } from '@/lib/error-message'

export function PasskeySetupCard({ onRegistered }: { onRegistered(created: PasskeyCreated): void }) {
  const { t } = useTranslation()
  const id = useId()
  const [name, setName] = useState(() => t('passkey.default_name'))
  const register = useMutation({
    mutationFn: (passkeyName: string) => registerPasskey(passkeyName),
    onSuccess: (created) => onRegistered(created),
  })

  if (!isWebAuthnSupported()) {
    return <p className="text-muted-foreground text-sm">{t('passkey.unsupported')}</p>
  }

  const trimmed = name.trim()
  return (
    <form
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault()
        if (trimmed) register.mutate(trimmed)
      }}
    >
      <p className="text-muted-foreground text-sm">{t('passkey.intro')}</p>
      <div className="space-y-2">
        <Label htmlFor={id}>{t('passkey.name_label')}</Label>
        <Input id={id} value={name} maxLength={64} onChange={(e) => setName(e.target.value)} />
      </div>
      {register.error ? (
        <p role="alert" className="text-destructive text-sm">
          {errorMessage(register.error, t)}
        </p>
      ) : null}
      <Button type="submit" className="w-full" disabled={register.isPending || !trimmed}>
        {t('passkey.register')}
      </Button>
    </form>
  )
}
```

- [ ] **Step 6: Tests laufen lassen → PASS**

Run: `cd web && npx vitest run src/features/setup`
Expected: PASS (recovery 4, totp 2, passkey 3).

- [ ] **Step 7: Gesamte Suite + Typecheck, Commit**

Run: `cd web && npx vitest run && npm run typecheck`
Expected: grün.

```bash
git add web/src/features/setup
git commit -m "feat(web): add recovery code panel and TOTP/passkey enrollment cards" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: Auth-Seiten und Routen – Login, 2FA, erzwungenes Setup

**Files:**
- Create: `web/src/features/auth/auth-layout.tsx`, `web/src/features/auth/login-page.tsx`, `web/src/features/auth/second-factor-page.tsx`, `web/src/features/auth/use-logout.ts`, `web/src/features/auth/logout-button.tsx`, `web/src/features/auth/route-guards.ts`, `web/src/features/setup/setup-2fa-page.tsx`, `web/src/features/layout/app-layout.tsx`, `web/src/routes/login/index.tsx`, `web/src/routes/login/2fa.tsx`, `web/src/routes/setup-2fa.tsx`, `web/src/routes/_app.tsx`
- Test: `web/src/features/auth/login-page.test.tsx`, `web/src/features/auth/second-factor-page.test.tsx`, `web/src/features/setup/setup-2fa-page.test.tsx`

**Interfaces:**
- Consumes: `applyMe`, `useMe`, `clearSession`, `loadMe` (Task 7); `pathForStage`, `appGuard`, `loginGuard`, `secondFactorGuard`, `setupGuard`, `AuthPath` (Task 7); `loginWithPasskey`, `isWebAuthnSupported` (Task 6); `CodeForm` (Task 8); `TotpSetupCard`, `PasskeySetupCard`, `RecoveryCodesPanel` (Task 9); `ThemeToggle` (Task 2); `RouterContext` (Task 8)
- Produces:
```ts
export function AuthLayout(props: { title: string; description?: string; wide?: boolean; children: ReactNode }): JSX.Element
export function LoginPage(): JSX.Element
export type SecondFactorMethod = 'totp' | 'webauthn' | 'recovery'
export function availableMethods(me: Me, webauthnSupported: boolean): SecondFactorMethod[]
export function SecondFactorPage(): JSX.Element
export function Setup2faPage(): JSX.Element
export function useLogout(): UseMutationResult<void, Error, void>
export function LogoutButton(props: { label?: string }): JSX.Element
export function guardBeforeLoad(guard: (me: Me | null) => AuthPath | null): (opts: { context: RouterContext }) => Promise<void>
export function AppLayout(): JSX.Element     // vorläufig: <main>+<Outlet/>; Task 20 ersetzt durch die App-Shell
// Routen: /login, /login/2fa, /setup-2fa, pfadloses Layout /_app (Guard stage=full)
```

- [ ] **Step 1: Failing Tests**

`web/src/features/auth/login-page.test.tsx`:
```tsx
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import type { Me } from '@/api/types'
import { MOCK_PASSWORD } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { LoginPage } from './login-page'
import { meKey } from './me'

async function fillAndSubmit(username: string, password: string) {
  const user = userEvent.setup()
  await user.type(screen.getByLabelText('Benutzername'), username)
  await user.type(screen.getByLabelText('Passwort'), password)
  await user.click(screen.getByRole('button', { name: 'Anmelden' }))
}

describe('LoginPage', () => {
  it('keeps submit disabled until both fields are filled', async () => {
    await renderWithProviders(<LoginPage />, { path: '/login' })
    expect(screen.getByRole('button', { name: 'Anmelden' })).toBeDisabled()
  })

  it('shows a generic error for wrong credentials and stays on /login', async () => {
    const { router } = await renderWithProviders(<LoginPage />, { path: '/login' })
    await fillAndSubmit('admin', 'definitely-wrong')
    expect(await screen.findByRole('alert')).toHaveTextContent('Benutzername oder Passwort falsch.')
    expect(router.state.location.pathname).toBe('/login')
  })

  it.each([
    ['admin', '/login/2fa', 'preauth'],
    ['newadmin', '/setup-2fa', 'setup'],
    ['alice', '/', 'full'],
  ])('sends %s to %s', async (username, target, stage) => {
    const { router, queryClient } = await renderWithProviders(<LoginPage />, { path: '/login' })
    await fillAndSubmit(username, MOCK_PASSWORD)
    await waitFor(() => expect(router.state.location.pathname).toBe(target))
    expect(queryClient.getQueryData<Me>(meKey)?.stage).toBe(stage)
  })
})
```

`web/src/features/auth/second-factor-page.test.tsx`:
```tsx
import { browserSupportsWebAuthn, startAuthentication } from '@simplewebauthn/browser'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { getCsrfToken } from '@/api/csrf'
import type { Me } from '@/api/types'
import { loginAs, MOCK_TOTP_CODE } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { meKey } from './me'
import { SecondFactorPage } from './second-factor-page'

vi.mock('@simplewebauthn/browser', () => ({
  browserSupportsWebAuthn: vi.fn(() => true),
  startAuthentication: vi.fn(),
  startRegistration: vi.fn(),
}))

beforeEach(() => {
  vi.mocked(browserSupportsWebAuthn).mockReturnValue(true)
})

function renderPage() {
  return renderWithProviders(<SecondFactorPage />, { path: '/login/2fa', me: loginAs('admin', { stage: 'preauth' }) })
}

describe('SecondFactorPage', () => {
  it('offers exactly the factors listed in Me.second_factors', async () => {
    await renderPage()
    expect(screen.getAllByRole('tab').map((tab) => tab.textContent)).toEqual(['Authenticator-App', 'Passkey', 'Wiederherstellungscode'])
  })

  it('lands on / with a fresh full-stage Me after a valid TOTP code', async () => {
    const user = userEvent.setup()
    const { router, queryClient } = await renderPage()
    await user.type(screen.getByLabelText('Code aus der Authenticator-App'), MOCK_TOTP_CODE)
    await user.click(screen.getByRole('button', { name: 'Bestätigen' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
    const me = queryClient.getQueryData<Me>(meKey)
    expect(me?.stage).toBe('full')
    expect(getCsrfToken()).toBe(me?.csrf_token)
  })

  it('shows an error for a wrong code without logging the user out', async () => {
    const user = userEvent.setup()
    const { router } = await renderPage()
    await user.type(screen.getByLabelText('Code aus der Authenticator-App'), '000000')
    await user.click(screen.getByRole('button', { name: 'Bestätigen' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Der Code ist ungültig.')
    expect(router.state.location.pathname).toBe('/login/2fa')
  })

  it('accepts a recovery code typed with dashes and capitals', async () => {
    const user = userEvent.setup()
    const { router } = await renderPage()
    await user.click(screen.getByRole('tab', { name: 'Wiederherstellungscode' }))
    await user.type(screen.getByLabelText('Einer deiner Wiederherstellungscodes'), 'ABCDE-FGHJK')
    await user.click(screen.getByRole('button', { name: 'Bestätigen' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
  })

  it('signs in with a passkey', async () => {
    vi.mocked(startAuthentication).mockResolvedValue({
      id: 'c',
      rawId: 'c',
      type: 'public-key',
      response: {},
      clientExtensionResults: {},
    } as unknown as Awaited<ReturnType<typeof startAuthentication>>)
    const user = userEvent.setup()
    const { router } = await renderPage()
    await user.click(screen.getByRole('tab', { name: 'Passkey' }))
    await user.click(screen.getByRole('button', { name: 'Mit Passkey anmelden' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
  })

  it('hides the passkey tab when WebAuthn is unsupported', async () => {
    vi.mocked(browserSupportsWebAuthn).mockReturnValue(false)
    await renderPage()
    expect(screen.getAllByRole('tab').map((tab) => tab.textContent)).toEqual(['Authenticator-App', 'Wiederherstellungscode'])
  })
})
```

`web/src/features/setup/setup-2fa-page.test.tsx`:
```tsx
import { browserSupportsWebAuthn, startRegistration } from '@simplewebauthn/browser'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Me } from '@/api/types'
import { meKey } from '@/features/auth/me'
import { loginAs, MOCK_TOTP_CODE } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { Setup2faPage } from './setup-2fa-page'

vi.mock('qrcode', () => ({ default: { toDataURL: vi.fn(async () => 'data:image/png;base64,QR') } }))
vi.mock('@simplewebauthn/browser', () => ({
  browserSupportsWebAuthn: vi.fn(() => true),
  startAuthentication: vi.fn(),
  startRegistration: vi.fn(),
}))

beforeEach(() => {
  vi.mocked(browserSupportsWebAuthn).mockReturnValue(true)
})

function renderPage() {
  return renderWithProviders(<Setup2faPage />, { path: '/setup-2fa', me: loginAs('newadmin', { stage: 'setup' }) })
}

describe('Setup2faPage', () => {
  it('keeps the recovery codes on screen after Me turned full, then continues to /', async () => {
    const user = userEvent.setup()
    const { router, queryClient } = await renderPage()
    await user.click(screen.getByRole('button', { name: 'Einrichtung starten' }))
    await user.type(await screen.findByLabelText('Code aus der Authenticator-App'), MOCK_TOTP_CODE)
    await user.click(screen.getByRole('button', { name: 'Aktivieren' }))

    const list = await screen.findByRole('list', { name: 'Wiederherstellungscodes' })
    expect(within(list).getAllByRole('listitem')).toHaveLength(10)
    await waitFor(() => expect(queryClient.getQueryData<Me>(meKey)?.stage).toBe('full'))
    expect(screen.getByRole('list', { name: 'Wiederherstellungscodes' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/setup-2fa')

    await user.click(screen.getByRole('checkbox', { name: 'Ich habe die Codes sicher gespeichert.' }))
    await user.click(screen.getByRole('button', { name: 'Weiter zum Panel' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
  })

  it('registers a passkey as first factor and shows the codes', async () => {
    vi.mocked(startRegistration).mockResolvedValue({
      id: 'c',
      rawId: 'c',
      type: 'public-key',
      response: {},
      clientExtensionResults: {},
    } as unknown as Awaited<ReturnType<typeof startRegistration>>)
    const user = userEvent.setup()
    await renderPage()
    await user.click(screen.getByRole('tab', { name: 'Passkey' }))
    await user.click(screen.getByRole('button', { name: 'Passkey registrieren' }))
    expect(await screen.findByRole('list', { name: 'Wiederherstellungscodes' })).toBeInTheDocument()
  })

  it('offers only TOTP when WebAuthn is unsupported', async () => {
    vi.mocked(browserSupportsWebAuthn).mockReturnValue(false)
    await renderPage()
    expect(screen.queryByRole('tab', { name: 'Passkey' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Einrichtung starten' })).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Tests laufen lassen → FAIL**

Run: `cd web && npx vitest run src/features/auth/login-page.test.tsx src/features/auth/second-factor-page.test.tsx src/features/setup/setup-2fa-page.test.tsx`
Expected: FAIL – Module `./login-page`, `./second-factor-page`, `./setup-2fa-page` fehlen.

- [ ] **Step 3: Gemeinsame Teile – `auth-layout.tsx`, `use-logout.ts`, `logout-button.tsx`, `route-guards.ts`**

```tsx
// web/src/features/auth/auth-layout.tsx
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { ThemeToggle } from '@/components/theme-toggle'
import { Card, CardContent, CardDescription, CardHeader } from '@/components/ui/card'
import { cn } from '@/lib/utils'

export function AuthLayout({ title, description, wide = false, children }: { title: string; description?: string; wide?: boolean; children: ReactNode }) {
  const { t } = useTranslation()
  return (
    <div className="bg-muted/40 relative flex min-h-svh flex-col items-center justify-center px-4 py-10">
      <div className="absolute top-3 right-3">
        <ThemeToggle />
      </div>
      <main id="main" className={cn('w-full', wide ? 'max-w-lg' : 'max-w-sm')}>
        <p className="mb-4 text-center text-lg font-semibold tracking-tight">{t('auth.brand')}</p>
        <Card>
          <CardHeader>
            <h1 className="text-xl font-semibold">{title}</h1>
            {description ? <CardDescription>{description}</CardDescription> : null}
          </CardHeader>
          <CardContent>{children}</CardContent>
        </Card>
      </main>
    </div>
  )
}
```

```ts
// web/src/features/auth/use-logout.ts
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { api } from '@/api/client'
import { unwrapVoid } from '@/api/errors'
import { clearSession } from './me'

export function useLogout() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  return useMutation({
    mutationFn: () => unwrapVoid(api.POST('/auth/logout')),
    onSettled: async () => {
      clearSession(queryClient)
      await navigate({ to: '/login' })
    },
  })
}
```

```tsx
// web/src/features/auth/logout-button.tsx
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { useLogout } from './use-logout'

export function LogoutButton({ label }: { label?: string }) {
  const { t } = useTranslation()
  const logout = useLogout()
  return (
    <Button type="button" variant="link" disabled={logout.isPending} onClick={() => logout.mutate()}>
      {label ?? t('auth.logout')}
    </Button>
  )
}
```

```ts
// web/src/features/auth/route-guards.ts
import { redirect } from '@tanstack/react-router'
import type { Me } from '@/api/types'
import type { RouterContext } from '@/router'
import type { AuthPath } from './guards'
import { loadMe } from './me'

/** Turns a pure guard into a TanStack Router beforeLoad hook. */
export function guardBeforeLoad(guard: (me: Me | null) => AuthPath | null) {
  return async ({ context }: { context: RouterContext }): Promise<void> => {
    const target = guard(await loadMe(context.queryClient))
    if (target) throw redirect({ to: target })
  }
}
```

- [ ] **Step 4: `web/src/features/auth/login-page.tsx`**

```tsx
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { errorMessage } from '@/lib/error-message'
import { AuthLayout } from './auth-layout'
import { pathForStage } from './guards'
import { applyMe } from './me'

export function LoginPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const login = useMutation({
    mutationFn: () => unwrap(api.POST('/auth/login', { body: { username: username.trim(), password } })),
    onSuccess: async (me) => {
      // Cache first, then navigate – the target route's guard reads this Me.
      applyMe(queryClient, me)
      await navigate({ to: pathForStage(me.stage) })
    },
  })

  return (
    <AuthLayout title={t('login.title')} description={t('login.description')}>
      <form
        className="space-y-4"
        noValidate
        onSubmit={(e) => {
          e.preventDefault()
          login.mutate()
        }}
      >
        <div className="space-y-2">
          <Label htmlFor="login-username">{t('login.username')}</Label>
          <Input
            id="login-username"
            autoComplete="username"
            autoCapitalize="none"
            spellCheck={false}
            maxLength={64}
            autoFocus
            value={username}
            onChange={(e) => setUsername(e.target.value)}
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="login-password">{t('login.password')}</Label>
          <Input
            id="login-password"
            type="password"
            autoComplete="current-password"
            maxLength={256}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </div>
        {login.error ? (
          <p role="alert" className="text-destructive text-sm">
            {errorMessage(login.error, t)}
          </p>
        ) : null}
        <Button type="submit" className="w-full" disabled={login.isPending || !username.trim() || !password}>
          {login.isPending ? t('login.submitting') : t('login.submit')}
        </Button>
      </form>
    </AuthLayout>
  )
}
```

- [ ] **Step 5: `web/src/features/auth/second-factor-page.tsx`**

```tsx
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { Me } from '@/api/types'
import { CodeForm } from '@/components/code-form'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { isWebAuthnSupported, loginWithPasskey } from '@/features/webauthn/webauthn'
import { errorMessage } from '@/lib/error-message'
import { AuthLayout } from './auth-layout'
import { pathForStage } from './guards'
import { LogoutButton } from './logout-button'
import { applyMe, useMe } from './me'

export type SecondFactorMethod = 'totp' | 'webauthn' | 'recovery'

export function availableMethods(me: Me, webauthnSupported: boolean): SecondFactorMethod[] {
  return me.second_factors.filter((f) => f !== 'webauthn' || webauthnSupported)
}

export function SecondFactorPage() {
  const { t } = useTranslation()
  const me = useMe()
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const finish = async (next: Me) => {
    applyMe(queryClient, next)
    await navigate({ to: pathForStage(next.stage) })
  }
  const totp = useMutation({ mutationFn: (code: string) => unwrap(api.POST('/auth/2fa/totp', { body: { code } })), onSuccess: finish })
  const recovery = useMutation({ mutationFn: (code: string) => unwrap(api.POST('/auth/2fa/recovery', { body: { code } })), onSuccess: finish })
  const passkey = useMutation({ mutationFn: loginWithPasskey, onSuccess: finish })

  const methods = me ? availableMethods(me, isWebAuthnSupported()) : []

  return (
    <AuthLayout title={t('second_factor.title')} description={me ? t('second_factor.description', { username: me.username }) : undefined}>
      {methods.length === 0 ? (
        <p role="alert" className="text-sm">
          {t('second_factor.no_method')}
        </p>
      ) : (
        <Tabs defaultValue={methods[0]}>
          {methods.length > 1 ? (
            <TabsList className="flex w-full">
              {methods.map((m) => (
                <TabsTrigger key={m} value={m} className="flex-1">
                  {t(`second_factor.tab_${m}`)}
                </TabsTrigger>
              ))}
            </TabsList>
          ) : null}
          {methods.includes('totp') ? (
            <TabsContent value="totp" className="pt-4">
              <CodeForm
                label={t('second_factor.totp_label')}
                submitLabel={t('second_factor.verify')}
                numeric
                pending={totp.isPending}
                error={totp.error ? errorMessage(totp.error, t) : null}
                onSubmit={(code) => totp.mutate(code)}
              />
            </TabsContent>
          ) : null}
          {methods.includes('webauthn') ? (
            <TabsContent value="webauthn" className="space-y-3 pt-4">
              <p className="text-muted-foreground text-sm">{t('second_factor.passkey_hint')}</p>
              {passkey.error ? (
                <p role="alert" className="text-destructive text-sm">
                  {errorMessage(passkey.error, t)}
                </p>
              ) : null}
              <Button type="button" className="w-full" disabled={passkey.isPending} onClick={() => passkey.mutate()}>
                {t('second_factor.passkey_button')}
              </Button>
            </TabsContent>
          ) : null}
          {methods.includes('recovery') ? (
            <TabsContent value="recovery" className="pt-4">
              <CodeForm
                label={t('second_factor.recovery_label')}
                submitLabel={t('second_factor.verify')}
                pending={recovery.isPending}
                error={recovery.error ? errorMessage(recovery.error, t) : null}
                onSubmit={(code) => recovery.mutate(code)}
              />
            </TabsContent>
          ) : null}
        </Tabs>
      )}
      <div className="mt-6 text-center">
        <LogoutButton label={t('auth.other_account')} />
      </div>
    </AuthLayout>
  )
}
```

- [ ] **Step 6: `web/src/features/setup/setup-2fa-page.tsx`**

```tsx
import { useNavigate } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { AuthLayout } from '@/features/auth/auth-layout'
import { LogoutButton } from '@/features/auth/logout-button'
import { useMe } from '@/features/auth/me'
import { isWebAuthnSupported } from '@/features/webauthn/webauthn'
import { PasskeySetupCard } from './passkey-setup-card'
import { RecoveryCodesPanel } from './recovery-codes-panel'
import { TotpSetupCard } from './totp-setup-card'

/**
 * Forced setup. The recovery codes live in local state, so they stay visible even though the API
 * middleware has already refreshed Me to stage "full" – the route guard only runs on navigation.
 */
export function Setup2faPage() {
  const { t } = useTranslation()
  const me = useMe()
  const navigate = useNavigate()
  const [codes, setCodes] = useState<string[] | null>(null)
  const passkeySupported = isWebAuthnSupported()

  const finish = () => void navigate({ to: '/' })
  const onFactorAdded = (newCodes: string[]) => {
    if (newCodes.length > 0) setCodes(newCodes)
    else finish()
  }

  if (codes) {
    return (
      <AuthLayout wide title={t('setup.codes_title')}>
        <RecoveryCodesPanel codes={codes} doneLabel={t('setup.finish')} onDone={finish} />
      </AuthLayout>
    )
  }

  return (
    <AuthLayout wide title={t('setup.title')} description={me?.role === 'admin' ? t('setup.description_admin') : t('setup.description_user')}>
      <Tabs defaultValue="totp">
        {passkeySupported ? (
          <TabsList className="grid w-full grid-cols-2">
            <TabsTrigger value="totp">{t('setup.tab_totp')}</TabsTrigger>
            <TabsTrigger value="passkey">{t('setup.tab_passkey')}</TabsTrigger>
          </TabsList>
        ) : null}
        <TabsContent value="totp" className="pt-4">
          <TotpSetupCard onEnabled={onFactorAdded} />
        </TabsContent>
        {passkeySupported ? (
          <TabsContent value="passkey" className="pt-4">
            <PasskeySetupCard onRegistered={(created) => onFactorAdded(created.recovery_codes)} />
          </TabsContent>
        ) : null}
      </Tabs>
      <div className="mt-6 text-center">
        <LogoutButton />
      </div>
    </AuthLayout>
  )
}
```

- [ ] **Step 7: `web/src/features/layout/app-layout.tsx` (vorläufig) und Route-Dateien**

```tsx
// web/src/features/layout/app-layout.tsx – replaced by the full app shell in Task 20
import { Outlet } from '@tanstack/react-router'

export function AppLayout() {
  return (
    <main id="main" className="mx-auto w-full max-w-6xl p-4 md:p-6">
      <Outlet />
    </main>
  )
}
```

```tsx
// web/src/routes/login/index.tsx
import { createFileRoute } from '@tanstack/react-router'
import { loginGuard } from '@/features/auth/guards'
import { LoginPage } from '@/features/auth/login-page'
import { guardBeforeLoad } from '@/features/auth/route-guards'

export const Route = createFileRoute('/login/')({
  beforeLoad: guardBeforeLoad(loginGuard),
  component: LoginPage,
})
```

```tsx
// web/src/routes/login/2fa.tsx
import { createFileRoute } from '@tanstack/react-router'
import { secondFactorGuard } from '@/features/auth/guards'
import { guardBeforeLoad } from '@/features/auth/route-guards'
import { SecondFactorPage } from '@/features/auth/second-factor-page'

export const Route = createFileRoute('/login/2fa')({
  beforeLoad: guardBeforeLoad(secondFactorGuard),
  component: SecondFactorPage,
})
```

```tsx
// web/src/routes/setup-2fa.tsx
import { createFileRoute } from '@tanstack/react-router'
import { setupGuard } from '@/features/auth/guards'
import { guardBeforeLoad } from '@/features/auth/route-guards'
import { Setup2faPage } from '@/features/setup/setup-2fa-page'

export const Route = createFileRoute('/setup-2fa')({
  beforeLoad: guardBeforeLoad(setupGuard),
  component: Setup2faPage,
})
```

```tsx
// web/src/routes/_app.tsx
import { createFileRoute } from '@tanstack/react-router'
import { appGuard } from '@/features/auth/guards'
import { guardBeforeLoad } from '@/features/auth/route-guards'
import { AppLayout } from '@/features/layout/app-layout'

export const Route = createFileRoute('/_app')({
  beforeLoad: guardBeforeLoad(appGuard),
  component: AppLayout,
})
```

- [ ] **Step 8: Tests laufen lassen → PASS**

Run: `cd web && npx vitest run src/features/auth src/features/setup`
Expected: PASS (login 5, second-factor 6, setup-page 3 sowie alle früheren Tests dieser Ordner).

- [ ] **Step 9: Route-Baum, Typecheck, Suite**

Run: `cd web && npx vite build && npm run typecheck && npx vitest run`
Expected: `src/routeTree.gen.ts` enthält `/login/`, `/login/2fa`, `/setup-2fa`, `/_app`; keine TS-Fehler; alle Tests grün.

- [ ] **Step 10: Manuell im Mock-Modus prüfen**

Run: `cd web && npm run dev:mock`, `http://localhost:5173/login` öffnen.
Expected: `admin` / `correct-horse-battery` → `/login/2fa` → `123456` → `/` (Phase-0-Startseite); `newadmin` → `/setup-2fa` mit QR-Code. `Ctrl+C`.

- [ ] **Step 11: Commit**

```bash
git add web/src/features web/src/routes web/src/routeTree.gen.ts
git commit -m "feat(web): add login, second-factor and forced 2FA setup pages with stage guards" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11: Dashboard mit Server-Stubs und Power-Buttons

**Files:**
- Create: `web/src/features/dashboard/servers.ts`, `web/src/features/dashboard/server-card.tsx`, `web/src/features/dashboard/create-server-dialog.tsx`, `web/src/features/dashboard/dashboard-page.tsx`, `web/src/routes/_app/index.tsx`
- Delete: `web/src/routes/index.tsx`, `web/src/routes/index.test.tsx` (Phase-0-Platzhalter)
- Test: `web/src/features/dashboard/dashboard-page.test.tsx`

**Interfaces:**
- Consumes: `api`, `unwrap`, `unwrapVoid`, `useErrorToast`, `errorMessage` (Task 5); `useMe` (Task 7); `PageHeader`, `ConfirmDialog` (Task 2); shadcn `Card`, `Badge`, `Dialog`, `Button`, `Input`, `Label`
- Produces:
```ts
export const serversKey: readonly ['servers']
export const serversQueryOptions                    // queryOptions({ queryKey: serversKey, queryFn: GET /servers })
export function ServerCard(props: { server: Server; canDelete: boolean }): JSX.Element   // <article aria-labelledby=Servername>
export function CreateServerDialog(props: { open: boolean; onOpenChange(open: boolean): void }): JSX.Element
export function DashboardPage(): JSX.Element
// Route: / (Kind von /_app)
```

- [ ] **Step 1: Failing Test `web/src/features/dashboard/dashboard-page.test.tsx`**

```tsx
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { loginAs, MOCK_TOTP_CODE } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { DashboardPage } from './dashboard-page'

describe('DashboardPage', () => {
  it('shows a restricted user only permitted servers and power buttons only with server.power', async () => {
    await renderWithProviders(<DashboardPage />, { me: loginAs('alice') })
    const survival = await screen.findByRole('article', { name: 'survival' })
    expect(within(survival).getByRole('button', { name: 'Starten' })).toBeInTheDocument()
    const creative = screen.getByRole('article', { name: 'creative' })
    expect(within(creative).queryByRole('button', { name: 'Starten' })).not.toBeInTheDocument()
    expect(within(creative).getByText('Keine Berechtigung für Start/Stopp.')).toBeInTheDocument()
    expect(screen.queryByRole('article', { name: 'lobby' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Server anlegen' })).not.toBeInTheDocument()
  })

  it('sends a power action and confirms it with a toast', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<DashboardPage />, { me: loginAs('alice') })
    const survival = await screen.findByRole('article', { name: 'survival' })
    await user.click(within(survival).getByRole('button', { name: 'Neu starten' }))
    expect(await screen.findByText('Neustart für „survival“ angefordert.')).toBeInTheDocument()
  })

  it('lets an admin create a server after confirming sudo', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<DashboardPage />, { me: loginAs('admin') })
    await user.click(await screen.findByRole('button', { name: 'Server anlegen' }))
    const dialog = screen.getByRole('dialog', { name: 'Server anlegen' })
    await user.type(within(dialog).getByLabelText('Name'), 'skyblock')
    await user.click(within(dialog).getByRole('button', { name: 'Anlegen' }))

    const sudo = await screen.findByRole('dialog', { name: 'Aktion bestätigen' })
    await user.type(within(sudo).getByLabelText('Code aus der Authenticator-App'), MOCK_TOTP_CODE)
    await user.click(within(sudo).getByRole('button', { name: 'Bestätigen' }))

    expect(await screen.findByRole('article', { name: 'skyblock' })).toBeInTheDocument()
  })

  it('validates the server name before sending', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<DashboardPage />, { me: loginAs('admin', { sudo: true }) })
    await user.click(await screen.findByRole('button', { name: 'Server anlegen' }))
    const dialog = screen.getByRole('dialog', { name: 'Server anlegen' })
    await user.type(within(dialog).getByLabelText('Name'), 'Bad Name')
    expect(within(dialog).getByText('Der Name entspricht nicht den Regeln.')).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Anlegen' })).toBeDisabled()
  })

  it('deletes a server after confirmation', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<DashboardPage />, { me: loginAs('admin', { sudo: true }) })
    const lobby = await screen.findByRole('article', { name: 'lobby' })
    await user.click(within(lobby).getByRole('button', { name: 'lobby löschen' }))
    const confirm = screen.getByRole('dialog', { name: 'Server „lobby“ löschen?' })
    await user.click(within(confirm).getByRole('button', { name: 'Löschen' }))
    expect(await screen.findByText('Server „lobby“ gelöscht.')).toBeInTheDocument()
    expect(screen.queryByRole('article', { name: 'lobby' })).not.toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Test laufen lassen → FAIL**

Run: `cd web && npx vitest run src/features/dashboard`
Expected: FAIL – `Failed to resolve import "./dashboard-page"`.

- [ ] **Step 3: `web/src/features/dashboard/servers.ts`**

```ts
import { queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'

export const serversKey = ['servers'] as const

export const serversQueryOptions = queryOptions({
  queryKey: serversKey,
  queryFn: () => unwrap(api.GET('/servers')),
})
```

- [ ] **Step 4: `web/src/features/dashboard/server-card.tsx`**

```tsx
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap, unwrapVoid } from '@/api/errors'
import type { PowerAction, Server } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader } from '@/components/ui/card'
import { useErrorToast } from '@/lib/error-message'
import { serversKey } from './servers'

const POWER_ACTIONS: PowerAction[] = ['start', 'stop', 'restart']

export function ServerCard({ server, canDelete }: { server: Server; canDelete: boolean }) {
  const { t } = useTranslation()
  const headingId = useId()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const [confirmDelete, setConfirmDelete] = useState(false)
  const canPower = server.permissions.includes('server.power')

  const power = useMutation({
    mutationFn: (action: PowerAction) =>
      unwrap(api.POST('/servers/{id}/power', { params: { path: { id: server.id } }, body: { action } })),
    onSuccess: (_data, action) =>
      toast.success(t('dashboard.power_requested', { action: t(`dashboard.power_noun.${action}`), name: server.name })),
    onError: notifyError,
  })
  const remove = useMutation({
    mutationFn: () => unwrapVoid(api.DELETE('/servers/{id}', { params: { path: { id: server.id } } })),
    onSuccess: () => {
      setConfirmDelete(false)
      toast.success(t('dashboard.deleted', { name: server.name }))
      void queryClient.invalidateQueries({ queryKey: serversKey })
    },
    onError: notifyError,
  })

  return (
    <article aria-labelledby={headingId} className="h-full">
      <Card className="h-full">
        <CardHeader>
          <h2 id={headingId} className="text-lg font-semibold">
            {server.name}
          </h2>
          <CardDescription>{t('dashboard.stub_hint')}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <ul aria-label={t('dashboard.permissions_label')} className="flex flex-wrap gap-1.5">
            {server.permissions.map((p) => (
              <li key={p}>
                <Badge variant="secondary">{t(`permission.${p}`)}</Badge>
              </li>
            ))}
          </ul>
          {canPower ? (
            <div role="group" aria-label={t('dashboard.power_group', { name: server.name })} className="flex flex-wrap gap-2">
              {POWER_ACTIONS.map((action) => (
                <Button
                  key={action}
                  type="button"
                  size="sm"
                  variant={action === 'stop' ? 'outline' : 'secondary'}
                  disabled={power.isPending}
                  onClick={() => power.mutate(action)}
                >
                  {t(`dashboard.power.${action}`)}
                </Button>
              ))}
            </div>
          ) : (
            <p className="text-muted-foreground text-sm">{t('dashboard.no_power')}</p>
          )}
          {canDelete ? (
            <Button
              type="button"
              size="sm"
              variant="ghost"
              className="text-destructive"
              aria-label={t('dashboard.delete_for', { name: server.name })}
              onClick={() => setConfirmDelete(true)}
            >
              {t('common.delete')}
            </Button>
          ) : null}
        </CardContent>
      </Card>
      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title={t('dashboard.delete_title', { name: server.name })}
        description={t('dashboard.delete_description')}
        confirmLabel={t('common.delete')}
        destructive
        pending={remove.isPending}
        onConfirm={() => remove.mutate()}
      />
    </article>
  )
}
```

- [ ] **Step 5: `web/src/features/dashboard/create-server-dialog.tsx`**

```tsx
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { errorMessage } from '@/lib/error-message'
import { cn } from '@/lib/utils'
import { serversKey } from './servers'

const SERVER_NAME = /^[a-z0-9][a-z0-9-]{1,31}$/

export function CreateServerDialog({ open, onOpenChange }: { open: boolean; onOpenChange(open: boolean): void }) {
  const { t } = useTranslation()
  const id = useId()
  const queryClient = useQueryClient()
  const [name, setName] = useState('')
  const trimmed = name.trim()
  const invalid = trimmed !== '' && !SERVER_NAME.test(trimmed)

  const create = useMutation({
    mutationFn: () => unwrap(api.POST('/servers', { body: { name: trimmed } })),
    onSuccess: (server) => {
      toast.success(t('dashboard.created', { name: server.name }))
      setName('')
      onOpenChange(false)
      void queryClient.invalidateQueries({ queryKey: serversKey })
    },
  })

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) create.reset()
        onOpenChange(next)
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('dashboard.create_server')}</DialogTitle>
          <DialogDescription>{t('dashboard.create_description')}</DialogDescription>
        </DialogHeader>
        <form
          className="space-y-4"
          noValidate
          onSubmit={(e) => {
            e.preventDefault()
            if (trimmed && !invalid) create.mutate()
          }}
        >
          <div className="space-y-2">
            <Label htmlFor={id}>{t('dashboard.name_label')}</Label>
            <Input
              id={id}
              value={name}
              onChange={(e) => setName(e.target.value)}
              autoCapitalize="none"
              spellCheck={false}
              maxLength={32}
              aria-invalid={invalid || undefined}
              aria-describedby={`${id}-hint`}
            />
            <p id={`${id}-hint`} className={cn('text-sm', invalid ? 'text-destructive' : 'text-muted-foreground')}>
              {invalid ? t('dashboard.name_invalid') : t('dashboard.name_hint')}
            </p>
          </div>
          {create.error ? (
            <p role="alert" className="text-destructive text-sm">
              {errorMessage(create.error, t)}
            </p>
          ) : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" disabled={create.isPending || !trimmed || invalid}>
              {t('common.create')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
```

- [ ] **Step 6: `web/src/features/dashboard/dashboard-page.tsx`**

```tsx
import { useQuery } from '@tanstack/react-query'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { PageHeader } from '@/components/page-header'
import { Button } from '@/components/ui/button'
import { useMe } from '@/features/auth/me'
import { errorMessage } from '@/lib/error-message'
import { CreateServerDialog } from './create-server-dialog'
import { ServerCard } from './server-card'
import { serversQueryOptions } from './servers'

export function DashboardPage() {
  const { t } = useTranslation()
  const me = useMe()
  const isAdmin = me?.role === 'admin'
  const [creating, setCreating] = useState(false)
  const servers = useQuery(serversQueryOptions)

  let content: ReactNode
  if (servers.isPending) {
    content = <p className="text-muted-foreground">{t('common.loading')}</p>
  } else if (servers.isError) {
    content = (
      <p role="alert" className="text-destructive">
        {errorMessage(servers.error, t)}
      </p>
    )
  } else if (servers.data.length === 0) {
    content = <p className="text-muted-foreground">{t('dashboard.empty')}</p>
  } else {
    content = (
      <ul className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {servers.data.map((server) => (
          <li key={server.id}>
            <ServerCard server={server} canDelete={isAdmin} />
          </li>
        ))}
      </ul>
    )
  }

  return (
    <>
      <PageHeader
        title={t('dashboard.title')}
        description={t('dashboard.description')}
        actions={
          isAdmin ? (
            <Button type="button" onClick={() => setCreating(true)}>
              {t('dashboard.create_server')}
            </Button>
          ) : undefined
        }
      />
      {content}
      {isAdmin ? <CreateServerDialog open={creating} onOpenChange={setCreating} /> : null}
    </>
  )
}
```

- [ ] **Step 7: Route und Platzhalter entfernen**

```bash
cd web && rm src/routes/index.tsx src/routes/index.test.tsx
```

```tsx
// web/src/routes/_app/index.tsx
import { createFileRoute } from '@tanstack/react-router'
import { DashboardPage } from '@/features/dashboard/dashboard-page'

export const Route = createFileRoute('/_app/')({ component: DashboardPage })
```

- [ ] **Step 8: Tests, Route-Baum, Typecheck**

Run: `cd web && npx vitest run src/features/dashboard && npx vite build && npm run typecheck && npx vitest run`
Expected: Dashboard-Tests PASS (5); `/` hängt jetzt unter `/_app` (geschützt); alles grün.

- [ ] **Step 9: Commit**

```bash
git add -A web/src/features/dashboard web/src/routes web/src/routeTree.gen.ts
git commit -m "feat(web): add dashboard with server stubs, power actions and admin server management" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 12: Benutzerverwaltung und Rechte-Matrix

**Files:**
- Create: `web/src/features/users/users-page.tsx`, `web/src/features/users/create-user-dialog.tsx`, `web/src/features/users/permission-matrix.ts`, `web/src/features/users/permissions-page.tsx`, `web/src/routes/_app/users/index.tsx`, `web/src/routes/_app/users/$id.permissions.tsx`
- Test: `web/src/features/users/permission-matrix.test.ts`, `web/src/features/users/users-page.test.tsx`, `web/src/features/users/permissions-page.test.tsx`

**Interfaces:**
- Consumes: `api`, `unwrap`, `unwrapVoid`, `useErrorToast`, `errorMessage` (Task 5); `useMe` (Task 7); `adminGuard`, `guardBeforeLoad` (Task 7/10); `serversQueryOptions` (Task 11); `DataList`, `NativeSelect`, `ConfirmDialog`, `PageHeader` (Task 2); `useFormat` (Task 1); `ALL_PERMISSIONS` (Task 1)
- Produces:
```ts
export const usersKey: readonly ['users']
export function UsersPage(): JSX.Element
export function CreateUserDialog(props: { open: boolean; onOpenChange(open: boolean): void }): JSX.Element
export type Matrix = Record<number, Permission[]>
export function toMatrix(grants: PermissionGrant[]): Matrix
export function fromMatrix(matrix: Matrix): PermissionGrant[]
export function toggle(matrix: Matrix, serverId: number, permission: Permission, on: boolean): Matrix
export function sameMatrix(a: Matrix, b: Matrix): boolean
export function UserPermissionsPage(props: { userId: number }): JSX.Element
// Routen: /users, /users/$id/permissions (beide adminGuard)
```

- [ ] **Step 1: Failing Tests**

`web/src/features/users/permission-matrix.test.ts`:
```ts
import { describe, expect, it } from 'vitest'
import { fromMatrix, sameMatrix, toggle, toMatrix } from './permission-matrix'

describe('permission matrix', () => {
  it('round-trips grants and drops empty servers', () => {
    const grants = [
      { server_id: 2, permissions: ['server.view' as const] },
      { server_id: 1, permissions: ['server.power' as const, 'server.view' as const] },
      { server_id: 3, permissions: [] },
    ]
    expect(fromMatrix(toMatrix(grants))).toEqual([
      { server_id: 1, permissions: ['server.view', 'server.power'] },
      { server_id: 2, permissions: ['server.view'] },
    ])
  })

  it('adds server.view whenever another permission is granted', () => {
    expect(toggle({}, 1, 'server.power', true)).toEqual({ 1: ['server.view', 'server.power'] })
  })

  it('removes every permission of a server when server.view is revoked', () => {
    expect(toggle({ 1: ['server.view', 'server.power'] }, 1, 'server.view', false)).toEqual({ 1: [] })
  })

  it('removes a single non-view permission', () => {
    expect(toggle({ 1: ['server.view', 'server.power'] }, 1, 'server.power', false)).toEqual({ 1: ['server.view'] })
  })

  it('compares matrices independent of order and empty entries', () => {
    expect(sameMatrix({ 1: ['server.power', 'server.view'], 2: [] }, { 1: ['server.view', 'server.power'] })).toBe(true)
    expect(sameMatrix({ 1: ['server.view'] }, {})).toBe(false)
  })
})
```

`web/src/features/users/users-page.test.tsx`:
```tsx
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { db, loginAs, MOCK_TOTP_CODE } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { UsersPage } from './users-page'

describe('UsersPage', () => {
  it('lists users with role, 2FA state and marks the current user', async () => {
    await renderWithProviders(<UsersPage />, { me: loginAs('admin') })
    const table = await screen.findByRole('table', { name: 'Benutzerliste' })
    const adminRow = within(table).getByRole('row', { name: /admin \(du\)/ })
    expect(within(adminRow).getByText('TOTP')).toBeInTheDocument()
    expect(within(adminRow).getByText('Passkeys: 1')).toBeInTheDocument()
    const aliceRow = within(table).getByRole('row', { name: /alice/ })
    expect(within(aliceRow).getByRole('combobox', { name: 'Rolle von alice' })).toHaveValue('user')
    expect(within(aliceRow).getByText('keine')).toBeInTheDocument()
  })

  it('creates a user after sudo confirmation', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<UsersPage />, { me: loginAs('admin') })
    await user.click(await screen.findByRole('button', { name: 'Benutzer anlegen' }))
    const dialog = screen.getByRole('dialog', { name: 'Benutzer anlegen' })
    await user.type(within(dialog).getByLabelText('Benutzername'), 'carol')
    await user.type(within(dialog).getByLabelText('Initiales Passwort'), 'long-enough-password')
    await user.click(within(dialog).getByRole('button', { name: 'Anlegen' }))
    const sudo = await screen.findByRole('dialog', { name: 'Aktion bestätigen' })
    await user.type(within(sudo).getByLabelText('Code aus der Authenticator-App'), MOCK_TOTP_CODE)
    await user.click(within(sudo).getByRole('button', { name: 'Bestätigen' }))
    expect(await screen.findByRole('row', { name: /carol/ })).toBeInTheDocument()
  })

  it('validates username and password before sending', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<UsersPage />, { me: loginAs('admin', { sudo: true }) })
    await user.click(await screen.findByRole('button', { name: 'Benutzer anlegen' }))
    const dialog = screen.getByRole('dialog', { name: 'Benutzer anlegen' })
    await user.type(within(dialog).getByLabelText('Benutzername'), 'a b')
    await user.type(within(dialog).getByLabelText('Initiales Passwort'), 'short')
    expect(within(dialog).getByText('Der Benutzername entspricht nicht den Regeln.')).toBeInTheDocument()
    expect(within(dialog).getByText('Das Passwort muss 12 bis 256 Zeichen lang sein.')).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Anlegen' })).toBeDisabled()
  })

  it('changes a role', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<UsersPage />, { me: loginAs('admin', { sudo: true }) })
    await user.selectOptions(await screen.findByRole('combobox', { name: 'Rolle von alice' }), 'admin')
    expect(await screen.findByText('Rolle von alice geändert: Administrator')).toBeInTheDocument()
    expect(db.users.find((u) => u.username === 'alice')?.role).toBe('admin')
  })

  it('deletes a user after confirmation and never offers self-deletion', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<UsersPage />, { me: loginAs('admin', { sudo: true }) })
    expect(await screen.findByRole('button', { name: 'admin löschen' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: 'bob löschen' }))
    const confirm = screen.getByRole('dialog', { name: 'Benutzer bob löschen?' })
    await user.click(within(confirm).getByRole('button', { name: 'Löschen' }))
    await waitFor(() => expect(screen.queryByRole('row', { name: /bob/ })).not.toBeInTheDocument())
  })

  it('resets the 2FA of a user', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<UsersPage />, { me: loginAs('admin', { sudo: true }) })
    await user.click(await screen.findByRole('button', { name: '2FA von bob zurücksetzen' }))
    const confirm = screen.getByRole('dialog', { name: '2FA von bob zurücksetzen?' })
    await user.click(within(confirm).getByRole('button', { name: '2FA zurücksetzen' }))
    expect(await screen.findByText('2FA von bob zurückgesetzt.')).toBeInTheDocument()
    expect(db.users.find((u) => u.username === 'bob')?.totpEnabled).toBe(false)
  })
})
```

`web/src/features/users/permissions-page.test.tsx`:
```tsx
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { db, loginAs } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { UserPermissionsPage } from './permissions-page'

describe('UserPermissionsPage', () => {
  it('shows the matrix and saves changed grants', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<UserPermissionsPage userId={2} />, { me: loginAs('admin', { sudo: true }) })
    expect(await screen.findByRole('heading', { name: 'Rechte von alice' })).toBeInTheDocument()
    expect(screen.getByRole('checkbox', { name: 'survival: Starten/Stoppen' })).toBeChecked()
    const lobbyPower = screen.getByRole('checkbox', { name: 'lobby: Starten/Stoppen' })
    expect(lobbyPower).not.toBeChecked()
    const save = screen.getByRole('button', { name: 'Speichern' })
    expect(save).toBeDisabled()

    await user.click(lobbyPower)
    expect(screen.getByRole('checkbox', { name: 'lobby: Ansehen' })).toBeChecked()
    expect(screen.getByText('Ungespeicherte Änderungen')).toBeInTheDocument()
    await user.click(save)

    expect(await screen.findByText('Rechte gespeichert.')).toBeInTheDocument()
    expect(db.grants.get(2)).toContainEqual({ server_id: 3, permissions: ['server.view', 'server.power'] })
    expect(screen.getByRole('button', { name: 'Speichern' })).toBeDisabled()
  })

  it('disables the matrix for administrators', async () => {
    await renderWithProviders(<UserPermissionsPage userId={3} />, { me: loginAs('admin') })
    expect(await screen.findByText('Administratoren haben automatisch alle Rechte auf allen Servern.')).toBeInTheDocument()
    expect(screen.getByRole('checkbox', { name: 'survival: Ansehen' })).toBeDisabled()
  })
})
```

- [ ] **Step 2: Tests laufen lassen → FAIL**

Run: `cd web && npx vitest run src/features/users`
Expected: FAIL – Module fehlen.

- [ ] **Step 3: `web/src/features/users/permission-matrix.ts`**

```ts
import { ALL_PERMISSIONS, type Permission, type PermissionGrant } from '@/api/types'

export type Matrix = Record<number, Permission[]>

function ordered(perms: Iterable<Permission>): Permission[] {
  const set = new Set(perms)
  return ALL_PERMISSIONS.filter((p) => set.has(p))
}

export function toMatrix(grants: PermissionGrant[]): Matrix {
  const matrix: Matrix = {}
  for (const grant of grants) matrix[grant.server_id] = ordered(grant.permissions)
  return matrix
}

export function fromMatrix(matrix: Matrix): PermissionGrant[] {
  return Object.entries(matrix)
    .map(([serverId, perms]) => ({ server_id: Number(serverId), permissions: ordered(perms) }))
    .filter((g) => g.permissions.length > 0)
    .sort((a, b) => a.server_id - b.server_id)
}

/** Stream-D semantics: every permission implies server.view; revoking view revokes everything on that server. */
export function toggle(matrix: Matrix, serverId: number, permission: Permission, on: boolean): Matrix {
  const current = new Set(matrix[serverId] ?? [])
  if (on) {
    current.add(permission)
    current.add('server.view')
  } else if (permission === 'server.view') {
    current.clear()
  } else {
    current.delete(permission)
  }
  return { ...matrix, [serverId]: ordered(current) }
}

export function sameMatrix(a: Matrix, b: Matrix): boolean {
  return JSON.stringify(fromMatrix(a)) === JSON.stringify(fromMatrix(b))
}
```

- [ ] **Step 4: `web/src/features/users/create-user-dialog.tsx`**

```tsx
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { Role } from '@/api/types'
import { NativeSelect } from '@/components/native-select'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { errorMessage } from '@/lib/error-message'
import { cn } from '@/lib/utils'

export const usersKey = ['users'] as const

const USERNAME = /^[a-zA-Z0-9_.-]{3,32}$/

export function CreateUserDialog({ open, onOpenChange }: { open: boolean; onOpenChange(open: boolean): void }) {
  const { t } = useTranslation()
  const id = useId()
  const queryClient = useQueryClient()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [role, setRole] = useState<Role>('user')
  const usernameInvalid = username !== '' && !USERNAME.test(username)
  const passwordInvalid = password !== '' && (password.length < 12 || password.length > 256)
  const complete = username !== '' && password !== '' && !usernameInvalid && !passwordInvalid

  const create = useMutation({
    mutationFn: () => unwrap(api.POST('/users', { body: { username, password, role } })),
    onSuccess: (created) => {
      toast.success(t('users.created', { name: created.username }))
      setUsername('')
      setPassword('')
      setRole('user')
      onOpenChange(false)
      void queryClient.invalidateQueries({ queryKey: usersKey })
    },
  })

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) create.reset()
        onOpenChange(next)
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('users.create')}</DialogTitle>
          <DialogDescription>{t('users.create_description')}</DialogDescription>
        </DialogHeader>
        <form
          className="space-y-4"
          noValidate
          onSubmit={(e) => {
            e.preventDefault()
            if (complete) create.mutate()
          }}
        >
          <div className="space-y-2">
            <Label htmlFor={`${id}-username`}>{t('users.username')}</Label>
            <Input
              id={`${id}-username`}
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              autoComplete="off"
              autoCapitalize="none"
              spellCheck={false}
              maxLength={32}
              aria-invalid={usernameInvalid || undefined}
              aria-describedby={`${id}-username-hint`}
            />
            <p id={`${id}-username-hint`} className={cn('text-sm', usernameInvalid ? 'text-destructive' : 'text-muted-foreground')}>
              {usernameInvalid ? t('users.username_invalid') : t('users.username_hint')}
            </p>
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${id}-password`}>{t('users.password')}</Label>
            <Input
              id={`${id}-password`}
              type="password"
              autoComplete="new-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              maxLength={256}
              aria-invalid={passwordInvalid || undefined}
              aria-describedby={`${id}-password-hint`}
            />
            <p id={`${id}-password-hint`} className={cn('text-sm', passwordInvalid ? 'text-destructive' : 'text-muted-foreground')}>
              {passwordInvalid ? t('users.password_invalid') : t('users.password_hint')}
            </p>
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${id}-role`}>{t('users.role_label')}</Label>
            <NativeSelect id={`${id}-role`} className="w-full" value={role} onChange={(e) => setRole(e.target.value as Role)}>
              <option value="user">{t('users.role.user')}</option>
              <option value="admin">{t('users.role.admin')}</option>
            </NativeSelect>
          </div>
          {create.error ? (
            <p role="alert" className="text-destructive text-sm">
              {errorMessage(create.error, t)}
            </p>
          ) : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" disabled={create.isPending || !complete}>
              {t('common.create')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
```

- [ ] **Step 5: `web/src/features/users/users-page.tsx`**

```tsx
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap, unwrapVoid } from '@/api/errors'
import type { Role, User } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { DataList, type Column } from '@/components/data-list'
import { NativeSelect } from '@/components/native-select'
import { PageHeader } from '@/components/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { useMe } from '@/features/auth/me'
import { errorMessage, useErrorToast } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import { CreateUserDialog, usersKey } from './create-user-dialog'

export { usersKey }

export function UsersPage() {
  const { t } = useTranslation()
  const me = useMe()
  const fmt = useFormat()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const [creating, setCreating] = useState(false)
  const [deleting, setDeleting] = useState<User | null>(null)
  const [resetting, setResetting] = useState<User | null>(null)
  const users = useQuery({ queryKey: usersKey, queryFn: () => unwrap(api.GET('/users')) })
  const invalidate = () => queryClient.invalidateQueries({ queryKey: usersKey })

  const changeRole = useMutation({
    mutationFn: ({ id, role }: { id: number; role: Role }) => unwrap(api.PATCH('/users/{id}', { params: { path: { id } }, body: { role } })),
    onSuccess: (updated) => {
      toast.success(t('users.role_changed', { name: updated.username, role: t(`users.role.${updated.role}`) }))
      void invalidate()
    },
    onError: (err) => {
      notifyError(err)
      void invalidate()
    },
  })
  const remove = useMutation({
    mutationFn: (u: User) => unwrapVoid(api.DELETE('/users/{id}', { params: { path: { id: u.id } } })),
    onSuccess: (_data, u) => {
      setDeleting(null)
      toast.success(t('users.deleted', { name: u.username }))
      void invalidate()
    },
    onError: notifyError,
  })
  const reset2fa = useMutation({
    mutationFn: (u: User) => unwrapVoid(api.POST('/users/{id}/reset-2fa', { params: { path: { id: u.id } } })),
    onSuccess: (_data, u) => {
      setResetting(null)
      toast.success(t('users.reset_done', { name: u.username }))
      void invalidate()
    },
    onError: notifyError,
  })

  const columns: Column<User>[] = [
    {
      id: 'username',
      header: t('users.col.username'),
      cell: (u) => (
        <span className="font-medium">
          {u.username}
          {u.id === me?.id ? ` ${t('users.you')}` : ''}
        </span>
      ),
    },
    {
      id: 'role',
      header: t('users.col.role'),
      cell: (u) => (
        <NativeSelect
          aria-label={t('users.role_for', { name: u.username })}
          value={u.role}
          disabled={u.id === me?.id || changeRole.isPending}
          onChange={(e) => changeRole.mutate({ id: u.id, role: e.target.value as Role })}
        >
          <option value="admin">{t('users.role.admin')}</option>
          <option value="user">{t('users.role.user')}</option>
        </NativeSelect>
      ),
    },
    {
      id: 'twofa',
      header: t('users.col.twofa'),
      cell: (u) =>
        !u.totp_enabled && u.passkey_count === 0 ? (
          <Badge variant="outline">{t('users.twofa_none')}</Badge>
        ) : (
          <span className="flex flex-wrap gap-1">
            {u.totp_enabled ? <Badge variant="secondary">{t('users.twofa_totp')}</Badge> : null}
            {u.passkey_count > 0 ? <Badge variant="secondary">{t('users.twofa_passkeys', { n: u.passkey_count })}</Badge> : null}
          </span>
        ),
    },
    {
      id: 'last_login',
      header: t('users.col.last_login'),
      cell: (u) => (u.last_login_at ? fmt.dateTime(u.last_login_at) : t('users.never')),
    },
    {
      id: 'actions',
      header: t('users.col.actions'),
      cell: (u) => (
        <div className="flex flex-wrap gap-2">
          <Button asChild size="sm" variant="outline">
            <Link to="/users/$id/permissions" params={{ id: String(u.id) }} aria-label={t('users.permissions_for', { name: u.username })}>
              {t('users.permissions')}
            </Link>
          </Button>
          <Button
            type="button"
            size="sm"
            variant="outline"
            aria-label={t('users.reset_for', { name: u.username })}
            disabled={!u.totp_enabled && u.passkey_count === 0}
            onClick={() => setResetting(u)}
          >
            {t('users.reset_2fa')}
          </Button>
          <Button
            type="button"
            size="sm"
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
  ]

  return (
    <>
      <PageHeader
        title={t('users.title')}
        description={t('users.description')}
        actions={
          <Button type="button" onClick={() => setCreating(true)}>
            {t('users.create')}
          </Button>
        }
      />
      {users.isPending ? <p className="text-muted-foreground">{t('common.loading')}</p> : null}
      {users.isError ? (
        <p role="alert" className="text-destructive">
          {errorMessage(users.error, t)}
        </p>
      ) : null}
      {users.data ? <DataList items={users.data} columns={columns} rowKey={(u) => u.id} caption={t('users.caption')} empty={t('common.loading')} /> : null}
      <CreateUserDialog open={creating} onOpenChange={setCreating} />
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={t('users.delete_title', { name: deleting?.username ?? '' })}
        description={t('users.delete_description')}
        confirmLabel={t('common.delete')}
        destructive
        pending={remove.isPending}
        onConfirm={() => deleting && remove.mutate(deleting)}
      />
      <ConfirmDialog
        open={resetting !== null}
        onOpenChange={(open) => !open && setResetting(null)}
        title={t('users.reset_title', { name: resetting?.username ?? '' })}
        description={t('users.reset_description')}
        confirmLabel={t('users.reset_2fa')}
        destructive
        pending={reset2fa.isPending}
        onConfirm={() => resetting && reset2fa.mutate(resetting)}
      />
    </>
  )
}
```

- [ ] **Step 6: `web/src/features/users/permissions-page.tsx`**

```tsx
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import { ALL_PERMISSIONS, type Server } from '@/api/types'
import { DataList, type Column } from '@/components/data-list'
import { PageHeader } from '@/components/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { serversQueryOptions } from '@/features/dashboard/servers'
import { errorMessage, useErrorToast } from '@/lib/error-message'
import { fromMatrix, sameMatrix, toggle, toMatrix, type Matrix } from './permission-matrix'

export function UserPermissionsPage({ userId }: { userId: number }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const permissionsKey = ['users', userId, 'permissions'] as const
  const user = useQuery({ queryKey: ['users', userId], queryFn: () => unwrap(api.GET('/users/{id}', { params: { path: { id: userId } } })) })
  const servers = useQuery(serversQueryOptions)
  const permissions = useQuery({
    queryKey: permissionsKey,
    queryFn: () => unwrap(api.GET('/users/{id}/permissions', { params: { path: { id: userId } } })),
  })
  // Local edits only; the saved matrix is always derived from the query (no effect-based syncing).
  const [draft, setDraft] = useState<Matrix | null>(null)
  const saved = permissions.data ? toMatrix(permissions.data.grants) : null
  const current = draft ?? saved ?? {}
  const dirty = draft !== null && saved !== null && !sameMatrix(draft, saved)

  const save = useMutation({
    mutationFn: (matrix: Matrix) =>
      unwrap(api.PUT('/users/{id}/permissions', { params: { path: { id: userId } }, body: { grants: fromMatrix(matrix) } })),
    onSuccess: (result) => {
      queryClient.setQueryData(permissionsKey, result)
      setDraft(null)
      toast.success(t('permissions.saved'))
    },
    onError: notifyError,
  })

  if (user.isPending || servers.isPending || permissions.isPending) {
    return <p className="text-muted-foreground">{t('common.loading')}</p>
  }
  if (user.isError || servers.isError || permissions.isError) {
    return (
      <p role="alert" className="text-destructive">
        {errorMessage(user.error ?? servers.error ?? permissions.error, t)}
      </p>
    )
  }

  const targetIsAdmin = user.data.role === 'admin'
  const columns: Column<Server>[] = [
    { id: 'server', header: t('permissions.col_server'), cell: (s) => <span className="font-medium">{s.name}</span> },
    ...ALL_PERMISSIONS.map(
      (p): Column<Server> => ({
        id: p,
        header: t(`permission.${p}`),
        cell: (s) => (
          <input
            type="checkbox"
            className="accent-primary size-4"
            aria-label={t('permissions.checkbox', { server: s.name, permission: t(`permission.${p}`) })}
            checked={targetIsAdmin || (current[s.id]?.includes(p) ?? false)}
            disabled={targetIsAdmin || save.isPending}
            onChange={(e) => setDraft(toggle(current, s.id, p, e.target.checked))}
          />
        ),
      }),
    ),
  ]

  return (
    <>
      <PageHeader
        title={t('permissions.title', { name: user.data.username })}
        description={t('permissions.description')}
        actions={
          <>
            <Button asChild variant="outline">
              <Link to="/users">{t('permissions.back')}</Link>
            </Button>
            <Button type="button" disabled={!dirty || save.isPending || targetIsAdmin} onClick={() => save.mutate(current)}>
              {t('common.save')}
            </Button>
          </>
        }
      />
      {targetIsAdmin ? <p className="text-muted-foreground mb-4 text-sm">{t('permissions.admin_note')}</p> : null}
      {dirty ? (
        <Badge variant="outline" className="mb-4">
          {t('permissions.unsaved')}
        </Badge>
      ) : null}
      <DataList items={servers.data} columns={columns} rowKey={(s) => s.id} caption={t('permissions.caption')} empty={t('permissions.no_servers')} />
    </>
  )
}
```

- [ ] **Step 7: Route-Dateien**

```tsx
// web/src/routes/_app/users/index.tsx
import { createFileRoute } from '@tanstack/react-router'
import { adminGuard } from '@/features/auth/guards'
import { guardBeforeLoad } from '@/features/auth/route-guards'
import { UsersPage } from '@/features/users/users-page'

export const Route = createFileRoute('/_app/users/')({
  beforeLoad: guardBeforeLoad(adminGuard),
  component: UsersPage,
})
```

```tsx
// web/src/routes/_app/users/$id.permissions.tsx
import { createFileRoute } from '@tanstack/react-router'
import { adminGuard } from '@/features/auth/guards'
import { guardBeforeLoad } from '@/features/auth/route-guards'
import { UserPermissionsPage } from '@/features/users/permissions-page'

export const Route = createFileRoute('/_app/users/$id/permissions')({
  beforeLoad: guardBeforeLoad(adminGuard),
  component: PermissionsRoute,
})

function PermissionsRoute() {
  const { id } = Route.useParams()
  return <UserPermissionsPage userId={Number(id)} />
}
```

- [ ] **Step 8: Tests laufen lassen → PASS**

Run: `cd web && npx vitest run src/features/users`
Expected: PASS (matrix 5, users 6, permissions 2).

- [ ] **Step 9: Route-Baum, Typecheck, Suite, Commit**

Run: `cd web && npx vite build && npm run typecheck && npx vitest run`
Expected: grün; `routeTree.gen.ts` enthält `/users/` und `/users/$id/permissions`.

```bash
git add web/src/features/users web/src/routes web/src/routeTree.gen.ts
git commit -m "feat(web): add user management and per-server permission matrix" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 13: Security Center – Balkendiagramm, Übersicht, Agent-Status

**Files:**
- Create: `web/src/features/security/bar-chart.tsx`, `web/src/features/security/agent-status-card.tsx`, `web/src/features/security/overview-tab.tsx`
- Test: `web/src/features/security/bar-chart.test.tsx`, `web/src/features/security/agent-status-card.test.tsx`, `web/src/features/security/overview-tab.test.tsx`

**Interfaces:**
- Consumes: `api`, `unwrap`, `errorMessage` (Task 5); `useFormat` (Task 1); shadcn `Card*`, `Badge`, `Button`
- Produces:
```ts
export function niceMax(value: number): number
export interface ChartLabels { time: string; success: string; failure: string }
export function LoginBarChart(props: { data: SeriesPoint[]; title: string; formatTick(t: number): string; labels: ChartLabels }): JSX.Element
export function AgentStatusCard(): JSX.Element
export function OverviewTab(): JSX.Element
```
Diagramm-Regeln (handgebautes SVG, keine Chart-Bibliothek): zwei Serien gestapelt (Erfolg unten `--chart-2`, Fehler oben `--chart-1`), 2 px Abstand zwischen Segmenten, Radius 2, eine y-Achse mit Gitterlinien bei 0/50/100 %, höchstens ~6 x-Beschriftungen, Legende immer sichtbar, pro Balken `<title>` als Hover-Tooltip über eine Trefferfläche in voller Spaltenbreite, `role="img"` mit Titel + Summen-Beschreibung und eine `sr-only`-Datentabelle.

- [ ] **Step 1: Failing Tests**

`web/src/features/security/bar-chart.test.tsx`:
```tsx
import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { LoginBarChart, niceMax } from './bar-chart'

const labels = { time: 'Zeit', success: 'Erfolgreich', failure: 'Fehlgeschlagen' }
const data = [
  { t: 1, success: 0, failure: 10 },
  { t: 2, success: 5, failure: 0 },
  { t: 3, success: 3, failure: 2 },
]

describe('niceMax', () => {
  it('rounds up to 1/2/5 × 10^n', () => {
    expect([0, 7, 10, 12, 45, 100, 101].map(niceMax)).toEqual([1, 10, 10, 20, 50, 100, 200])
  })
})

describe('LoginBarChart', () => {
  it('exposes title and totals to assistive technology', () => {
    render(<LoginBarChart data={data} title="Login-Versuche" formatTick={(t) => `T${t}`} labels={labels} />)
    expect(screen.getByRole('img', { name: 'Login-Versuche Erfolgreich: 8, Fehlgeschlagen: 12' })).toBeInTheDocument()
    const table = screen.getByRole('table', { name: 'Login-Versuche' })
    expect(within(table).getAllByRole('row')).toHaveLength(4)
  })

  it('scales stacked bars against a nice maximum with a 2px gap', () => {
    render(<LoginBarChart data={data} title="Login-Versuche" formatTick={(t) => `T${t}`} labels={labels} />)
    const bars = screen.getAllByTestId('chart-bar')
    expect(bars).toHaveLength(3)
    expect(bars[0].querySelector('[data-series="failure"]')).toHaveAttribute('height', '180')
    expect(bars[0].querySelector('[data-series="success"]')).toBeNull()
    expect(bars[1].querySelector('[data-series="success"]')).toHaveAttribute('height', '90')
    expect(bars[2].querySelector('[data-series="failure"]')).toHaveAttribute('y', '100')
  })

  it('shows a legend for both series', () => {
    render(<LoginBarChart data={data} title="Login-Versuche" formatTick={(t) => `T${t}`} labels={labels} />)
    const legend = screen.getByTestId('chart-legend')
    expect(within(legend).getByText('Erfolgreich')).toBeInTheDocument()
    expect(within(legend).getByText('Fehlgeschlagen')).toBeInTheDocument()
  })
})
```

`web/src/features/security/agent-status-card.test.tsx`:
```tsx
import { screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { db, loginAs } from '@/mocks/db'
import { server } from '@/mocks/node'
import { renderWithProviders } from '@/test/render'
import { AgentStatusCard } from './agent-status-card'

describe('AgentStatusCard', () => {
  it('shows firewall backend, version and pending syncs', async () => {
    await renderWithProviders(<AgentStatusCard />, { me: loginAs('admin') })
    expect(await screen.findByText('Erreichbar')).toBeInTheDocument()
    expect(screen.getByText('noop')).toBeInTheDocument()
    expect(screen.getByText('0.1.0-dev')).toBeInTheDocument()
  })

  it('degrades gracefully when the agent is unreachable', async () => {
    db.agent = { reachable: false, firewall: '', version: '', pending_sync: 3 }
    await renderWithProviders(<AgentStatusCard />, { me: loginAs('admin') })
    expect(await screen.findByText('Nicht erreichbar')).toBeInTheDocument()
    expect(screen.getByText(/Sperren wirken weiterhin in der App/)).toBeInTheDocument()
  })

  it('treats 503 agent_unavailable like an unreachable agent', async () => {
    server.use(http.get('*/api/security/agent', () => HttpResponse.json({ error: 'agent_unavailable', message: 'down' }, { status: 503 })))
    await renderWithProviders(<AgentStatusCard />, { me: loginAs('admin') })
    expect(await screen.findByText('Nicht erreichbar')).toBeInTheDocument()
  })
})
```

`web/src/features/security/overview-tab.test.tsx`:
```tsx
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { loginAs } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { OverviewTab } from './overview-tab'

describe('OverviewTab', () => {
  it('shows KPIs, the 24h chart and top IPs', async () => {
    await renderWithProviders(<OverviewTab />, { me: loginAs('admin') })
    expect(await screen.findByRole('img', { name: /Login-Versuche \(24 Stunden\)/ })).toBeInTheDocument()
    expect(screen.getAllByTestId('chart-bar')).toHaveLength(24)
    expect(within(screen.getByRole('region', { name: 'Fehlversuche' })).getByText('4')).toBeInTheDocument()
    expect(within(screen.getByRole('region', { name: 'Aktive Jails' })).getByText('2')).toBeInTheDocument()
    expect(within(screen.getByRole('list', { name: 'Top-IPs (Fehlversuche)' })).getAllByRole('listitem')).toHaveLength(4)
  })

  it('switches the range', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<OverviewTab />, { me: loginAs('admin') })
    await screen.findByRole('img', { name: /24 Stunden/ })
    const button = screen.getByRole('button', { name: '7 Tage' })
    await user.click(button)
    expect(button).toHaveAttribute('aria-pressed', 'true')
    await waitFor(() => expect(screen.getAllByTestId('chart-bar')).toHaveLength(7))
  })
})
```

- [ ] **Step 2: Tests laufen lassen → FAIL**

Run: `cd web && npx vitest run src/features/security`
Expected: FAIL – Module fehlen.

- [ ] **Step 3: `web/src/features/security/bar-chart.tsx`**

```tsx
import { useId } from 'react'
import type { SeriesPoint } from '@/api/types'

const W = 640
const H = 220
const M = { top: 12, right: 8, bottom: 28, left: 36 }
const INNER_W = W - M.left - M.right
const INNER_H = H - M.top - M.bottom
const BASELINE = M.top + INNER_H
const GAP = 2

export function niceMax(value: number): number {
  if (value <= 0) return 1
  const exp = 10 ** Math.floor(Math.log10(value))
  const f = value / exp
  const nice = f <= 1 ? 1 : f <= 2 ? 2 : f <= 5 ? 5 : 10
  return nice * exp
}

export interface ChartLabels {
  time: string
  success: string
  failure: string
}

interface LoginBarChartProps {
  data: SeriesPoint[]
  title: string
  formatTick(t: number): string
  labels: ChartLabels
}

export function LoginBarChart({ data, title, formatTick, labels }: LoginBarChartProps) {
  const titleId = useId()
  const descId = useId()
  const max = niceMax(Math.max(0, ...data.map((d) => d.success + d.failure)))
  const slot = data.length > 0 ? INNER_W / data.length : INNER_W
  const barW = Math.max(2, Math.min(28, slot * 0.7))
  const scale = (v: number) => (v / max) * INNER_H
  const tickEvery = Math.max(1, Math.ceil(data.length / 6))
  const totals = data.reduce((acc, d) => ({ success: acc.success + d.success, failure: acc.failure + d.failure }), { success: 0, failure: 0 })

  return (
    <figure className="space-y-3">
      <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-labelledby={`${titleId} ${descId}`} className="h-auto w-full">
        <title id={titleId}>{title}</title>
        <desc id={descId}>{`${labels.success}: ${totals.success}, ${labels.failure}: ${totals.failure}`}</desc>
        {[0, 0.5, 1].map((f) => {
          const y = BASELINE - f * INNER_H
          return (
            <g key={f}>
              <line x1={M.left} x2={W - M.right} y1={y} y2={y} className="stroke-border" strokeWidth={1} />
              <text x={M.left - 6} y={y} dy="0.32em" textAnchor="end" className="fill-muted-foreground text-[10px]">
                {Math.round(max * f)}
              </text>
            </g>
          )
        })}
        {data.map((d, i) => {
          const x = M.left + i * slot + (slot - barW) / 2
          const hs = scale(d.success)
          const hf = scale(d.failure)
          const gap = hs > 0 && hf > 0 ? GAP : 0
          return (
            <g key={d.t} data-testid="chart-bar">
              <title>{`${formatTick(d.t)} – ${labels.success}: ${d.success}, ${labels.failure}: ${d.failure}`}</title>
              <rect x={M.left + i * slot} y={M.top} width={slot} height={INNER_H} fill="transparent" />
              {hs > 0 ? <rect data-series="success" x={x} y={BASELINE - hs} width={barW} height={hs} rx={2} className="fill-[var(--chart-2)]" /> : null}
              {hf > 0 ? (
                <rect data-series="failure" x={x} y={BASELINE - hs - gap - hf} width={barW} height={hf} rx={2} className="fill-[var(--chart-1)]" />
              ) : null}
              {i % tickEvery === 0 ? (
                <text x={x + barW / 2} y={H - 8} textAnchor="middle" className="fill-muted-foreground text-[10px]">
                  {formatTick(d.t)}
                </text>
              ) : null}
            </g>
          )
        })}
      </svg>
      <figcaption data-testid="chart-legend" className="flex flex-wrap gap-4 text-sm">
        <span className="flex items-center gap-2">
          <span aria-hidden="true" className="inline-block size-3 rounded-sm bg-[var(--chart-2)]" />
          {labels.success}
        </span>
        <span className="flex items-center gap-2">
          <span aria-hidden="true" className="inline-block size-3 rounded-sm bg-[var(--chart-1)]" />
          {labels.failure}
        </span>
      </figcaption>
      <table className="sr-only">
        <caption>{title}</caption>
        <thead>
          <tr>
            <th scope="col">{labels.time}</th>
            <th scope="col">{labels.success}</th>
            <th scope="col">{labels.failure}</th>
          </tr>
        </thead>
        <tbody>
          {data.map((d) => (
            <tr key={d.t}>
              <td>{formatTick(d.t)}</td>
              <td>{d.success}</td>
              <td>{d.failure}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </figure>
  )
}
```

- [ ] **Step 4: `web/src/features/security/agent-status-card.tsx`**

```tsx
import { useQuery } from '@tanstack/react-query'
import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { useFormat } from '@/lib/format'

/** Agent outages are expected in dev and must never break the page: errors render as "unreachable". */
export function AgentStatusCard() {
  const { t } = useTranslation()
  const fmt = useFormat()
  const headingId = useId()
  const agent = useQuery({
    queryKey: ['security', 'agent'],
    queryFn: () => unwrap(api.GET('/security/agent')),
    refetchInterval: 30_000,
    retry: false,
  })
  const reachable = agent.data?.reachable === true

  return (
    <section aria-labelledby={headingId}>
      <Card className="h-full">
        <CardHeader>
          <div className="flex items-center justify-between gap-2">
            <h3 id={headingId} className="font-semibold">
              {t('security.agent.title')}
            </h3>
            {agent.isPending ? null : (
              <Badge variant={reachable ? 'secondary' : 'outline'}>{reachable ? t('security.agent.reachable') : t('security.agent.unreachable')}</Badge>
            )}
          </div>
        </CardHeader>
        <CardContent className="space-y-2 text-sm">
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
        </CardContent>
      </Card>
    </section>
  )
}
```

- [ ] **Step 5: `web/src/features/security/overview-tab.tsx`**

```tsx
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { SecurityRange } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader } from '@/components/ui/card'
import { errorMessage } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import { AgentStatusCard } from './agent-status-card'
import { LoginBarChart } from './bar-chart'

const RANGES: SecurityRange[] = ['24h', '7d', '30d']

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <section aria-label={label}>
      <Card className="h-full">
        <CardHeader>
          <CardDescription>{label}</CardDescription>
          <p className="text-3xl font-semibold tabular-nums">{value}</p>
        </CardHeader>
      </Card>
    </section>
  )
}

interface ListItem {
  key: string
  label: string
  value: string
}

function ListCard({ title, items, empty }: { title: string; items: ListItem[]; empty: string }) {
  const id = useId()
  return (
    <Card>
      <CardHeader>
        <h3 id={id} className="font-semibold">
          {title}
        </h3>
      </CardHeader>
      <CardContent>
        {items.length === 0 ? (
          <p className="text-muted-foreground text-sm">{empty}</p>
        ) : (
          <ul aria-labelledby={id} className="divide-y text-sm">
            {items.map((item) => (
              <li key={item.key} className="flex justify-between gap-3 py-1.5">
                <span className="min-w-0 break-all">{item.label}</span>
                <span className="text-muted-foreground shrink-0 tabular-nums">{item.value}</span>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  )
}

export function OverviewTab() {
  const { t } = useTranslation()
  const fmt = useFormat()
  const [range, setRange] = useState<SecurityRange>('24h')
  const overview = useQuery({
    queryKey: ['security', 'overview', range],
    queryFn: () => unwrap(api.GET('/security/overview', { params: { query: { range } } })),
    placeholderData: keepPreviousData,
  })
  const dayFormat = new Intl.DateTimeFormat(fmt.locale, { day: '2-digit', month: '2-digit' })
  const formatTick = (ts: number) => (range === '24h' ? fmt.time(ts) : dayFormat.format(new Date(ts)))
  const data = overview.data

  return (
    <div className="space-y-6">
      <div role="group" aria-label={t('security.overview.range_label')} className="flex flex-wrap gap-2">
        {RANGES.map((r) => (
          <Button key={r} type="button" size="sm" variant={r === range ? 'default' : 'outline'} aria-pressed={r === range} onClick={() => setRange(r)}>
            {t(`security.overview.ranges.${r}`)}
          </Button>
        ))}
      </div>
      {overview.isError ? (
        <p role="alert" className="text-destructive">
          {errorMessage(overview.error, t)}
        </p>
      ) : null}
      {!data ? (
        <p className="text-muted-foreground">{t('common.loading')}</p>
      ) : (
        <>
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <Stat label={t('security.overview.failures_total')} value={fmt.number(data.failures_total)} />
            <Stat label={t('security.overview.active_jails')} value={fmt.number(data.active_jails)} />
            <AgentStatusCard />
          </div>
          <Card>
            <CardContent className="pt-6">
              {data.series.length === 0 ? (
                <p className="text-muted-foreground text-sm">{t('security.overview.no_data')}</p>
              ) : (
                <LoginBarChart
                  data={data.series}
                  title={t('security.overview.chart_title', { range: t(`security.overview.ranges.${data.range}`) })}
                  formatTick={formatTick}
                  labels={{ time: t('security.overview.time'), success: t('security.overview.success'), failure: t('security.overview.failure') }}
                />
              )}
            </CardContent>
          </Card>
          <div className="grid gap-4 md:grid-cols-2">
            <ListCard
              title={t('security.overview.top_ips')}
              empty={t('security.overview.no_data')}
              items={data.top_ips.map((ip) => ({
                key: ip.ip_key,
                label: ip.country ? `${ip.ip_key} (${ip.country})` : ip.ip_key,
                value: fmt.number(ip.failures),
              }))}
            />
            <ListCard
              title={t('security.overview.top_countries')}
              empty={t('security.overview.no_countries')}
              items={data.top_countries.map((c) => ({ key: c.country, label: c.country, value: fmt.number(c.failures) }))}
            />
          </div>
          <ListCard
            title={t('security.overview.new_devices')}
            empty={t('security.overview.no_data')}
            items={data.new_device_logins.map((d, i) => ({
              key: `${d.ts}-${i}`,
              label: `${d.username} · ${d.ip}${d.country ? ` (${d.country})` : ''}`,
              value: fmt.dateTime(d.ts),
            }))}
          />
        </>
      )}
    </div>
  )
}
```

- [ ] **Step 6: Tests laufen lassen → PASS**

Run: `cd web && npx vitest run src/features/security`
Expected: PASS (bar-chart 4, agent 3, overview 2).

- [ ] **Step 7: Gesamte Suite + Typecheck, Commit**

Run: `cd web && npx vitest run && npm run typecheck`
Expected: grün.

```bash
git add web/src/features/security
git commit -m "feat(web): add security overview with SVG login chart and agent status" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 14: Security Center – Login-Versuche und Jails

**Files:**
- Create: `web/src/features/security/attempts-tab.tsx`, `web/src/features/security/jails-tab.tsx`, `web/src/features/security/create-jail-dialog.tsx`
- Test: `web/src/features/security/attempts-tab.test.tsx`, `web/src/features/security/jails-tab.test.tsx`

**Interfaces:**
- Consumes: `api`, `unwrap`, `unwrapVoid`, `errorMessage`, `useErrorToast` (Task 5); `DataList`, `NativeSelect` (Task 2); `useFormat` (Task 1); shadcn `Switch`, `Badge`, `Dialog*`, `Input`, `Label`, `Button`
- Produces:
```ts
export function AttemptsTab(): JSX.Element        // Filter Ergebnis + IP, Cursor-Paging „Mehr laden“
export function JailsTab(): JSX.Element           // Nur-aktive-Schalter, Aufheben (Sudo), manuelle Sperre
export function CreateJailDialog(props: { open: boolean; onOpenChange(open: boolean): void }): JSX.Element
export const securityKey: readonly ['security']   // Präfix aller Security-Queries (in jails-tab.ts exportiert)
```

- [ ] **Step 1: Failing Tests**

`web/src/features/security/attempts-tab.test.tsx`:
```tsx
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { loginAs } from '@/mocks/db'
import { setViewport } from '@/test/media'
import { renderWithProviders } from '@/test/render'
import { AttemptsTab } from './attempts-tab'

describe('AttemptsTab', () => {
  it('pages through attempts with "load more"', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<AttemptsTab />, { me: loginAs('admin') })
    const table = await screen.findByRole('table', { name: 'Login-Versuche' })
    expect(within(table).getAllByRole('row')).toHaveLength(51)
    await user.click(screen.getByRole('button', { name: 'Mehr laden' }))
    await waitFor(() => expect(within(screen.getByRole('table', { name: 'Login-Versuche' })).getAllByRole('row')).toHaveLength(101))
  })

  it('filters failures of one IP', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<AttemptsTab />, { me: loginAs('admin') })
    await screen.findByRole('table', { name: 'Login-Versuche' })
    await user.selectOptions(screen.getByLabelText('Ergebnis'), 'failure')
    await user.type(screen.getByLabelText('IP-Adresse'), '203.0.113.9')
    await user.click(screen.getByRole('button', { name: 'Filtern' }))
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Mehr laden' })).not.toBeInTheDocument())
    const rows = within(screen.getByRole('table', { name: 'Login-Versuche' })).getAllByRole('row').slice(1)
    expect(rows.length).toBeGreaterThan(0)
    for (const row of rows) {
      expect(within(row).getByText('Fehlgeschlagen')).toBeInTheDocument()
      expect(within(row).getByText(/203\.0\.113\.9/)).toBeInTheDocument()
    }
  })

  it('renders cards instead of a table on a phone', async () => {
    setViewport('mobile')
    await renderWithProviders(<AttemptsTab />, { me: loginAs('admin') })
    const list = await screen.findByRole('list', { name: 'Login-Versuche' })
    expect(within(list).getAllByRole('listitem')).toHaveLength(50)
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })
})
```

`web/src/features/security/jails-tab.test.tsx`:
```tsx
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { loginAs, MOCK_TOTP_CODE } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { JailsTab } from './jails-tab'

describe('JailsTab', () => {
  it('lists active jails and can include lifted ones', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<JailsTab />, { me: loginAs('admin') })
    const table = await screen.findByRole('table', { name: 'Jails' })
    expect(within(table).getAllByRole('row')).toHaveLength(3)
    await user.click(screen.getByRole('switch', { name: 'Nur aktive' }))
    expect(await screen.findByText('Aufgehoben von admin')).toBeInTheDocument()
  })

  it('lifts a jail after sudo confirmation', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<JailsTab />, { me: loginAs('admin') })
    await user.click(await screen.findByRole('button', { name: 'Sperre für 203.0.113.5/32 aufheben' }))
    const sudo = await screen.findByRole('dialog', { name: 'Aktion bestätigen' })
    await user.type(within(sudo).getByLabelText('Code aus der Authenticator-App'), MOCK_TOTP_CODE)
    await user.click(within(sudo).getByRole('button', { name: 'Bestätigen' }))
    expect(await screen.findByText('Sperre für 203.0.113.5/32 aufgehoben.')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Sperre für 203.0.113.5/32 aufheben' })).not.toBeInTheDocument())
  })

  it('jails an IP manually and refuses the own IP', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<JailsTab />, { me: loginAs('admin', { sudo: true }) })
    await user.click(await screen.findByRole('button', { name: 'IP sperren' }))
    const dialog = screen.getByRole('dialog', { name: 'IP sperren' })
    await user.type(within(dialog).getByLabelText('IP-Adresse'), '198.51.100.7')
    await user.type(within(dialog).getByLabelText('Grund'), 'test')
    await user.click(within(dialog).getByRole('button', { name: 'IP sperren' }))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('refusing to jail your own IP')

    const ip = within(dialog).getByLabelText('IP-Adresse')
    await user.clear(ip)
    await user.type(ip, '203.0.113.77')
    await user.click(within(dialog).getByRole('button', { name: 'IP sperren' }))
    expect(await screen.findByText('203.0.113.77/32 gesperrt.')).toBeInTheDocument()
    expect(await screen.findByRole('row', { name: /203\.0\.113\.77\/32/ })).toBeInTheDocument()
    expect(screen.queryByRole('dialog', { name: 'IP sperren' })).not.toBeInTheDocument()
  })

  it('validates the duration', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<JailsTab />, { me: loginAs('admin', { sudo: true }) })
    await user.click(await screen.findByRole('button', { name: 'IP sperren' }))
    const dialog = screen.getByRole('dialog', { name: 'IP sperren' })
    const duration = within(dialog).getByLabelText('Dauer (Minuten)')
    await user.clear(duration)
    await user.type(duration, '20000')
    expect(within(dialog).getByText('Die Dauer muss zwischen 1 und 10080 Minuten liegen.')).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'IP sperren' })).toBeDisabled()
  })
})
```

- [ ] **Step 2: Tests laufen lassen → FAIL**

Run: `cd web && npx vitest run src/features/security/attempts-tab.test.tsx src/features/security/jails-tab.test.tsx`
Expected: FAIL – Module fehlen.

- [ ] **Step 3: `web/src/features/security/attempts-tab.tsx`**

```tsx
import { useInfiniteQuery } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { LoginAttempt } from '@/api/types'
import { DataList, type Column } from '@/components/data-list'
import { NativeSelect } from '@/components/native-select'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { errorMessage } from '@/lib/error-message'
import { useFormat } from '@/lib/format'

type ResultFilter = 'all' | 'success' | 'failure'

interface Filters {
  result: ResultFilter
  ip: string
}

export function AttemptsTab() {
  const { t } = useTranslation()
  const fmt = useFormat()
  const id = useId()
  const [result, setResult] = useState<ResultFilter>('all')
  const [ipInput, setIpInput] = useState('')
  const [filters, setFilters] = useState<Filters>({ result: 'all', ip: '' })

  const attempts = useInfiniteQuery({
    queryKey: ['security', 'attempts', filters],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) =>
      unwrap(
        api.GET('/security/attempts', {
          params: {
            query: {
              success: filters.result === 'all' ? undefined : filters.result === 'success',
              ip: filters.ip || undefined,
              cursor: pageParam,
              limit: 50,
            },
          },
        }),
      ),
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  })
  const items = attempts.data?.pages.flatMap((page) => page.items) ?? []

  const columns: Column<LoginAttempt>[] = [
    { id: 'time', header: t('security.attempts.col.time'), cell: (a) => fmt.dateTime(a.ts) },
    { id: 'username', header: t('security.attempts.col.username'), cell: (a) => a.username },
    {
      id: 'ip',
      header: t('security.attempts.col.ip'),
      cell: (a) => (
        <span className="font-mono text-xs">
          {a.ip}
          {a.country ? ` (${a.country})` : ''}
        </span>
      ),
    },
    { id: 'stage', header: t('security.attempts.col.stage'), cell: (a) => t(`security.attempts.stage.${a.stage}`) },
    {
      id: 'result',
      header: t('security.attempts.col.result'),
      cell: (a) => (
        <Badge variant={a.success ? 'secondary' : 'destructive'}>
          {a.success ? t('security.attempts.result_success') : t('security.attempts.result_failure')}
        </Badge>
      ),
    },
    { id: 'reason', header: t('security.attempts.col.reason'), cell: (a) => a.reason },
    {
      id: 'user_agent',
      header: t('security.attempts.col.user_agent'),
      cell: (a) => (
        <span className="block max-w-56 truncate" title={a.user_agent}>
          {a.user_agent}
        </span>
      ),
    },
  ]

  return (
    <div className="space-y-4">
      <form
        className="flex flex-col gap-3 sm:flex-row sm:items-end"
        onSubmit={(e) => {
          e.preventDefault()
          setFilters({ result, ip: ipInput.trim() })
        }}
      >
        <div className="space-y-1">
          <Label htmlFor={`${id}-result`}>{t('security.attempts.result_label')}</Label>
          <NativeSelect id={`${id}-result`} className="w-full sm:w-auto" value={result} onChange={(e) => setResult(e.target.value as ResultFilter)}>
            <option value="all">{t('security.attempts.result_all')}</option>
            <option value="success">{t('security.attempts.result_success')}</option>
            <option value="failure">{t('security.attempts.result_failure')}</option>
          </NativeSelect>
        </div>
        <div className="space-y-1">
          <Label htmlFor={`${id}-ip`}>{t('security.attempts.ip_label')}</Label>
          <Input id={`${id}-ip`} value={ipInput} onChange={(e) => setIpInput(e.target.value)} autoComplete="off" spellCheck={false} />
        </div>
        <Button type="submit" variant="secondary">
          {t('common.apply_filters')}
        </Button>
      </form>
      {attempts.isError ? (
        <p role="alert" className="text-destructive">
          {errorMessage(attempts.error, t)}
        </p>
      ) : null}
      {attempts.isPending ? (
        <p className="text-muted-foreground">{t('common.loading')}</p>
      ) : (
        <DataList items={items} columns={columns} rowKey={(a) => a.id} caption={t('security.attempts.caption')} empty={t('security.attempts.empty')} />
      )}
      {attempts.hasNextPage ? (
        <Button type="button" variant="outline" disabled={attempts.isFetchingNextPage} onClick={() => void attempts.fetchNextPage()}>
          {t('common.load_more')}
        </Button>
      ) : null}
    </div>
  )
}
```

- [ ] **Step 4: `web/src/features/security/create-jail-dialog.tsx`**

```tsx
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { errorMessage } from '@/lib/error-message'

export function CreateJailDialog({ open, onOpenChange }: { open: boolean; onOpenChange(open: boolean): void }) {
  const { t } = useTranslation()
  const id = useId()
  const queryClient = useQueryClient()
  const [ip, setIp] = useState('')
  const [duration, setDuration] = useState('60')
  const [reason, setReason] = useState('')
  const minutes = Number(duration)
  const durationInvalid = !Number.isInteger(minutes) || minutes < 1 || minutes > 10080
  const complete = ip.trim() !== '' && !durationInvalid && reason.length <= 200

  const create = useMutation({
    mutationFn: () => unwrap(api.POST('/security/jails', { body: { ip: ip.trim(), duration_minutes: minutes, reason: reason.trim() } })),
    onSuccess: (jail) => {
      toast.success(t('security.jails.created', { ip: jail.ip_key }))
      setIp('')
      setReason('')
      setDuration('60')
      onOpenChange(false)
      void queryClient.invalidateQueries({ queryKey: ['security'] })
    },
  })

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) create.reset()
        onOpenChange(next)
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('security.jails.create')}</DialogTitle>
          <DialogDescription>{t('security.jails.create_description')}</DialogDescription>
        </DialogHeader>
        <form
          className="space-y-4"
          noValidate
          onSubmit={(e) => {
            e.preventDefault()
            if (complete) create.mutate()
          }}
        >
          <div className="space-y-2">
            <Label htmlFor={`${id}-ip`}>{t('security.jails.ip')}</Label>
            <Input id={`${id}-ip`} value={ip} onChange={(e) => setIp(e.target.value)} autoComplete="off" spellCheck={false} />
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${id}-duration`}>{t('security.jails.duration')}</Label>
            <Input
              id={`${id}-duration`}
              type="number"
              inputMode="numeric"
              min={1}
              max={10080}
              value={duration}
              onChange={(e) => setDuration(e.target.value)}
              aria-invalid={durationInvalid || undefined}
            />
            {durationInvalid ? <p className="text-destructive text-sm">{t('security.jails.duration_invalid')}</p> : null}
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${id}-reason`}>{t('security.jails.reason')}</Label>
            <Input id={`${id}-reason`} value={reason} maxLength={200} onChange={(e) => setReason(e.target.value)} />
          </div>
          {create.error ? (
            <p role="alert" className="text-destructive text-sm">
              {errorMessage(create.error, t)}
            </p>
          ) : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" variant="destructive" disabled={create.isPending || !complete}>
              {t('security.jails.create')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
```

- [ ] **Step 5: `web/src/features/security/jails-tab.tsx`**

```tsx
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap, unwrapVoid } from '@/api/errors'
import type { Jail } from '@/api/types'
import { DataList, type Column } from '@/components/data-list'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { errorMessage, useErrorToast } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import { CreateJailDialog } from './create-jail-dialog'

export const securityKey = ['security'] as const

/** "Now" is the moment the list was fetched, so render stays pure. */
function isActive(jail: Jail, fetchedAt: number): boolean {
  return jail.lifted_at === null && jail.expires_at > fetchedAt
}

export function JailsTab() {
  const { t } = useTranslation()
  const fmt = useFormat()
  const id = useId()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const [onlyActive, setOnlyActive] = useState(true)
  const [creating, setCreating] = useState(false)
  const jails = useQuery({
    queryKey: ['security', 'jails', onlyActive],
    queryFn: () => unwrap(api.GET('/security/jails', { params: { query: { active: onlyActive } } })),
  })
  const lift = useMutation({
    mutationFn: (jail: Jail) => unwrapVoid(api.DELETE('/security/jails/{id}', { params: { path: { id: jail.id } } })),
    onSuccess: (_data, jail) => {
      toast.success(t('security.jails.lifted', { ip: jail.ip_key }))
      void queryClient.invalidateQueries({ queryKey: securityKey })
    },
    onError: notifyError,
  })
  const fetchedAt = jails.dataUpdatedAt

  const status = (jail: Jail) => {
    if (jail.lifted_at !== null) return t('security.jails.status_lifted', { by: jail.lifted_by ?? '?' })
    return isActive(jail, fetchedAt) ? t('security.jails.status_active') : t('security.jails.status_expired')
  }

  const columns: Column<Jail>[] = [
    {
      id: 'ip',
      header: t('security.jails.col.ip'),
      cell: (j) => (
        <span className="flex flex-wrap items-center gap-2">
          <span className="font-mono text-xs">{j.ip_key}</span>
          {j.manual ? <Badge variant="outline">{t('security.jails.manual')}</Badge> : null}
        </span>
      ),
    },
    { id: 'reason', header: t('security.jails.col.reason'), cell: (j) => j.reason },
    { id: 'level', header: t('security.jails.col.level'), cell: (j) => fmt.number(j.level) },
    { id: 'since', header: t('security.jails.col.since'), cell: (j) => fmt.dateTime(j.created_at) },
    { id: 'until', header: t('security.jails.col.until'), cell: (j) => fmt.dateTime(j.expires_at) },
    { id: 'status', header: t('security.jails.col.status'), cell: status },
    {
      id: 'firewall',
      header: t('security.jails.col.firewall'),
      cell: (j) => <Badge variant={j.firewall_synced ? 'secondary' : 'outline'}>{j.firewall_synced ? t('security.jails.synced') : t('security.jails.pending')}</Badge>,
    },
    {
      id: 'actions',
      header: '',
      cell: (j) =>
        isActive(j, fetchedAt) ? (
          <Button
            type="button"
            size="sm"
            variant="outline"
            aria-label={t('security.jails.lift_for', { ip: j.ip_key })}
            disabled={lift.isPending}
            onClick={() => lift.mutate(j)}
          >
            {t('security.jails.lift')}
          </Button>
        ) : null,
    },
  ]

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <Switch id={`${id}-active`} checked={onlyActive} onCheckedChange={setOnlyActive} />
          <Label htmlFor={`${id}-active`}>{t('security.jails.only_active')}</Label>
        </div>
        <Button type="button" variant="destructive" onClick={() => setCreating(true)}>
          {t('security.jails.create')}
        </Button>
      </div>
      {jails.isError ? (
        <p role="alert" className="text-destructive">
          {errorMessage(jails.error, t)}
        </p>
      ) : null}
      {jails.isPending ? (
        <p className="text-muted-foreground">{t('common.loading')}</p>
      ) : (
        <DataList items={jails.data ?? []} columns={columns} rowKey={(j) => j.id} caption={t('security.jails.caption')} empty={t('security.jails.empty')} />
      )}
      <CreateJailDialog open={creating} onOpenChange={setCreating} />
    </div>
  )
}
```

- [ ] **Step 6: Tests laufen lassen → PASS**

Run: `cd web && npx vitest run src/features/security`
Expected: PASS (inkl. attempts 3, jails 4).

- [ ] **Step 7: Gesamte Suite + Typecheck, Commit**

Run: `cd web && npx vitest run && npm run typecheck`
Expected: grün.

```bash
git add web/src/features/security
git commit -m "feat(web): add login attempt log with cursor paging and jail management" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 15: Security Center – IP-Regeln und Einstellungen

**Files:**
- Create: `web/src/features/security/ip-rules-tab.tsx`, `web/src/features/security/create-ip-rule-dialog.tsx`, `web/src/features/security/countries.ts`, `web/src/features/security/settings-tab.tsx`
- Test: `web/src/features/security/countries.test.ts`, `web/src/features/security/ip-rules-tab.test.tsx`, `web/src/features/security/settings-tab.test.tsx`

**Interfaces:**
- Consumes: `api`, `unwrap`, `unwrapVoid`, `errorMessage`, `useErrorToast` (Task 5); `DataList`, `NativeSelect`, `ConfirmDialog` (Task 2); `useFormat` (Task 1); `securityKey` (Task 14); shadcn `Switch`, `Badge`, `Dialog*`, `Input`, `Label`, `Button`
- Produces:
```ts
export function IpRulesTab(): JSX.Element
export function CreateIpRuleDialog(props: { open: boolean; onOpenChange(open: boolean): void }): JSX.Element
export function parseCountries(input: string): { codes: string[]; invalid: string[] }
export function formatCountries(codes: string[]): string
export function SettingsTab(): JSX.Element
```

- [ ] **Step 1: Failing Tests**

`web/src/features/security/countries.test.ts`:
```ts
import { describe, expect, it } from 'vitest'
import { formatCountries, parseCountries } from './countries'

describe('parseCountries', () => {
  it('normalises, dedupes and separates invalid entries', () => {
    expect(parseCountries(' de, AT;ch  de\nGermany')).toEqual({ codes: ['DE', 'AT', 'CH'], invalid: ['GERMANY'] })
  })

  it('treats an empty input as "all countries"', () => {
    expect(parseCountries('   ')).toEqual({ codes: [], invalid: [] })
  })

  it('formats codes for the input field', () => {
    expect(formatCountries(['DE', 'AT'])).toBe('DE, AT')
  })
})
```

`web/src/features/security/ip-rules-tab.test.tsx`:
```tsx
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { loginAs } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { IpRulesTab } from './ip-rules-tab'

describe('IpRulesTab', () => {
  it('lists allow and deny rules', async () => {
    await renderWithProviders(<IpRulesTab />, { me: loginAs('admin') })
    const table = await screen.findByRole('table', { name: 'IP-Regeln' })
    const allow = within(table).getByRole('row', { name: /198\.51\.100\.0\/24/ })
    expect(within(allow).getByText('Erlauben')).toBeInTheDocument()
    expect(within(allow).getByText('nie')).toBeInTheDocument()
    expect(within(within(table).getByRole('row', { name: /192\.0\.2\.0\/24/ })).getByText('Sperren')).toBeInTheDocument()
  })

  it('validates CIDR notation and adds a rule', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<IpRulesTab />, { me: loginAs('admin', { sudo: true }) })
    await user.click(await screen.findByRole('button', { name: 'Regel hinzufügen' }))
    const dialog = screen.getByRole('dialog', { name: 'Regel hinzufügen' })
    const cidr = within(dialog).getByLabelText('CIDR')
    await user.type(cidr, '203.0.113.0')
    expect(within(dialog).getByText('Ungültige CIDR-Notation.')).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Regel hinzufügen' })).toBeDisabled()
    await user.type(cidr, '/24')
    await user.selectOptions(within(dialog).getByLabelText('Art'), 'deny')
    await user.type(within(dialog).getByLabelText('Kommentar'), 'abuse')
    await user.click(within(dialog).getByRole('button', { name: 'Regel hinzufügen' }))
    expect(await screen.findByText('Regel 203.0.113.0/24 hinzugefügt.')).toBeInTheDocument()
    expect(await screen.findByRole('row', { name: /203\.0\.113\.0\/24/ })).toBeInTheDocument()
  })

  it('deletes a rule after confirmation', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<IpRulesTab />, { me: loginAs('admin', { sudo: true }) })
    await user.click(await screen.findByRole('button', { name: 'Regel 192.0.2.0/24 löschen' }))
    const confirm = screen.getByRole('dialog', { name: 'Regel 192.0.2.0/24 löschen?' })
    await user.click(within(confirm).getByRole('button', { name: 'Löschen' }))
    await waitFor(() => expect(screen.queryByRole('row', { name: /192\.0\.2\.0\/24/ })).not.toBeInTheDocument())
  })
})
```

`web/src/features/security/settings-tab.test.tsx`:
```tsx
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { db, loginAs } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { SettingsTab } from './settings-tab'

describe('SettingsTab', () => {
  it('saves 2FA enforcement and allowed countries', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<SettingsTab />, { me: loginAs('admin', { sudo: true }) })
    const enforce = await screen.findByRole('switch', { name: 'Zwei-Faktor-Anmeldung für alle Benutzer erzwingen' })
    expect(enforce).toHaveAttribute('aria-checked', 'false')
    const save = screen.getByRole('button', { name: 'Speichern' })
    expect(save).toBeDisabled()
    await user.click(enforce)
    await user.type(screen.getByLabelText('Erlaubte Länder'), 'de, at')
    await user.click(save)
    expect(await screen.findByText('Einstellungen gespeichert.')).toBeInTheDocument()
    expect(db.settings).toEqual({ enforce_2fa_for_users: true, admin_allowlist_only: false, allowed_countries: ['DE', 'AT'] })
  })

  it('blocks invalid country codes', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<SettingsTab />, { me: loginAs('admin', { sudo: true }) })
    await user.type(await screen.findByLabelText('Erlaubte Länder'), 'Germany')
    expect(screen.getByText('Ungültige Ländercodes: GERMANY')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Speichern' })).toBeDisabled()
  })

  it('disables the country filter without GeoIP database', async () => {
    db.geoipAvailable = false
    await renderWithProviders(<SettingsTab />, { me: loginAs('admin') })
    expect(await screen.findByText('Keine GeoIP-Datenbank gefunden – der Länderfilter ist deaktiviert.')).toBeInTheDocument()
    expect(screen.getByLabelText('Erlaubte Länder')).toBeDisabled()
  })

  it('reports a lock-out conflict from the server', async () => {
    const user = userEvent.setup()
    db.ipRules = []
    await renderWithProviders(<SettingsTab />, { me: loginAs('admin', { sudo: true }) })
    await user.click(await screen.findByRole('switch', { name: 'Admin-Zugang nur aus der Allowlist' }))
    await user.click(screen.getByRole('button', { name: 'Speichern' }))
    expect(await screen.findByText(/would lock you out/)).toBeInTheDocument()
    expect(db.settings.admin_allowlist_only).toBe(false)
  })
})
```

- [ ] **Step 2: Tests laufen lassen → FAIL**

Run: `cd web && npx vitest run src/features/security/countries.test.ts src/features/security/ip-rules-tab.test.tsx src/features/security/settings-tab.test.tsx`
Expected: FAIL – Module fehlen.

- [ ] **Step 3: `web/src/features/security/countries.ts`**

```ts
const COUNTRY = /^[A-Z]{2}$/

export function parseCountries(input: string): { codes: string[]; invalid: string[] } {
  const tokens = input
    .split(/[\s,;]+/)
    .map((token) => token.trim().toUpperCase())
    .filter(Boolean)
  const unique = [...new Set(tokens)]
  return { codes: unique.filter((c) => COUNTRY.test(c)), invalid: unique.filter((c) => !COUNTRY.test(c)) }
}

export function formatCountries(codes: string[]): string {
  return codes.join(', ')
}
```

- [ ] **Step 4: `web/src/features/security/create-ip-rule-dialog.tsx`**

```tsx
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { IPRuleKind } from '@/api/types'
import { NativeSelect } from '@/components/native-select'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { errorMessage } from '@/lib/error-message'

const CIDR = /^[0-9a-fA-F:.]+\/\d{1,3}$/

export function CreateIpRuleDialog({ open, onOpenChange }: { open: boolean; onOpenChange(open: boolean): void }) {
  const { t } = useTranslation()
  const id = useId()
  const queryClient = useQueryClient()
  const [cidr, setCidr] = useState('')
  const [kind, setKind] = useState<IPRuleKind>('allow')
  const [comment, setComment] = useState('')
  const [expires, setExpires] = useState('')
  const trimmed = cidr.trim()
  const cidrInvalid = trimmed !== '' && !CIDR.test(trimmed)
  const complete = trimmed !== '' && !cidrInvalid

  const create = useMutation({
    mutationFn: () =>
      unwrap(
        api.POST('/security/ip-rules', {
          body: { cidr: trimmed, kind, comment: comment.trim(), expires_at: expires ? new Date(expires).getTime() : null },
        }),
      ),
    onSuccess: (rule) => {
      toast.success(t('security.ip_rules.created', { cidr: rule.cidr }))
      setCidr('')
      setComment('')
      setExpires('')
      setKind('allow')
      onOpenChange(false)
      void queryClient.invalidateQueries({ queryKey: ['security', 'ip-rules'] })
    },
  })

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) create.reset()
        onOpenChange(next)
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('security.ip_rules.create')}</DialogTitle>
          <DialogDescription>{t('security.ip_rules.create_description')}</DialogDescription>
        </DialogHeader>
        <form
          className="space-y-4"
          noValidate
          onSubmit={(e) => {
            e.preventDefault()
            if (complete) create.mutate()
          }}
        >
          <div className="space-y-2">
            <Label htmlFor={`${id}-cidr`}>{t('security.ip_rules.cidr')}</Label>
            <Input
              id={`${id}-cidr`}
              value={cidr}
              onChange={(e) => setCidr(e.target.value)}
              autoComplete="off"
              spellCheck={false}
              aria-invalid={cidrInvalid || undefined}
            />
            {cidrInvalid ? <p className="text-destructive text-sm">{t('security.ip_rules.cidr_invalid')}</p> : null}
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${id}-kind`}>{t('security.ip_rules.kind')}</Label>
            <NativeSelect id={`${id}-kind`} className="w-full" value={kind} onChange={(e) => setKind(e.target.value as IPRuleKind)}>
              <option value="allow">{t('security.ip_rules.kind_allow')}</option>
              <option value="deny">{t('security.ip_rules.kind_deny')}</option>
            </NativeSelect>
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${id}-comment`}>{t('security.ip_rules.comment')}</Label>
            <Input id={`${id}-comment`} value={comment} maxLength={200} onChange={(e) => setComment(e.target.value)} />
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${id}-expires`}>{t('security.ip_rules.expires')}</Label>
            <Input id={`${id}-expires`} type="datetime-local" value={expires} onChange={(e) => setExpires(e.target.value)} />
          </div>
          {create.error ? (
            <p role="alert" className="text-destructive text-sm">
              {errorMessage(create.error, t)}
            </p>
          ) : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" disabled={create.isPending || !complete}>
              {t('security.ip_rules.create')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
```

- [ ] **Step 5: `web/src/features/security/ip-rules-tab.tsx`**

```tsx
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap, unwrapVoid } from '@/api/errors'
import type { IPRule } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { DataList, type Column } from '@/components/data-list'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { errorMessage, useErrorToast } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import { CreateIpRuleDialog } from './create-ip-rule-dialog'

const rulesKey = ['security', 'ip-rules'] as const

export function IpRulesTab() {
  const { t } = useTranslation()
  const fmt = useFormat()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const [creating, setCreating] = useState(false)
  const [deleting, setDeleting] = useState<IPRule | null>(null)
  const rules = useQuery({ queryKey: rulesKey, queryFn: () => unwrap(api.GET('/security/ip-rules')) })
  const remove = useMutation({
    mutationFn: (rule: IPRule) => unwrapVoid(api.DELETE('/security/ip-rules/{id}', { params: { path: { id: rule.id } } })),
    onSuccess: (_data, rule) => {
      setDeleting(null)
      toast.success(t('security.ip_rules.deleted', { cidr: rule.cidr }))
      void queryClient.invalidateQueries({ queryKey: rulesKey })
    },
    onError: notifyError,
  })

  const columns: Column<IPRule>[] = [
    { id: 'cidr', header: t('security.ip_rules.col.cidr'), cell: (r) => <span className="font-mono text-xs">{r.cidr}</span> },
    {
      id: 'kind',
      header: t('security.ip_rules.col.kind'),
      cell: (r) => <Badge variant={r.kind === 'allow' ? 'secondary' : 'destructive'}>{t(`security.ip_rules.kind_${r.kind}`)}</Badge>,
    },
    { id: 'comment', header: t('security.ip_rules.col.comment'), cell: (r) => r.comment },
    {
      id: 'expires',
      header: t('security.ip_rules.col.expires'),
      cell: (r) => (r.expires_at === null ? t('security.ip_rules.never') : fmt.dateTime(r.expires_at)),
    },
    { id: 'created_by', header: t('security.ip_rules.col.created_by'), cell: (r) => r.created_by },
    { id: 'created_at', header: t('security.ip_rules.col.created_at'), cell: (r) => fmt.dateTime(r.created_at) },
    {
      id: 'actions',
      header: '',
      cell: (r) => (
        <Button
          type="button"
          size="sm"
          variant="ghost"
          className="text-destructive"
          aria-label={t('security.ip_rules.delete_for', { cidr: r.cidr })}
          onClick={() => setDeleting(r)}
        >
          {t('common.delete')}
        </Button>
      ),
    },
  ]

  return (
    <div className="space-y-4">
      <div className="flex justify-end">
        <Button type="button" onClick={() => setCreating(true)}>
          {t('security.ip_rules.create')}
        </Button>
      </div>
      {rules.isError ? (
        <p role="alert" className="text-destructive">
          {errorMessage(rules.error, t)}
        </p>
      ) : null}
      {rules.isPending ? (
        <p className="text-muted-foreground">{t('common.loading')}</p>
      ) : (
        <DataList items={rules.data ?? []} columns={columns} rowKey={(r) => r.id} caption={t('security.ip_rules.caption')} empty={t('security.ip_rules.empty')} />
      )}
      <CreateIpRuleDialog open={creating} onOpenChange={setCreating} />
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={t('security.ip_rules.delete_title', { cidr: deleting?.cidr ?? '' })}
        description={t('security.ip_rules.delete_description')}
        confirmLabel={t('common.delete')}
        destructive
        pending={remove.isPending}
        onConfirm={() => deleting && remove.mutate(deleting)}
      />
    </div>
  )
}
```

- [ ] **Step 6: `web/src/features/security/settings-tab.tsx`**

```tsx
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { errorMessage, useErrorToast } from '@/lib/error-message'
import { formatCountries, parseCountries } from './countries'

const settingsKey = ['security', 'settings'] as const

interface FormState {
  enforce: boolean
  allowlistOnly: boolean
  countries: string
}

function SwitchRow({ id, label, hint, checked, onChange }: { id: string; label: string; hint: string; checked: boolean; onChange(next: boolean): void }) {
  return (
    <div className="flex items-start justify-between gap-4 rounded-md border p-4">
      <div className="space-y-1">
        <Label htmlFor={id}>{label}</Label>
        <p id={`${id}-hint`} className="text-muted-foreground text-sm">
          {hint}
        </p>
      </div>
      <Switch id={id} checked={checked} onCheckedChange={onChange} aria-describedby={`${id}-hint`} />
    </div>
  )
}

export function SettingsTab() {
  const { t } = useTranslation()
  const id = useId()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const settings = useQuery({ queryKey: settingsKey, queryFn: () => unwrap(api.GET('/security/settings')) })
  // null = untouched; the form shows the server state until the admin edits something.
  const [form, setForm] = useState<FormState | null>(null)
  const save = useMutation({
    mutationFn: (value: FormState) =>
      unwrap(
        api.PUT('/security/settings', {
          body: {
            enforce_2fa_for_users: value.enforce,
            admin_allowlist_only: value.allowlistOnly,
            allowed_countries: parseCountries(value.countries).codes,
          },
        }),
      ),
    onSuccess: (result) => {
      queryClient.setQueryData(settingsKey, result)
      setForm(null)
      toast.success(t('security.settings.saved'))
    },
    onError: notifyError,
  })

  if (settings.isPending) return <p className="text-muted-foreground">{t('common.loading')}</p>
  if (settings.isError) {
    return (
      <p role="alert" className="text-destructive">
        {errorMessage(settings.error, t)}
      </p>
    )
  }

  const value: FormState = form ?? {
    enforce: settings.data.enforce_2fa_for_users,
    allowlistOnly: settings.data.admin_allowlist_only,
    countries: formatCountries(settings.data.allowed_countries),
  }
  const update = (patch: Partial<FormState>) => setForm({ ...value, ...patch })
  const parsed = parseCountries(value.countries)
  const geoip = settings.data.geoip_available

  return (
    <form
      className="max-w-2xl space-y-4"
      noValidate
      onSubmit={(e) => {
        e.preventDefault()
        if (parsed.invalid.length === 0) save.mutate(value)
      }}
    >
      <SwitchRow
        id={`${id}-enforce`}
        label={t('security.settings.enforce_2fa')}
        hint={t('security.settings.enforce_2fa_hint')}
        checked={value.enforce}
        onChange={(enforce) => update({ enforce })}
      />
      <SwitchRow
        id={`${id}-allowlist`}
        label={t('security.settings.allowlist_only')}
        hint={t('security.settings.allowlist_only_hint')}
        checked={value.allowlistOnly}
        onChange={(allowlistOnly) => update({ allowlistOnly })}
      />
      <div className="space-y-2 rounded-md border p-4">
        <Label htmlFor={`${id}-countries`}>{t('security.settings.countries')}</Label>
        <Input
          id={`${id}-countries`}
          value={value.countries}
          disabled={!geoip}
          autoComplete="off"
          spellCheck={false}
          aria-invalid={parsed.invalid.length > 0 || undefined}
          aria-describedby={`${id}-countries-hint`}
          onChange={(e) => update({ countries: e.target.value })}
        />
        <p id={`${id}-countries-hint`} className="text-muted-foreground text-sm">
          {geoip ? t('security.settings.countries_hint') : t('security.settings.geoip_missing')}
        </p>
        {parsed.invalid.length > 0 ? (
          <p className="text-destructive text-sm">{t('security.settings.countries_invalid', { codes: parsed.invalid.join(', ') })}</p>
        ) : null}
      </div>
      <Button type="submit" disabled={form === null || save.isPending || parsed.invalid.length > 0}>
        {t('common.save')}
      </Button>
    </form>
  )
}
```

- [ ] **Step 7: Tests laufen lassen → PASS**

Run: `cd web && npx vitest run src/features/security`
Expected: PASS (countries 3, ip-rules 3, settings 4 sowie Tasks 13/14).

- [ ] **Step 8: Gesamte Suite + Typecheck, Commit**

Run: `cd web && npx vitest run && npm run typecheck`
Expected: grün.

```bash
git add web/src/features/security
git commit -m "feat(web): add IP rule management and security settings with GeoIP hint" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 16: Security Center – Benachrichtigungen, Seite und Route

**Files:**
- Create: `web/src/features/security/notifier-dialog.tsx`, `web/src/features/security/notifiers-tab.tsx`, `web/src/features/security/security-page.tsx`, `web/src/routes/_app/security.tsx`
- Test: `web/src/features/security/notifiers-tab.test.tsx`, `web/src/features/security/security-page.test.tsx`

**Interfaces:**
- Consumes: alle Tabs aus Task 13–15; `adminGuard`, `guardBeforeLoad` (Task 7/10); `PageHeader`, `DataList`, `NativeSelect`, `ConfirmDialog` (Task 2); `api`, `unwrap`, `unwrapVoid`, `errorMessage`, `useErrorToast` (Task 5)
- Produces:
```ts
export const notifiersKey: readonly ['security', 'notifiers']
export function NotifierDialog(props: { open: boolean; onOpenChange(open: boolean): void; notifier: Notifier | null }): JSX.Element  // null = anlegen
export function NotifiersTab(): JSX.Element
export function SecurityPage(): JSX.Element
// Route: /security (adminGuard)
```
Beim Bearbeiten werden URL/Topic/Token nur gesendet, wenn sie neu eingegeben wurden (das Backend liefert Secrets nur maskiert zurück). Vertragsdetails: `testNotifier` meldet eine fehlgeschlagene Zustellung als `400 validation_failed` mit Klartext-`message` (UI: Fehler-Toast „Eingaben ungültig (<message>)“); ein doppelter Name bei `PATCH` ist ebenfalls `400 validation_failed` (bei `POST` bleibt es `409 conflict`). Im Mock schlägt der Test für deaktivierte Benachrichtigungen fehl.

- [ ] **Step 1: Failing Tests**

`web/src/features/security/notifiers-tab.test.tsx`:
```tsx
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { db, loginAs } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { NotifiersTab } from './notifiers-tab'

describe('NotifiersTab', () => {
  it('lists notifiers with masked targets', async () => {
    await renderWithProviders(<NotifiersTab />, { me: loginAs('admin') })
    const table = await screen.findByRole('table', { name: 'Benachrichtigungen' })
    const discord = within(table).getByRole('row', { name: /Discord #alerts/ })
    expect(within(discord).getByText('https://discord.com/api/webhooks/…/****')).toBeInTheDocument()
    expect(within(discord).getByRole('switch', { name: 'Discord #alerts aktiv' })).toHaveAttribute('aria-checked', 'true')
  })

  it('creates an ntfy notifier', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<NotifiersTab />, { me: loginAs('admin', { sudo: true }) })
    await user.click(await screen.findByRole('button', { name: 'Benachrichtigung hinzufügen' }))
    const dialog = screen.getByRole('dialog', { name: 'Benachrichtigung hinzufügen' })
    await user.selectOptions(within(dialog).getByLabelText('Dienst'), 'ntfy')
    await user.type(within(dialog).getByLabelText('Name'), 'ntfy Server')
    await user.type(within(dialog).getByLabelText('Server-URL'), 'https://ntfy.example.org')
    expect(within(dialog).getByRole('button', { name: 'Anlegen' })).toBeDisabled()
    await user.type(within(dialog).getByLabelText('Topic'), 'mc-alerts')
    await user.click(within(dialog).getByRole('button', { name: 'Anlegen' }))
    expect(await screen.findByText('Benachrichtigung ntfy Server angelegt.')).toBeInTheDocument()
    expect(await screen.findByRole('row', { name: /ntfy Server/ })).toBeInTheDocument()
  })

  it('rejects non-http URLs', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<NotifiersTab />, { me: loginAs('admin', { sudo: true }) })
    await user.click(await screen.findByRole('button', { name: 'Benachrichtigung hinzufügen' }))
    const dialog = screen.getByRole('dialog', { name: 'Benachrichtigung hinzufügen' })
    await user.type(within(dialog).getByLabelText('Webhook-URL'), 'javascript:alert(1)')
    expect(within(dialog).getByText('Bitte eine http(s)-URL angeben.')).toBeInTheDocument()
  })

  it('sends a test message', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<NotifiersTab />, { me: loginAs('admin') })
    await user.click(await screen.findByRole('button', { name: 'Testnachricht an Discord #alerts senden' }))
    expect(await screen.findByText('Testnachricht an Discord #alerts gesendet.')).toBeInTheDocument()
  })

  it('shows the delivery error of a failed test message', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<NotifiersTab />, { me: loginAs('admin') })
    await user.click(await screen.findByRole('button', { name: 'Testnachricht an ntfy Handy senden' }))
    expect(await screen.findByText('Eingaben ungültig (delivery failed: HTTP 404 from target)')).toBeInTheDocument()
  })

  it('reports a duplicate name on rename as validation error', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<NotifiersTab />, { me: loginAs('admin', { sudo: true }) })
    await user.click(await screen.findByRole('button', { name: 'Discord #alerts bearbeiten' }))
    const dialog = screen.getByRole('dialog', { name: 'Benachrichtigung bearbeiten' })
    const name = within(dialog).getByLabelText('Name')
    await user.clear(name)
    await user.type(name, 'ntfy Handy')
    await user.click(within(dialog).getByRole('button', { name: 'Speichern' }))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('Eingaben ungültig (name already in use)')
  })

  it('toggles a notifier and renames it without resending the secret URL', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<NotifiersTab />, { me: loginAs('admin', { sudo: true }) })
    await user.click(await screen.findByRole('switch', { name: 'ntfy Handy aktiv' }))
    await screen.findByText('Benachrichtigung ntfy Handy gespeichert.')
    expect(db.notifiers.find((n) => n.id === 2)?.enabled).toBe(true)

    await user.click(screen.getByRole('button', { name: 'Discord #alerts bearbeiten' }))
    const dialog = screen.getByRole('dialog', { name: 'Benachrichtigung bearbeiten' })
    expect(within(dialog).getByText('Leer lassen, um die gespeicherte URL zu behalten.')).toBeInTheDocument()
    const name = within(dialog).getByLabelText('Name')
    await user.clear(name)
    await user.type(name, 'Discord #ops')
    await user.click(within(dialog).getByRole('button', { name: 'Speichern' }))
    expect(await screen.findByRole('row', { name: /Discord #ops/ })).toBeInTheDocument()
    expect(db.notifiers.find((n) => n.id === 1)?.target_masked).toBe('https://discord.com/api/webhooks/…/****')
  })

  it('deletes a notifier after confirmation', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<NotifiersTab />, { me: loginAs('admin', { sudo: true }) })
    await user.click(await screen.findByRole('button', { name: 'ntfy Handy löschen' }))
    await user.click(within(screen.getByRole('dialog', { name: 'ntfy Handy löschen?' })).getByRole('button', { name: 'Löschen' }))
    expect(await screen.findByText('Benachrichtigung ntfy Handy gelöscht.')).toBeInTheDocument()
  })
})
```

`web/src/features/security/security-page.test.tsx`:
```tsx
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { loginAs } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { SecurityPage } from './security-page'

describe('SecurityPage', () => {
  it('shows all six tabs and switches between them', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<SecurityPage />, { me: loginAs('admin') })
    expect(screen.getByRole('heading', { name: 'Security Center' })).toBeInTheDocument()
    expect(screen.getAllByRole('tab').map((tab) => tab.textContent)).toEqual([
      'Übersicht',
      'Login-Versuche',
      'Jails',
      'IP-Regeln',
      'Einstellungen',
      'Benachrichtigungen',
    ])
    expect(await screen.findByRole('img', { name: /Login-Versuche/ })).toBeInTheDocument()
    await user.click(screen.getByRole('tab', { name: 'Jails' }))
    expect(await screen.findByRole('table', { name: 'Jails' })).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Tests laufen lassen → FAIL**

Run: `cd web && npx vitest run src/features/security/notifiers-tab.test.tsx src/features/security/security-page.test.tsx`
Expected: FAIL – Module fehlen.

- [ ] **Step 3: `web/src/features/security/notifier-dialog.tsx`**

```tsx
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { Notifier, NotifierKind } from '@/api/types'
import { NativeSelect } from '@/components/native-select'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { errorMessage } from '@/lib/error-message'

export const notifiersKey = ['security', 'notifiers'] as const

const HTTP_URL = /^https?:\/\/\S+$/

interface UpdateNotifierBody {
  name?: string
  enabled?: boolean
  url?: string
  topic?: string
  token?: string
}

/** Mounted per dialog opening (keyed), so the initial state comes straight from props. */
function NotifierForm({ notifier, onClose }: { notifier: Notifier | null; onClose(): void }) {
  const { t } = useTranslation()
  const id = useId()
  const queryClient = useQueryClient()
  const editing = notifier !== null
  const [kind, setKind] = useState<NotifierKind>(notifier?.kind ?? 'discord')
  const [name, setName] = useState(notifier?.name ?? '')
  const [enabled, setEnabled] = useState(notifier?.enabled ?? true)
  const [url, setUrl] = useState('')
  const [topic, setTopic] = useState('')
  const [token, setToken] = useState('')
  const trimmedName = name.trim()
  const urlInvalid = url !== '' && !HTTP_URL.test(url)
  const complete =
    trimmedName.length >= 1 && trimmedName.length <= 64 && !urlInvalid && (editing || (url !== '' && (kind === 'discord' || topic.trim() !== '')))

  const submit = useMutation({
    mutationFn: () => {
      if (notifier) {
        const body: UpdateNotifierBody = { name: trimmedName, enabled }
        if (url) body.url = url
        if (topic.trim()) body.topic = topic.trim()
        if (token) body.token = token
        return unwrap(api.PATCH('/security/notifiers/{id}', { params: { path: { id: notifier.id } }, body }))
      }
      return unwrap(
        api.POST('/security/notifiers', {
          body: kind === 'ntfy' ? { kind, name: trimmedName, enabled, url, topic: topic.trim(), ...(token ? { token } : {}) } : { kind, name: trimmedName, enabled, url },
        }),
      )
    },
    onSuccess: (saved) => {
      toast.success(editing ? t('security.notifiers.updated', { name: saved.name }) : t('security.notifiers.created', { name: saved.name }))
      void queryClient.invalidateQueries({ queryKey: notifiersKey })
      onClose()
    },
  })

  return (
    <form
      className="space-y-4"
      noValidate
      onSubmit={(e) => {
        e.preventDefault()
        if (complete) submit.mutate()
      }}
    >
      <div className="space-y-2">
        <Label htmlFor={`${id}-kind`}>{t('security.notifiers.kind')}</Label>
        <NativeSelect id={`${id}-kind`} className="w-full" value={kind} disabled={editing} onChange={(e) => setKind(e.target.value as NotifierKind)}>
          <option value="discord">{t('security.notifiers.kind_discord')}</option>
          <option value="ntfy">{t('security.notifiers.kind_ntfy')}</option>
        </NativeSelect>
      </div>
      <div className="space-y-2">
        <Label htmlFor={`${id}-name`}>{t('security.notifiers.name')}</Label>
        <Input id={`${id}-name`} value={name} maxLength={64} onChange={(e) => setName(e.target.value)} />
      </div>
      <div className="flex items-center gap-2">
        <Switch id={`${id}-enabled`} checked={enabled} onCheckedChange={setEnabled} />
        <Label htmlFor={`${id}-enabled`}>{t('security.notifiers.enabled')}</Label>
      </div>
      <div className="space-y-2">
        <Label htmlFor={`${id}-url`}>{t(`security.notifiers.url_${kind}`)}</Label>
        <Input
          id={`${id}-url`}
          type="url"
          inputMode="url"
          value={url}
          autoComplete="off"
          spellCheck={false}
          aria-invalid={urlInvalid || undefined}
          onChange={(e) => setUrl(e.target.value)}
        />
        {urlInvalid ? <p className="text-destructive text-sm">{t('security.notifiers.url_invalid')}</p> : null}
        {editing ? <p className="text-muted-foreground text-sm">{t('security.notifiers.url_keep')}</p> : null}
      </div>
      {kind === 'ntfy' ? (
        <>
          <div className="space-y-2">
            <Label htmlFor={`${id}-topic`}>{t('security.notifiers.topic')}</Label>
            <Input id={`${id}-topic`} value={topic} autoComplete="off" spellCheck={false} onChange={(e) => setTopic(e.target.value)} />
            {!editing ? <p className="text-muted-foreground text-sm">{t('security.notifiers.topic_required')}</p> : null}
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${id}-token`}>{t('security.notifiers.token')}</Label>
            <Input id={`${id}-token`} type="password" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value)} />
          </div>
        </>
      ) : null}
      {submit.error ? (
        <p role="alert" className="text-destructive text-sm">
          {errorMessage(submit.error, t)}
        </p>
      ) : null}
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onClose}>
          {t('common.cancel')}
        </Button>
        <Button type="submit" disabled={submit.isPending || !complete}>
          {editing ? t('common.save') : t('common.create')}
        </Button>
      </DialogFooter>
    </form>
  )
}

export function NotifierDialog({ open, onOpenChange, notifier }: { open: boolean; onOpenChange(open: boolean): void; notifier: Notifier | null }) {
  const { t } = useTranslation()
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{notifier ? t('security.notifiers.edit_title') : t('security.notifiers.create')}</DialogTitle>
          <DialogDescription>{t('security.notifiers.create_description')}</DialogDescription>
        </DialogHeader>
        {open ? <NotifierForm key={notifier?.id ?? 'new'} notifier={notifier} onClose={() => onOpenChange(false)} /> : null}
      </DialogContent>
    </Dialog>
  )
}
```

- [ ] **Step 4: `web/src/features/security/notifiers-tab.tsx`**

```tsx
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap, unwrapVoid } from '@/api/errors'
import type { Notifier } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { DataList, type Column } from '@/components/data-list'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { errorMessage, useErrorToast } from '@/lib/error-message'
import { NotifierDialog, notifiersKey } from './notifier-dialog'

export function NotifiersTab() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const [editing, setEditing] = useState<Notifier | null>(null)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [deleting, setDeleting] = useState<Notifier | null>(null)
  const notifiers = useQuery({ queryKey: notifiersKey, queryFn: () => unwrap(api.GET('/security/notifiers')) })
  const invalidate = () => queryClient.invalidateQueries({ queryKey: notifiersKey })

  const toggle = useMutation({
    mutationFn: ({ notifier, enabled }: { notifier: Notifier; enabled: boolean }) =>
      unwrap(api.PATCH('/security/notifiers/{id}', { params: { path: { id: notifier.id } }, body: { enabled } })),
    onSuccess: (saved) => {
      toast.success(t('security.notifiers.updated', { name: saved.name }))
      void invalidate()
    },
    onError: notifyError,
  })
  const test = useMutation({
    mutationFn: (notifier: Notifier) => unwrapVoid(api.POST('/security/notifiers/{id}/test', { params: { path: { id: notifier.id } } })),
    onSuccess: (_data, notifier) => toast.success(t('security.notifiers.test_sent', { name: notifier.name })),
    onError: notifyError,
  })
  const remove = useMutation({
    mutationFn: (notifier: Notifier) => unwrapVoid(api.DELETE('/security/notifiers/{id}', { params: { path: { id: notifier.id } } })),
    onSuccess: (_data, notifier) => {
      setDeleting(null)
      toast.success(t('security.notifiers.deleted', { name: notifier.name }))
      void invalidate()
    },
    onError: notifyError,
  })

  const openDialog = (notifier: Notifier | null) => {
    setEditing(notifier)
    setDialogOpen(true)
  }

  const columns: Column<Notifier>[] = [
    { id: 'name', header: t('security.notifiers.col.name'), cell: (n) => <span className="font-medium">{n.name}</span> },
    { id: 'kind', header: t('security.notifiers.col.kind'), cell: (n) => t(`security.notifiers.kind_${n.kind}`) },
    { id: 'target', header: t('security.notifiers.col.target'), cell: (n) => <span className="font-mono text-xs break-all">{n.target_masked}</span> },
    {
      id: 'enabled',
      header: t('security.notifiers.col.enabled'),
      cell: (n) => (
        <Switch
          checked={n.enabled}
          disabled={toggle.isPending}
          aria-label={t('security.notifiers.enabled_for', { name: n.name })}
          onCheckedChange={(enabled) => toggle.mutate({ notifier: n, enabled })}
        />
      ),
    },
    {
      id: 'actions',
      header: t('security.notifiers.col.actions'),
      cell: (n) => (
        <div className="flex flex-wrap gap-2">
          <Button type="button" size="sm" variant="outline" aria-label={t('security.notifiers.test_for', { name: n.name })} onClick={() => test.mutate(n)}>
            {t('security.notifiers.test')}
          </Button>
          <Button type="button" size="sm" variant="outline" aria-label={t('security.notifiers.edit_for', { name: n.name })} onClick={() => openDialog(n)}>
            {t('common.edit')}
          </Button>
          <Button
            type="button"
            size="sm"
            variant="ghost"
            className="text-destructive"
            aria-label={t('security.notifiers.delete_for', { name: n.name })}
            onClick={() => setDeleting(n)}
          >
            {t('common.delete')}
          </Button>
        </div>
      ),
    },
  ]

  return (
    <div className="space-y-4">
      <div className="flex justify-end">
        <Button type="button" onClick={() => openDialog(null)}>
          {t('security.notifiers.create')}
        </Button>
      </div>
      {notifiers.isError ? (
        <p role="alert" className="text-destructive">
          {errorMessage(notifiers.error, t)}
        </p>
      ) : null}
      {notifiers.isPending ? (
        <p className="text-muted-foreground">{t('common.loading')}</p>
      ) : (
        <DataList items={notifiers.data ?? []} columns={columns} rowKey={(n) => n.id} caption={t('security.notifiers.caption')} empty={t('security.notifiers.empty')} />
      )}
      <NotifierDialog open={dialogOpen} onOpenChange={setDialogOpen} notifier={editing} />
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={t('security.notifiers.delete_title', { name: deleting?.name ?? '' })}
        description={t('security.notifiers.delete_description')}
        confirmLabel={t('common.delete')}
        destructive
        pending={remove.isPending}
        onConfirm={() => deleting && remove.mutate(deleting)}
      />
    </div>
  )
}
```

- [ ] **Step 5: `web/src/features/security/security-page.tsx` und Route**

```tsx
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
```

```tsx
// web/src/routes/_app/security.tsx
import { createFileRoute } from '@tanstack/react-router'
import { adminGuard } from '@/features/auth/guards'
import { guardBeforeLoad } from '@/features/auth/route-guards'
import { SecurityPage } from '@/features/security/security-page'

export const Route = createFileRoute('/_app/security')({
  beforeLoad: guardBeforeLoad(adminGuard),
  component: SecurityPage,
})
```

- [ ] **Step 6: Tests laufen lassen → PASS**

Run: `cd web && npx vitest run src/features/security`
Expected: PASS (notifiers 8, security-page 1 sowie Tasks 13–15).

- [ ] **Step 7: Route-Baum, Typecheck, Suite, Commit**

Run: `cd web && npx vite build && npm run typecheck && npx vitest run`
Expected: grün.

```bash
git add web/src/features/security web/src/routes web/src/routeTree.gen.ts
git commit -m "feat(web): add notifier management and assemble the security center page" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 17: Audit-Log mit Filtern, Paging und Kettenstatus

**Files:**
- Create: `web/src/features/audit/audit-filters.ts`, `web/src/features/audit/audit-page.tsx`, `web/src/routes/_app/audit.tsx`
- Test: `web/src/features/audit/audit-filters.test.ts`, `web/src/features/audit/audit-page.test.tsx`

**Interfaces:**
- Consumes: `api`, `unwrap`, `errorMessage` (Task 5); `DataList`, `NativeSelect`, `PageHeader` (Task 2); `useFormat` (Task 1); `adminGuard`, `guardBeforeLoad` (Task 7/10)
- Produces:
```ts
export interface AuditFilters { actor: string; action: string; result: AuditResult | ''; from: string; to: string }   // from/to = <input type="date">-Wert
export const EMPTY_FILTERS: AuditFilters
export function dateInputToMs(value: string, endOfDay: boolean): number | undefined   // lokale Zeitzone
export function toAuditQuery(filters: AuditFilters): { actor?: string; action?: string; result?: AuditResult; from?: number; to?: number }
export function AuditPage(): JSX.Element
// Route: /audit (adminGuard)
```

- [ ] **Step 1: Failing Tests**

`web/src/features/audit/audit-filters.test.ts`:
```ts
import { describe, expect, it } from 'vitest'
import { dateInputToMs, EMPTY_FILTERS, toAuditQuery } from './audit-filters'

describe('audit filters', () => {
  it('converts date inputs to local start and end of day', () => {
    expect(dateInputToMs('2026-10-08', false)).toBe(new Date(2026, 9, 8, 0, 0, 0, 0).getTime())
    expect(dateInputToMs('2026-10-08', true)).toBe(new Date(2026, 9, 8, 23, 59, 59, 999).getTime())
    expect(dateInputToMs('', false)).toBeUndefined()
    expect(dateInputToMs('08.10.2026', false)).toBeUndefined()
  })

  it('omits empty filters from the query', () => {
    expect(toAuditQuery(EMPTY_FILTERS)).toEqual({ actor: undefined, action: undefined, result: undefined, from: undefined, to: undefined })
    expect(toAuditQuery({ ...EMPTY_FILTERS, actor: ' admin ', result: 'denied' })).toMatchObject({ actor: 'admin', result: 'denied' })
  })
})
```

`web/src/features/audit/audit-page.test.tsx`:
```tsx
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { loginAs } from '@/mocks/db'
import { server } from '@/mocks/node'
import { renderWithProviders } from '@/test/render'
import { AuditPage } from './audit-page'

describe('AuditPage', () => {
  it('shows a valid chain status', async () => {
    await renderWithProviders(<AuditPage />, { me: loginAs('admin') })
    expect(await screen.findByRole('status')).toHaveTextContent('Kette gültig (60 Einträge geprüft)')
  })

  it('shows the first broken entry of a tampered chain', async () => {
    server.use(http.get('*/api/audit/verify', () => HttpResponse.json({ ok: false, checked: 17, first_bad_id: 17 })))
    await renderWithProviders(<AuditPage />, { me: loginAs('admin') })
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Kette ungültig ab Eintrag #17'))
  })

  it('pages with "load more"', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<AuditPage />, { me: loginAs('admin') })
    const table = await screen.findByRole('table', { name: 'Audit-Einträge' })
    expect(within(table).getAllByRole('row')).toHaveLength(51)
    await user.click(screen.getByRole('button', { name: 'Mehr laden' }))
    await waitFor(() => expect(within(screen.getByRole('table', { name: 'Audit-Einträge' })).getAllByRole('row')).toHaveLength(61))
    expect(screen.queryByRole('button', { name: 'Mehr laden' })).not.toBeInTheDocument()
  })

  it('filters by result', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<AuditPage />, { me: loginAs('admin') })
    await screen.findByRole('table', { name: 'Audit-Einträge' })
    await user.selectOptions(screen.getByLabelText('Ergebnis'), 'denied')
    await user.click(screen.getByRole('button', { name: 'Filtern' }))
    await waitFor(() => expect(within(screen.getByRole('table', { name: 'Audit-Einträge' })).getAllByRole('row')).toHaveLength(9))
    for (const row of within(screen.getByRole('table', { name: 'Audit-Einträge' })).getAllByRole('row').slice(1)) {
      expect(within(row).getByText('Abgelehnt')).toBeInTheDocument()
    }
  })
})
```

- [ ] **Step 2: Tests laufen lassen → FAIL**

Run: `cd web && npx vitest run src/features/audit`
Expected: FAIL – Module fehlen.

- [ ] **Step 3: `web/src/features/audit/audit-filters.ts`**

```ts
import type { AuditResult } from '@/api/types'

export interface AuditFilters {
  actor: string
  action: string
  result: AuditResult | ''
  from: string
  to: string
}

export const EMPTY_FILTERS: AuditFilters = { actor: '', action: '', result: '', from: '', to: '' }

/** <input type="date"> yields YYYY-MM-DD; interpret it in the browser's local time zone. */
export function dateInputToMs(value: string, endOfDay: boolean): number | undefined {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return undefined
  const [y, m, d] = value.split('-').map(Number)
  return endOfDay ? new Date(y, m - 1, d, 23, 59, 59, 999).getTime() : new Date(y, m - 1, d, 0, 0, 0, 0).getTime()
}

export function toAuditQuery(filters: AuditFilters) {
  return {
    actor: filters.actor.trim() || undefined,
    action: filters.action.trim() || undefined,
    result: filters.result || undefined,
    from: dateInputToMs(filters.from, false),
    to: dateInputToMs(filters.to, true),
  }
}
```

- [ ] **Step 4: `web/src/features/audit/audit-page.tsx`**

```tsx
import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { AuditEntry, AuditResult } from '@/api/types'
import { DataList, type Column } from '@/components/data-list'
import { NativeSelect } from '@/components/native-select'
import { PageHeader } from '@/components/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { errorMessage } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import { EMPTY_FILTERS, toAuditQuery, type AuditFilters } from './audit-filters'

const RESULT_VARIANT: Record<AuditResult, 'secondary' | 'destructive' | 'outline'> = {
  success: 'secondary',
  failure: 'destructive',
  denied: 'outline',
}

function ChainStatus() {
  const { t } = useTranslation()
  const verify = useQuery({ queryKey: ['audit', 'verify'], queryFn: () => unwrap(api.GET('/audit/verify')) })
  let text = t('audit.chain_checking')
  let variant: 'secondary' | 'destructive' | 'outline' = 'outline'
  if (verify.isError) {
    text = t('audit.chain_error')
    variant = 'destructive'
  } else if (verify.data?.ok) {
    text = t('audit.chain_ok', { checked: verify.data.checked })
    variant = 'secondary'
  } else if (verify.data) {
    text = t('audit.chain_broken', { id: verify.data.first_bad_id ?? '?' })
    variant = 'destructive'
  }
  return (
    <div className="flex flex-wrap items-center gap-2">
      <span role="status">
        <Badge variant={variant}>{text}</Badge>
      </span>
      <Button type="button" size="sm" variant="outline" disabled={verify.isFetching} onClick={() => void verify.refetch()}>
        {t('audit.verify_again')}
      </Button>
    </div>
  )
}

export function AuditPage() {
  const { t } = useTranslation()
  const fmt = useFormat()
  const id = useId()
  const [draft, setDraft] = useState<AuditFilters>(EMPTY_FILTERS)
  const [filters, setFilters] = useState<AuditFilters>(EMPTY_FILTERS)
  const entries = useInfiniteQuery({
    queryKey: ['audit', 'entries', filters],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) => unwrap(api.GET('/audit', { params: { query: { ...toAuditQuery(filters), cursor: pageParam, limit: 50 } } })),
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  })
  const items = entries.data?.pages.flatMap((page) => page.items) ?? []
  const update = (patch: Partial<AuditFilters>) => setDraft({ ...draft, ...patch })

  const columns: Column<AuditEntry>[] = [
    { id: 'time', header: t('audit.col.time'), cell: (e) => fmt.dateTime(e.ts) },
    {
      id: 'actor',
      header: t('audit.col.actor'),
      cell: (e) => (
        <span>
          {e.actor_name} <span className="text-muted-foreground text-xs">({t(`audit.actor_type.${e.actor_type}`)})</span>
        </span>
      ),
    },
    { id: 'action', header: t('audit.col.action'), cell: (e) => <code className="text-xs">{e.action}</code> },
    { id: 'target', header: t('audit.col.target'), cell: (e) => <span className="break-all">{e.target}</span> },
    { id: 'ip', header: t('audit.col.ip'), cell: (e) => <span className="font-mono text-xs">{e.ip || '—'}</span> },
    { id: 'result', header: t('audit.col.result'), cell: (e) => <Badge variant={RESULT_VARIANT[e.result]}>{t(`audit.result.${e.result}`)}</Badge> },
    {
      id: 'details',
      header: t('audit.col.details'),
      cell: (e) => {
        const json = JSON.stringify(e.details)
        return json === '{}' ? (
          '—'
        ) : (
          <code className="block max-w-64 truncate text-xs" title={json}>
            {json}
          </code>
        )
      },
    },
    {
      id: 'hash',
      header: t('audit.col.hash'),
      cell: (e) => (
        <code className="text-muted-foreground text-xs" title={e.hash}>
          {e.hash.slice(0, 12)}
        </code>
      ),
    },
  ]

  return (
    <>
      <PageHeader title={t('audit.title')} description={t('audit.description')} actions={<ChainStatus />} />
      <form
        className="mb-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-6 lg:items-end"
        onSubmit={(e) => {
          e.preventDefault()
          setFilters(draft)
        }}
      >
        <div className="space-y-1">
          <Label htmlFor={`${id}-actor`}>{t('audit.actor')}</Label>
          <Input id={`${id}-actor`} value={draft.actor} onChange={(e) => update({ actor: e.target.value })} />
        </div>
        <div className="space-y-1">
          <Label htmlFor={`${id}-action`}>{t('audit.action')}</Label>
          <Input id={`${id}-action`} value={draft.action} spellCheck={false} onChange={(e) => update({ action: e.target.value })} />
        </div>
        <div className="space-y-1">
          <Label htmlFor={`${id}-result`}>{t('audit.result_label')}</Label>
          <NativeSelect id={`${id}-result`} className="w-full" value={draft.result} onChange={(e) => update({ result: e.target.value as AuditResult | '' })}>
            <option value="">{t('audit.result_all')}</option>
            <option value="success">{t('audit.result.success')}</option>
            <option value="failure">{t('audit.result.failure')}</option>
            <option value="denied">{t('audit.result.denied')}</option>
          </NativeSelect>
        </div>
        <div className="space-y-1">
          <Label htmlFor={`${id}-from`}>{t('audit.from')}</Label>
          <Input id={`${id}-from`} type="date" value={draft.from} onChange={(e) => update({ from: e.target.value })} />
        </div>
        <div className="space-y-1">
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
      {entries.isError ? (
        <p role="alert" className="text-destructive">
          {errorMessage(entries.error, t)}
        </p>
      ) : null}
      {entries.isPending ? (
        <p className="text-muted-foreground">{t('common.loading')}</p>
      ) : (
        <DataList items={items} columns={columns} rowKey={(e) => e.id} caption={t('audit.caption')} empty={t('audit.empty')} />
      )}
      {entries.hasNextPage ? (
        <Button type="button" variant="outline" className="mt-4" disabled={entries.isFetchingNextPage} onClick={() => void entries.fetchNextPage()}>
          {t('common.load_more')}
        </Button>
      ) : null}
    </>
  )
}
```

- [ ] **Step 5: `web/src/routes/_app/audit.tsx`**

```tsx
import { createFileRoute } from '@tanstack/react-router'
import { AuditPage } from '@/features/audit/audit-page'
import { adminGuard } from '@/features/auth/guards'
import { guardBeforeLoad } from '@/features/auth/route-guards'

export const Route = createFileRoute('/_app/audit')({
  beforeLoad: guardBeforeLoad(adminGuard),
  component: AuditPage,
})
```

- [ ] **Step 6: Tests laufen lassen → PASS**

Run: `cd web && npx vitest run src/features/audit`
Expected: PASS (filters 2, page 4).

- [ ] **Step 7: Route-Baum, Typecheck, Suite, Commit**

Run: `cd web && npx vite build && npm run typecheck && npx vitest run`
Expected: grün.

```bash
git add web/src/features/audit web/src/routes web/src/routeTree.gen.ts
git commit -m "feat(web): add audit log with filters, cursor paging and chain verification badge" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 18: Profil I – Sprache, Passwort, Sitzungen

**Files:**
- Create: `web/src/lib/user-agent.ts`, `web/src/features/profile/section-card.tsx`, `web/src/features/profile/language-card.tsx`, `web/src/features/profile/password-card.tsx`, `web/src/features/profile/session-columns.tsx`, `web/src/features/profile/sessions-card.tsx`, `web/src/features/profile/admin-sessions-card.tsx`
- Test: `web/src/lib/user-agent.test.ts`, `web/src/features/profile/language-card.test.tsx`, `web/src/features/profile/password-card.test.tsx`, `web/src/features/profile/sessions-card.test.tsx`

**Interfaces:**
- Consumes: `api`, `unwrap`, `unwrapVoid`, `isApiError`, `errorMessage`, `useErrorToast` (Task 5); `useMe` (Task 7); `SUPPORTED_LOCALES` (Task 1); `DataList`, `NativeSelect`, `ConfirmDialog` (Task 2); `useFormat` (Task 1)
- Produces:
```ts
export interface DeviceInfo { browser: string | null; os: string | null }
export function describeUserAgent(ua: string): DeviceInfo
export function SectionCard(props: { title: string; description?: string; actions?: ReactNode; className?: string; children: ReactNode }): JSX.Element   // <section aria-labelledby>
export function LanguageCard(): JSX.Element
export function PasswordCard(): JSX.Element
export function useDeviceLabel(): (ua: string) => string
export function buildSessionColumns(opts: { t: TFunction; fmt: ReturnType<typeof useFormat>; deviceLabel(ua: string): string; withUser: boolean; pending: boolean; onRevoke(s: Session): void }): Column<Session>[]
export const mySessionsKey: readonly ['sessions', 'mine']
export function SessionsCard(): JSX.Element
export const allSessionsKey: readonly ['sessions', 'all']
export function AdminSessionsCard(): JSX.Element
```

- [ ] **Step 1: Failing Tests**

`web/src/lib/user-agent.test.ts`:
```ts
import { describe, expect, it } from 'vitest'
import { describeUserAgent } from './user-agent'

describe('describeUserAgent', () => {
  it.each([
    ['Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1', 'Safari', 'iOS'],
    ['Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:131.0) Gecko/20100101 Firefox/131.0', 'Firefox', 'Windows'],
    ['Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36', 'Chrome', 'Linux'],
    ['Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36 Edg/129.0.0.0', 'Edge', 'macOS'],
    ['Mozilla/5.0 (Linux; Android 15) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Mobile Safari/537.36', 'Chrome', 'Android'],
  ])('recognises %s', (ua, browser, os) => {
    expect(describeUserAgent(ua)).toEqual({ browser, os })
  })

  it('returns nulls for unknown agents', () => {
    expect(describeUserAgent('python-requests/2.32.3')).toEqual({ browser: null, os: null })
  })
})
```

`web/src/features/profile/language-card.test.tsx`:
```tsx
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { db, loginAs } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { LanguageCard } from './language-card'

describe('LanguageCard', () => {
  it('stores the language and switches the UI immediately', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<LanguageCard />, { me: loginAs('admin') })
    await user.selectOptions(screen.getByLabelText('Sprache der Oberfläche'), 'en')
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Language' })).toBeInTheDocument())
    expect(document.documentElement.lang).toBe('en')
    expect(db.users.find((u) => u.username === 'admin')?.locale).toBe('en')
  })
})
```

`web/src/features/profile/password-card.test.tsx`:
```tsx
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { getCsrfToken } from '@/api/csrf'
import { currentSession, db, loginAs, MOCK_PASSWORD } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { PasswordCard } from './password-card'

async function fill(current: string, next: string, confirm: string) {
  const user = userEvent.setup()
  await user.type(screen.getByLabelText('Aktuelles Passwort'), current)
  await user.type(screen.getByLabelText('Neues Passwort'), next)
  await user.type(screen.getByLabelText('Neues Passwort wiederholen'), confirm)
  return user
}

describe('PasswordCard', () => {
  it('checks length and confirmation locally', async () => {
    await renderWithProviders(<PasswordCard />, { me: loginAs('admin') })
    await fill(MOCK_PASSWORD, 'short', 'other')
    expect(screen.getByText('Das neue Passwort muss 12 bis 256 Zeichen lang sein.')).toBeInTheDocument()
    expect(screen.getByText('Die Passwörter stimmen nicht überein.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Passwort ändern' })).toBeDisabled()
  })

  it('explains a wrong current password', async () => {
    await renderWithProviders(<PasswordCard />, { me: loginAs('admin') })
    const user = await fill('not-the-password', 'brand-new-password', 'brand-new-password')
    await user.click(screen.getByRole('button', { name: 'Passwort ändern' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Das aktuelle Passwort ist falsch.')
  })

  it('changes the password, signs out other sessions and refreshes the CSRF token', async () => {
    await renderWithProviders(<PasswordCard />, { me: loginAs('admin') })
    const user = await fill(MOCK_PASSWORD, 'brand-new-password', 'brand-new-password')
    await user.click(screen.getByRole('button', { name: 'Passwort ändern' }))
    expect(await screen.findByText('Passwort geändert. Andere Sitzungen wurden abgemeldet.')).toBeInTheDocument()
    expect(db.sessions.filter((s) => s.userId === 1)).toHaveLength(1)
    expect(getCsrfToken()).toBe(currentSession()?.csrf)
    expect(screen.getByLabelText('Aktuelles Passwort')).toHaveValue('')
  })
})
```

`web/src/features/profile/sessions-card.test.tsx`:
```tsx
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { loginAs, MOCK_TOTP_CODE } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { AdminSessionsCard } from './admin-sessions-card'
import { SessionsCard } from './sessions-card'

describe('SessionsCard', () => {
  it('lists own sessions and flags the current one', async () => {
    await renderWithProviders(<SessionsCard />, { me: loginAs('admin') })
    const table = await screen.findByRole('table', { name: 'Deine Sitzungen' })
    expect(within(table).getAllByRole('row')).toHaveLength(4)
    expect(within(table).getByText('Diese Sitzung')).toBeInTheDocument()
    expect(within(table).getByText('Safari auf iOS')).toBeInTheDocument()
  })

  it('revokes a single other session', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<SessionsCard />, { me: loginAs('admin') })
    await user.click(await screen.findByRole('button', { name: 'Sitzung Firefox auf Windows abmelden' }))
    await waitFor(() => expect(within(screen.getByRole('table', { name: 'Deine Sitzungen' })).getAllByRole('row')).toHaveLength(3))
  })

  it('revokes all other sessions after confirmation', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<SessionsCard />, { me: loginAs('admin') })
    await user.click(await screen.findByRole('button', { name: 'Alle anderen abmelden' }))
    await user.click(within(screen.getByRole('dialog', { name: 'Alle anderen Sitzungen abmelden?' })).getByRole('button', { name: 'Alle anderen abmelden' }))
    await waitFor(() => expect(within(screen.getByRole('table', { name: 'Deine Sitzungen' })).getAllByRole('row')).toHaveLength(2))
  })
})

describe('AdminSessionsCard', () => {
  it('shows sessions of all users and revokes one after sudo', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<AdminSessionsCard />, { me: loginAs('admin') })
    const table = await screen.findByRole('table', { name: 'Sitzungen aller Benutzer' })
    const aliceRow = within(table).getByRole('row', { name: /alice/ })
    await user.click(within(aliceRow).getByRole('button', { name: 'Sitzung Chrome auf Linux abmelden' }))
    const sudo = await screen.findByRole('dialog', { name: 'Aktion bestätigen' })
    await user.type(within(sudo).getByLabelText('Code aus der Authenticator-App'), MOCK_TOTP_CODE)
    await user.click(within(sudo).getByRole('button', { name: 'Bestätigen' }))
    await waitFor(() => expect(within(screen.getByRole('table', { name: 'Sitzungen aller Benutzer' })).queryByRole('row', { name: /alice/ })).not.toBeInTheDocument())
  })
})
```

- [ ] **Step 2: Tests laufen lassen → FAIL**

Run: `cd web && npx vitest run src/lib/user-agent.test.ts src/features/profile`
Expected: FAIL – Module fehlen.

- [ ] **Step 3: `web/src/lib/user-agent.ts` und `web/src/features/profile/section-card.tsx`**

```ts
// web/src/lib/user-agent.ts
export interface DeviceInfo {
  browser: string | null
  os: string | null
}

/** Coarse, privacy-friendly summary for session lists (order matters: Edge/Chrome/Safari share tokens). */
export function describeUserAgent(ua: string): DeviceInfo {
  let browser: string | null = null
  if (/Edg\//.test(ua)) browser = 'Edge'
  else if (/Firefox\//.test(ua)) browser = 'Firefox'
  else if (/Chrome\//.test(ua)) browser = 'Chrome'
  else if (/Safari\//.test(ua)) browser = 'Safari'

  let os: string | null = null
  if (/iPhone|iPad/.test(ua)) os = 'iOS'
  else if (/Android/.test(ua)) os = 'Android'
  else if (/Mac OS X/.test(ua)) os = 'macOS'
  else if (/Windows/.test(ua)) os = 'Windows'
  else if (/Linux/.test(ua)) os = 'Linux'

  return { browser, os }
}
```

```tsx
// web/src/features/profile/section-card.tsx
import { useId, type ReactNode } from 'react'
import { Card, CardContent, CardDescription, CardHeader } from '@/components/ui/card'
import { cn } from '@/lib/utils'

export function SectionCard({
  title,
  description,
  actions,
  className,
  children,
}: {
  title: string
  description?: string
  actions?: ReactNode
  className?: string
  children: ReactNode
}) {
  const id = useId()
  return (
    <section aria-labelledby={id} className={cn('min-w-0', className)}>
      <Card className="h-full">
        <CardHeader>
          <div className="flex flex-wrap items-start justify-between gap-2">
            <div className="space-y-1">
              <h2 id={id} className="leading-none font-semibold">
                {title}
              </h2>
              {description ? <CardDescription>{description}</CardDescription> : null}
            </div>
            {actions ? <div className="flex flex-wrap gap-2">{actions}</div> : null}
          </div>
        </CardHeader>
        <CardContent>{children}</CardContent>
      </Card>
    </section>
  )
}
```

- [ ] **Step 4: `web/src/features/profile/language-card.tsx`**

```tsx
import { useMutation } from '@tanstack/react-query'
import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { Locale } from '@/api/types'
import { NativeSelect } from '@/components/native-select'
import { Label } from '@/components/ui/label'
import { useMe } from '@/features/auth/me'
import { SUPPORTED_LOCALES } from '@/i18n'
import { useErrorToast } from '@/lib/error-message'
import { SectionCard } from './section-card'

export function LanguageCard() {
  const { t } = useTranslation()
  const id = useId()
  const me = useMe()
  const notifyError = useErrorToast()
  // PATCH /me returns Me; the API middleware applies it (cache + i18n language + <html lang>).
  const update = useMutation({
    mutationFn: (locale: Locale) => unwrap(api.PATCH('/me', { body: { locale } })),
    onSuccess: () => toast.success(t('profile.language.saved')),
    onError: notifyError,
  })
  return (
    <SectionCard title={t('profile.language.title')}>
      <div className="space-y-2">
        <Label htmlFor={id}>{t('profile.language.label')}</Label>
        <NativeSelect
          id={id}
          className="w-full"
          value={me?.locale ?? 'de'}
          disabled={update.isPending}
          onChange={(e) => update.mutate(e.target.value as Locale)}
        >
          {SUPPORTED_LOCALES.map((locale) => (
            <option key={locale} value={locale}>
              {t(`profile.language.${locale}`)}
            </option>
          ))}
        </NativeSelect>
      </div>
    </SectionCard>
  )
}
```

- [ ] **Step 5: `web/src/features/profile/password-card.tsx`**

```tsx
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { isApiError, unwrapVoid } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { errorMessage } from '@/lib/error-message'
import { SectionCard } from './section-card'

export function PasswordCard() {
  const { t } = useTranslation()
  const id = useId()
  const queryClient = useQueryClient()
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const tooShort = next !== '' && (next.length < 12 || next.length > 256)
  const mismatch = confirm !== '' && confirm !== next
  const complete = current !== '' && next !== '' && confirm !== '' && !tooShort && !mismatch

  const change = useMutation({
    mutationFn: () => unwrapVoid(api.POST('/me/password', { body: { current_password: current, new_password: next } })),
    onSuccess: () => {
      setCurrent('')
      setNext('')
      setConfirm('')
      toast.success(t('profile.password.changed'))
      void queryClient.invalidateQueries({ queryKey: ['sessions'] })
    },
  })
  const error = change.error
    ? isApiError(change.error, 'invalid_credentials')
      ? t('profile.password.wrong_current')
      : errorMessage(change.error, t)
    : null

  return (
    <SectionCard title={t('profile.password.title')} description={t('profile.password.hint')}>
      <form
        className="space-y-4"
        noValidate
        onSubmit={(e) => {
          e.preventDefault()
          if (complete) change.mutate()
        }}
      >
        <div className="space-y-2">
          <Label htmlFor={`${id}-current`}>{t('profile.password.current')}</Label>
          <Input id={`${id}-current`} type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} />
        </div>
        <div className="space-y-2">
          <Label htmlFor={`${id}-new`}>{t('profile.password.new')}</Label>
          <Input
            id={`${id}-new`}
            type="password"
            autoComplete="new-password"
            value={next}
            maxLength={256}
            aria-invalid={tooShort || undefined}
            onChange={(e) => setNext(e.target.value)}
          />
          {tooShort ? <p className="text-destructive text-sm">{t('profile.password.too_short')}</p> : null}
        </div>
        <div className="space-y-2">
          <Label htmlFor={`${id}-confirm`}>{t('profile.password.confirm')}</Label>
          <Input
            id={`${id}-confirm`}
            type="password"
            autoComplete="new-password"
            value={confirm}
            aria-invalid={mismatch || undefined}
            onChange={(e) => setConfirm(e.target.value)}
          />
          {mismatch ? <p className="text-destructive text-sm">{t('profile.password.mismatch')}</p> : null}
        </div>
        {error ? (
          <p role="alert" className="text-destructive text-sm">
            {error}
          </p>
        ) : null}
        <Button type="submit" disabled={change.isPending || !complete}>
          {t('profile.password.submit')}
        </Button>
      </form>
    </SectionCard>
  )
}
```

- [ ] **Step 6: `web/src/features/profile/session-columns.tsx`**

```tsx
import type { TFunction } from 'i18next'
import { useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import type { Session } from '@/api/types'
import type { Column } from '@/components/data-list'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import type { useFormat } from '@/lib/format'
import { describeUserAgent } from '@/lib/user-agent'

export function useDeviceLabel(): (ua: string) => string {
  const { t } = useTranslation()
  return useCallback(
    (ua: string) => {
      const info = describeUserAgent(ua)
      return t('profile.sessions.device', {
        browser: info.browser ?? t('profile.sessions.unknown'),
        os: info.os ?? t('profile.sessions.unknown'),
      })
    },
    [t],
  )
}

interface SessionColumnsOptions {
  t: TFunction
  fmt: ReturnType<typeof useFormat>
  deviceLabel(ua: string): string
  withUser: boolean
  pending: boolean
  onRevoke(session: Session): void
}

export function buildSessionColumns({ t, fmt, deviceLabel, withUser, pending, onRevoke }: SessionColumnsOptions): Column<Session>[] {
  const columns: Column<Session>[] = [
    {
      id: 'device',
      header: t('profile.sessions.col.device'),
      cell: (s) => (
        <span className="flex flex-wrap items-center gap-2">
          <span title={s.user_agent}>{deviceLabel(s.user_agent)}</span>
          {s.current ? <Badge variant="secondary">{t('profile.sessions.current')}</Badge> : null}
        </span>
      ),
    },
    {
      id: 'location',
      header: t('profile.sessions.col.location'),
      cell: (s) => (
        <span className="font-mono text-xs">
          {s.ip}
          {s.country ? ` (${s.country})` : ''}
        </span>
      ),
    },
    { id: 'last_seen', header: t('profile.sessions.col.last_seen'), cell: (s) => fmt.relative(s.last_seen_at) },
    { id: 'created', header: t('profile.sessions.col.created'), cell: (s) => fmt.dateTime(s.created_at) },
    {
      id: 'actions',
      header: t('profile.sessions.col.actions'),
      cell: (s) =>
        s.current ? null : (
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={pending}
            aria-label={t('profile.sessions.revoke_for', { device: deviceLabel(s.user_agent) })}
            onClick={() => onRevoke(s)}
          >
            {t('profile.sessions.revoke')}
          </Button>
        ),
    },
  ]
  if (withUser) columns.unshift({ id: 'user', header: t('profile.sessions.col.user'), cell: (s) => s.username })
  return columns
}
```

- [ ] **Step 7: `web/src/features/profile/sessions-card.tsx` und `admin-sessions-card.tsx`**

```tsx
// web/src/features/profile/sessions-card.tsx
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap, unwrapVoid } from '@/api/errors'
import type { Session } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { DataList } from '@/components/data-list'
import { Button } from '@/components/ui/button'
import { errorMessage, useErrorToast } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import { SectionCard } from './section-card'
import { buildSessionColumns, useDeviceLabel } from './session-columns'

export const mySessionsKey = ['sessions', 'mine'] as const

export function SessionsCard() {
  const { t } = useTranslation()
  const fmt = useFormat()
  const deviceLabel = useDeviceLabel()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const [confirmOthers, setConfirmOthers] = useState(false)
  const sessions = useQuery({ queryKey: mySessionsKey, queryFn: () => unwrap(api.GET('/me/sessions')) })
  const invalidate = () => queryClient.invalidateQueries({ queryKey: ['sessions'] })

  const revoke = useMutation({
    mutationFn: (session: Session) => unwrapVoid(api.DELETE('/me/sessions/{id}', { params: { path: { id: session.id } } })),
    onSuccess: () => {
      toast.success(t('profile.sessions.revoked'))
      void invalidate()
    },
    onError: notifyError,
  })
  const revokeOthers = useMutation({
    mutationFn: () => unwrapVoid(api.POST('/me/sessions/revoke-others')),
    onSuccess: () => {
      setConfirmOthers(false)
      toast.success(t('profile.sessions.revoked_others'))
      void invalidate()
    },
    onError: notifyError,
  })
  const hasOthers = (sessions.data ?? []).some((s) => !s.current)

  return (
    <SectionCard
      title={t('profile.sessions.title')}
      className="md:col-span-2"
      actions={
        <Button type="button" variant="outline" size="sm" disabled={!hasOthers} onClick={() => setConfirmOthers(true)}>
          {t('profile.sessions.revoke_others')}
        </Button>
      }
    >
      {sessions.isError ? (
        <p role="alert" className="text-destructive">
          {errorMessage(sessions.error, t)}
        </p>
      ) : null}
      {sessions.isPending ? (
        <p className="text-muted-foreground">{t('common.loading')}</p>
      ) : (
        <DataList
          items={sessions.data ?? []}
          columns={buildSessionColumns({ t, fmt, deviceLabel, withUser: false, pending: revoke.isPending, onRevoke: (s) => revoke.mutate(s) })}
          rowKey={(s) => s.id}
          caption={t('profile.sessions.caption')}
          empty={t('profile.sessions.empty')}
        />
      )}
      <ConfirmDialog
        open={confirmOthers}
        onOpenChange={setConfirmOthers}
        title={t('profile.sessions.revoke_others_title')}
        description={t('profile.sessions.revoke_others_description')}
        confirmLabel={t('profile.sessions.revoke_others')}
        destructive
        pending={revokeOthers.isPending}
        onConfirm={() => revokeOthers.mutate()}
      />
    </SectionCard>
  )
}
```

```tsx
// web/src/features/profile/admin-sessions-card.tsx
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap, unwrapVoid } from '@/api/errors'
import type { Session } from '@/api/types'
import { DataList } from '@/components/data-list'
import { errorMessage, useErrorToast } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import { SectionCard } from './section-card'
import { buildSessionColumns, useDeviceLabel } from './session-columns'

export const allSessionsKey = ['sessions', 'all'] as const

export function AdminSessionsCard() {
  const { t } = useTranslation()
  const fmt = useFormat()
  const deviceLabel = useDeviceLabel()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const sessions = useQuery({ queryKey: allSessionsKey, queryFn: () => unwrap(api.GET('/admin/sessions')) })
  const revoke = useMutation({
    mutationFn: (session: Session) => unwrapVoid(api.DELETE('/admin/sessions/{id}', { params: { path: { id: session.id } } })),
    onSuccess: () => {
      toast.success(t('profile.sessions.revoked'))
      void queryClient.invalidateQueries({ queryKey: ['sessions'] })
    },
    onError: notifyError,
  })

  return (
    <SectionCard title={t('profile.sessions.all_title')} className="md:col-span-2">
      {sessions.isError ? (
        <p role="alert" className="text-destructive">
          {errorMessage(sessions.error, t)}
        </p>
      ) : null}
      {sessions.isPending ? (
        <p className="text-muted-foreground">{t('common.loading')}</p>
      ) : (
        <DataList
          items={sessions.data ?? []}
          columns={buildSessionColumns({ t, fmt, deviceLabel, withUser: true, pending: revoke.isPending, onRevoke: (s) => revoke.mutate(s) })}
          rowKey={(s) => s.id}
          caption={t('profile.sessions.all_caption')}
          empty={t('profile.sessions.empty')}
        />
      )}
    </SectionCard>
  )
}
```

- [ ] **Step 8: Tests laufen lassen → PASS**

Run: `cd web && npx vitest run src/lib/user-agent.test.ts src/features/profile`
Expected: PASS (user-agent 6, language 1, password 3, sessions 4).

- [ ] **Step 9: Gesamte Suite + Typecheck, Commit**

Run: `cd web && npx vitest run && npm run typecheck`
Expected: grün.

```bash
git add web/src/lib/user-agent.ts web/src/lib/user-agent.test.ts web/src/features/profile
git commit -m "feat(web): add profile language, password change and session management" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 19: Profil II – TOTP, Recovery-Codes, Passkeys; Profilseite und Route

**Files:**
- Create: `web/src/features/profile/two-factor-card.tsx`, `web/src/features/profile/recovery-card.tsx`, `web/src/features/profile/passkeys-card.tsx`, `web/src/features/profile/profile-page.tsx`, `web/src/routes/_app/profile.tsx`
- Test: `web/src/features/profile/two-factor-card.test.tsx`, `web/src/features/profile/recovery-card.test.tsx`, `web/src/features/profile/passkeys-card.test.tsx`, `web/src/features/profile/profile-page.test.tsx`

**Interfaces:**
- Consumes: `TotpSetupCard`, `PasskeySetupCard`, `RecoveryCodesPanel` (Task 9); `isWebAuthnSupported` (Task 6); `useMe`, `refreshMe` (Task 7); `SectionCard`, `LanguageCard`, `PasswordCard`, `SessionsCard`, `AdminSessionsCard` (Task 18); `ConfirmDialog`, `DataList`, `PageHeader` (Task 2)
- Produces:
```ts
export function TwoFactorCard(): JSX.Element
export function RecoveryCard(): JSX.Element
export const passkeysKey: readonly ['passkeys']
export function PasskeysCard(): JSX.Element
export function ProfilePage(): JSX.Element
// Route: /profile (jeder angemeldete Benutzer)
```
Nach jeder Änderung an 2FA-Methoden wird `refreshMe(queryClient)` aufgerufen, weil `totp_enabled`, `passkey_count` und `recovery_codes_remaining` im `Me` stehen (Sudo-Dialog und 2FA-Tabs hängen davon ab).

- [ ] **Step 1: Failing Tests**

`web/src/features/profile/two-factor-card.test.tsx`:
```tsx
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { loginAs, MOCK_TOTP_CODE } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { TwoFactorCard } from './two-factor-card'

vi.mock('qrcode', () => ({ default: { toDataURL: vi.fn(async () => 'data:image/png;base64,QR') } }))

describe('TwoFactorCard', () => {
  it('sets up TOTP for a user without 2FA and shows the first recovery codes', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<TwoFactorCard />, { me: loginAs('alice') })
    const card = screen.getByRole('region', { name: 'Authenticator-App (TOTP)' })
    expect(within(card).getByText('Nicht eingerichtet')).toBeInTheDocument()
    await user.click(within(card).getByRole('button', { name: 'Einrichten' }))
    const dialog = screen.getByRole('dialog', { name: 'Authenticator-App einrichten' })
    await user.click(within(dialog).getByRole('button', { name: 'Einrichtung starten' }))
    await user.type(await within(dialog).findByLabelText('Code aus der Authenticator-App'), MOCK_TOTP_CODE)
    await user.click(within(dialog).getByRole('button', { name: 'Aktivieren' }))
    expect(await within(dialog).findByRole('list', { name: 'Wiederherstellungscodes' })).toBeInTheDocument()
    await user.click(within(dialog).getByRole('checkbox', { name: 'Ich habe die Codes sicher gespeichert.' }))
    await user.click(within(dialog).getByRole('button', { name: 'Fertig' }))
    await waitFor(() => expect(within(card).getByText('Aktiv')).toBeInTheDocument())
  })

  it('disables TOTP after confirmation and sudo', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<TwoFactorCard />, { me: loginAs('admin') })
    const card = screen.getByRole('region', { name: 'Authenticator-App (TOTP)' })
    await user.click(within(card).getByRole('button', { name: 'Deaktivieren' }))
    await user.click(within(screen.getByRole('dialog', { name: 'Authenticator-App deaktivieren?' })).getByRole('button', { name: 'Deaktivieren' }))
    const sudo = await screen.findByRole('dialog', { name: 'Aktion bestätigen' })
    await user.type(within(sudo).getByLabelText('Code aus der Authenticator-App'), MOCK_TOTP_CODE)
    await user.click(within(sudo).getByRole('button', { name: 'Bestätigen' }))
    await waitFor(() => expect(within(card).getByText('Nicht eingerichtet')).toBeInTheDocument())
  })
})
```

`web/src/features/profile/recovery-card.test.tsx`:
```tsx
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { loginAs } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { RecoveryCard } from './recovery-card'

describe('RecoveryCard', () => {
  it('shows the remaining codes and regenerates them', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<RecoveryCard />, { me: loginAs('admin', { sudo: true }) })
    expect(screen.getByText('Verbleibende Codes: 10')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Neue Codes erzeugen' }))
    await user.click(within(screen.getByRole('dialog', { name: 'Neue Wiederherstellungscodes erzeugen?' })).getByRole('button', { name: 'Neue Codes erzeugen' }))
    const dialog = await screen.findByRole('dialog', { name: 'Neue Wiederherstellungscodes' })
    expect(within(within(dialog).getByRole('list', { name: 'Wiederherstellungscodes' })).getAllByRole('listitem')).toHaveLength(10)
  })

  it('is hidden for accounts without a second factor', async () => {
    await renderWithProviders(<RecoveryCard />, { me: loginAs('alice') })
    expect(screen.queryByRole('region', { name: 'Wiederherstellungscodes' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Neue Codes erzeugen' })).not.toBeInTheDocument()
  })
})
```

`web/src/features/profile/passkeys-card.test.tsx`:
```tsx
import { browserSupportsWebAuthn, startRegistration } from '@simplewebauthn/browser'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { loginAs } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { PasskeysCard } from './passkeys-card'

vi.mock('@simplewebauthn/browser', () => ({
  browserSupportsWebAuthn: vi.fn(() => true),
  startAuthentication: vi.fn(),
  startRegistration: vi.fn(),
}))

beforeEach(() => {
  vi.mocked(browserSupportsWebAuthn).mockReturnValue(true)
  vi.mocked(startRegistration).mockResolvedValue({
    id: 'c',
    rawId: 'c',
    type: 'public-key',
    response: {},
    clientExtensionResults: {},
  } as unknown as Awaited<ReturnType<typeof startRegistration>>)
})

describe('PasskeysCard', () => {
  it('lists, adds and removes passkeys', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<PasskeysCard />, { me: loginAs('admin', { sudo: true }) })
    const table = await screen.findByRole('table', { name: 'Registrierte Passkeys' })
    expect(within(table).getByText('MacBook Touch ID')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Passkey hinzufügen' }))
    const dialog = screen.getByRole('dialog', { name: 'Passkey hinzufügen' })
    const name = within(dialog).getByLabelText('Name des Passkeys')
    await user.clear(name)
    await user.type(name, 'YubiKey')
    await user.click(within(dialog).getByRole('button', { name: 'Passkey registrieren' }))
    expect(await screen.findByText('Passkey YubiKey hinzugefügt.')).toBeInTheDocument()
    expect(await screen.findByRole('row', { name: /YubiKey/ })).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'MacBook Touch ID entfernen' }))
    await user.click(within(screen.getByRole('dialog', { name: 'Passkey MacBook Touch ID entfernen?' })).getByRole('button', { name: 'Löschen' }))
    await waitFor(() => expect(screen.queryByRole('row', { name: /MacBook Touch ID/ })).not.toBeInTheDocument())
  })

  it('hides "add" when the browser lacks WebAuthn', async () => {
    vi.mocked(browserSupportsWebAuthn).mockReturnValue(false)
    await renderWithProviders(<PasskeysCard />, { me: loginAs('admin') })
    expect(await screen.findByText('Dieser Browser unterstützt keine Passkeys.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Passkey hinzufügen' })).not.toBeInTheDocument()
  })
})
```

`web/src/features/profile/profile-page.test.tsx`:
```tsx
import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { loginAs } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { ProfilePage } from './profile-page'

describe('ProfilePage', () => {
  it('shows the admin-only session overview only to admins', async () => {
    await renderWithProviders(<ProfilePage />, { me: loginAs('alice') })
    expect(screen.getByRole('heading', { name: 'Profil' })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'Aktive Sitzungen' })).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'Alle Sitzungen (Admin)' })).not.toBeInTheDocument()
  })

  it('includes all sessions for admins', async () => {
    await renderWithProviders(<ProfilePage />, { me: loginAs('admin') })
    expect(screen.getByRole('region', { name: 'Alle Sitzungen (Admin)' })).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Tests laufen lassen → FAIL**

Run: `cd web && npx vitest run src/features/profile`
Expected: FAIL – `two-factor-card`, `recovery-card`, `passkeys-card`, `profile-page` fehlen.

- [ ] **Step 3: `web/src/features/profile/two-factor-card.tsx`**

```tsx
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrapVoid } from '@/api/errors'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { refreshMe, useMe } from '@/features/auth/me'
import { RecoveryCodesPanel } from '@/features/setup/recovery-codes-panel'
import { TotpSetupCard } from '@/features/setup/totp-setup-card'
import { useErrorToast } from '@/lib/error-message'
import { SectionCard } from './section-card'

export function TwoFactorCard() {
  const { t } = useTranslation()
  const me = useMe()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const [setupOpen, setSetupOpen] = useState(false)
  const [codes, setCodes] = useState<string[] | null>(null)
  const [confirmDisable, setConfirmDisable] = useState(false)
  const enabled = me?.totp_enabled === true

  const disable = useMutation({
    mutationFn: () => unwrapVoid(api.DELETE('/me/totp')),
    onSuccess: async () => {
      setConfirmDisable(false)
      toast.success(t('profile.totp.disabled_done'))
      await refreshMe(queryClient)
    },
    onError: (err) => {
      setConfirmDisable(false)
      notifyError(err)
    },
  })

  const closeSetup = () => {
    setSetupOpen(false)
    setCodes(null)
  }

  return (
    <SectionCard
      title={t('profile.totp.title')}
      actions={<Badge variant={enabled ? 'secondary' : 'outline'}>{enabled ? t('profile.totp.enabled') : t('profile.totp.disabled')}</Badge>}
    >
      {enabled ? (
        <Button type="button" variant="outline" onClick={() => setConfirmDisable(true)}>
          {t('profile.totp.disable')}
        </Button>
      ) : (
        <Button type="button" onClick={() => setSetupOpen(true)}>
          {t('profile.totp.setup')}
        </Button>
      )}
      <Dialog open={setupOpen} onOpenChange={(open) => (open ? setSetupOpen(true) : closeSetup())}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{t('profile.totp.setup_title')}</DialogTitle>
            <DialogDescription>{t('profile.totp.setup_description')}</DialogDescription>
          </DialogHeader>
          {codes ? (
            <RecoveryCodesPanel codes={codes} doneLabel={t('profile.recovery.done')} onDone={closeSetup} />
          ) : (
            <TotpSetupCard
              onEnabled={(newCodes) => {
                toast.success(t('profile.totp.enabled_done'))
                void refreshMe(queryClient)
                if (newCodes.length > 0) setCodes(newCodes)
                else closeSetup()
              }}
            />
          )}
        </DialogContent>
      </Dialog>
      <ConfirmDialog
        open={confirmDisable}
        onOpenChange={setConfirmDisable}
        title={t('profile.totp.disable_title')}
        description={t('profile.totp.disable_description')}
        confirmLabel={t('profile.totp.disable')}
        destructive
        pending={disable.isPending}
        onConfirm={() => disable.mutate()}
      />
    </SectionCard>
  )
}
```

- [ ] **Step 4: `web/src/features/profile/recovery-card.tsx`**

```tsx
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { refreshMe, useMe } from '@/features/auth/me'
import { RecoveryCodesPanel } from '@/features/setup/recovery-codes-panel'
import { useErrorToast } from '@/lib/error-message'
import { SectionCard } from './section-card'

export function RecoveryCard() {
  const { t } = useTranslation()
  const me = useMe()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const [confirm, setConfirm] = useState(false)
  const [codes, setCodes] = useState<string[] | null>(null)
  const regenerate = useMutation({
    mutationFn: () => unwrap(api.POST('/me/recovery-codes')),
    onSuccess: (result) => {
      setConfirm(false)
      setCodes(result.codes)
      void refreshMe(queryClient)
    },
    onError: (err) => {
      setConfirm(false)
      notifyError(err)
    },
  })

  if (!me || (!me.totp_enabled && me.passkey_count === 0)) return null

  return (
    <SectionCard title={t('profile.recovery.title')}>
      <div className="space-y-3">
        <p className="text-sm">{t('profile.recovery.remaining', { remaining: me.recovery_codes_remaining })}</p>
        <Button type="button" variant="outline" onClick={() => setConfirm(true)}>
          {t('profile.recovery.regenerate')}
        </Button>
      </div>
      <ConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        title={t('profile.recovery.regenerate_title')}
        description={t('profile.recovery.regenerate_description')}
        confirmLabel={t('profile.recovery.regenerate')}
        pending={regenerate.isPending}
        onConfirm={() => regenerate.mutate()}
      />
      <Dialog open={codes !== null} onOpenChange={(open) => !open && setCodes(null)}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{t('profile.recovery.new_codes_title')}</DialogTitle>
            <DialogDescription>{t('recovery.description')}</DialogDescription>
          </DialogHeader>
          {codes ? <RecoveryCodesPanel codes={codes} doneLabel={t('profile.recovery.done')} onDone={() => setCodes(null)} /> : null}
        </DialogContent>
      </Dialog>
    </SectionCard>
  )
}
```

- [ ] **Step 5: `web/src/features/profile/passkeys-card.tsx`**

```tsx
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap, unwrapVoid } from '@/api/errors'
import type { Passkey, PasskeyCreated } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { DataList, type Column } from '@/components/data-list'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { refreshMe } from '@/features/auth/me'
import { PasskeySetupCard } from '@/features/setup/passkey-setup-card'
import { RecoveryCodesPanel } from '@/features/setup/recovery-codes-panel'
import { isWebAuthnSupported } from '@/features/webauthn/webauthn'
import { errorMessage, useErrorToast } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import { SectionCard } from './section-card'

export const passkeysKey = ['passkeys'] as const

export function PasskeysCard() {
  const { t } = useTranslation()
  const fmt = useFormat()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const supported = isWebAuthnSupported()
  const [adding, setAdding] = useState(false)
  const [codes, setCodes] = useState<string[] | null>(null)
  const [deleting, setDeleting] = useState<Passkey | null>(null)
  const passkeys = useQuery({ queryKey: passkeysKey, queryFn: () => unwrap(api.GET('/me/passkeys')) })

  const remove = useMutation({
    mutationFn: (passkey: Passkey) => unwrapVoid(api.DELETE('/me/passkeys/{id}', { params: { path: { id: passkey.id } } })),
    onSuccess: async (_data, passkey) => {
      setDeleting(null)
      toast.success(t('profile.passkeys.deleted', { name: passkey.name }))
      await queryClient.invalidateQueries({ queryKey: passkeysKey })
      await refreshMe(queryClient)
    },
    onError: (err) => {
      setDeleting(null)
      notifyError(err)
    },
  })

  const onRegistered = (created: PasskeyCreated) => {
    toast.success(t('profile.passkeys.added', { name: created.passkey.name }))
    void queryClient.invalidateQueries({ queryKey: passkeysKey })
    void refreshMe(queryClient)
    if (created.recovery_codes.length > 0) setCodes(created.recovery_codes)
    else setAdding(false)
  }
  const closeAdd = () => {
    setAdding(false)
    setCodes(null)
  }

  const columns: Column<Passkey>[] = [
    { id: 'name', header: t('profile.passkeys.col.name'), cell: (p) => <span className="font-medium">{p.name}</span> },
    { id: 'created', header: t('profile.passkeys.col.created'), cell: (p) => fmt.dateTime(p.created_at) },
    {
      id: 'last_used',
      header: t('profile.passkeys.col.last_used'),
      cell: (p) => (p.last_used_at === null ? t('profile.passkeys.never') : fmt.relative(p.last_used_at)),
    },
    {
      id: 'actions',
      header: t('profile.passkeys.col.actions'),
      cell: (p) => (
        <Button
          type="button"
          size="sm"
          variant="ghost"
          className="text-destructive"
          aria-label={t('profile.passkeys.delete_for', { name: p.name })}
          onClick={() => setDeleting(p)}
        >
          {t('common.delete')}
        </Button>
      ),
    },
  ]

  return (
    <SectionCard
      title={t('profile.passkeys.title')}
      className="md:col-span-2"
      actions={
        supported ? (
          <Button type="button" size="sm" onClick={() => setAdding(true)}>
            {t('profile.passkeys.add')}
          </Button>
        ) : undefined
      }
    >
      {!supported ? <p className="text-muted-foreground mb-3 text-sm">{t('passkey.unsupported')}</p> : null}
      {passkeys.isError ? (
        <p role="alert" className="text-destructive">
          {errorMessage(passkeys.error, t)}
        </p>
      ) : null}
      {passkeys.isPending ? (
        <p className="text-muted-foreground">{t('common.loading')}</p>
      ) : (
        <DataList items={passkeys.data ?? []} columns={columns} rowKey={(p) => p.id} caption={t('profile.passkeys.caption')} empty={t('profile.passkeys.empty')} />
      )}
      <Dialog open={adding} onOpenChange={(open) => (open ? setAdding(true) : closeAdd())}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{t('profile.passkeys.add')}</DialogTitle>
            <DialogDescription>{t('profile.passkeys.add_description')}</DialogDescription>
          </DialogHeader>
          {codes ? (
            <RecoveryCodesPanel codes={codes} doneLabel={t('profile.recovery.done')} onDone={closeAdd} />
          ) : (
            <PasskeySetupCard onRegistered={onRegistered} />
          )}
        </DialogContent>
      </Dialog>
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={t('profile.passkeys.delete_title', { name: deleting?.name ?? '' })}
        description={t('profile.passkeys.delete_description')}
        confirmLabel={t('common.delete')}
        destructive
        pending={remove.isPending}
        onConfirm={() => deleting && remove.mutate(deleting)}
      />
    </SectionCard>
  )
}
```

- [ ] **Step 6: `web/src/features/profile/profile-page.tsx` und Route**

```tsx
// web/src/features/profile/profile-page.tsx
import { useTranslation } from 'react-i18next'
import { PageHeader } from '@/components/page-header'
import { useMe } from '@/features/auth/me'
import { AdminSessionsCard } from './admin-sessions-card'
import { LanguageCard } from './language-card'
import { PasskeysCard } from './passkeys-card'
import { PasswordCard } from './password-card'
import { RecoveryCard } from './recovery-card'
import { SessionsCard } from './sessions-card'
import { TwoFactorCard } from './two-factor-card'

export function ProfilePage() {
  const { t } = useTranslation()
  const me = useMe()
  return (
    <>
      <PageHeader title={t('profile.title')} description={me ? t('nav.signed_in_as', { username: me.username }) : t('profile.description')} />
      <div className="grid gap-4 md:grid-cols-2">
        <LanguageCard />
        <PasswordCard />
        <TwoFactorCard />
        <RecoveryCard />
        <PasskeysCard />
        <SessionsCard />
        {me?.role === 'admin' ? <AdminSessionsCard /> : null}
      </div>
    </>
  )
}
```

```tsx
// web/src/routes/_app/profile.tsx
import { createFileRoute } from '@tanstack/react-router'
import { ProfilePage } from '@/features/profile/profile-page'

export const Route = createFileRoute('/_app/profile')({ component: ProfilePage })
```

- [ ] **Step 7: Tests laufen lassen → PASS**

Run: `cd web && npx vitest run src/features/profile`
Expected: PASS (two-factor 2, recovery 2, passkeys 2, profile-page 2 sowie Task 18).

- [ ] **Step 8: Route-Baum, Typecheck, Suite, Commit**

Run: `cd web && npx vite build && npm run typecheck && npx vitest run`
Expected: grün.

```bash
git add web/src/features/profile web/src/routes web/src/routeTree.gen.ts
git commit -m "feat(web): add profile 2FA management (TOTP, passkeys, recovery codes) and profile route" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 20: App-Shell – responsive Navigation, rollenbasiertes Menü, Abmelden

**Files:**
- Create: `web/src/features/layout/nav.ts`, `web/src/components/ui/sheet.tsx` (via shadcn)
- Modify: `web/src/features/layout/app-layout.tsx` (ersetzt die vorläufige Fassung aus Task 10)
- Test: `web/src/features/layout/app-layout.test.tsx`

**Interfaces:**
- Consumes: `useMe` (Task 7); `useLogout` (Task 10); `ThemeToggle`, `useMediaQuery`, `DESKTOP_QUERY` (Task 2); alle Routen aus Task 10–19; shadcn `Sheet*`, `Button`; `lucide-react`
- Produces:
```ts
export type NavPath = '/' | '/users' | '/security' | '/audit' | '/profile'
export interface NavItem { to: NavPath; labelKey: string; icon: LucideIcon; adminOnly: boolean }
export const NAV_ITEMS: readonly NavItem[]
export function navItemsFor(role: Role): NavItem[]
export function AppLayout(): JSX.Element   // Sidebar ≥ 768 px, Sheet darunter, Skip-Link, <main id="main">
```

- [ ] **Step 1: shadcn-Sheet hinzufügen**

Run: `cd web && npx shadcn@latest add sheet --yes`
Expected: `src/components/ui/sheet.tsx` angelegt; `package.json`-Abhängigkeiten unverändert (Sheet nutzt dieselbe Radix-Dialog-Basis wie `dialog`). Prüfen mit `git diff --stat web/package.json` – nur falls dort eine neue Abhängigkeit auftaucht: Änderung verwerfen (`git checkout web/package.json web/package-lock.json`) und Lead informieren.

- [ ] **Step 2: Failing Test `web/src/features/layout/app-layout.test.tsx`**

```tsx
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { db, loginAs } from '@/mocks/db'
import { setViewport } from '@/test/media'
import { renderWithProviders } from '@/test/render'
import { AppLayout } from './app-layout'

describe('AppLayout', () => {
  it('shows the full navigation to admins on desktop', async () => {
    await renderWithProviders(<AppLayout />, { me: loginAs('admin') })
    const nav = screen.getByRole('navigation', { name: 'Hauptnavigation' })
    expect(within(nav).getAllByRole('link').map((l) => l.textContent)).toEqual(['Übersicht', 'Benutzer', 'Security Center', 'Audit-Log', 'Profil'])
    expect(screen.getByRole('link', { name: 'Zum Inhalt springen' })).toHaveAttribute('href', '#main')
  })

  it('hides admin areas from regular users', async () => {
    await renderWithProviders(<AppLayout />, { me: loginAs('alice') })
    const nav = screen.getByRole('navigation', { name: 'Hauptnavigation' })
    expect(within(nav).getAllByRole('link').map((l) => l.textContent)).toEqual(['Übersicht', 'Profil'])
  })

  it('marks the current page', async () => {
    await renderWithProviders(<AppLayout />, { path: '/profile', me: loginAs('alice') })
    expect(screen.getByRole('link', { name: 'Profil' })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('link', { name: 'Übersicht' })).not.toHaveAttribute('aria-current')
  })

  it('uses a sheet instead of the sidebar on phones', async () => {
    setViewport('mobile')
    const user = userEvent.setup()
    const { router } = await renderWithProviders(<AppLayout />, { me: loginAs('admin') })
    expect(screen.queryByRole('navigation', { name: 'Hauptnavigation' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Menü öffnen' }))
    const sheet = await screen.findByRole('dialog', { name: 'mc-panel' })
    await user.click(within(sheet).getByRole('link', { name: 'Profil' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/profile'))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('signs out', async () => {
    const user = userEvent.setup()
    const { router } = await renderWithProviders(<AppLayout />, { me: loginAs('admin') })
    await user.click(screen.getByRole('button', { name: 'Abmelden' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/login'))
    expect(db.currentSessionId).toBeNull()
  })
})
```

- [ ] **Step 3: Test laufen lassen → FAIL**

Run: `cd web && npx vitest run src/features/layout`
Expected: FAIL – `Failed to resolve import "./nav"` bzw. fehlende Navigation in der vorläufigen `AppLayout`.

- [ ] **Step 4: `web/src/features/layout/nav.ts`**

```ts
import { LayoutDashboard, ScrollText, ShieldCheck, UserRound, Users, type LucideIcon } from 'lucide-react'
import type { Role } from '@/api/types'

export type NavPath = '/' | '/users' | '/security' | '/audit' | '/profile'

export interface NavItem {
  to: NavPath
  labelKey: string
  icon: LucideIcon
  adminOnly: boolean
}

export const NAV_ITEMS: readonly NavItem[] = [
  { to: '/', labelKey: 'nav.dashboard', icon: LayoutDashboard, adminOnly: false },
  { to: '/users', labelKey: 'nav.users', icon: Users, adminOnly: true },
  { to: '/security', labelKey: 'nav.security', icon: ShieldCheck, adminOnly: true },
  { to: '/audit', labelKey: 'nav.audit', icon: ScrollText, adminOnly: true },
  { to: '/profile', labelKey: 'nav.profile', icon: UserRound, adminOnly: false },
]

export function navItemsFor(role: Role): NavItem[] {
  return NAV_ITEMS.filter((item) => !item.adminOnly || role === 'admin')
}
```

- [ ] **Step 5: `web/src/features/layout/app-layout.tsx`** (komplette Datei)

```tsx
import { Link, Outlet } from '@tanstack/react-router'
import { LogOut, Menu } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ThemeToggle } from '@/components/theme-toggle'
import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle, SheetTrigger } from '@/components/ui/sheet'
import { useMe } from '@/features/auth/me'
import { useLogout } from '@/features/auth/use-logout'
import { DESKTOP_QUERY, useMediaQuery } from '@/lib/use-media-query'
import { navItemsFor, type NavItem } from './nav'

function NavLinks({ items, onNavigate }: { items: NavItem[]; onNavigate?: () => void }) {
  const { t } = useTranslation()
  return (
    <nav aria-label={t('nav.label')} className="p-2">
      <ul className="space-y-1">
        {items.map((item) => {
          const Icon = item.icon
          return (
            <li key={item.to}>
              <Link
                to={item.to}
                activeOptions={{ exact: item.to === '/' }}
                activeProps={{ 'aria-current': 'page', className: 'bg-accent text-accent-foreground' }}
                onClick={onNavigate}
                className="hover:bg-accent focus-visible:ring-ring flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium outline-none focus-visible:ring-2"
              >
                <Icon className="size-4" aria-hidden="true" />
                {t(item.labelKey)}
              </Link>
            </li>
          )
        })}
      </ul>
    </nav>
  )
}

export function AppLayout() {
  const { t } = useTranslation()
  const me = useMe()
  const logout = useLogout()
  const desktop = useMediaQuery(DESKTOP_QUERY)
  const [menuOpen, setMenuOpen] = useState(false)
  const items = navItemsFor(me?.role ?? 'user')

  return (
    <div className="bg-background min-h-svh md:grid md:grid-cols-[15rem_minmax(0,1fr)]">
      <a
        href="#main"
        className="focus:bg-background sr-only focus:not-sr-only focus:absolute focus:top-2 focus:left-2 focus:z-50 focus:rounded-md focus:px-3 focus:py-2"
      >
        {t('nav.skip')}
      </a>
      {desktop ? (
        <aside className="bg-muted/30 sticky top-0 flex h-svh flex-col border-r">
          <div className="flex h-14 items-center px-4 font-semibold">{t('auth.brand')}</div>
          <NavLinks items={items} />
        </aside>
      ) : null}
      <div className="flex min-w-0 flex-col">
        <header className="bg-background/95 sticky top-0 z-10 flex h-14 items-center gap-2 border-b px-4 backdrop-blur">
          {!desktop ? (
            <>
              <Sheet open={menuOpen} onOpenChange={setMenuOpen}>
                <SheetTrigger asChild>
                  <Button type="button" variant="ghost" size="icon" aria-label={t('nav.open_menu')}>
                    <Menu className="size-5" aria-hidden="true" />
                  </Button>
                </SheetTrigger>
                <SheetContent side="left" className="w-72 p-0">
                  <SheetHeader className="border-b px-4 py-3">
                    <SheetTitle>{t('auth.brand')}</SheetTitle>
                    <SheetDescription className="sr-only">{t('nav.menu_description')}</SheetDescription>
                  </SheetHeader>
                  <NavLinks items={items} onNavigate={() => setMenuOpen(false)} />
                </SheetContent>
              </Sheet>
              <span className="font-semibold">{t('auth.brand')}</span>
            </>
          ) : null}
          <div className="ml-auto flex items-center gap-1">
            {me ? <span className="text-muted-foreground hidden text-sm sm:inline">{t('nav.signed_in_as', { username: me.username })}</span> : null}
            <ThemeToggle />
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label={t('auth.logout')}
              title={t('auth.logout')}
              disabled={logout.isPending}
              onClick={() => logout.mutate()}
            >
              <LogOut className="size-4" aria-hidden="true" />
            </Button>
          </div>
        </header>
        <main id="main" tabIndex={-1} className="mx-auto w-full max-w-6xl flex-1 p-4 outline-none md:p-6">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
```

- [ ] **Step 6: Test laufen lassen → PASS**

Run: `cd web && npx vitest run src/features/layout`
Expected: PASS (5 Tests).

- [ ] **Step 7: Build, Typecheck, Lint, gesamte Suite**

Run: `cd web && npx vite build && npm run typecheck && npm run lint && npx vitest run`
Expected: alles grün; `npm run lint` ohne Fehler (Warnungen von `react-refresh/only-export-components` für Dateien, die Komponenten und Konstanten exportieren, sind zulässig).

- [ ] **Step 8: Manuell im Mock-Modus prüfen (Desktop + 360 px)**

Run: `cd web && npm run dev:mock`, im Browser `admin`/`correct-horse-battery`/`123456`.
Expected: Sidebar mit fünf Einträgen; in den DevTools auf 360 px Breite: Hamburger-Menü öffnet Sheet, Tabellen werden zu Karten, kein horizontales Scrollen der Seite; Theme-Umschalter wechselt Hell → Dunkel → System und überlebt ein Neuladen (`localStorage['mcpanel.theme']`). `Ctrl+C`.

- [ ] **Step 9: Commit**

```bash
git add web/src/features/layout web/src/components/ui/sheet.tsx web/src/routeTree.gen.ts
git commit -m "feat(web): add responsive app shell with role-based navigation and mobile sheet" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 21: Playwright – TOTP-Helfer, Smoke gegen MSW, E2E gegen echtes Backend

**Files:**
- Create: `web/playwright.config.ts`, `web/e2e/totp.ts`, `web/e2e/totp.spec.ts`, `web/e2e/smoke.spec.ts`, `web/e2e/login-totp.spec.ts`
- Test: die drei Specs selbst

**Interfaces:**
- Consumes: Skripte `dev:mock`, `e2e`, `e2e:real` (Task 4); gesamte UI
- Produces:
```ts
// e2e/totp.ts – RFC 4226/6238 ohne Abhängigkeiten (node:crypto)
export function base32Decode(input: string): Buffer
export function hotp(key: Buffer, counter: number, digits?: number): string
export function totp(secretBase32: string, timeMs?: number, stepSeconds?: number, digits?: number): string
```
Projekte: `mock` (immer; startet `vite --mode mock --port 5174`, Specs `totp.spec.ts` + `smoke.spec.ts`) und `real` (nur wenn `E2E_BASE_URL` gesetzt; Spec `login-totp.spec.ts`).

- [ ] **Step 1: Failing Spec `web/e2e/totp.spec.ts`** (RFC-6238-Anhang-B-Vektoren, SHA-1, 8 Stellen)

```ts
import { expect, test } from '@playwright/test'
import { base32Decode, totp } from './totp'

// ASCII "12345678901234567890" in base32
const SECRET = 'GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ'

const VECTORS: Array<[number, string]> = [
  [59, '94287082'],
  [1111111109, '07081804'],
  [1111111111, '14050471'],
  [1234567890, '89005924'],
  [2000000000, '69279037'],
  [20000000000, '65353130'],
]

test('decodes base32', () => {
  expect(base32Decode(SECRET).toString('ascii')).toBe('12345678901234567890')
  expect(base32Decode('gezd gnbv')).toEqual(base32Decode('GEZDGNBV'))
})

for (const [seconds, code] of VECTORS) {
  test(`RFC 6238 vector T=${seconds}`, () => {
    expect(totp(SECRET, seconds * 1000, 30, 8)).toBe(code)
  })
}

test('defaults to 6 digits', () => {
  expect(totp(SECRET, 59_000)).toBe('287082')
})
```

- [ ] **Step 2: `web/playwright.config.ts`**

```ts
import { defineConfig, devices } from '@playwright/test'

const MOCK_PORT = 5174
const realBaseURL = process.env.E2E_BASE_URL

export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? 'github' : 'list',
  use: { locale: 'de-DE', trace: 'retain-on-failure' },
  projects: [
    {
      name: 'mock',
      testMatch: /(totp|smoke)\.spec\.ts$/,
      use: { ...devices['Desktop Chrome'], baseURL: `http://localhost:${MOCK_PORT}`, locale: 'de-DE' },
    },
    ...(realBaseURL
      ? [
          {
            name: 'real',
            testMatch: /login-totp\.spec\.ts$/,
            use: { ...devices['Desktop Chrome'], baseURL: realBaseURL, locale: 'de-DE' },
          },
        ]
      : []),
  ],
  webServer: {
    command: `npx vite --mode mock --port ${MOCK_PORT} --strictPort`,
    url: `http://localhost:${MOCK_PORT}`,
    reuseExistingServer: !process.env.CI,
    timeout: 60_000,
  },
})
```

- [ ] **Step 3: Spec laufen lassen → FAIL**

Run: `cd web && npx playwright install chromium && npx playwright test --project=mock e2e/totp.spec.ts`
Expected: FAIL – `Cannot find module './totp'`.

- [ ] **Step 4: `web/e2e/totp.ts`**

```ts
import { createHmac } from 'node:crypto'

const ALPHABET = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'

export function base32Decode(input: string): Buffer {
  const clean = input.toUpperCase().replace(/\s+/g, '').replace(/=+$/, '')
  const out: number[] = []
  let bits = 0
  let value = 0
  for (const ch of clean) {
    const index = ALPHABET.indexOf(ch)
    if (index < 0) throw new Error(`invalid base32 character "${ch}"`)
    value = ((value << 5) | index) & 0xffff
    bits += 5
    if (bits >= 8) {
      out.push((value >>> (bits - 8)) & 0xff)
      bits -= 8
    }
  }
  return Buffer.from(out)
}

export function hotp(key: Buffer, counter: number, digits = 6): string {
  const message = Buffer.alloc(8)
  message.writeBigUInt64BE(BigInt(counter))
  const mac = createHmac('sha1', key).update(message).digest()
  const offset = mac[mac.length - 1] & 0x0f
  const binary = ((mac[offset] & 0x7f) << 24) | (mac[offset + 1] << 16) | (mac[offset + 2] << 8) | mac[offset + 3]
  return String(binary % 10 ** digits).padStart(digits, '0')
}

export function totp(secretBase32: string, timeMs: number = Date.now(), stepSeconds = 30, digits = 6): string {
  return hotp(base32Decode(secretBase32), Math.floor(timeMs / 1000 / stepSeconds), digits)
}
```

- [ ] **Step 5: Spec laufen lassen → PASS**

Run: `cd web && npx playwright test --project=mock e2e/totp.spec.ts`
Expected: 8 passed (der Vite-Mock-Server wird gestartet, aber nicht benutzt).

- [ ] **Step 6: `web/e2e/smoke.spec.ts`** (läuft jetzt gegen MSW)

```ts
import { expect, test, type Page } from '@playwright/test'

async function signInAsAdmin(page: Page) {
  await page.goto('/')
  await expect(page).toHaveURL(/\/login$/)
  await page.getByLabel('Benutzername').fill('admin')
  await page.getByLabel('Passwort').fill('correct-horse-battery')
  await page.getByRole('button', { name: 'Anmelden' }).click()
  await expect(page).toHaveURL(/\/login\/2fa$/)
  await page.getByLabel('Code aus der Authenticator-App').fill('123456')
  await page.getByRole('button', { name: 'Bestätigen' }).click()
  await expect(page.getByRole('heading', { name: 'Server', level: 1 })).toBeVisible()
}

test('admin signs in with password and TOTP and opens the security center', async ({ page }) => {
  await signInAsAdmin(page)
  await page.getByRole('link', { name: 'Security Center' }).click()
  await expect(page.getByRole('img', { name: /Login-Versuche \(24 Stunden\)/ })).toBeVisible()
  await page.getByRole('link', { name: 'Audit-Log' }).click()
  await expect(page.getByRole('status')).toContainText('Kette gültig')
})

test('sensitive actions ask for sudo once and then succeed', async ({ page }) => {
  await signInAsAdmin(page)
  await page.getByRole('button', { name: 'Server anlegen' }).click()
  await page.getByRole('dialog', { name: 'Server anlegen' }).getByLabel('Name').fill('skyblock')
  await page.getByRole('dialog', { name: 'Server anlegen' }).getByRole('button', { name: 'Anlegen' }).click()
  const sudo = page.getByRole('dialog', { name: 'Aktion bestätigen' })
  await sudo.getByLabel('Code aus der Authenticator-App').fill('123456')
  await sudo.getByRole('button', { name: 'Bestätigen' }).click()
  await expect(page.getByRole('article', { name: 'skyblock' })).toBeVisible()
  await expect(sudo).toBeHidden()
})

test.describe('phone (360 px)', () => {
  test.use({ viewport: { width: 360, height: 740 } })

  test('navigates via the menu sheet and shows cards instead of tables', async ({ page }) => {
    await signInAsAdmin(page)
    await page.getByRole('button', { name: 'Menü öffnen' }).click()
    await page.getByRole('dialog').getByRole('link', { name: 'Security Center' }).click()
    await page.getByRole('tab', { name: 'Login-Versuche' }).click()
    await expect(page.getByRole('list', { name: 'Login-Versuche' })).toBeVisible()
    await expect(page.locator('table:visible')).toHaveCount(0)
    const scrollWidth = await page.evaluate(() => document.documentElement.scrollWidth)
    expect(scrollWidth).toBeLessThanOrEqual(360)
  })
})

test('theme preference survives a reload', async ({ page }) => {
  await page.goto('/login')
  await page.getByRole('button', { name: /Farbschema wechseln/ }).click()
  const theme = await page.evaluate(() => localStorage.getItem('mcpanel.theme'))
  await page.reload()
  const hasDark = await page.evaluate(() => document.documentElement.classList.contains('dark'))
  expect(hasDark).toBe(theme === 'dark')
})
```

- [ ] **Step 7: Smoke laufen lassen → PASS**

Run: `cd web && npm run e2e`
Expected: alle `mock`-Tests grün (totp 8, smoke 4).

- [ ] **Step 8: `web/e2e/login-totp.spec.ts`** (echtes Backend, Phase 2)

```ts
import { expect, test, type Page } from '@playwright/test'
import { totp } from './totp'

const username = process.env.E2E_ADMIN_USER ?? 'e2e-admin'
const password = process.env.E2E_ADMIN_PASSWORD ?? ''

test.skip(!process.env.E2E_BASE_URL || !password, 'requires E2E_BASE_URL and E2E_ADMIN_PASSWORD of a fresh admin without 2FA')

async function submitPassword(page: Page) {
  await page.goto('/login')
  await page.getByLabel('Benutzername').fill(username)
  await page.getByLabel('Passwort').fill(password)
  await page.getByRole('button', { name: 'Anmelden' }).click()
}

test('fresh admin: forced TOTP setup, recovery codes, sign-in again with TOTP', async ({ page }) => {
  await submitPassword(page)
  await expect(page).toHaveURL(/\/setup-2fa$/)

  await page.getByRole('button', { name: 'Einrichtung starten' }).click()
  await expect(page.getByRole('img', { name: 'QR-Code für die Authenticator-App' })).toBeVisible()
  const secret = ((await page.getByTestId('totp-secret').textContent()) ?? '').trim()
  expect(secret).toMatch(/^[A-Z2-7]+=*$/)
  await page.getByLabel('Code aus der Authenticator-App').fill(totp(secret))
  await page.getByRole('button', { name: 'Aktivieren' }).click()

  await expect(page.getByRole('list', { name: 'Wiederherstellungscodes' }).getByRole('listitem')).toHaveCount(10)
  await page.getByRole('checkbox', { name: 'Ich habe die Codes sicher gespeichert.' }).check()
  await page.getByRole('button', { name: 'Weiter zum Panel' }).click()
  await expect(page.getByRole('heading', { name: 'Server', level: 1 })).toBeVisible()

  const sid = (await page.context().cookies()).find((c) => c.name === '__Host-sid')
  expect(sid).toMatchObject({ httpOnly: true, secure: true, sameSite: 'Strict', path: '/' })

  await page.getByRole('button', { name: 'Abmelden' }).click()
  await expect(page).toHaveURL(/\/login$/)

  await submitPassword(page)
  await expect(page).toHaveURL(/\/login\/2fa$/)
  // The setup code consumed the current 30 s step (replay protection); the next step is within the ±1 tolerance.
  await page.getByLabel('Code aus der Authenticator-App').fill(totp(secret, Date.now() + 30_000))
  await page.getByRole('button', { name: 'Bestätigen' }).click()
  await expect(page.getByRole('heading', { name: 'Server', level: 1 })).toBeVisible()
})
```

- [ ] **Step 9: Real-Spec ohne Backend prüfen (wird übersprungen)**

Run: `cd web && npx playwright test --list`
Expected: nur das Projekt `mock` wird gelistet (ohne `E2E_BASE_URL` existiert `real` nicht); `npx tsc --noEmit --module esnext --moduleResolution bundler --target es2022 --types node --skipLibCheck e2e/*.ts playwright.config.ts` meldet keine Fehler.

- [ ] **Step 10: Abschluss – alles grün**

Run: `cd web && npx vitest run && npm run typecheck && npm run lint && cd .. && make web-build && test ! -f internal/webui/dist/mockServiceWorker.js && echo OK`
Expected: Vitest grün, keine TS-/Lint-Fehler, Build ok, `OK`.

- [ ] **Step 11: Commit**

```bash
git add web/playwright.config.ts web/e2e
git commit -m "test(web): add Playwright smoke tests against MSW and real-backend TOTP e2e" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Self-Review

**1. Spec-Abdeckung (§4, §7.4, §8 und Aufgabenumfang Stream E):**

| Anforderung | Task |
|---|---|
| Login → Stage-abhängig `/login/2fa`, `/setup-2fa`, `/` (§4.3) | 7 (Guards), 10 |
| 2FA per TOTP, Passkey, Recovery-Code gemäß `second_factors` | 10 |
| Erzwungenes Setup: TOTP-QR (`data:`-URL), Passkey, Recovery-Codes mit Kopieren/Download/Bestätigung | 9, 10 |
| Sudo-Dialog global, TOTP oder Passkey, genau eine Wiederholung (§4.5) | 5, 6, 8 |
| CSRF-Token automatisch, Aktualisierung nach Rotation (§4.6) | 5, 7 |
| 401 → `/login`, `setup_2fa_required` → `/setup-2fa` | 5, 7 |
| Dashboard mit Server-Stubs und Power-Buttons nach Recht | 11 |
| Benutzerverwaltung (Anlegen, Rolle, Löschen, 2FA-Reset) + Rechte-Matrix (Server × Recht, PUT) | 12 |
| Security Center: Diagramm 24h/7d/30d, Top-IPs, Top-Länder, neue Geräte (§7.4) | 13 |
| Login-Versuche mit Filtern + Cursor-Paging | 14 |
| Jails mit Aufheben + manueller Sperre | 14 |
| IP-Regeln, Einstellungen (2FA erzwingen, Allowlist-only, Länder, GeoIP-Hinweis) | 15 |
| Benachrichtigungen CRUD + Test senden | 16 |
| Agent-Status (auch „nicht erreichbar“) | 13 |
| Audit-Log mit Filtern (Akteur, Aktion, Ergebnis, Zeitraum), Paging, Kettenstatus | 17 |
| Profil: Sprache, Passwort, TOTP an/aus, Passkeys, Recovery neu, eigene Sitzungen, Admin: alle Sitzungen | 18, 19 |
| Responsive Shell, Sheet ab 360 px, Tabellen → Karten, rollenbasierte Navigation | 2, 20 |
| Dark Mode (System + Umschalter, `mcpanel.theme`, Klassen-Strategie, kein Inline-Script) | 2, 8 |
| de/en, Sprache aus `Me.locale`, `Intl`-Formatierung | 1, 7, 18 |
| WebAuthn via @simplewebauthn/browser, `optionsJSON = publicKey`, Feature-Detection | 6, 8, 9, 10, 19 |
| MSW für alle 57 Operationen, Browser-Worker per `VITE_MSW=1`, Tests mit `msw/node` | 3, 4 |
| Playwright: Smoke gegen MSW, Login+TOTP-Setup gegen echtes Backend, TOTP via node:crypto | 21 |

Bewusste Abweichung: „Aktive Sessions aller Benutzer (Admin)“ (§7.4) liegt gemäß Stream-Auftrag im Profil (`AdminSessionsCard`), nicht als eigener Security-Center-Tab. Fehler beim TOTP-Setup (z. B. `409 conflict`, wenn TOTP bereits aktiv ist) erscheinen als Inline-Alert in der Setup-Karte statt als Toast – sichtbarer im Kontext; der Einrichten-Button ist bei `Me.totp_enabled` ohnehin ausgeblendet.

**2. Platzhalter-Scan:** Keine „TBD/TODO/später“-Stellen. Die vorläufige `AppLayout` aus Task 10 ist eine lauffähige Zwischenstufe (geschützte Route mit `<Outlet/>`) und wird in Task 20 vollständig ersetzt.

**3. Typ-Konsistenz (geprüft):** `unwrap`/`unwrapVoid` (Task 5) überall für JSON- bzw. 204-Antworten; `applyMe`, `refreshMe`, `clearSession`, `meKey` (Task 7) mit identischen Signaturen in Tasks 8–20; `guardBeforeLoad(guard)` (Task 10) für alle Routen; `serversQueryOptions` (Task 11) in Task 12 wiederverwendet; `securityKey`/Query-Präfix `['security', …]` in Tasks 13–16; `Column<T>`/`DataList` (Task 2) überall; `RecoveryCodesPanel`, `TotpSetupCard`, `PasskeySetupCard` (Task 9) mit denselben Props in Tasks 10 und 19; i18n-Schlüssel stammen ausschließlich aus `de.ts`/`en.ts` (Task 1), der Schlüsselscan-Test schlägt bei Abweichungen fehl.

**4. Review Focus:** Alle fünf Punkte haben einen benannten Test in der zuständigen Task (siehe Abschnitt „Review Focus“). Zusätzlich gepinnt: 401 `invalid_code` führt nicht zum Logout (Task 5, 10), `csrf_invalid` wird genau einmal nach Me-Refresh wiederholt (Task 5), Agent-503 bricht die Seite nicht (Task 13), MSW-Worker landet nie im Go-Embed (Task 4, 21).

## Wiring-Vertrag

Was Phase 2 (Lead) mit dem Frontend tun muss:

1. **Gegen das echte Backend starten:** `make dev` (agent + `panel serve -dev` auf `127.0.0.1:8080` + Vite auf `http://localhost:5173`, Proxy `/api`). **Ohne** `VITE_MSW`/`--mode mock`. Einen frischen Admin per `panel admin create` (Stream-D-CLI) anlegen und im Browser den Ablauf Login → erzwungenes TOTP-Setup → Recovery-Codes → Dashboard durchklicken.
2. **Vertrag synchron halten:** Nach jeder Änderung an `api/openapi.yaml` `cd web && npm run gen:api && npm run typecheck`. Der Mock-Coverage-Test (`src/mocks/handlers.test.ts`) erzwingt, dass jede `operationId` einen MSW-Handler hat.
3. **Session-Rotation abgleichen (Stream A):** `ROTATING_PATHS` in `src/api/client.ts` = `/auth/sudo/totp`, `/auth/sudo/webauthn/finish`, `/me/totp/enable`, `/me/passkeys/finish`, `/me/password`; `ME_PATHS` = Endpunkte, die `Me` zurückgeben. Rotiert Stream A die Session bei weiteren Endpunkten (ohne `Me` zu liefern), diese dort ergänzen. Der Client heilt `csrf_invalid` zusätzlich einmalig per Me-Refresh.
4. **Semantik bestätigen und ggf. Mock anpassen:** `GET /security/jails?active=false` = alle Jails (inkl. aufgehobener); `GET /security/attempts?ip=` = Teilstring-Filter; WebAuthn-Optionen als `{publicKey: …}`; jedes Recht impliziert `server.view` (Stream D); `testNotifier` → `400 validation_failed` bei Zustellfehler; `PATCH /security/notifiers/{id}` mit doppeltem Namen → `400 validation_failed`; `setupTotp` → `409 conflict` bei aktivem TOTP; `GET /security/agent` darf `503 agent_unavailable` liefern.
5. **Produktions-Build prüfen:** `make web-build && make build`, dann `./bin/panel serve -dev` und `curl -sI http://127.0.0.1:8080/ | grep -i content-security-policy`; im Browser-DevTools-Konsole keine CSP-Verletzungen; `internal/webui/dist/mockServiceWorker.js` darf nicht existieren.
6. **Playwright gegen MSW (sofort lauffähig, auch in CI):**
   ```bash
   cd web
   npx playwright install --with-deps chromium
   npm run e2e            # Projekt "mock": startet `vite --mode mock --port 5174`
   ```
7. **Playwright gegen echtes Backend (Phase 2):**
   ```bash
   make dev &                                   # Vite :5173 → panel :8080
   ./.dev/bin/panel admin create …              # frischer Admin OHNE 2FA (Stream-D-CLI)
   cd web
   E2E_BASE_URL=http://localhost:5173 \
   E2E_ADMIN_USER=e2e-admin \
   E2E_ADMIN_PASSWORD='<passwort-des-frischen-admins>' \
   npm run e2e:real
   ```
   Der Test richtet TOTP für diesen Admin ein; für einen erneuten Lauf einen neuen Admin anlegen oder die Dev-DB (`.dev/`) zurücksetzen. Der zweite Login nutzt den TOTP-Code des nächsten 30-s-Schritts (Replay-Schutz des Backends, ±1 Schritt Toleranz).
8. **Umgebungsvariablen:** `VITE_MSW=1` (MSW im Browser; alternativ `npm run dev:mock`), `E2E_BASE_URL`, `E2E_ADMIN_USER` (Standard `e2e-admin`), `E2E_ADMIN_PASSWORD`, `CI` (Playwright: Retries, kein Server-Reuse, GitHub-Reporter).
9. **CI (Stream F):** `cd web && npm ci && npm test && npm run typecheck && npm run lint && npm run build`; optional zusätzlich `npx playwright install --with-deps chromium && npm run e2e`.
10. **Merge-Hinweis:** Stream E wird zuletzt gemerged (Übersicht). Konflikte sind nur in `web/package.json` (Skripte, `msw.workerDirectory`) und `web/src/routeTree.gen.ts` möglich; letzteren per `npx vite build` neu erzeugen statt manuell zu mergen.
