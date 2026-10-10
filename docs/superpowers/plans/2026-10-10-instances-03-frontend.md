# Teilprojekt 2 – Frontend (Aufgaben 17–22) – Implementierungsplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Das React-Frontend für echte Minecraft-Instanzen: Dashboard mit Status, Adresse und Ressourcen, eine Seite zum Anlegen (`/servers/new`), eine Detailseite mit Übersicht, Einstellungen und Löschen (`/servers/$id`) und eine neue Einstellungsseite (`/settings`) für Basisdomain, CurseForge-Key und Docker-Status – vollständig gegen den MSW-Mock (V5) getestet, bereit für das echte panel aus Aufgaben 11–16.

**Architecture:** Ein neues Feature-Modul `web/src/features/servers/` bündelt alles Instanz-Spezifische: Query-Optionen und Mutationen (`api.ts`), die exakte Portierung von `instancespec.JavaFor` (`java.ts`, gegen dieselben Testfälle wie Go geprüft), clientseitige Spiegel der Validierungsregeln für Inline-Fehler (`validation.ts`, das panel bleibt maßgeblich), Formatierung (`format.ts`) und Status-Logik inkl. Polling-Intervall (`status.ts`). Ein gemeinsames, zustandsbehaftetes `ServerForm` (Anlegen und Bearbeiten) arbeitet auf einem reinen Formularmodell (`form-model.ts`), das Eingaben validiert und in API-Requests auflöst. Seiten sind dünne Hüllen, Route-Dateien unter `src/routes/_app/` noch dünner. Die neue Einstellungsseite liegt in `web/src/features/settings/`.

**Tech Stack:** React 19 + TypeScript + Vite, TanStack Router (file-based, `routeTree.gen.ts` wird vom Vite-Plugin erzeugt und eingecheckt) + TanStack Query v5, Tailwind v4, shadcn/ui (vorhandene Komponenten), react-i18next, openapi-fetch, sonner, lucide-react, MSW 2, Vitest + Testing Library + user-event, Playwright. **Keine neuen npm-Abhängigkeiten.**

**Spec:** `docs/superpowers/specs/2026-10-10-mc-panel-instances-design.md` (insbesondere §6 Kataloge, §6.1 Java-Ableitung, §7 Eigenschaften, §8 panel-API, §9 Frontend)

## Global Constraints

Es gelten alle Global Constraints aus `docs/superpowers/plans/2026-10-10-instances-00-overview.md` (und damit TP1 sowie `CLAUDE.md`). Verbindlich für das Frontend sind die Verträge **V4** (OpenAPI-Schemas, Operationen, Zustandstabelle) und **V5** (MSW-Mock: `db.servers` als `MockServer[]` mit `status: ServerStatus`, `db.serverSettings`, `db.catalogs`, Seed und Verhalten). Zusätzlich, frontend-spezifisch:

- **Eigentum:** nur `web/**` (außer `web/go.mod`). Kein Go-Code, kein `api/openapi.yaml`, kein `web/src/api/schema.d.ts` von Hand. Einzige Ausnahme: Aufgabe 17 ändert in `web/src/mocks/handlers/servers.ts` die Java-Ableitung (V5 sieht das vor). Gelesen (nie geschrieben) wird `internal/agent/instancespec/testdata/java_cases.json`.
- **Keine neuen npm-Abhängigkeiten, keine neuen shadcn-Komponenten.** Erlaubt sind native Elemente, wie sie der Code schon nutzt (Checkbox, Radio, `<input type="range">`, `<textarea>`, `NativeSelect`). Aufklappbare Abschnitte baut `CollapsibleSection` (Button mit `aria-expanded`), nicht `<details>`.
- **Kein visuelles Redesign:** gleiche Abstände, Karten, Typografie und Farben wie bisher (`Card`, `SectionCard`, `PageHeader`, `Badge`-Varianten `default|secondary|destructive|outline`).
- **Routen:** `/servers/new` (Admin, `guardBeforeLoad(adminGuard)`), `/servers/$id` (jeder angemeldete Nutzer; 403/404 kommen von der API; nicht-numerische IDs → 404-Seite), `/settings` (Admin). Neuer Navigationspunkt „Einstellungen“ (lucide `Settings`, nur Admins) zwischen „Audit-Log“ und „Profil“; `NavPath` wird erweitert.
- **Links kommen mit ihrem Ziel:** TanStack Router prüft `to` gegen den Routenbaum, ein Link auf eine noch nicht existierende Route bricht `tsc`. Deshalb: Aufgabe 18 zeigt den Basisdomain-Hinweis ohne Link und die Karte ohne Detail-Link; Aufgabe 19 navigiert nach dem Anlegen vorerst auf `/`; Aufgabe 20 ergänzt den Detail-Link und stellt die Navigation nach dem Anlegen auf `/servers/$id` um; Aufgabe 21 ergänzt die Links auf `/settings`.
- **Polling:** `FAST_POLL_MS = 3_000`, solange ein Server „beschäftigt“ ist: `status.operation !== ''` oder `state ∈ {creating, starting, stopping, restarting}` oder (`desired_state = running` und `state = stopped`) oder (`desired_state = stopped` und `state = running`). Sonst `SLOW_POLL_MS = 15_000`. `error`, `unknown` und `not_configured` gelten bewusst als „ruhig“ (schnelleres Abfragen ändert daran nichts). Die Detailseite hört nach 403/404 auf zu pollen. Liste und Detail pollen unabhängig (`refetchInterval` als Funktion).
- **Arbeitsspeicher:** UI in GiB, API in MiB. Bereich 1–32 GiB, Schritt 0,25 GiB, Default **4 GiB** (4096 MiB). Schieberegler (`<input type="range">`) und Zahlenfeld zeigen denselben Wert.
- **Requests:** `createServer` sendet immer die effektive `java` (Override oder abgeleitet) und `properties` ohne `motd`, wenn das Feld leer ist (dann gilt der Servername, Spec §7). `updateServer` sendet `version`, `loader_version`, `modpack`, `java`, `memory_mib` und das vollständige `properties`-Objekt – `java` immer, weil ein ausgelassenes `java` nach einem Versionswechsel sonst veraltet stehen bliebe (V4: „Omitted fields stay unchanged“).
- **Version:** Leeres Versionsfeld im Formular heißt „neueste Version aus dem Katalog“ (`catalog.versions[0]`). Liefert der Katalog einen Fehler (502 `catalog_unavailable`, 404) oder keine Versionen, wird das Feld zum Freitext. Neoforge: Katalog liefert Minecraft-Versionen, `loaders: []`; die NeoForge-Version ist optionaler Freitext unter „Erweitert“ (leer = `null` = neueste stabile). Fabric: Loader als Auswahl aus `catalog.loaders`, Default „Neueste (empfohlen)“ = `null`.
- **Validierung:** `validation.ts` spiegelt `instancespec` (Namens-Regex, Version, Modpack, Speicher, Eigenschaften, Basisdomain, CurseForge-Key) nur für Inline-Fehler; das panel bleibt maßgeblich, Serverfehler erscheinen unter dem Formular (`role="alert"`). Fehler einzelner Felder erscheinen, sobald das Feld bearbeitet wurde, alle nach dem ersten Absenden. Ein Abbruch des Sudo-Dialogs ist nie ein Fehler (`isSudoCancelled`).
- **i18n:** Texte in `de` (Standard) und `en`, Schlüsselgleichheit erzwingen `Messages`-Typ und `web/src/i18n/i18n.test.ts`. Namensräume: `servers.*` (Zustände, Arten, Formular `servers.form.*`, Eigenschaften `servers.props.*`, Validierung `servers.validation.*`, Anlegen `servers.create.*`, Detailseite `servers.detail.*`/`servers.overview.*`/`servers.settings.*`/`servers.delete.*`), `settings.*` (Einstellungsseite), `nav.settings`, neue Fehlercodes `errors.docker_unavailable|base_domain_missing|catalog_unavailable`. Zur Laufzeit gebaute Schlüssel kommen in `DYNAMIC_KEYS`; Validierungsschlüssel prüft `validation.test.ts`.
- **Texte:** Satzanfang groß, sonst Satzschreibung; Buttons sagen genau, was passiert: „Server anlegen“, „Änderungen speichern“, „Jetzt neu starten“, „Server löschen“, „Basisdomain speichern“, „Key speichern“, „Key entfernen“. Fehlermeldungen sagen, was zu tun ist.
- **Barrierefreiheit:** jedes Eingabeelement hat ein `<Label htmlFor>` (über `FieldRow`), Fehler über `aria-invalid` + `aria-describedby`, Radiogruppe in `<fieldset>`/`<legend>`, Karten als `<article aria-labelledby>`, Abschnitte als `<section aria-labelledby>`.
- **Imports von Typen** mit `import type`/`type`-Modifier (`verbatimModuleSyntax`); keine ungenutzten Imports (`noUnusedLocals`). `.tsx`-Dateien exportieren nur Komponenten (und Typen), Konstanten und Hooks liegen in `.ts`-Dateien (`react-refresh/only-export-components`).
- **Prüfen nach jeder Aufgabe:** `cd web && npm run lint && npm run typecheck && npm test && npm run build`, danach `make test` im Repo-Wurzelverzeichnis und `git status --porcelain` (nach `npm run build` darf außer den Änderungen der Aufgabe nichts offen sein – CI prüft das). Nach neuen Route-Dateien zuerst `cd web && npx vite build` (erzeugt `src/routeTree.gen.ts`), sonst scheitert `tsc -b`. Ab Aufgabe 19 zusätzlich `cd web && npm run e2e` (einmalig vorher `npx playwright install chromium`).
- **Commits:** Conventional Commits, zweites `-m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"`, nur die in der Aufgabe genannten Pfade.

## Review Focus

1. **Veraltetes Java nach Versionswechsel:** Ein Admin ändert bei einem Paper-Server `1.21.11` (java21) auf `1.20.4`. Bliebe `java` im PATCH weg, liefe der Server weiter mit java21 statt java17. Erwartet: `updateServer` enthält immer die effektive Java-Version; ein bewusst gesetzter Override bleibt erhalten. → Aufgabe 19 (`form-model.test.ts`: `re-derives java when an existing server changes its version`, `keeps an explicit java override`).
2. **Polling-Sturm:** Ein Server im Zustand `error` (Soll `running`) oder `unknown` (agent weg) darf die API nicht dauerhaft alle 3 s abfragen; eine 403/404-Detailseite gar nicht. Erwartet: 15 s bzw. kein Polling. → Aufgabe 17 (`status.test.ts`), Aufgabe 20 (Detailseite: `refetchInterval` liefert `false` ohne Daten).
3. **Katalog nicht erreichbar:** `502 catalog_unavailable` darf das Anlegen nicht blockieren und nicht dreimal wiederholt werden. Erwartet: Freitextfeld mit Erklärung, Java aus `javaFor`. → Aufgabe 17 (`api.test.ts`: `retry: false`), Aufgabe 19 (`falls back to free text when the version list is unavailable`).
4. **Unsichtbare Steuerzeichen und Spielernamen:** Ein aus einem Chat kopiertes MOTD mit Zeilenumbruch oder geschütztem Leerzeichen, eine Whitelist mit `a,b,c`. Erwartet: Inline-Fehler vor dem Senden (Spiegel von `unicode.IsPrint` bzw. `^[A-Za-z0-9_]{3,16}$`); das panel lehnt Übersehenes mit `400` ab. → Aufgabe 17 (`validation.test.ts`), Aufgabe 19.
5. **Sudo-Abbruch und Basisdomain fehlt:** Bricht der Admin den Sudo-Dialog ab, erscheint keine Fehlermeldung; fehlt die Basisdomain, ist „Server anlegen“ im Dashboard gesperrt und die Anlegen-Seite zeigt den 409 `base_domain_missing` verständlich. → Aufgabe 18, Aufgabe 19, Aufgabe 20 (`shows no error after the user cancelled the sudo dialog`).

## Dateistruktur

```
web/
  playwright.config.ts                         (22) mock-Projekt nimmt servers.spec.ts auf
  e2e/
    smoke.spec.ts                              (19) Sudo-Test nutzt „Benutzer anlegen“ statt des alten Dialogs
    servers.spec.ts                            (22) Anlegen → Detail → MOTD → Neustart; eingeschränkter Nutzer
  src/
    api/types.ts                               (17) Aliase InstanceType … UpdateServerSettingsRequest
    api/errors.ts                              (17) KNOWN_CODES + docker_unavailable, base_domain_missing, catalog_unavailable
    i18n/de.ts, en.ts, i18n.test.ts            (17–21) Texte, DYNAMIC_KEYS, ERROR_CODES
    mocks/handlers/servers.ts                  (17) Java-Ableitung über javaFor
    app/router.integration.test.tsx            (19–21) Guards/404 der neuen Routen
    features/
      servers/
        api.ts                                 (17) Keys, queryOptions, Mutationsfunktionen
        java.ts                                (17) javaFor = instancespec.JavaFor
        validation.ts                          (17) Konstanten + Prüfungen (Spiegel von instancespec)
        format.ts                              (17) GiB, CPU, Java-Label, Adresse, describeInstance
        status.ts                              (17) Badge-Variante, Label-Key, isBusy, pollInterval, powerAllowed
        status-badge.tsx                       (18) StatusBadge
        address-line.tsx                       (18) Adresse mit Kopieren
        use-power.ts                           (18) usePower (Mutation + Toast + Invalidierung)
        power-buttons.tsx                      (18) Start/Stopp (Bestätigung)/Neustart
        form-model.ts                          (19) Formularwert, Defaults, Validierung, Auflösung in Requests
        field-row.tsx                          (19) Label + Control + Hinweis/Fehler
        collapsible-section.tsx                (19) „Erweitert“, „Eigenschaften“
        properties-fields.tsx                  (19) Eigenschaften (Spec §7)
        server-form.tsx                        (19) ServerForm (create/edit)
        create-server-page.tsx                 (19) /servers/new
        server-overview.tsx                    (20) Tab Übersicht inkl. „Neustart nötig“-Banner
        server-settings-tab.tsx                (20) Tab Einstellungen (ServerForm edit)
        delete-server-section.tsx              (20) Löschen mit Namensbestätigung
        server-detail-page.tsx                 (20) /servers/$id
      dashboard/
        dashboard-page.tsx, server-card.tsx    (18–21) neue Karte, Polling, Basisdomain-Hinweis, Link auf /servers/new
        create-server-dialog.tsx               (19) gelöscht
        servers.ts                             (17) Re-Export, (18) gelöscht
      settings/
        settings-card.tsx                      (21) Karte mit h3
        base-domain-card.tsx                   (21) Basisdomain
        curseforge-card.tsx                    (21) CurseForge-Key (nur schreibbar)
        instance-status-card.tsx               (21) agent/Docker, unbekannte Container
        settings-page.tsx                      (21) /settings
      layout/nav.ts                            (21) Navigationspunkt „Einstellungen“
      users/permissions-page.tsx               (18) Import von serversQueryOptions
    routes/_app/
      servers/new.tsx                          (19)
      servers/$id.tsx                          (20)
      settings.tsx                             (21)
    routeTree.gen.ts                           (19–21) generiert, eingecheckt
```

## Annahmen über Aufgaben 11–16 (Stand beim Start von Aufgabe 17)

- `api/openapi.yaml` und `web/src/api/schema.d.ts` enthalten exakt V4 (inkl. der drei neuen Fehlercodes im `Error`-Enum, `PowerResponse` unverändert).
- Der MSW-Mock verhält sich exakt wie V5. Insbesondere: `MockServer.status` ist ein gespeichertes `ServerStatus`, das die Handler (abgesehen von den in V5 beschriebenen Übergängen) unverändert ausliefern – Tests setzen Werte wie `memory_bytes`, `restart_required` oder `last_error` direkt in `db.servers[…].status`. `db.serverSettings` und `db.catalogs` existieren wie in V5; ein fehlender `catalogs`-Eintrag ergibt `502 catalog_unavailable`. `getServerSettings` liefert `unknown_containers: []`. Die Route `GET /servers/settings` wird im Mock vor `GET /servers/:id` registriert (sonst fängt `:id` sie ab).
- Die Inline-Java-Ableitung des Mocks heißt `mockJavaFor(type, version)` und liegt in `web/src/mocks/handlers/servers.ts` (Aufgabe 17 ersetzt sie durch `javaFor`).
- Der Neoforge-Katalog liefert Minecraft-Versionen (dedupliziert, neueste zuerst) mit abgeleitetem `java` und `loaders: []`.
- Die vorhandenen Dashboard-Dateien kompilieren: `CreateServerDialog({ open, onOpenChange })` in `web/src/features/dashboard/create-server-dialog.tsx` existiert weiter (mit festen Defaults minimal angepasst) und importiert `serversKey` aus `./servers`. `web/src/api/types.ts` enthält ggf. schon einige der neuen Aliase (Aufgabe 17 legt den Endzustand fest).

---
### Task 17: Grundlagen – API-Modul, Java-Ableitung, Validierung, Formatierung, Status

**Files:**
- Create: `web/src/features/servers/api.ts`, `web/src/features/servers/java.ts`, `web/src/features/servers/validation.ts`, `web/src/features/servers/format.ts`, `web/src/features/servers/status.ts`
- Modify: `web/src/api/types.ts`, `web/src/api/errors.ts`, `web/src/i18n/de.ts`, `web/src/i18n/en.ts`, `web/src/i18n/i18n.test.ts`, `web/src/mocks/handlers/servers.ts`, `web/src/features/dashboard/servers.ts` (wird Re-Export)
- Test: `web/src/features/servers/java.test.ts`, `web/src/features/servers/validation.test.ts`, `web/src/features/servers/format.test.ts`, `web/src/features/servers/status.test.ts`, `web/src/features/servers/api.test.ts`

**Interfaces:**
- Consumes: V4-Typen aus `web/src/api/schema.d.ts`; V5-Mock (`db.servers`, `db.serverSettings`, `db.catalogs`); `api`, `unwrap`, `unwrapVoid` (`web/src/api`); `internal/agent/instancespec/testdata/java_cases.json` (Aufgabe 2)
- Produces:
```ts
// web/src/api/types.ts (zusätzlich)
export type PowerResponse, InstanceType, JavaVersion, ServerState, ServerProperties, Difficulty, Gamemode, LevelType,
  InstanceConfig, ServerStatus, ServerOperation, Catalog, CatalogVersion, ServerSettings, DockerState,
  UnknownContainer, CreateServerRequest, UpdateServerRequest, UpdateServerSettingsRequest

// web/src/features/servers/api.ts
export const serversKey: readonly ['servers']
export function serverKey(id: number): readonly ['servers', number]
export function catalogKey(type: InstanceType): readonly ['server-catalog', InstanceType]
export const serverSettingsKey: readonly ['server-settings']
export const serversQueryOptions            // GET /servers → Server[]
export function serverQueryOptions(id: number)            // GET /servers/{id} → Server
export function catalogQueryOptions(type: InstanceType)   // GET /servers/catalog/{type} → Catalog; staleTime 1 h, retry false
export const serverSettingsQueryOptions     // GET /servers/settings → ServerSettings
export function createServer(body: CreateServerRequest): Promise<Server>
export function updateServer(id: number, body: UpdateServerRequest): Promise<Server>
export function deleteServer(id: number): Promise<void>
export function powerServer(id: number, action: PowerAction): Promise<PowerResponse>
export function updateServerSettings(body: UpdateServerSettingsRequest): Promise<ServerSettings>

// web/src/features/servers/java.ts
export function javaFor(type: InstanceType, version: string): JavaVersion

// web/src/features/servers/validation.ts
export interface FieldError { key: string; params?: Record<string, string | number> }
export const INSTANCE_TYPES: readonly InstanceType[]; JAVA_VERSIONS: readonly JavaVersion[]
export const DIFFICULTIES: readonly Difficulty[]; GAMEMODES: readonly Gamemode[]; LEVEL_TYPES: readonly LevelType[]
export const MIN_MEMORY_MIB = 1024, MAX_MEMORY_MIB = 32768, MEMORY_STEP_MIB = 256, DEFAULT_MEMORY_MIB = 4096
export const MAX_MOTD_LENGTH = 150, MAX_SEED_LENGTH = 64, MAX_WHITELIST = 200, MAX_OPS = 50, MAX_BASE_DOMAIN_LENGTH = 200
export function isModpack(type: InstanceType): boolean
export function hasLoader(type: InstanceType): boolean
export function validateServerName(name: string): FieldError | null
export function validateVersion(version: string): FieldError | null
export function validateLoaderVersion(loader: string): FieldError | null
export function validateModpack(type: InstanceType, modpack: string): FieldError | null
export function memoryMiBFromGiB(text: string): number | null
export function validateMemoryMiB(mib: number | null): FieldError | null
export function validateText(value: string, maxLength: number): FieldError | null
export function validateIntInRange(text: string, min: number, max: number): FieldError | null
export function parsePlayerNames(text: string): { names: string[]; invalid: string[] }
export function validatePlayerNames(text: string, max: number): FieldError | null
export function normalizeBaseDomain(domain: string): string
export function validateBaseDomain(domain: string): FieldError | null
export function validateCurseForgeKey(key: string): FieldError | null

// web/src/features/servers/format.ts
export function formatGiB(bytes: number, locale: string): string
export function formatCpu(percent: number, locale: string): string
export function memoryInputValue(mib: number): string
export function javaLabel(java: JavaVersion): string
export function serverAddress(name: string, baseDomain: string | null): string | null
export function describeInstance(instance: InstanceConfig, t: TFunction): string

// web/src/features/servers/status.ts
export const FAST_POLL_MS = 3_000, SLOW_POLL_MS = 15_000
export const SERVER_STATES: readonly ServerState[]; POWER_ACTIONS: readonly PowerAction[]
export type BadgeVariant = 'default' | 'secondary' | 'destructive' | 'outline'
export function stateBadgeVariant(state: ServerState): BadgeVariant
export function stateLabelKey(state: ServerState): string
export function isBusy(status: ServerStatus): boolean
export function pollInterval(servers: readonly Server[] | undefined): number
export function powerAllowed(state: ServerState, action: PowerAction): boolean
```

- [ ] **Step 1: Typ-Aliase, Fehlercodes und Basis-Texte**

`web/src/api/types.ts`: direkt nach `export type PowerAction = Schemas['PowerRequest']['action']` einfügen (hat Aufgabe 12–15 einzelne Aliase schon angelegt, diese nicht doppeln – der Endzustand ist genau diese Liste):

```ts
export type PowerResponse = Schemas['PowerResponse']
export type InstanceType = Schemas['InstanceType']
export type JavaVersion = Schemas['JavaVersion']
export type ServerState = Schemas['ServerState']
export type ServerProperties = Schemas['ServerProperties']
export type Difficulty = NonNullable<ServerProperties['difficulty']>
export type Gamemode = NonNullable<ServerProperties['gamemode']>
export type LevelType = NonNullable<ServerProperties['level_type']>
export type InstanceConfig = Schemas['InstanceConfig']
export type ServerStatus = Schemas['ServerStatus']
export type ServerOperation = Schemas['ServerStatus']['operation']
export type Catalog = Schemas['Catalog']
export type CatalogVersion = Schemas['CatalogVersion']
export type ServerSettings = Schemas['ServerSettings']
export type DockerState = Schemas['ServerSettings']['docker']
export type UnknownContainer = Schemas['UnknownContainer']
export type CreateServerRequest = Schemas['CreateServerRequest']
export type UpdateServerRequest = Schemas['UpdateServerRequest']
export type UpdateServerSettingsRequest = Schemas['UpdateServerSettingsRequest']
```

`web/src/api/errors.ts`: `KNOWN_CODES` vollständig durch diesen Endzustand ersetzen (ohne die drei neuen Codes würde `toApiError` sie als `unknown` melden):

```ts
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
  'docker_unavailable',
  'base_domain_missing',
  'catalog_unavailable',
  'internal',
])
```

`web/src/i18n/i18n.test.ts`: in `ERROR_CODES` nach `'agent_unavailable',` ergänzen:

```ts
  'docker_unavailable',
  'base_domain_missing',
  'catalog_unavailable',
```

und am Ende von `DYNAMIC_KEYS` (nach der `nav.${k}`-Zeile) ergänzen:

```ts
  ...['not_configured', 'unknown', 'creating', 'starting', 'running', 'stopping', 'stopped', 'restarting', 'error'].map(
    (s) => `servers.state.${s}`,
  ),
  ...['vanilla', 'paper', 'purpur', 'fabric', 'neoforge', 'modrinth', 'curseforge'].map((k) => `servers.type.${k}`),
```

- [ ] **Step 2: i18n-Test laufen lassen → FAIL**

Run: `cd web && npx vitest run src/i18n/i18n.test.ts`
Expected: FAIL – `translates every API error code` (`docker_unavailable`) und `defines every dynamic key` (`servers.state.*`, `servers.type.*`).

- [ ] **Step 3: Texte ergänzen**

`web/src/i18n/de.ts`, im Block `errors` nach `agent_unavailable` (vorhandene gleichnamige Schlüssel aus Aufgabe 12–15 durch diese Texte ersetzen):

```ts
    docker_unavailable: 'Docker ist auf dem Host nicht erreichbar.',
    base_domain_missing: 'Es ist noch keine Basisdomain festgelegt. Trag sie unter Einstellungen ein.',
    catalog_unavailable: 'Die Versionsliste ist gerade nicht abrufbar.',
```

`web/src/i18n/en.ts`, gleiche Stelle:

```ts
    docker_unavailable: 'Docker is not reachable on the host.',
    base_domain_missing: 'No base domain is set yet. Add it under Settings.',
    catalog_unavailable: 'The version list is currently unavailable.',
```

`web/src/i18n/de.ts`, neuer Top-Level-Block direkt nach dem Block `dashboard: { … },`:

```ts
  servers: {
    state: {
      not_configured: 'Nicht eingerichtet',
      unknown: 'Unbekannt',
      creating: 'Wird eingerichtet',
      starting: 'Startet',
      running: 'Läuft',
      stopping: 'Stoppt',
      stopped: 'Gestoppt',
      restarting: 'Startet neu',
      error: 'Fehler',
    },
    type: {
      vanilla: 'Vanilla',
      paper: 'Paper',
      purpur: 'Purpur',
      fabric: 'Fabric',
      neoforge: 'NeoForge',
      modrinth: 'Modrinth-Modpack',
      curseforge: 'CurseForge-Modpack',
    },
    describe: {
      version: '{{type}} {{version}}',
      latest: '{{type}} (neueste Version)',
      modpack: '{{type}}: {{modpack}}',
    },
    validation: {
      name_required: 'Gib einen Namen ein.',
      name_invalid: 'Nur Kleinbuchstaben, Ziffern und Bindestrich, 2–32 Zeichen, am Anfang kein Bindestrich.',
      version_required: 'Gib eine Minecraft-Version an.',
      version_invalid: 'Ungültige Version. Erlaubt sind z. B. 1.21.11 oder LATEST.',
      loader_invalid: 'Ungültige Loader-Version.',
      modpack_required: 'Gib das Modpack an.',
      modpack_modrinth: 'Nur Kleinbuchstaben, Ziffern und Bindestrich, wie in der Modrinth-Adresse.',
      modpack_curseforge: 'Gib den Projektnamen oder einen Link der Form https://www.curseforge.com/minecraft/modpacks/<name> an.',
      curseforge_key_missing: 'Für CurseForge-Modpacks fehlt der API-Key. Hinterlege ihn zuerst in den Einstellungen.',
      java_curseforge: 'CurseForge-Modpacks brauchen mindestens Java 17.',
      memory: 'Gib {{min}} bis {{max}} GiB in Schritten von 0,25 GiB an.',
      text_too_long: 'Höchstens {{max}} Zeichen.',
      text_control: 'Zeilenumbrüche und Steuerzeichen sind nicht erlaubt.',
      integer_range: 'Gib eine ganze Zahl von {{min}} bis {{max}} an.',
      players_invalid: 'Ungültige Namen: {{names}}. Erlaubt sind 3–16 Buchstaben, Ziffern oder Unterstriche.',
      players_too_many: 'Höchstens {{max}} Namen.',
      eula_required: 'Ohne Zustimmung zur EULA kann kein Server angelegt werden.',
      base_domain_required: 'Gib eine Basisdomain ein.',
      base_domain_invalid: 'Ungültige Domain. Beispiel: mc.example.com (mindestens zwei Teile, nur Kleinbuchstaben, Ziffern und Bindestrich).',
      curseforge_key_required: 'Gib einen API-Key ein.',
      curseforge_key_invalid: 'Der Key enthält unzulässige Zeichen oder ist länger als 128 Zeichen.',
    },
  },
```

`web/src/i18n/en.ts`, gleiche Stelle:

```ts
  servers: {
    state: {
      not_configured: 'Not set up',
      unknown: 'Unknown',
      creating: 'Setting up',
      starting: 'Starting',
      running: 'Running',
      stopping: 'Stopping',
      stopped: 'Stopped',
      restarting: 'Restarting',
      error: 'Error',
    },
    type: {
      vanilla: 'Vanilla',
      paper: 'Paper',
      purpur: 'Purpur',
      fabric: 'Fabric',
      neoforge: 'NeoForge',
      modrinth: 'Modrinth modpack',
      curseforge: 'CurseForge modpack',
    },
    describe: {
      version: '{{type}} {{version}}',
      latest: '{{type}} (latest version)',
      modpack: '{{type}}: {{modpack}}',
    },
    validation: {
      name_required: 'Enter a name.',
      name_invalid: 'Only lowercase letters, digits and hyphens, 2–32 characters, not starting with a hyphen.',
      version_required: 'Enter a Minecraft version.',
      version_invalid: 'Invalid version. Use e.g. 1.21.11 or LATEST.',
      loader_invalid: 'Invalid loader version.',
      modpack_required: 'Enter the modpack.',
      modpack_modrinth: 'Only lowercase letters, digits and hyphens, as in the Modrinth URL.',
      modpack_curseforge: 'Enter the project name or a link like https://www.curseforge.com/minecraft/modpacks/<name>.',
      curseforge_key_missing: 'CurseForge modpacks need an API key. Add it in the settings first.',
      java_curseforge: 'CurseForge modpacks need at least Java 17.',
      memory: 'Enter {{min}} to {{max}} GiB in steps of 0.25 GiB.',
      text_too_long: 'At most {{max}} characters.',
      text_control: 'Line breaks and control characters are not allowed.',
      integer_range: 'Enter a whole number from {{min}} to {{max}}.',
      players_invalid: 'Invalid names: {{names}}. Use 3–16 letters, digits or underscores.',
      players_too_many: 'At most {{max}} names.',
      eula_required: 'You must accept the EULA to create a server.',
      base_domain_required: 'Enter a base domain.',
      base_domain_invalid: 'Invalid domain. Example: mc.example.com (at least two parts, only lowercase letters, digits and hyphens).',
      curseforge_key_required: 'Enter an API key.',
      curseforge_key_invalid: 'The key contains invalid characters or is longer than 128 characters.',
    },
  },
```

Run: `cd web && npx vitest run src/i18n/i18n.test.ts`
Expected: PASS.

- [ ] **Step 4: Failing Test `web/src/features/servers/java.test.ts`**

```ts
import fs from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'
import type { InstanceType, JavaVersion, Server } from '@/api/types'
import { loginAs } from '@/mocks/db'
import { callApi } from '@/test/call-api'
import { javaFor } from './java'

interface JavaCase {
  type: InstanceType
  version: string
  java: JavaVersion
}

// Same fixture as internal/agent/instancespec/java_test.go – Go and TypeScript must agree on every case.
const cases = JSON.parse(
  fs.readFileSync(path.resolve(import.meta.dirname, '../../../../internal/agent/instancespec/testdata/java_cases.json'), 'utf8'),
) as JavaCase[]

describe('javaFor', () => {
  it('matches every shared case of instancespec.JavaFor', () => {
    expect(cases.length).toBeGreaterThanOrEqual(10)
    const mismatches = cases
      .filter((c) => javaFor(c.type, c.version) !== c.java)
      .map((c) => `${c.type} ${JSON.stringify(c.version)}: got ${javaFor(c.type, c.version)}, want ${c.java}`)
    expect(mismatches).toEqual([])
  })

  it('treats empty or garbled versions like unknown formats', () => {
    expect(javaFor('paper', '')).toBe('java25')
    expect(javaFor('paper', 'foo')).toBe('java25')
    expect(javaFor('paper', '1.x')).toBe('java25')
    expect(javaFor('vanilla', '1')).toBe('java25')
    expect(javaFor('modrinth', '1.12.2')).toBe('java21')
  })
})

describe('mock java derivation', () => {
  it('derives java with javaFor when createServer omits it', async () => {
    const me = loginAs('admin', { sudo: true })
    const res = await callApi('POST', '/servers', {
      csrf: me.csrf_token,
      body: { name: 'legacy', type: 'paper', version: '1.16.5', memory_mib: 2048, eula_accepted: true },
    })
    expect(res.status).toBe(201)
    expect((res.body as Server).instance?.java).toBe('java8')
  })
})
```

Run: `cd web && npx vitest run src/features/servers/java.test.ts`
Expected: FAIL – `Failed to resolve import "./java"`.

- [ ] **Step 5: `web/src/features/servers/validation.ts`** (wird von `java.ts` gebraucht)

```ts
import type { Difficulty, Gamemode, InstanceType, JavaVersion, LevelType } from '@/api/types'

/** An i18n key plus interpolation values; components translate it, tests check that the key exists. */
export interface FieldError {
  key: string
  params?: Record<string, string | number>
}

export const INSTANCE_TYPES: readonly InstanceType[] = ['vanilla', 'paper', 'purpur', 'fabric', 'neoforge', 'modrinth', 'curseforge']
export const JAVA_VERSIONS: readonly JavaVersion[] = ['java8', 'java17', 'java21', 'java25']
export const DIFFICULTIES: readonly Difficulty[] = ['peaceful', 'easy', 'normal', 'hard']
export const GAMEMODES: readonly Gamemode[] = ['survival', 'creative', 'adventure', 'spectator']
export const LEVEL_TYPES: readonly LevelType[] = ['normal', 'flat', 'large_biomes', 'amplified']

// Mirrors of internal/agent/instancespec – the panel stays authoritative, these only drive inline hints.
export const MIN_MEMORY_MIB = 1024
export const MAX_MEMORY_MIB = 32768
export const MEMORY_STEP_MIB = 256
export const DEFAULT_MEMORY_MIB = 4096
export const MAX_MOTD_LENGTH = 150
export const MAX_SEED_LENGTH = 64
export const MAX_WHITELIST = 200
export const MAX_OPS = 50
export const MAX_BASE_DOMAIN_LENGTH = 200

const SERVER_NAME_RE = /^[a-z0-9][a-z0-9-]{1,31}$/
const VERSION_RE = /^[0-9A-Za-z][0-9A-Za-z.\-+_]{0,63}$/
const SLUG_RE = /^[a-z0-9-]{1,64}$/
const CURSEFORGE_URL_RE = /^https:\/\/www\.curseforge\.com\/minecraft\/modpacks\/[a-z0-9-]{1,64}$/
const CURSEFORGE_KEY_RE = /^[A-Za-z0-9$./_-]{1,128}$/
const PLAYER_RE = /^[A-Za-z0-9_]{3,16}$/
const DNS_LABEL_RE = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/
// Go's unicode.IsPrint rejects control/format/unassigned characters and every space except U+0020.
const NON_PRINTABLE_RE = /\p{C}|[^\S ]/u

function fieldError(key: string, params?: Record<string, string | number>): FieldError {
  return params ? { key, params } : { key }
}

export function isModpack(type: InstanceType): boolean {
  return type === 'modrinth' || type === 'curseforge'
}

export function hasLoader(type: InstanceType): boolean {
  return type === 'fabric' || type === 'neoforge'
}

export function validateServerName(name: string): FieldError | null {
  if (name === '') return fieldError('servers.validation.name_required')
  return SERVER_NAME_RE.test(name) ? null : fieldError('servers.validation.name_invalid')
}

export function validateVersion(version: string): FieldError | null {
  if (version === '') return fieldError('servers.validation.version_required')
  return version === 'LATEST' || VERSION_RE.test(version) ? null : fieldError('servers.validation.version_invalid')
}

/** Empty means "latest loader". */
export function validateLoaderVersion(loader: string): FieldError | null {
  if (loader === '') return null
  return VERSION_RE.test(loader) ? null : fieldError('servers.validation.loader_invalid')
}

export function validateModpack(type: InstanceType, modpack: string): FieldError | null {
  if (!isModpack(type)) return null
  if (modpack === '') return fieldError('servers.validation.modpack_required')
  if (type === 'modrinth') return SLUG_RE.test(modpack) ? null : fieldError('servers.validation.modpack_modrinth')
  return SLUG_RE.test(modpack) || CURSEFORGE_URL_RE.test(modpack) ? null : fieldError('servers.validation.modpack_curseforge')
}

/** Parses the GiB input ("4", "4.25", "4,25") into MiB; null for empty or non-numeric input. */
export function memoryMiBFromGiB(text: string): number | null {
  const normalized = text.trim().replace(',', '.')
  if (normalized === '') return null
  const gib = Number(normalized)
  return Number.isFinite(gib) ? gib * 1024 : null
}

export function validateMemoryMiB(mib: number | null): FieldError | null {
  const valid = mib !== null && Number.isInteger(mib) && mib >= MIN_MEMORY_MIB && mib <= MAX_MEMORY_MIB && mib % MEMORY_STEP_MIB === 0
  return valid ? null : fieldError('servers.validation.memory', { min: MIN_MEMORY_MIB / 1024, max: MAX_MEMORY_MIB / 1024 })
}

/** MOTD and seed: printable text, counted in code points like Go's utf8.RuneCountInString. */
export function validateText(value: string, maxLength: number): FieldError | null {
  if ([...value].length > maxLength) return fieldError('servers.validation.text_too_long', { max: maxLength })
  return NON_PRINTABLE_RE.test(value) ? fieldError('servers.validation.text_control') : null
}

export function validateIntInRange(text: string, min: number, max: number): FieldError | null {
  const trimmed = text.trim()
  const n = Number(trimmed)
  return /^\d+$/.test(trimmed) && n >= min && n <= max ? null : fieldError('servers.validation.integer_range', { min, max })
}

/** Splits a whitelist/ops text area on commas, semicolons and whitespace. */
export function parsePlayerNames(text: string): { names: string[]; invalid: string[] } {
  const names = text.split(/[\s,;]+/).filter((n) => n !== '')
  return { names, invalid: names.filter((n) => !PLAYER_RE.test(n)) }
}

export function validatePlayerNames(text: string, max: number): FieldError | null {
  const { names, invalid } = parsePlayerNames(text)
  if (invalid.length > 0) return fieldError('servers.validation.players_invalid', { names: invalid.join(', ') })
  return names.length > max ? fieldError('servers.validation.players_too_many', { max }) : null
}

/** The panel stores base domains in lower case; the form sends them that way. */
export function normalizeBaseDomain(domain: string): string {
  return domain.trim().toLowerCase()
}

export function validateBaseDomain(domain: string): FieldError | null {
  if (domain === '') return fieldError('servers.validation.base_domain_required')
  const labels = domain.split('.')
  const valid = domain.length <= MAX_BASE_DOMAIN_LENGTH && labels.length >= 2 && labels.every((l) => DNS_LABEL_RE.test(l))
  return valid ? null : fieldError('servers.validation.base_domain_invalid')
}

export function validateCurseForgeKey(key: string): FieldError | null {
  if (key === '') return fieldError('servers.validation.curseforge_key_required')
  return CURSEFORGE_KEY_RE.test(key) ? null : fieldError('servers.validation.curseforge_key_invalid')
}
```

- [ ] **Step 6: `web/src/features/servers/java.ts`**

```ts
import type { InstanceType, JavaVersion } from '@/api/types'
import { isModpack } from './validation'

/**
 * Exact port of instancespec.JavaFor (spec §6.1): derives the itzg image variant from the Minecraft version.
 * Modpacks default to java21; unknown formats (LATEST, snapshots, year-based versions like 26.1) get the newest image.
 * Both implementations are checked against internal/agent/instancespec/testdata/java_cases.json.
 */
export function javaFor(type: InstanceType, version: string): JavaVersion {
  if (isModpack(type)) return 'java21'
  const parts = version.split('.')
  const major = leadingInt(parts[0])
  if (major !== 1 || parts.length < 2) return 'java25'
  const minor = leadingInt(parts[1])
  if (minor === null) return 'java25'
  const patch = parts.length >= 3 ? (leadingInt(parts[2]) ?? 0) : 0
  if (minor <= 16) return 'java8'
  if (minor <= 19 || (minor === 20 && patch <= 4)) return 'java17'
  if (minor <= 21) return 'java21'
  return 'java25'
}

/** Leading decimal digits of s ("4-rc1" → 4); null when s does not start with a digit. */
function leadingInt(s: string): number | null {
  const match = /^\d+/.exec(s)
  return match ? Number(match[0]) : null
}
```

- [ ] **Step 7: Mock auf `javaFor` umstellen**

In `web/src/mocks/handlers/servers.ts` die Inline-Ableitung aus V5 entfernen: die lokale Funktion `mockJavaFor` samt ihrer Versionstabelle löschen, `import { javaFor } from '@/features/servers/java'` zu den Imports nehmen und jeden Aufruf `mockJavaFor(` durch `javaFor(` ersetzen. Kontrolle:

Run: `grep -rn "mockJavaFor\|java8\|java17" web/src/mocks/handlers/servers.ts`
Expected: keine Treffer (die Seeds mit `java21`/`java25` liegen in `db.ts`).

Run: `cd web && npx vitest run src/features/servers/java.test.ts`
Expected: PASS (3 Tests).

- [ ] **Step 8: Absicherung gegen die Go-Fälle – `web/src/features/servers/validation.test.ts`**

```ts
import { describe, expect, it } from 'vitest'
import i18n from '@/i18n'
import {
  memoryMiBFromGiB,
  normalizeBaseDomain,
  parsePlayerNames,
  validateBaseDomain,
  validateCurseForgeKey,
  validateIntInRange,
  validateLoaderVersion,
  validateMemoryMiB,
  validateModpack,
  validatePlayerNames,
  validateServerName,
  validateText,
  validateVersion,
  type FieldError,
} from './validation'

function expectError(error: FieldError | null, key: string): void {
  expect(error?.key).toBe(key)
  expect(i18n.exists(key), key).toBe(true)
}

describe('server names (instancespec.ValidName)', () => {
  it('accepts valid names', () => {
    for (const n of ['survival', 'a1', 'creative-2', 'a'.repeat(32)]) expect(validateServerName(n), n).toBeNull()
  })

  it('rejects invalid names', () => {
    expectError(validateServerName(''), 'servers.validation.name_required')
    for (const n of ['a', '-x', 'Upper', 'a_b', 'a.b', 'a'.repeat(33)]) expectError(validateServerName(n), 'servers.validation.name_invalid')
  })
})

describe('base domain (instancespec.ValidBaseDomain)', () => {
  it('accepts lowercase DNS names with at least two labels', () => {
    for (const d of ['mc.example.com', 'example.de', 'mc.localhost', 'a-b.c-d.example']) expect(validateBaseDomain(d), d).toBeNull()
  })

  it('rejects everything else', () => {
    expectError(validateBaseDomain(''), 'servers.validation.base_domain_required')
    for (const d of ['localhost', 'MC.example.com', '-a.example', 'a..b', 'a.b.', '.a.b', 'a_b.example', 'a.b:25565', 'a.'.repeat(101) + 'de']) {
      expectError(validateBaseDomain(d), 'servers.validation.base_domain_invalid')
    }
  })

  it('normalizes input before validation', () => {
    expect(normalizeBaseDomain('  MC.Example.com ')).toBe('mc.example.com')
  })
})

describe('versions, loaders and modpacks', () => {
  it('validates versions like instancespec', () => {
    for (const v of ['LATEST', '1.21.11', '26.4-snapshot-3', '1.20.4-rc1']) expect(validateVersion(v), v).toBeNull()
    expectError(validateVersion(''), 'servers.validation.version_required')
    for (const v of ['1.21 4', '1.21\nEULA=FALSE', '1'.repeat(65), '.1']) expectError(validateVersion(v), 'servers.validation.version_invalid')
  })

  it('treats an empty loader as latest', () => {
    expect(validateLoaderVersion('')).toBeNull()
    expect(validateLoaderVersion('0.19.5')).toBeNull()
    expectError(validateLoaderVersion('0.19 5'), 'servers.validation.loader_invalid')
  })

  it('checks modpacks per type', () => {
    expect(validateModpack('paper', '')).toBeNull()
    expectError(validateModpack('modrinth', ''), 'servers.validation.modpack_required')
    expect(validateModpack('modrinth', 'cobblemon-fabric')).toBeNull()
    expectError(validateModpack('modrinth', 'Bad Slug'), 'servers.validation.modpack_modrinth')
    expect(validateModpack('curseforge', 'atm10')).toBeNull()
    expect(validateModpack('curseforge', 'https://www.curseforge.com/minecraft/modpacks/all-the-mods-10')).toBeNull()
    expectError(validateModpack('curseforge', 'https://evil.example/minecraft/modpacks/x'), 'servers.validation.modpack_curseforge')
  })
})

describe('memory', () => {
  it('parses GiB input into MiB', () => {
    expect(memoryMiBFromGiB('4')).toBe(4096)
    expect(memoryMiBFromGiB('4.25')).toBe(4352)
    expect(memoryMiBFromGiB(' 6,5 ')).toBe(6656)
    expect(memoryMiBFromGiB('')).toBeNull()
    expect(memoryMiBFromGiB('abc')).toBeNull()
  })

  it('allows 1 to 32 GiB in steps of 0.25 GiB', () => {
    for (const mib of [1024, 4352, 32768]) expect(validateMemoryMiB(mib), String(mib)).toBeNull()
    for (const mib of [null, 768, 33024, 4000, 4403.2]) expectError(validateMemoryMiB(mib), 'servers.validation.memory')
    expect(validateMemoryMiB(512)?.params).toEqual({ min: 1, max: 32 })
  })
})

describe('properties', () => {
  it('rejects control characters and overlong text like unicode.IsPrint', () => {
    expect(validateText('§aWillkommen auf unserem Server äöü', 150)).toBeNull()
    expect(validateText('ä'.repeat(150), 150)).toBeNull()
    expectError(validateText('ä'.repeat(151), 150), 'servers.validation.text_too_long')
    for (const v of ['a\nENABLE_RCON=false', 'a\u0000b', 'a\tb', 'a b', 'a​b']) expectError(validateText(v, 150), 'servers.validation.text_control')
  })

  it('accepts whole numbers in range only', () => {
    expect(validateIntInRange('20', 1, 500)).toBeNull()
    expect(validateIntInRange('0', 0, 256)).toBeNull()
    for (const v of ['0', '501', '', '2.5', '-1', 'abc']) expectError(validateIntInRange(v, 1, 500), 'servers.validation.integer_range')
  })

  it('parses and checks player lists', () => {
    expect(parsePlayerNames('Notch, jeb_\nDinnerbone;  ')).toEqual({ names: ['Notch', 'jeb_', 'Dinnerbone'], invalid: [] })
    expect(validatePlayerNames('Notch jeb_', 200)).toBeNull()
    const invalid = validatePlayerNames('ab, Notch, a-b', 200)
    expectError(invalid, 'servers.validation.players_invalid')
    expect(invalid?.params).toEqual({ names: 'ab, a-b' })
    expectError(validatePlayerNames(Array.from({ length: 51 }, (_, i) => `player${i}`).join(','), 50), 'servers.validation.players_too_many')
  })

  it('checks CurseForge keys', () => {
    expect(validateCurseForgeKey('$2a$10$abcdefghijklmnopqrstuv')).toBeNull()
    expectError(validateCurseForgeKey(''), 'servers.validation.curseforge_key_required')
    expectError(validateCurseForgeKey('k\nX=1'), 'servers.validation.curseforge_key_invalid')
    expectError(validateCurseForgeKey('x'.repeat(129)), 'servers.validation.curseforge_key_invalid')
  })
})
```

Run: `cd web && npx vitest run src/features/servers/validation.test.ts`
Expected: PASS (die Implementierung aus Step 5 liegt schon vor; der Test sichert sie gegen die Go-Fälle ab). Schlägt ein Fall fehl, die Regex in `validation.ts` korrigieren, nicht den Test.

- [ ] **Step 9: Failing Test `web/src/features/servers/format.test.ts`**

```ts
import { describe, expect, it } from 'vitest'
import type { InstanceConfig } from '@/api/types'
import i18n from '@/i18n'
import { describeInstance, formatCpu, formatGiB, javaLabel, memoryInputValue, serverAddress } from './format'

const GIB = 1024 ** 3
const t = i18n.t.bind(i18n)

function instance(patch: Partial<InstanceConfig>): InstanceConfig {
  return { type: 'paper', version: '1.21.11', loader_version: null, modpack: null, java: 'java21', memory_mib: 4096, properties: {}, ...patch }
}

describe('servers format', () => {
  it('formats bytes as GiB with at most one decimal', () => {
    expect(formatGiB(2 * GIB, 'de')).toBe('2')
    expect(formatGiB(2.5 * GIB, 'de')).toBe('2,5')
    expect(formatGiB(2.5 * GIB, 'en')).toBe('2.5')
  })

  it('formats CPU percent per locale', () => {
    expect(formatCpu(12.5, 'de')).toBe('12,5')
    expect(formatCpu(3, 'en')).toBe('3')
  })

  it('converts MiB into the GiB input value', () => {
    expect(memoryInputValue(4096)).toBe('4')
    expect(memoryInputValue(4352)).toBe('4.25')
  })

  it('labels java versions and builds addresses', () => {
    expect(javaLabel('java8')).toBe('Java 8')
    expect(javaLabel('java25')).toBe('Java 25')
    expect(serverAddress('survival', 'mc.example.com')).toBe('survival.mc.example.com')
    expect(serverAddress('survival', null)).toBeNull()
  })

  it('describes an instance', () => {
    expect(describeInstance(instance({}), t)).toBe('Paper 1.21.11')
    expect(describeInstance(instance({ type: 'vanilla', version: 'LATEST', java: 'java25' }), t)).toBe('Vanilla (neueste Version)')
    expect(describeInstance(instance({ type: 'modrinth', version: '', modpack: 'cobblemon-fabric' }), t)).toBe(
      'Modrinth-Modpack: cobblemon-fabric',
    )
  })
})
```

Run: `cd web && npx vitest run src/features/servers/format.test.ts`
Expected: FAIL – `Failed to resolve import "./format"`.

- [ ] **Step 10: `web/src/features/servers/format.ts`**

```ts
import type { TFunction } from 'i18next'
import type { InstanceConfig, JavaVersion } from '@/api/types'
import { isModpack } from './validation'

const GIB = 1024 ** 3

export function formatGiB(bytes: number, locale: string): string {
  return new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }).format(bytes / GIB)
}

export function formatCpu(percent: number, locale: string): string {
  return new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }).format(percent)
}

/** Value of the GiB input ("4", "4.25"); the API speaks MiB. */
export function memoryInputValue(mib: number): string {
  return String(mib / 1024)
}

export function javaLabel(java: JavaVersion): string {
  return `Java ${java.slice('java'.length)}`
}

export function serverAddress(name: string, baseDomain: string | null): string | null {
  return baseDomain ? `${name}.${baseDomain}` : null
}

/** "Paper 1.21.11", "Vanilla (neueste Version)" or "Modrinth-Modpack: cobblemon-fabric". */
export function describeInstance(instance: InstanceConfig, t: TFunction): string {
  const type = t(`servers.type.${instance.type}`)
  if (isModpack(instance.type)) return t('servers.describe.modpack', { type, modpack: instance.modpack ?? '' })
  if (instance.version === 'LATEST') return t('servers.describe.latest', { type })
  return t('servers.describe.version', { type, version: instance.version })
}
```

Run: `cd web && npx vitest run src/features/servers/format.test.ts`
Expected: PASS (5 Tests).

- [ ] **Step 11: Failing Test `web/src/features/servers/status.test.ts`**

```ts
import { describe, expect, it } from 'vitest'
import type { Server, ServerStatus } from '@/api/types'
import i18n from '@/i18n'
import { FAST_POLL_MS, isBusy, pollInterval, powerAllowed, SERVER_STATES, SLOW_POLL_MS, stateBadgeVariant, stateLabelKey } from './status'

const settled: ServerStatus = {
  state: 'running',
  desired_state: 'running',
  restart_required: false,
  operation: '',
  last_error: '',
  started_at: 1,
  memory_bytes: 0,
  memory_limit_bytes: 0,
  cpu_percent: 0,
}

function status(patch: Partial<ServerStatus>): ServerStatus {
  return { ...settled, ...patch }
}

function server(patch: Partial<ServerStatus>): Server {
  return { id: 1, name: 'survival', permissions: [], address: null, instance: null, status: status(patch) }
}

describe('server status', () => {
  it('is busy while the agent works or the state is transitional', () => {
    expect(isBusy(status({}))).toBe(false)
    expect(isBusy(status({ operation: 'pulling' }))).toBe(true)
    for (const state of ['creating', 'starting', 'stopping', 'restarting'] as const) expect(isBusy(status({ state })), state).toBe(true)
  })

  it('is busy while the state lags behind the desired state', () => {
    expect(isBusy(status({ state: 'stopped', desired_state: 'running' }))).toBe(true)
    expect(isBusy(status({ state: 'running', desired_state: 'stopped' }))).toBe(true)
    expect(isBusy(status({ state: 'stopped', desired_state: 'stopped' }))).toBe(false)
  })

  it('treats error, unknown and not configured as settled', () => {
    expect(isBusy(status({ state: 'error', desired_state: 'running', last_error: 'exit 1' }))).toBe(false)
    expect(isBusy(status({ state: 'unknown', desired_state: 'running' }))).toBe(false)
    expect(isBusy(status({ state: 'not_configured', desired_state: null }))).toBe(false)
  })

  it('polls fast only while any server is busy', () => {
    expect(FAST_POLL_MS).toBe(3_000)
    expect(SLOW_POLL_MS).toBe(15_000)
    expect(pollInterval(undefined)).toBe(SLOW_POLL_MS)
    expect(pollInterval([server({}), server({ state: 'stopped', desired_state: 'stopped' })])).toBe(SLOW_POLL_MS)
    expect(pollInterval([server({}), server({ operation: 'creating', state: 'creating' })])).toBe(FAST_POLL_MS)
  })

  it('allows only power actions that make sense in the current state', () => {
    expect(powerAllowed('running', 'start')).toBe(false)
    expect(powerAllowed('running', 'stop')).toBe(true)
    expect(powerAllowed('running', 'restart')).toBe(true)
    expect(powerAllowed('stopped', 'start')).toBe(true)
    expect(powerAllowed('stopped', 'stop')).toBe(false)
    expect(powerAllowed('stopped', 'restart')).toBe(false)
    expect(powerAllowed('creating', 'start')).toBe(false)
    expect(powerAllowed('unknown', 'start')).toBe(true)
    expect(powerAllowed('error', 'restart')).toBe(true)
    expect(powerAllowed('not_configured', 'start')).toBe(false)
  })

  it('maps every state to a translated label and a badge variant', () => {
    for (const state of SERVER_STATES) expect(i18n.exists(stateLabelKey(state)), state).toBe(true)
    expect(stateBadgeVariant('running')).toBe('default')
    expect(stateBadgeVariant('stopped')).toBe('secondary')
    expect(stateBadgeVariant('error')).toBe('destructive')
    expect(stateBadgeVariant('creating')).toBe('outline')
  })
})
```

Run: `cd web && npx vitest run src/features/servers/status.test.ts`
Expected: FAIL – `Failed to resolve import "./status"`.

- [ ] **Step 12: `web/src/features/servers/status.ts`**

```ts
import type { PowerAction, Server, ServerState, ServerStatus } from '@/api/types'

export const FAST_POLL_MS = 3_000
export const SLOW_POLL_MS = 15_000

export const SERVER_STATES: readonly ServerState[] = [
  'not_configured',
  'unknown',
  'creating',
  'starting',
  'running',
  'stopping',
  'stopped',
  'restarting',
  'error',
]

export const POWER_ACTIONS: readonly PowerAction[] = ['start', 'stop', 'restart']

export type BadgeVariant = 'default' | 'secondary' | 'destructive' | 'outline'

const BADGE_VARIANTS: Record<ServerState, BadgeVariant> = {
  not_configured: 'outline',
  unknown: 'outline',
  creating: 'outline',
  starting: 'outline',
  running: 'default',
  stopping: 'outline',
  stopped: 'secondary',
  restarting: 'outline',
  error: 'destructive',
}

const TRANSITIONAL: ReadonlySet<ServerState> = new Set<ServerState>(['creating', 'starting', 'stopping', 'restarting'])

const POWER_STATES: Record<PowerAction, ReadonlySet<ServerState>> = {
  // unknown: the agent is away – the panel stores the wish and applies it later (spec §3.1).
  start: new Set<ServerState>(['stopped', 'error', 'unknown']),
  stop: new Set<ServerState>(['running', 'starting', 'restarting', 'error', 'unknown']),
  restart: new Set<ServerState>(['running', 'error', 'unknown']),
}

export function stateBadgeVariant(state: ServerState): BadgeVariant {
  return BADGE_VARIANTS[state]
}

export function stateLabelKey(state: ServerState): string {
  return `servers.state.${state}`
}

/**
 * True while the state is expected to change soon (spec §9: poll every 3 s while an operation runs or the state
 * differs from desired_state). error, unknown and not_configured count as settled: polling faster cannot change them.
 */
export function isBusy(status: ServerStatus): boolean {
  if (status.operation !== '' || TRANSITIONAL.has(status.state)) return true
  return (status.desired_state === 'running' && status.state === 'stopped') || (status.desired_state === 'stopped' && status.state === 'running')
}

export function pollInterval(servers: readonly Server[] | undefined): number {
  return servers?.some((s) => isBusy(s.status)) ? FAST_POLL_MS : SLOW_POLL_MS
}

export function powerAllowed(state: ServerState, action: PowerAction): boolean {
  return POWER_STATES[action].has(state)
}
```

Run: `cd web && npx vitest run src/features/servers/status.test.ts`
Expected: PASS (6 Tests).

- [ ] **Step 13: Failing Test `web/src/features/servers/api.test.ts`**

```ts
import { describe, expect, it } from 'vitest'
import { setCsrfToken } from '@/api/csrf'
import { db, loginAs } from '@/mocks/db'
import { createTestQueryClient } from '@/test/render'
import {
  catalogQueryOptions,
  createServer,
  powerServer,
  serverQueryOptions,
  serverSettingsQueryOptions,
  serversQueryOptions,
  updateServer,
  updateServerSettings,
} from './api'

function signIn(username: string, sudo = false): void {
  setCsrfToken(loginAs(username, { sudo }).csrf_token)
}

describe('servers api against the mock', () => {
  it('lists servers with instance, address and status', async () => {
    signIn('admin')
    const servers = await createTestQueryClient().fetchQuery(serversQueryOptions)
    expect(servers.map((s) => [s.name, s.status.state])).toEqual([
      ['survival', 'running'],
      ['creative', 'stopped'],
      ['lobby', 'not_configured'],
    ])
    expect(servers[0].address).toBe('survival.mc.example.com')
    expect(servers[2].instance).toBeNull()
  })

  it('creates a server that runs from the next read on', async () => {
    signIn('admin', true)
    const created = await createServer({ name: 'skyblock', type: 'paper', version: '26.2', memory_mib: 4096, eula_accepted: true })
    expect(created.status.state).toBe('creating')
    const fresh = await createTestQueryClient().fetchQuery(serverQueryOptions(created.id))
    expect(fresh.status.state).toBe('running')
  })

  it('flags a restart after changing a running server and clears it on restart', async () => {
    signIn('admin', true)
    const updated = await updateServer(1, { memory_mib: 6144 })
    expect(updated.status.restart_required).toBe(true)
    await powerServer(1, 'restart')
    const fresh = await createTestQueryClient().fetchQuery(serverQueryOptions(1))
    expect(fresh.status.restart_required).toBe(false)
    expect(fresh.instance?.memory_mib).toBe(6144)
  })

  it('reports an unavailable catalog as catalog_unavailable without retrying', async () => {
    signIn('admin')
    delete db.catalogs.vanilla
    expect(catalogQueryOptions('vanilla').retry).toBe(false)
    await expect(createTestQueryClient().fetchQuery(catalogQueryOptions('vanilla'))).rejects.toMatchObject({
      status: 502,
      code: 'catalog_unavailable',
    })
  })

  it('stores the CurseForge key write-only', async () => {
    signIn('admin', true)
    const settings = await updateServerSettings({ curseforge_api_key: 'abc$def' })
    expect(settings.curseforge_api_key_set).toBe(true)
    expect(JSON.stringify(settings)).not.toContain('abc$def')
    const fresh = await createTestQueryClient().fetchQuery(serverSettingsQueryOptions)
    expect(fresh.base_domain).toBe('mc.example.com')
    expect(fresh.curseforge_api_key_set).toBe(true)
  })
})
```

Run: `cd web && npx vitest run src/features/servers/api.test.ts`
Expected: FAIL – `Failed to resolve import "./api"`.

- [ ] **Step 14: `web/src/features/servers/api.ts`**

```ts
import { queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { unwrap, unwrapVoid } from '@/api/errors'
import type {
  CreateServerRequest,
  InstanceType,
  PowerAction,
  PowerResponse,
  Server,
  ServerSettings,
  UpdateServerRequest,
  UpdateServerSettingsRequest,
} from '@/api/types'

export const serversKey = ['servers'] as const
/** Detail keys share the list prefix, so invalidating serversKey refreshes open detail pages too. */
export const serverKey = (id: number) => ['servers', id] as const
export const catalogKey = (type: InstanceType) => ['server-catalog', type] as const
export const serverSettingsKey = ['server-settings'] as const

/** The panel caches catalogs for an hour; on errors the form switches to free text instead of retrying. */
const CATALOG_STALE_MS = 60 * 60_000

export const serversQueryOptions = queryOptions({
  queryKey: serversKey,
  queryFn: () => unwrap(api.GET('/servers')),
})

export function serverQueryOptions(id: number) {
  return queryOptions({
    queryKey: serverKey(id),
    queryFn: () => unwrap(api.GET('/servers/{id}', { params: { path: { id } } })),
  })
}

export function catalogQueryOptions(type: InstanceType) {
  return queryOptions({
    queryKey: catalogKey(type),
    queryFn: () => unwrap(api.GET('/servers/catalog/{type}', { params: { path: { type } } })),
    staleTime: CATALOG_STALE_MS,
    retry: false,
  })
}

export const serverSettingsQueryOptions = queryOptions({
  queryKey: serverSettingsKey,
  queryFn: () => unwrap(api.GET('/servers/settings')),
})

export function createServer(body: CreateServerRequest): Promise<Server> {
  return unwrap(api.POST('/servers', { body }))
}

export function updateServer(id: number, body: UpdateServerRequest): Promise<Server> {
  return unwrap(api.PATCH('/servers/{id}', { params: { path: { id } }, body }))
}

export function deleteServer(id: number): Promise<void> {
  return unwrapVoid(api.DELETE('/servers/{id}', { params: { path: { id } } }))
}

export function powerServer(id: number, action: PowerAction): Promise<PowerResponse> {
  return unwrap(api.POST('/servers/{id}/power', { params: { path: { id } }, body: { action } }))
}

export function updateServerSettings(body: UpdateServerSettingsRequest): Promise<ServerSettings> {
  return unwrap(api.PUT('/servers/settings', { body }))
}
```

`web/src/features/dashboard/servers.ts` vollständig ersetzen (übergangsweise, Aufgabe 18 löscht die Datei):

```ts
// Moved to @/features/servers/api; the dashboard and the permissions page switch over in task 18.
export { serversKey, serversQueryOptions } from '@/features/servers/api'
```

Run: `cd web && npx vitest run src/features/servers`
Expected: PASS (alle fünf Testdateien).

- [ ] **Step 15: Gesamtprüfung**

Run: `cd web && npm run lint && npm run typecheck && npm test && npm run build`
Expected: alles grün; `npm test` enthält die bestehenden Dashboard- und Mock-Tests unverändert grün.

Run: `make test`
Expected: PASS (Go unverändert).

Run: `git status --porcelain`
Expected: nur die in **Files** genannten Pfade.

- [ ] **Step 16: Commit**

```bash
git add web/src/api/types.ts web/src/api/errors.ts web/src/i18n/de.ts web/src/i18n/en.ts web/src/i18n/i18n.test.ts \
  web/src/mocks/handlers/servers.ts web/src/features/servers web/src/features/dashboard/servers.ts
git commit -m "feat(web): add servers foundations (api, java derivation, validation, status)" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 18: Dashboard – Serverkarte mit Status, Adresse, Ressourcen und Polling

**Files:**
- Create: `web/src/features/servers/status-badge.tsx`, `web/src/features/servers/address-line.tsx`, `web/src/features/servers/use-power.ts`, `web/src/features/servers/power-buttons.tsx`
- Modify: `web/src/features/dashboard/server-card.tsx` (neu geschrieben), `web/src/features/dashboard/dashboard-page.tsx` (neu geschrieben), `web/src/features/dashboard/create-server-dialog.tsx` (nur Import), `web/src/features/users/permissions-page.tsx` (nur Import), `web/src/i18n/de.ts`, `web/src/i18n/en.ts`, `web/src/i18n/i18n.test.ts`
- Delete: `web/src/features/dashboard/servers.ts`
- Test: `web/src/features/dashboard/dashboard-page.test.tsx` (neu geschrieben)

**Interfaces:**
- Consumes: Aufgabe 17 (`serversQueryOptions`, `serversKey`, `serverSettingsQueryOptions`, `deleteServer`, `powerServer`, `describeInstance`, `formatGiB`, `pollInterval`, `powerAllowed`, `POWER_ACTIONS`, `stateBadgeVariant`, `stateLabelKey`); `useMe`, `useErrorToast`, `useFormat`, `ConfirmDialog`, `PageHeader`, shadcn `Card`, `Badge`, `Button`; `CreateServerDialog({ open, onOpenChange })` (Stand nach Aufgabe 11–16)
- Produces:
```ts
export function StatusBadge(props: { state: ServerState }): JSX.Element
export function AddressLine(props: { address: string | null }): JSX.Element      // Adresse + Button „Adresse <x> kopieren“
export function usePower(server: Pick<Server, 'id' | 'name'>): UseMutationResult<PowerResponse, Error, PowerAction>
export function PowerButtons(props: { server: Pick<Server, 'id' | 'name' | 'status'> }): JSX.Element // role=group „Steuerung für <name>“
export function ServerCard(props: { server: Server; canDelete: boolean }): JSX.Element               // <article aria-labelledby=Name>
export function DashboardPage(): JSX.Element  // Polling über pollInterval; Admin: Basisdomain-Hinweis, „Server anlegen“ gesperrt ohne Basisdomain
```

- [ ] **Step 1: Failing Test – `web/src/features/dashboard/dashboard-page.test.tsx` vollständig ersetzen**

```tsx
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { db, loginAs, MOCK_TOTP_CODE } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { DashboardPage } from './dashboard-page'

const GIB = 1024 ** 3

function mockServer(id: number) {
  const server = db.servers.find((s) => s.id === id)
  if (!server) throw new Error(`mock server ${id} missing`)
  return server
}

describe('DashboardPage', () => {
  it('shows a restricted user only permitted servers and power buttons only with server.power', async () => {
    await renderWithProviders(<DashboardPage />, { me: loginAs('alice') })
    const survival = await screen.findByRole('article', { name: 'survival' })
    expect(within(survival).getByRole('group', { name: 'Steuerung für survival' })).toBeInTheDocument()
    expect(within(survival).getByRole('button', { name: 'Starten' })).toBeInTheDocument()
    const creative = screen.getByRole('article', { name: 'creative' })
    expect(within(creative).queryByRole('button', { name: 'Starten' })).not.toBeInTheDocument()
    expect(within(creative).getByText('Keine Berechtigung für Start/Stopp.')).toBeInTheDocument()
    expect(screen.queryByRole('article', { name: 'lobby' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Server anlegen' })).not.toBeInTheDocument()
  })

  it('shows status, type, version, address and memory on each card', async () => {
    const survival = mockServer(1)
    survival.status = { ...survival.status, memory_bytes: 2 * GIB, memory_limit_bytes: 5 * GIB }
    await renderWithProviders(<DashboardPage />, { me: loginAs('admin') })

    const card = await screen.findByRole('article', { name: 'survival' })
    expect(within(card).getByText('Läuft')).toBeInTheDocument()
    expect(within(card).getByText('Paper 1.21.11')).toBeInTheDocument()
    expect(within(card).getByText('survival.mc.example.com')).toBeInTheDocument()
    expect(within(card).getByText('RAM: 2 / 5 GiB')).toBeInTheDocument()
    expect(within(card).getByRole('button', { name: 'Starten' })).toBeDisabled()
    expect(within(card).getByRole('button', { name: 'Stoppen' })).toBeEnabled()

    const creative = screen.getByRole('article', { name: 'creative' })
    expect(within(creative).getByText('Gestoppt')).toBeInTheDocument()
    expect(within(creative).getByText('Vanilla 26.3')).toBeInTheDocument()
    expect(within(creative).queryByText(/^RAM:/)).not.toBeInTheDocument()

    const lobby = screen.getByRole('article', { name: 'lobby' })
    expect(within(lobby).getByText('Nicht eingerichtet')).toBeInTheDocument()
    expect(within(lobby).getByText('Noch ohne Minecraft-Instanz – kann nur gelöscht werden.')).toBeInTheDocument()
    expect(within(lobby).queryByRole('group')).not.toBeInTheDocument()
  })

  it('marks servers that need a restart', async () => {
    const survival = mockServer(1)
    survival.revision += 1
    survival.status = { ...survival.status, restart_required: true }
    await renderWithProviders(<DashboardPage />, { me: loginAs('alice') })
    const card = await screen.findByRole('article', { name: 'survival' })
    expect(within(card).getByText('Neustart nötig')).toBeInTheDocument()
    expect(within(screen.getByRole('article', { name: 'creative' })).queryByText('Neustart nötig')).not.toBeInTheDocument()
  })

  it('copies the address to the clipboard', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<DashboardPage />, { me: loginAs('alice') })
    const survival = await screen.findByRole('article', { name: 'survival' })
    await user.click(within(survival).getByRole('button', { name: 'Adresse survival.mc.example.com kopieren' }))
    expect(await navigator.clipboard.readText()).toBe('survival.mc.example.com')
    expect(await screen.findByText('Adresse kopiert.')).toBeInTheDocument()
  })

  it('sends a power action and confirms it with a toast', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<DashboardPage />, { me: loginAs('alice') })
    const survival = await screen.findByRole('article', { name: 'survival' })
    await user.click(within(survival).getByRole('button', { name: 'Neu starten' }))
    expect(await screen.findByText('Neustart für „survival“ angefordert.')).toBeInTheDocument()
  })

  it('asks for confirmation before stopping a server', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<DashboardPage />, { me: loginAs('alice') })
    const survival = await screen.findByRole('article', { name: 'survival' })
    await user.click(within(survival).getByRole('button', { name: 'Stoppen' }))
    const confirm = screen.getByRole('dialog', { name: 'Server „survival“ stoppen?' })
    expect(screen.queryByText('Stopp für „survival“ angefordert.')).not.toBeInTheDocument()
    await user.click(within(confirm).getByRole('button', { name: 'Stoppen' }))
    expect(await screen.findByText('Stopp für „survival“ angefordert.')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Server „survival“ stoppen?' })).not.toBeInTheDocument())
  })

  it('blocks server creation for admins until a base domain is set', async () => {
    db.serverSettings.base_domain = null
    await renderWithProviders(<DashboardPage />, { me: loginAs('admin') })
    expect(await screen.findByText('Keine Basisdomain festgelegt')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Server anlegen' })).toBeDisabled()
  })

  it('lets admins open server creation when a base domain is set', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<DashboardPage />, { me: loginAs('admin') })
    const create = await screen.findByRole('button', { name: 'Server anlegen' })
    await waitFor(() => expect(create).toBeEnabled())
    expect(screen.queryByText('Keine Basisdomain festgelegt')).not.toBeInTheDocument()
    await user.click(create)
    expect(screen.getByRole('dialog', { name: 'Server anlegen' })).toBeInTheDocument()
  })

  it('shows no error toast after the user cancelled the sudo dialog', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<DashboardPage />, { me: loginAs('admin') })
    const lobby = await screen.findByRole('article', { name: 'lobby' })
    await user.click(within(lobby).getByRole('button', { name: 'lobby löschen' }))
    await user.click(within(screen.getByRole('dialog', { name: 'Server „lobby“ löschen?' })).getByRole('button', { name: 'Löschen' }))
    const sudo = await screen.findByRole('dialog', { name: 'Aktion bestätigen' })
    await user.click(within(sudo).getByRole('button', { name: 'Abbrechen' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Aktion bestätigen' })).not.toBeInTheDocument())
    await new Promise((resolve) => setTimeout(resolve, 100))
    expect(screen.queryByText('Diese Aktion muss bestätigt werden.')).not.toBeInTheDocument()
    expect(screen.getByRole('dialog', { name: 'Server „lobby“ löschen?' })).toBeInTheDocument()
  })

  it('deletes a server after confirmation and sudo', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<DashboardPage />, { me: loginAs('admin') })
    const lobby = await screen.findByRole('article', { name: 'lobby' })
    await user.click(within(lobby).getByRole('button', { name: 'lobby löschen' }))
    await user.click(within(screen.getByRole('dialog', { name: 'Server „lobby“ löschen?' })).getByRole('button', { name: 'Löschen' }))
    const sudo = await screen.findByRole('dialog', { name: 'Aktion bestätigen' })
    await user.type(within(sudo).getByLabelText('Code aus der Authenticator-App'), MOCK_TOTP_CODE)
    await user.click(within(sudo).getByRole('button', { name: 'Bestätigen' }))
    expect(await screen.findByText('Server „lobby“ gelöscht.')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByRole('article', { name: 'lobby' })).not.toBeInTheDocument())
  })
})
```

- [ ] **Step 2: Test laufen lassen → FAIL**

Run: `cd web && npx vitest run src/features/dashboard`
Expected: FAIL – u. a. `Unable to find role="group" and name "Steuerung für survival"` (alte Karte) und `Unable to find an element with the text: Läuft`.

- [ ] **Step 3: Texte**

`web/src/i18n/de.ts`, im Block `dashboard`: `description` ersetzen, die Schlüssel `stub_hint`, `permissions_label`, `power_group`, `no_power`, `power`, `power_noun`, `stop_title`, `stop_description` und `power_requested` **entfernen** (sie ziehen nach `servers` um), zwei neue ergänzen. Alle übrigen Schlüssel (auch solche, die Aufgabe 11–16 für den Dialog ergänzt hat) bleiben:

```ts
    description: 'Deine Minecraft-Server auf einen Blick.',
    base_domain_missing_title: 'Keine Basisdomain festgelegt',
    base_domain_missing: 'Server sind unter <name>.<basisdomain> erreichbar. Solange keine Basisdomain eingetragen ist, kannst du keine Server anlegen.',
```

`web/src/i18n/en.ts`, gleiche Änderung:

```ts
    description: 'Your Minecraft servers at a glance.',
    base_domain_missing_title: 'No base domain set',
    base_domain_missing: 'Servers are reachable at <name>.<base domain>. You cannot create servers until a base domain is set.',
```

`web/src/i18n/de.ts`, im Block `servers` nach `describe: { … },` einfügen:

```ts
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
    power_group: 'Steuerung für {{name}}',
    no_power: 'Keine Berechtigung für Start/Stopp.',
    stop_title: 'Server „{{name}}“ stoppen?',
    stop_description: 'Spieler werden getrennt, sobald der Server heruntergefahren wird.',
    power_requested: '{{action}} für „{{name}}“ angefordert.',
    address: {
      missing: 'Noch keine Adresse – es fehlt die Basisdomain.',
      copy: 'Adresse {{address}} kopieren',
      copied: 'Adresse kopiert.',
      copy_failed: 'Kopieren fehlgeschlagen.',
    },
    memory_usage: 'RAM: {{used}} / {{limit}} GiB',
    restart_required: 'Neustart nötig',
    not_configured_hint: 'Noch ohne Minecraft-Instanz – kann nur gelöscht werden.',
```

`web/src/i18n/en.ts`, gleiche Stelle:

```ts
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
    power_group: 'Controls for {{name}}',
    no_power: 'No permission to start/stop.',
    stop_title: 'Stop server “{{name}}”?',
    stop_description: 'Players are disconnected as soon as the server shuts down.',
    power_requested: '{{action}} requested for “{{name}}”.',
    address: {
      missing: 'No address yet – the base domain is missing.',
      copy: 'Copy address {{address}}',
      copied: 'Address copied.',
      copy_failed: 'Copy failed.',
    },
    memory_usage: 'RAM: {{used}} / {{limit}} GiB',
    restart_required: 'Restart required',
    not_configured_hint: 'No Minecraft instance yet – can only be deleted.',
```

`web/src/i18n/i18n.test.ts`, in `DYNAMIC_KEYS` die Zeile

```ts
  ...['start', 'stop', 'restart'].flatMap((a) => [`dashboard.power.${a}`, `dashboard.power_noun.${a}`]),
```

ersetzen durch

```ts
  ...['start', 'stop', 'restart'].flatMap((a) => [`servers.power.${a}`, `servers.power_noun.${a}`]),
```

- [ ] **Step 4: `web/src/features/servers/status-badge.tsx`**

```tsx
import { useTranslation } from 'react-i18next'
import type { ServerState } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { stateBadgeVariant, stateLabelKey } from './status'

export function StatusBadge({ state }: { state: ServerState }) {
  const { t } = useTranslation()
  return <Badge variant={stateBadgeVariant(state)}>{t(stateLabelKey(state))}</Badge>
}
```

- [ ] **Step 5: `web/src/features/servers/address-line.tsx`**

```tsx
import { Copy } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'

export function AddressLine({ address }: { address: string | null }) {
  const { t } = useTranslation()
  if (!address) return <p className="text-muted-foreground text-sm">{t('servers.address.missing')}</p>

  async function copy(value: string) {
    try {
      await navigator.clipboard.writeText(value)
      toast.success(t('servers.address.copied'))
    } catch {
      toast.error(t('servers.address.copy_failed'))
    }
  }

  const label = t('servers.address.copy', { address })
  return (
    <div className="flex min-w-0 items-center gap-1">
      <span className="truncate font-mono text-sm" title={address}>
        {address}
      </span>
      <Button type="button" variant="ghost" size="icon-sm" aria-label={label} title={label} onClick={() => void copy(address)}>
        <Copy aria-hidden="true" />
      </Button>
    </div>
  )
}
```

- [ ] **Step 6: `web/src/features/servers/use-power.ts`**

```ts
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { PowerAction, Server } from '@/api/types'
import { useErrorToast } from '@/lib/error-message'
import { powerServer, serversKey } from './api'

/** Power action with toast and refresh of list and detail (both share the serversKey prefix). */
export function usePower(server: Pick<Server, 'id' | 'name'>) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  return useMutation({
    mutationFn: (action: PowerAction) => powerServer(server.id, action),
    onSuccess: (_data, action) => {
      toast.success(t('servers.power_requested', { action: t(`servers.power_noun.${action}`), name: server.name }))
      void queryClient.invalidateQueries({ queryKey: serversKey })
    },
    onError: notifyError,
  })
}
```

- [ ] **Step 7: `web/src/features/servers/power-buttons.tsx`**

```tsx
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { Server } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { POWER_ACTIONS, powerAllowed } from './status'
import { usePower } from './use-power'

/** Start, stop (after confirmation) and restart; actions that make no sense in the current state are disabled. */
export function PowerButtons({ server }: { server: Pick<Server, 'id' | 'name' | 'status'> }) {
  const { t } = useTranslation()
  const [confirmStop, setConfirmStop] = useState(false)
  const power = usePower(server)

  return (
    <>
      <div role="group" aria-label={t('servers.power_group', { name: server.name })} className="flex flex-wrap gap-2">
        {POWER_ACTIONS.map((action) => (
          <Button
            key={action}
            type="button"
            size="sm"
            variant={action === 'stop' ? 'outline' : 'secondary'}
            disabled={power.isPending || !powerAllowed(server.status.state, action)}
            onClick={() => (action === 'stop' ? setConfirmStop(true) : power.mutate(action))}
          >
            {t(`servers.power.${action}`)}
          </Button>
        ))}
      </div>
      <ConfirmDialog
        open={confirmStop}
        onOpenChange={setConfirmStop}
        title={t('servers.stop_title', { name: server.name })}
        description={t('servers.stop_description')}
        confirmLabel={t('servers.power.stop')}
        destructive
        pending={power.isPending}
        onConfirm={() => power.mutate('stop', { onSettled: () => setConfirmStop(false) })}
      />
    </>
  )
}
```

- [ ] **Step 8: `web/src/features/dashboard/server-card.tsx` vollständig ersetzen**

```tsx
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { Server } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader } from '@/components/ui/card'
import { AddressLine } from '@/features/servers/address-line'
import { deleteServer, serversKey } from '@/features/servers/api'
import { describeInstance, formatGiB } from '@/features/servers/format'
import { PowerButtons } from '@/features/servers/power-buttons'
import { StatusBadge } from '@/features/servers/status-badge'
import { useErrorToast } from '@/lib/error-message'
import { useFormat } from '@/lib/format'

export function ServerCard({ server, canDelete }: { server: Server; canDelete: boolean }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const headingId = useId()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const [confirmDelete, setConfirmDelete] = useState(false)
  const { instance, status } = server
  const canPower = server.permissions.includes('server.power')
  const showMemory = status.state === 'running' && status.memory_limit_bytes > 0

  const remove = useMutation({
    mutationFn: () => deleteServer(server.id),
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
          <div className="flex items-start justify-between gap-2">
            <h2 id={headingId} className="min-w-0 text-lg font-semibold break-words">
              {server.name}
            </h2>
            <StatusBadge state={status.state} />
          </div>
          <CardDescription>{instance ? describeInstance(instance, t) : t('servers.not_configured_hint')}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          {instance ? <AddressLine address={server.address} /> : null}
          {showMemory ? (
            <p className="text-muted-foreground text-sm tabular-nums">
              {t('servers.memory_usage', { used: formatGiB(status.memory_bytes, fmt.locale), limit: formatGiB(status.memory_limit_bytes, fmt.locale) })}
            </p>
          ) : null}
          {status.restart_required ? <Badge variant="outline">{t('servers.restart_required')}</Badge> : null}
          {instance && canPower ? <PowerButtons server={server} /> : null}
          {instance && !canPower ? <p className="text-muted-foreground text-sm">{t('servers.no_power')}</p> : null}
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

- [ ] **Step 9: `web/src/features/dashboard/dashboard-page.tsx` vollständig ersetzen**

```tsx
import { useQuery } from '@tanstack/react-query'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { PageHeader } from '@/components/page-header'
import { Button } from '@/components/ui/button'
import { useMe } from '@/features/auth/me'
import { serverSettingsQueryOptions, serversQueryOptions } from '@/features/servers/api'
import { pollInterval } from '@/features/servers/status'
import { errorMessage } from '@/lib/error-message'
import { CreateServerDialog } from './create-server-dialog'
import { ServerCard } from './server-card'

export function DashboardPage() {
  const { t } = useTranslation()
  const me = useMe()
  const isAdmin = me?.role === 'admin'
  const [creating, setCreating] = useState(false)
  // 3 s while a server is changing state, 15 s otherwise (spec §9).
  const servers = useQuery({ ...serversQueryOptions, refetchInterval: (query) => pollInterval(query.state.data) })
  // Only admins may read the instance settings; without a base domain no server can be created.
  const settings = useQuery({ ...serverSettingsQueryOptions, enabled: isAdmin })
  const baseDomainMissing = isAdmin && settings.data?.base_domain === null

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
            <Button type="button" disabled={settings.isPending || baseDomainMissing} onClick={() => setCreating(true)}>
              {t('dashboard.create_server')}
            </Button>
          ) : undefined
        }
      />
      {baseDomainMissing ? (
        <div role="status" className="mb-6 rounded-md border p-4 text-sm">
          <p className="font-medium">{t('dashboard.base_domain_missing_title')}</p>
          <p className="text-muted-foreground mt-1">{t('dashboard.base_domain_missing')}</p>
        </div>
      ) : null}
      {content}
      {isAdmin ? <CreateServerDialog open={creating} onOpenChange={setCreating} /> : null}
    </>
  )
}
```

- [ ] **Step 10: Übergangsdatei entfernen und Importe umstellen**

```bash
git rm -q web/src/features/dashboard/servers.ts
```

In `web/src/features/users/permissions-page.tsx` die Zeile

```ts
import { serversQueryOptions } from '@/features/dashboard/servers'
```

ersetzen durch

```ts
import { serversQueryOptions } from '@/features/servers/api'
```

In `web/src/features/dashboard/create-server-dialog.tsx` den Import `from './servers'` durch `from '@/features/servers/api'` ersetzen (die importierten Namen bleiben gleich).

Run: `grep -rn "features/dashboard/servers\|from './servers'" web/src`
Expected: keine Treffer.

- [ ] **Step 11: Tests → PASS**

Run: `cd web && npx vitest run src/features/dashboard src/i18n`
Expected: PASS (Dashboard 10 Tests, i18n grün).

- [ ] **Step 12: Gesamtprüfung**

Run: `cd web && npm run lint && npm run typecheck && npm test && npm run build`
Expected: alles grün.

Run: `make test`
Expected: PASS.

- [ ] **Step 13: Commit**

```bash
git add -A web/src/features/servers web/src/features/dashboard web/src/features/users/permissions-page.tsx \
  web/src/i18n/de.ts web/src/i18n/en.ts web/src/i18n/i18n.test.ts
git commit -m "feat(web): show instance status, address and resources on the dashboard" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 19: Seite „Server anlegen“ (`/servers/new`) mit gemeinsamem `ServerForm`

**Files:**
- Create: `web/src/features/servers/form-model.ts`, `web/src/features/servers/field-row.tsx`, `web/src/features/servers/collapsible-section.tsx`, `web/src/features/servers/properties-fields.tsx`, `web/src/features/servers/server-form.tsx`, `web/src/features/servers/create-server-page.tsx`, `web/src/routes/_app/servers/new.tsx`
- Modify: `web/src/features/dashboard/dashboard-page.tsx`, `web/src/features/dashboard/dashboard-page.test.tsx`, `web/src/i18n/de.ts`, `web/src/i18n/en.ts`, `web/src/i18n/i18n.test.ts`, `web/src/app/router.integration.test.tsx`, `web/e2e/smoke.spec.ts`, `web/src/routeTree.gen.ts` (generiert)
- Delete: `web/src/features/dashboard/create-server-dialog.tsx`
- Test: `web/src/features/servers/form-model.test.ts`, `web/src/features/servers/create-server-page.test.tsx`

**Interfaces:**
- Consumes: Aufgabe 17 (`catalogQueryOptions`, `serverSettingsQueryOptions`, `createServer`, `serversKey`, `javaFor`, `validation.ts`, `memoryInputValue`, `javaLabel`, `serverAddress`); `adminGuard`, `guardBeforeLoad`; `isSudoCancelled`, `errorMessage`; `NativeSelect`, shadcn `Input`, `Label`, `Switch`, `Button`
- Produces:
```ts
// web/src/features/servers/form-model.ts
export const EULA_URL = 'https://aka.ms/MinecraftEULA'
export interface PropertiesDraft { motd: string; difficulty: Difficulty; gamemode: Gamemode; max_players: string; pvp: boolean;
  hardcore: boolean; allow_flight: boolean; enable_command_block: boolean; online_mode: boolean; whitelist_enabled: boolean;
  whitelist: string; ops: string; view_distance: string; simulation_distance: string; spawn_protection: string; seed: string; level_type: LevelType }
export interface ServerFormValue { name: string; type: InstanceType; version: string; manualVersion: boolean; loaderVersion: string;
  modpack: string; javaOverride: JavaVersion | ''; memoryGiB: string; properties: PropertiesDraft; eulaAccepted: boolean }
export interface ServerFormResult { name: string; type: InstanceType; version: string; loader_version: string | null;
  modpack: string | null; java: JavaVersion; memory_mib: number; properties: ServerProperties }
export const PROPERTY_FIELDS: readonly ['properties.motd', 'properties.max_players', 'properties.whitelist', 'properties.ops',
  'properties.view_distance', 'properties.simulation_distance', 'properties.spawn_protection', 'properties.seed']
export type PropertyField = (typeof PROPERTY_FIELDS)[number]
export type FormField = 'name' | 'type' | 'version' | 'loader_version' | 'modpack' | 'java' | 'memory' | 'eula' | PropertyField
export type FormErrors = Partial<Record<FormField, FieldError>>
export interface FormContext { mode: 'create' | 'edit'; catalog: Catalog | undefined; curseforgeKeySet: boolean }
export function defaultPropertiesDraft(): PropertiesDraft
export function emptyServerForm(): ServerFormValue
export function formFromInstance(name: string, instance: InstanceConfig): ServerFormValue
export function effectiveVersion(value: ServerFormValue, catalog: Catalog | undefined): string
export function effectiveJava(value: ServerFormValue, catalog: Catalog | undefined): JavaVersion
export function validateServerForm(value: ServerFormValue, ctx: FormContext): FormErrors
export function resolveServerForm(value: ServerFormValue, ctx: FormContext): ServerFormResult | null
export function sameFormValue(a: ServerFormValue, b: ServerFormValue): boolean

// Komponenten
export function FieldRow(props: { id: string; label: string; hint?: string; error?: FieldError;
  children(control: ControlProps): ReactNode }): JSX.Element   // ControlProps = { id; 'aria-describedby'?; 'aria-invalid'?: true }
export function CollapsibleSection(props: { title: string; description?: string; forceOpen?: boolean; children: ReactNode }): JSX.Element
export function PropertiesFields(props: { idPrefix: string; mode: 'create' | 'edit'; serverName: string; value: PropertiesDraft;
  error(field: PropertyField): FieldError | undefined; onChange(patch: Partial<PropertiesDraft>, field?: PropertyField): void }): JSX.Element
export interface ServerFormProps { mode: 'create' | 'edit'; initial: ServerFormValue; baseDomain?: string | null;
  curseforgeKeySet?: boolean; submitLabel: string; pending: boolean; error: unknown; notice?: ReactNode;
  onSubmit(result: ServerFormResult): void }
export function ServerForm(props: ServerFormProps): JSX.Element
export function CreateServerPage(props: { onCreated(server: Server): void }): JSX.Element
// Route: /servers/new (Kind von /_app, adminGuard); navigiert nach dem Anlegen vorerst auf / (Aufgabe 20: Detailseite)
```

- [ ] **Step 1: Failing Test `web/src/features/servers/form-model.test.ts`**

```ts
import { describe, expect, it } from 'vitest'
import type { Catalog, InstanceConfig } from '@/api/types'
import {
  effectiveJava,
  effectiveVersion,
  emptyServerForm,
  formFromInstance,
  resolveServerForm,
  sameFormValue,
  validateServerForm,
  type FormContext,
  type ServerFormValue,
} from './form-model'

const paperCatalog: Catalog = {
  versions: [
    { id: '26.2', java: 'java25' },
    { id: '1.21.11', java: 'java21' },
    { id: '1.20.4', java: 'java17' },
  ],
  loaders: [],
}

function ctx(mode: FormContext['mode'], catalog?: Catalog, curseforgeKeySet = false): FormContext {
  return { mode, catalog, curseforgeKeySet }
}

function form(patch: Partial<ServerFormValue>): ServerFormValue {
  return { ...emptyServerForm(), name: 'skyblock', eulaAccepted: true, ...patch }
}

const survival: InstanceConfig = {
  type: 'paper',
  version: '1.21.11',
  loader_version: null,
  modpack: null,
  java: 'java21',
  memory_mib: 4352,
  properties: {
    motd: 'Hallo',
    difficulty: 'hard',
    gamemode: 'survival',
    max_players: 8,
    pvp: false,
    hardcore: false,
    allow_flight: true,
    enable_command_block: false,
    online_mode: true,
    whitelist_enabled: true,
    whitelist: ['Notch', 'jeb_'],
    ops: [],
    view_distance: 12,
    simulation_distance: 8,
    spawn_protection: 0,
    seed: '42',
    level_type: 'flat',
  },
}

describe('server form model', () => {
  it('starts with the spec defaults', () => {
    const value = emptyServerForm()
    expect(value).toMatchObject({ type: 'paper', version: '', memoryGiB: '4', javaOverride: '', eulaAccepted: false })
    expect(value.properties).toMatchObject({
      motd: '',
      difficulty: 'normal',
      gamemode: 'survival',
      max_players: '20',
      pvp: true,
      online_mode: true,
      view_distance: '10',
      simulation_distance: '10',
      spawn_protection: '16',
      level_type: 'normal',
    })
  })

  it('resolves a new paper server with the newest catalog version and spec defaults', () => {
    expect(resolveServerForm(form({}), ctx('create', paperCatalog))).toEqual({
      name: 'skyblock',
      type: 'paper',
      version: '26.2',
      loader_version: null,
      modpack: null,
      java: 'java25',
      memory_mib: 4096,
      properties: {
        difficulty: 'normal',
        gamemode: 'survival',
        max_players: 20,
        pvp: true,
        hardcore: false,
        allow_flight: false,
        enable_command_block: false,
        online_mode: true,
        whitelist_enabled: false,
        whitelist: [],
        ops: [],
        view_distance: 10,
        simulation_distance: 10,
        spawn_protection: 16,
        seed: '',
        level_type: 'normal',
      },
    })
  })

  it('requires name and EULA only when creating', () => {
    expect(Object.keys(validateServerForm(emptyServerForm(), ctx('create', paperCatalog))).sort()).toEqual(['eula', 'name'])
    expect(validateServerForm(formFromInstance('survival', survival), ctx('edit', paperCatalog))).toEqual({})
  })

  it('needs a version until the catalog has loaded', () => {
    expect(effectiveVersion(form({}), undefined)).toBe('')
    expect(validateServerForm(form({}), ctx('create')).version?.key).toBe('servers.validation.version_required')
  })

  it('derives java from the catalog, falls back to javaFor and honours an override', () => {
    expect(effectiveJava(form({ version: '1.20.4' }), paperCatalog)).toBe('java17')
    expect(effectiveJava(form({ version: '1.12.2', manualVersion: true }), undefined)).toBe('java8')
    expect(effectiveJava(form({ version: '1.12.2', javaOverride: 'java17' }), undefined)).toBe('java17')
  })

  it('sends no version for modpacks and checks the slug', () => {
    expect(validateServerForm(form({ type: 'modrinth', modpack: 'Bad Slug' }), ctx('create')).modpack?.key).toBe(
      'servers.validation.modpack_modrinth',
    )
    expect(resolveServerForm(form({ type: 'modrinth', modpack: ' cobblemon-fabric ' }), ctx('create'))).toMatchObject({
      version: '',
      modpack: 'cobblemon-fabric',
      loader_version: null,
      java: 'java21',
    })
  })

  it('maps an empty fabric loader to the latest one', () => {
    const fabric: Catalog = { versions: [{ id: '26.3', java: 'java25' }], loaders: ['0.19.5', '0.19.4'] }
    expect(resolveServerForm(form({ type: 'fabric' }), ctx('create', fabric))?.loader_version).toBeNull()
    expect(resolveServerForm(form({ type: 'fabric', loaderVersion: '0.19.4' }), ctx('create', fabric))?.loader_version).toBe('0.19.4')
  })

  it('blocks CurseForge without a stored key and with java8', () => {
    const curseforge = form({ type: 'curseforge', modpack: 'atm10' })
    expect(validateServerForm(curseforge, ctx('create')).type?.key).toBe('servers.validation.curseforge_key_missing')
    expect(validateServerForm({ ...curseforge, javaOverride: 'java8' }, ctx('create', undefined, true)).java?.key).toBe(
      'servers.validation.java_curseforge',
    )
    expect(validateServerForm(curseforge, ctx('create', undefined, true))).toEqual({})
  })

  it('accepts memory in quarter GiB steps only', () => {
    expect(resolveServerForm(form({ memoryGiB: '6.25' }), ctx('create', paperCatalog))?.memory_mib).toBe(6400)
    for (const memoryGiB of ['4.3', '0.5', '33', '']) {
      expect(validateServerForm(form({ memoryGiB }), ctx('create', paperCatalog)).memory, memoryGiB).toBeDefined()
    }
  })

  it('validates properties and parses player lists', () => {
    const value = form({ properties: { ...emptyServerForm().properties, whitelist: 'Notch, jeb_\nDinnerbone', max_players: '0', motd: 'a\nb' } })
    expect(Object.keys(validateServerForm(value, ctx('create', paperCatalog))).sort()).toEqual(['properties.max_players', 'properties.motd'])
    const fixed = { ...value, properties: { ...value.properties, max_players: '8', motd: 'Hallo' } }
    expect(resolveServerForm(fixed, ctx('create', paperCatalog))?.properties).toMatchObject({
      motd: 'Hallo',
      max_players: 8,
      whitelist: ['Notch', 'jeb_', 'Dinnerbone'],
    })
  })

  it('round-trips an existing instance in edit mode', () => {
    expect(resolveServerForm(formFromInstance('survival', survival), ctx('edit', paperCatalog))).toEqual({
      name: 'survival',
      type: 'paper',
      version: '1.21.11',
      loader_version: null,
      modpack: null,
      java: 'java21',
      memory_mib: 4352,
      properties: survival.properties,
    })
  })

  it('re-derives java when an existing server changes its version', () => {
    const edited = { ...formFromInstance('survival', survival), version: '1.20.4' }
    expect(resolveServerForm(edited, ctx('edit', paperCatalog))?.java).toBe('java17')
  })

  it('keeps an explicit java override', () => {
    const value = formFromInstance('survival', { ...survival, java: 'java25' })
    expect(value.javaOverride).toBe('java25')
    expect(resolveServerForm({ ...value, version: '1.20.4' }, ctx('edit', paperCatalog))?.java).toBe('java25')
  })

  it('detects changes against the initial value', () => {
    const initial = formFromInstance('survival', survival)
    expect(sameFormValue(initial, formFromInstance('survival', survival))).toBe(true)
    expect(sameFormValue(initial, { ...initial, memoryGiB: '5' })).toBe(false)
  })
})
```

Run: `cd web && npx vitest run src/features/servers/form-model.test.ts`
Expected: FAIL – `Failed to resolve import "./form-model"`.

- [ ] **Step 2: `web/src/features/servers/form-model.ts`**

```ts
import type { Catalog, Difficulty, Gamemode, InstanceConfig, InstanceType, JavaVersion, LevelType, ServerProperties } from '@/api/types'
import { memoryInputValue } from './format'
import { javaFor } from './java'
import {
  DEFAULT_MEMORY_MIB,
  hasLoader,
  isModpack,
  MAX_MOTD_LENGTH,
  MAX_OPS,
  MAX_SEED_LENGTH,
  MAX_WHITELIST,
  memoryMiBFromGiB,
  parsePlayerNames,
  validateIntInRange,
  validateLoaderVersion,
  validateMemoryMiB,
  validateModpack,
  validatePlayerNames,
  validateServerName,
  validateText,
  validateVersion,
  type FieldError,
} from './validation'

export const EULA_URL = 'https://aka.ms/MinecraftEULA'

/** Properties as edited in the form: numbers and player lists stay text until they are valid. */
export interface PropertiesDraft {
  motd: string
  difficulty: Difficulty
  gamemode: Gamemode
  max_players: string
  pvp: boolean
  hardcore: boolean
  allow_flight: boolean
  enable_command_block: boolean
  online_mode: boolean
  whitelist_enabled: boolean
  whitelist: string
  ops: string
  view_distance: string
  simulation_distance: string
  spawn_protection: string
  seed: string
  level_type: LevelType
}

export interface ServerFormValue {
  name: string
  type: InstanceType
  /** '' = newest version of the catalog. */
  version: string
  /** Free text instead of the catalog select. */
  manualVersion: boolean
  /** '' = latest loader (null in the API). */
  loaderVersion: string
  modpack: string
  /** '' = derived from type and version. */
  javaOverride: JavaVersion | ''
  memoryGiB: string
  properties: PropertiesDraft
  eulaAccepted: boolean
}

export interface ServerFormResult {
  name: string
  type: InstanceType
  version: string
  loader_version: string | null
  modpack: string | null
  java: JavaVersion
  memory_mib: number
  properties: ServerProperties
}

export const PROPERTY_FIELDS = [
  'properties.motd',
  'properties.max_players',
  'properties.whitelist',
  'properties.ops',
  'properties.view_distance',
  'properties.simulation_distance',
  'properties.spawn_protection',
  'properties.seed',
] as const

export type PropertyField = (typeof PROPERTY_FIELDS)[number]
export type FormField = 'name' | 'type' | 'version' | 'loader_version' | 'modpack' | 'java' | 'memory' | 'eula' | PropertyField
export type FormErrors = Partial<Record<FormField, FieldError>>

export interface FormContext {
  mode: 'create' | 'edit'
  catalog: Catalog | undefined
  curseforgeKeySet: boolean
}

/** Defaults of spec §7; the MOTD default (server name) is applied by the panel when motd is omitted. */
export function defaultPropertiesDraft(): PropertiesDraft {
  return {
    motd: '',
    difficulty: 'normal',
    gamemode: 'survival',
    max_players: '20',
    pvp: true,
    hardcore: false,
    allow_flight: false,
    enable_command_block: false,
    online_mode: true,
    whitelist_enabled: false,
    whitelist: '',
    ops: '',
    view_distance: '10',
    simulation_distance: '10',
    spawn_protection: '16',
    seed: '',
    level_type: 'normal',
  }
}

export function emptyServerForm(): ServerFormValue {
  return {
    name: '',
    type: 'paper',
    version: '',
    manualVersion: false,
    loaderVersion: '',
    modpack: '',
    javaOverride: '',
    memoryGiB: memoryInputValue(DEFAULT_MEMORY_MIB),
    properties: defaultPropertiesDraft(),
    eulaAccepted: false,
  }
}

function numberText(value: number | undefined, fallback: string): string {
  return value === undefined ? fallback : String(value)
}

export function formFromInstance(name: string, instance: InstanceConfig): ServerFormValue {
  const p = instance.properties
  const d = defaultPropertiesDraft()
  return {
    name,
    type: instance.type,
    version: instance.version,
    manualVersion: false,
    loaderVersion: instance.loader_version ?? '',
    modpack: instance.modpack ?? '',
    // A derived java stays "automatic" so that a version change re-derives it; an explicit override stays visible.
    javaOverride: instance.java === javaFor(instance.type, instance.version) ? '' : instance.java,
    memoryGiB: memoryInputValue(instance.memory_mib),
    properties: {
      motd: p.motd ?? name,
      difficulty: p.difficulty ?? d.difficulty,
      gamemode: p.gamemode ?? d.gamemode,
      max_players: numberText(p.max_players, d.max_players),
      pvp: p.pvp ?? d.pvp,
      hardcore: p.hardcore ?? d.hardcore,
      allow_flight: p.allow_flight ?? d.allow_flight,
      enable_command_block: p.enable_command_block ?? d.enable_command_block,
      online_mode: p.online_mode ?? d.online_mode,
      whitelist_enabled: p.whitelist_enabled ?? d.whitelist_enabled,
      whitelist: (p.whitelist ?? []).join('\n'),
      ops: (p.ops ?? []).join('\n'),
      view_distance: numberText(p.view_distance, d.view_distance),
      simulation_distance: numberText(p.simulation_distance, d.simulation_distance),
      spawn_protection: numberText(p.spawn_protection, d.spawn_protection),
      seed: p.seed ?? d.seed,
      level_type: p.level_type ?? d.level_type,
    },
    eulaAccepted: true,
  }
}

export function effectiveVersion(value: ServerFormValue, catalog: Catalog | undefined): string {
  if (isModpack(value.type)) return ''
  const typed = value.version.trim()
  return typed !== '' ? typed : (catalog?.versions[0]?.id ?? '')
}

export function effectiveJava(value: ServerFormValue, catalog: Catalog | undefined): JavaVersion {
  if (value.javaOverride !== '') return value.javaOverride
  const version = effectiveVersion(value, catalog)
  return catalog?.versions.find((v) => v.id === version)?.java ?? javaFor(value.type, version)
}

export function validateServerForm(value: ServerFormValue, ctx: FormContext): FormErrors {
  const errors: FormErrors = {}
  const check = (field: FormField, error: FieldError | null) => {
    if (error) errors[field] = error
  }
  if (ctx.mode === 'create') {
    check('name', validateServerName(value.name.trim()))
    if (value.type === 'curseforge' && !ctx.curseforgeKeySet) errors.type = { key: 'servers.validation.curseforge_key_missing' }
    if (!value.eulaAccepted) errors.eula = { key: 'servers.validation.eula_required' }
  }
  if (!isModpack(value.type)) check('version', validateVersion(effectiveVersion(value, ctx.catalog)))
  if (hasLoader(value.type)) check('loader_version', validateLoaderVersion(value.loaderVersion.trim()))
  check('modpack', validateModpack(value.type, value.modpack.trim()))
  if (value.type === 'curseforge' && effectiveJava(value, ctx.catalog) === 'java8') errors.java = { key: 'servers.validation.java_curseforge' }
  check('memory', validateMemoryMiB(memoryMiBFromGiB(value.memoryGiB)))
  const p = value.properties
  check('properties.motd', validateText(p.motd, MAX_MOTD_LENGTH))
  check('properties.max_players', validateIntInRange(p.max_players, 1, 500))
  check('properties.view_distance', validateIntInRange(p.view_distance, 3, 32))
  check('properties.simulation_distance', validateIntInRange(p.simulation_distance, 3, 32))
  check('properties.spawn_protection', validateIntInRange(p.spawn_protection, 0, 256))
  check('properties.whitelist', validatePlayerNames(p.whitelist, MAX_WHITELIST))
  check('properties.ops', validatePlayerNames(p.ops, MAX_OPS))
  check('properties.seed', validateText(p.seed, MAX_SEED_LENGTH))
  return errors
}

/** Turns a valid form into the fields of CreateServerRequest/UpdateServerRequest; null while anything is invalid. */
export function resolveServerForm(value: ServerFormValue, ctx: FormContext): ServerFormResult | null {
  if (Object.keys(validateServerForm(value, ctx)).length > 0) return null
  const p = value.properties
  const properties: ServerProperties = {
    difficulty: p.difficulty,
    gamemode: p.gamemode,
    max_players: Number(p.max_players.trim()),
    pvp: p.pvp,
    hardcore: p.hardcore,
    allow_flight: p.allow_flight,
    enable_command_block: p.enable_command_block,
    online_mode: p.online_mode,
    whitelist_enabled: p.whitelist_enabled,
    whitelist: parsePlayerNames(p.whitelist).names,
    ops: parsePlayerNames(p.ops).names,
    view_distance: Number(p.view_distance.trim()),
    simulation_distance: Number(p.simulation_distance.trim()),
    spawn_protection: Number(p.spawn_protection.trim()),
    seed: p.seed,
    level_type: p.level_type,
  }
  // A new server without MOTD shows its name (spec §7); an existing one keeps exactly what the admin typed.
  if (ctx.mode === 'edit' || p.motd !== '') properties.motd = p.motd
  const loader = value.loaderVersion.trim()
  return {
    name: value.name.trim(),
    type: value.type,
    version: effectiveVersion(value, ctx.catalog),
    loader_version: hasLoader(value.type) && loader !== '' ? loader : null,
    modpack: isModpack(value.type) ? value.modpack.trim() : null,
    java: effectiveJava(value, ctx.catalog),
    memory_mib: memoryMiBFromGiB(value.memoryGiB) ?? DEFAULT_MEMORY_MIB,
    properties,
  }
}

export function sameFormValue(a: ServerFormValue, b: ServerFormValue): boolean {
  return JSON.stringify(a) === JSON.stringify(b)
}
```

Run: `cd web && npx vitest run src/features/servers/form-model.test.ts`
Expected: PASS (14 Tests).

- [ ] **Step 3: Texte**

`web/src/i18n/de.ts`, im Block `servers` nach `not_configured_hint` einfügen:

```ts
    type_description: {
      vanilla: 'Offizieller Server von Mojang, ohne Mods und Plugins.',
      paper: 'Schneller Server mit Plugins (Bukkit/Spigot). Passt für die meisten.',
      purpur: 'Paper mit vielen zusätzlichen Einstellungen.',
      fabric: 'Leichter Mod-Loader für Fabric-Mods.',
      neoforge: 'Mod-Loader für NeoForge-Mods, Nachfolger von Forge.',
      modrinth: 'Installiert ein fertiges Modpack von Modrinth.',
      curseforge: 'Installiert ein fertiges Modpack von CurseForge (braucht einen API-Key).',
    },
    create: {
      title: 'Server anlegen',
      description: 'Der Server wird eingerichtet und danach automatisch gestartet.',
      submit: 'Server anlegen',
      created: 'Server „{{name}}“ angelegt.',
      base_domain_missing_title: 'Keine Basisdomain festgelegt',
      base_domain_missing: 'Server lassen sich erst anlegen, wenn eine Basisdomain eingetragen ist.',
    },
    form: {
      name: 'Name',
      name_hint: 'Wird Teil der Adresse: 2–32 Zeichen, Kleinbuchstaben, Ziffern und Bindestrich.',
      address_preview: 'Adresse: {{address}}',
      address_unavailable: 'Adresse: noch keine – erst wenn eine Basisdomain festgelegt ist.',
      type: 'Art',
      curseforge_key_hint: 'Für CurseForge-Modpacks braucht mc-panel einen API-Key. Hinterlege ihn in den Einstellungen.',
      version: 'Minecraft-Version',
      version_latest: 'Immer die neueste (LATEST)',
      version_loading: 'Versionen werden geladen …',
      version_hint: 'Neueste stabile Versionen zuerst.',
      version_manual: 'Version von Hand eingeben',
      version_from_list: 'Aus der Liste wählen',
      version_manual_hint: 'Zum Beispiel 1.21.11 oder LATEST.',
      version_catalog_failed: 'Die Versionsliste ist gerade nicht abrufbar. Trag die Version von Hand ein, z. B. 1.21.11 oder LATEST.',
      fabric_loader: 'Fabric-Loader',
      loader_latest: 'Neueste (empfohlen)',
      modpack_modrinth: 'Modrinth-Projekt',
      modpack_modrinth_hint: 'Der Name aus der Adresse modrinth.com/modpack/<name>, z. B. cobblemon-fabric.',
      modpack_curseforge: 'CurseForge-Modpack',
      modpack_curseforge_hint: 'Projektname oder Link, z. B. all-the-mods-10 oder https://www.curseforge.com/minecraft/modpacks/all-the-mods-10.',
      memory: 'Arbeitsspeicher (GiB)',
      memory_slider: 'Arbeitsspeicher, Schieberegler',
      memory_hint: '1 bis 32 GiB in Schritten von 0,25 GiB. Der Container darf 25 % mehr nutzen.',
      advanced: 'Erweitert',
      advanced_description: 'Java- und Loader-Version – nur ändern, wenn du weißt, wozu.',
      java: 'Java-Version',
      java_auto: 'Automatisch ({{java}})',
      java_hint: 'Wird aus der Minecraft-Version abgeleitet. Ändere sie nur, wenn ein Modpack eine andere Java-Version braucht.',
      neoforge_version: 'NeoForge-Version',
      neoforge_version_hint: 'Leer lassen für die neueste stabile NeoForge-Version zur gewählten Minecraft-Version.',
      properties: 'Eigenschaften',
      properties_description: 'Einstellungen aus server.properties. Die Voreinstellungen passen für die meisten Server.',
      eula: 'Ich akzeptiere die Minecraft-EULA.',
      eula_link: 'Minecraft-EULA lesen (öffnet einen neuen Tab)',
      eula_hint: 'Ohne Zustimmung startet kein Minecraft-Server.',
      fix_errors: 'Bitte korrigiere die markierten Felder.',
    },
    props: {
      motd: 'Nachricht im Serverbrowser (MOTD)',
      motd_hint_create: 'Leer lassen, dann steht dort der Servername.',
      difficulty: 'Schwierigkeit',
      gamemode: 'Spielmodus',
      max_players: 'Maximale Spielerzahl',
      pvp: 'PvP',
      hardcore: 'Hardcore',
      allow_flight: 'Fliegen erlauben',
      enable_command_block: 'Befehlsblöcke',
      online_mode: 'Online-Modus (Konten bei Mojang prüfen)',
      online_mode_warning: 'Ohne Online-Modus kann sich jeder mit beliebigem Namen anmelden – auch mit dem Namen eines Operators.',
      whitelist_enabled: 'Whitelist aktiv',
      whitelist: 'Whitelist',
      whitelist_hint: 'Spielernamen, getrennt durch Komma oder Zeilenumbruch (höchstens 200).',
      ops: 'Operatoren',
      ops_hint: 'Spielernamen mit Admin-Rechten im Spiel (höchstens 50).',
      view_distance: 'Sichtweite (Chunks)',
      simulation_distance: 'Simulationsweite (Chunks)',
      spawn_protection: 'Spawn-Schutz (Blöcke)',
      seed: 'Seed',
      level_type: 'Welttyp',
      world_hint: 'Seed und Welttyp wirken nur, wenn eine neue Welt erzeugt wird.',
    },
    difficulty: {
      peaceful: 'Friedlich',
      easy: 'Einfach',
      normal: 'Normal',
      hard: 'Schwer',
    },
    gamemode: {
      survival: 'Überleben',
      creative: 'Kreativ',
      adventure: 'Abenteuer',
      spectator: 'Zuschauer',
    },
    level_type: {
      normal: 'Normal',
      flat: 'Flach',
      large_biomes: 'Große Biome',
      amplified: 'Zerklüftet',
    },
```

`web/src/i18n/en.ts`, gleiche Stelle:

```ts
    type_description: {
      vanilla: 'Official server by Mojang, without mods or plugins.',
      paper: 'Fast server with plugins (Bukkit/Spigot). Fits most.',
      purpur: 'Paper with many additional settings.',
      fabric: 'Lightweight mod loader for Fabric mods.',
      neoforge: 'Mod loader for NeoForge mods, successor of Forge.',
      modrinth: 'Installs a ready-made modpack from Modrinth.',
      curseforge: 'Installs a ready-made modpack from CurseForge (needs an API key).',
    },
    create: {
      title: 'Create server',
      description: 'The server is set up and then started automatically.',
      submit: 'Create server',
      created: 'Server “{{name}}” created.',
      base_domain_missing_title: 'No base domain set',
      base_domain_missing: 'You can create servers once a base domain is set.',
    },
    form: {
      name: 'Name',
      name_hint: 'Becomes part of the address: 2–32 characters, lowercase letters, digits and hyphens.',
      address_preview: 'Address: {{address}}',
      address_unavailable: 'Address: none yet – it needs a base domain.',
      type: 'Type',
      curseforge_key_hint: 'CurseForge modpacks need an API key. Add it in the settings.',
      version: 'Minecraft version',
      version_latest: 'Always the latest (LATEST)',
      version_loading: 'Loading versions …',
      version_hint: 'Newest stable versions first.',
      version_manual: 'Enter a version manually',
      version_from_list: 'Choose from the list',
      version_manual_hint: 'For example 1.21.11 or LATEST.',
      version_catalog_failed: 'The version list is currently unavailable. Enter the version manually, e.g. 1.21.11 or LATEST.',
      fabric_loader: 'Fabric loader',
      loader_latest: 'Latest (recommended)',
      modpack_modrinth: 'Modrinth project',
      modpack_modrinth_hint: 'The name from modrinth.com/modpack/<name>, e.g. cobblemon-fabric.',
      modpack_curseforge: 'CurseForge modpack',
      modpack_curseforge_hint: 'Project name or link, e.g. all-the-mods-10 or https://www.curseforge.com/minecraft/modpacks/all-the-mods-10.',
      memory: 'Memory (GiB)',
      memory_slider: 'Memory, slider',
      memory_hint: '1 to 32 GiB in steps of 0.25 GiB. The container may use 25 % more.',
      advanced: 'Advanced',
      advanced_description: 'Java and loader version – only change them if you know why.',
      java: 'Java version',
      java_auto: 'Automatic ({{java}})',
      java_hint: 'Derived from the Minecraft version. Only change it if a modpack needs a different Java version.',
      neoforge_version: 'NeoForge version',
      neoforge_version_hint: 'Leave empty for the latest stable NeoForge version for the selected Minecraft version.',
      properties: 'Properties',
      properties_description: 'Settings from server.properties. The defaults suit most servers.',
      eula: 'I accept the Minecraft EULA.',
      eula_link: 'Read the Minecraft EULA (opens a new tab)',
      eula_hint: 'No Minecraft server starts without accepting it.',
      fix_errors: 'Please fix the marked fields.',
    },
    props: {
      motd: 'Message in the server list (MOTD)',
      motd_hint_create: 'Leave empty to show the server name.',
      difficulty: 'Difficulty',
      gamemode: 'Game mode',
      max_players: 'Max players',
      pvp: 'PvP',
      hardcore: 'Hardcore',
      allow_flight: 'Allow flight',
      enable_command_block: 'Command blocks',
      online_mode: 'Online mode (verify accounts with Mojang)',
      online_mode_warning: 'Without online mode anyone can join with any name – including an operator’s name.',
      whitelist_enabled: 'Whitelist active',
      whitelist: 'Whitelist',
      whitelist_hint: 'Player names, separated by commas or line breaks (at most 200).',
      ops: 'Operators',
      ops_hint: 'Player names with admin rights in the game (at most 50).',
      view_distance: 'View distance (chunks)',
      simulation_distance: 'Simulation distance (chunks)',
      spawn_protection: 'Spawn protection (blocks)',
      seed: 'Seed',
      level_type: 'World type',
      world_hint: 'Seed and world type only apply when a new world is generated.',
    },
    difficulty: {
      peaceful: 'Peaceful',
      easy: 'Easy',
      normal: 'Normal',
      hard: 'Hard',
    },
    gamemode: {
      survival: 'Survival',
      creative: 'Creative',
      adventure: 'Adventure',
      spectator: 'Spectator',
    },
    level_type: {
      normal: 'Normal',
      flat: 'Flat',
      large_biomes: 'Large biomes',
      amplified: 'Amplified',
    },
```

In beiden Dateien im Block `dashboard` die nur vom Dialog genutzten Schlüssel entfernen: `create_description`, `name_label`, `name_hint`, `name_invalid`, `created` (und Schlüssel, die Aufgabe 11–16 ausschließlich für den Dialog ergänzt hat; Kontrolle in Step 12).

`web/src/i18n/i18n.test.ts`, am Ende von `DYNAMIC_KEYS` ergänzen:

```ts
  ...['vanilla', 'paper', 'purpur', 'fabric', 'neoforge', 'modrinth', 'curseforge'].map((k) => `servers.type_description.${k}`),
  ...['peaceful', 'easy', 'normal', 'hard'].map((d) => `servers.difficulty.${d}`),
  ...['survival', 'creative', 'adventure', 'spectator'].map((g) => `servers.gamemode.${g}`),
  ...['normal', 'flat', 'large_biomes', 'amplified'].map((l) => `servers.level_type.${l}`),
  ...[
    'pvp',
    'hardcore',
    'allow_flight',
    'enable_command_block',
    'online_mode',
    'whitelist_enabled',
    'max_players',
    'view_distance',
    'simulation_distance',
    'spawn_protection',
  ].map((p) => `servers.props.${p}`),
```

- [ ] **Step 4: `web/src/features/servers/field-row.tsx`**

```tsx
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Label } from '@/components/ui/label'
import { cn } from '@/lib/utils'
import type { FieldError } from './validation'

export interface ControlProps {
  id: string
  'aria-describedby'?: string
  'aria-invalid'?: true
}

export interface FieldRowProps {
  id: string
  label: string
  hint?: string
  error?: FieldError
  children(control: ControlProps): ReactNode
}

/** Label, control and one message line: the error replaces the hint and is linked via aria-describedby. */
export function FieldRow({ id, label, hint, error, children }: FieldRowProps) {
  const { t } = useTranslation()
  const messageId = `${id}-message`
  const message = error ? t(error.key, error.params ?? {}) : hint
  return (
    <div className="space-y-2">
      <Label htmlFor={id}>{label}</Label>
      {children({ id, 'aria-describedby': message ? messageId : undefined, 'aria-invalid': error ? true : undefined })}
      {message ? (
        <p id={messageId} className={cn('text-sm', error ? 'text-destructive' : 'text-muted-foreground')}>
          {message}
        </p>
      ) : null}
    </div>
  )
}
```

- [ ] **Step 5: `web/src/features/servers/collapsible-section.tsx`**

```tsx
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
```

- [ ] **Step 6: `web/src/features/servers/properties-fields.tsx`**

```tsx
import { useTranslation } from 'react-i18next'
import type { Difficulty, Gamemode, LevelType } from '@/api/types'
import { NativeSelect } from '@/components/native-select'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { FieldRow } from './field-row'
import type { PropertiesDraft, PropertyField } from './form-model'
import { DIFFICULTIES, GAMEMODES, LEVEL_TYPES, type FieldError } from './validation'

type SwitchKey = 'pvp' | 'hardcore' | 'allow_flight' | 'enable_command_block' | 'online_mode' | 'whitelist_enabled'
type NumberKey = 'max_players' | 'spawn_protection' | 'view_distance' | 'simulation_distance'

const SWITCHES: readonly SwitchKey[] = ['pvp', 'hardcore', 'allow_flight', 'enable_command_block', 'online_mode', 'whitelist_enabled']

const NUMBERS: readonly { key: NumberKey; field: PropertyField; min: number; max: number }[] = [
  { key: 'max_players', field: 'properties.max_players', min: 1, max: 500 },
  { key: 'spawn_protection', field: 'properties.spawn_protection', min: 0, max: 256 },
  { key: 'view_distance', field: 'properties.view_distance', min: 3, max: 32 },
  { key: 'simulation_distance', field: 'properties.simulation_distance', min: 3, max: 32 },
]

// Same look as the shadcn Input; there is no shadcn textarea in this project.
const TEXTAREA_CLASS =
  'border-input focus-visible:border-ring focus-visible:ring-ring/50 aria-invalid:border-destructive aria-invalid:ring-destructive/20 dark:bg-input/30 min-h-20 w-full rounded-lg border bg-transparent px-2.5 py-1.5 text-base outline-none focus-visible:ring-3 aria-invalid:ring-3 md:text-sm'

export interface PropertiesFieldsProps {
  idPrefix: string
  mode: 'create' | 'edit'
  serverName: string
  value: PropertiesDraft
  error(field: PropertyField): FieldError | undefined
  onChange(patch: Partial<PropertiesDraft>, field?: PropertyField): void
}

export function PropertiesFields({ idPrefix, mode, serverName, value, error, onChange }: PropertiesFieldsProps) {
  const { t } = useTranslation()
  return (
    <div className="space-y-4">
      <FieldRow
        id={`${idPrefix}-motd`}
        label={t('servers.props.motd')}
        hint={mode === 'create' ? t('servers.props.motd_hint_create') : undefined}
        error={error('properties.motd')}
      >
        {(c) => (
          <Input
            {...c}
            value={value.motd}
            autoComplete="off"
            placeholder={mode === 'create' && serverName !== '' ? serverName : undefined}
            onChange={(e) => onChange({ motd: e.target.value }, 'properties.motd')}
          />
        )}
      </FieldRow>
      <div className="grid gap-4 sm:grid-cols-2">
        <FieldRow id={`${idPrefix}-difficulty`} label={t('servers.props.difficulty')}>
          {(c) => (
            <NativeSelect {...c} className="w-full" value={value.difficulty} onChange={(e) => onChange({ difficulty: e.target.value as Difficulty })}>
              {DIFFICULTIES.map((d) => (
                <option key={d} value={d}>
                  {t(`servers.difficulty.${d}`)}
                </option>
              ))}
            </NativeSelect>
          )}
        </FieldRow>
        <FieldRow id={`${idPrefix}-gamemode`} label={t('servers.props.gamemode')}>
          {(c) => (
            <NativeSelect {...c} className="w-full" value={value.gamemode} onChange={(e) => onChange({ gamemode: e.target.value as Gamemode })}>
              {GAMEMODES.map((g) => (
                <option key={g} value={g}>
                  {t(`servers.gamemode.${g}`)}
                </option>
              ))}
            </NativeSelect>
          )}
        </FieldRow>
        {NUMBERS.map(({ key, field, min, max }) => (
          <FieldRow key={key} id={`${idPrefix}-${key}`} label={t(`servers.props.${key}`)} error={error(field)}>
            {(c) => (
              <Input
                {...c}
                type="number"
                inputMode="numeric"
                min={min}
                max={max}
                step={1}
                value={value[key]}
                onChange={(e) => {
                  const patch: Partial<PropertiesDraft> = {}
                  patch[key] = e.target.value
                  onChange(patch, field)
                }}
              />
            )}
          </FieldRow>
        ))}
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        {SWITCHES.map((key) => (
          <div key={key} className="flex items-center justify-between gap-3 rounded-md border px-3 py-2">
            <Label htmlFor={`${idPrefix}-${key}`}>{t(`servers.props.${key}`)}</Label>
            <Switch
              id={`${idPrefix}-${key}`}
              checked={value[key]}
              onCheckedChange={(checked) => {
                const patch: Partial<PropertiesDraft> = {}
                patch[key] = checked
                onChange(patch)
              }}
            />
          </div>
        ))}
      </div>
      {value.online_mode ? null : <p className="text-destructive text-sm">{t('servers.props.online_mode_warning')}</p>}
      <div className="grid gap-4 sm:grid-cols-2">
        <FieldRow id={`${idPrefix}-whitelist`} label={t('servers.props.whitelist')} hint={t('servers.props.whitelist_hint')} error={error('properties.whitelist')}>
          {(c) => (
            <textarea
              {...c}
              rows={3}
              className={TEXTAREA_CLASS}
              spellCheck={false}
              value={value.whitelist}
              onChange={(e) => onChange({ whitelist: e.target.value }, 'properties.whitelist')}
            />
          )}
        </FieldRow>
        <FieldRow id={`${idPrefix}-ops`} label={t('servers.props.ops')} hint={t('servers.props.ops_hint')} error={error('properties.ops')}>
          {(c) => (
            <textarea
              {...c}
              rows={3}
              className={TEXTAREA_CLASS}
              spellCheck={false}
              value={value.ops}
              onChange={(e) => onChange({ ops: e.target.value }, 'properties.ops')}
            />
          )}
        </FieldRow>
      </div>
      <p className="text-muted-foreground text-sm">{t('servers.props.world_hint')}</p>
      <div className="grid gap-4 sm:grid-cols-2">
        <FieldRow id={`${idPrefix}-seed`} label={t('servers.props.seed')} error={error('properties.seed')}>
          {(c) => <Input {...c} value={value.seed} autoComplete="off" spellCheck={false} onChange={(e) => onChange({ seed: e.target.value }, 'properties.seed')} />}
        </FieldRow>
        <FieldRow id={`${idPrefix}-level-type`} label={t('servers.props.level_type')}>
          {(c) => (
            <NativeSelect {...c} className="w-full" value={value.level_type} onChange={(e) => onChange({ level_type: e.target.value as LevelType })}>
              {LEVEL_TYPES.map((l) => (
                <option key={l} value={l}>
                  {t(`servers.level_type.${l}`)}
                </option>
              ))}
            </NativeSelect>
          )}
        </FieldRow>
      </div>
    </div>
  )
}
```

- [ ] **Step 7: Failing Test `web/src/features/servers/create-server-page.test.tsx`**

```tsx
import { screen, waitFor, within } from '@testing-library/react'
import userEvent, { type UserEvent } from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { db, loginAs, MOCK_TOTP_CODE } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { CreateServerPage } from './create-server-page'

function createdInstance(name: string) {
  return db.servers.find((s) => s.name === name)?.instance
}

async function fillRequired(user: UserEvent, name: string): Promise<void> {
  await user.type(await screen.findByLabelText('Name'), name)
  await user.click(screen.getByRole('checkbox', { name: 'Ich akzeptiere die Minecraft-EULA.' }))
}

describe('CreateServerPage', () => {
  it('creates a paper server with the newest version after sudo', async () => {
    const user = userEvent.setup()
    const onCreated = vi.fn()
    await renderWithProviders(<CreateServerPage onCreated={onCreated} />, { me: loginAs('admin') })
    await fillRequired(user, 'skyblock')
    expect(screen.getByText('Adresse: skyblock.mc.example.com')).toBeInTheDocument()
    expect(screen.getByRole('radio', { name: 'Paper' })).toBeChecked()
    await screen.findByRole('option', { name: '26.2' })
    expect(screen.getByRole('combobox', { name: 'Minecraft-Version' })).toHaveValue('26.2')
    expect(screen.getByRole('spinbutton', { name: 'Arbeitsspeicher (GiB)' })).toHaveValue(4)
    expect(screen.getByRole('link', { name: 'Minecraft-EULA lesen (öffnet einen neuen Tab)' })).toHaveAttribute(
      'href',
      'https://aka.ms/MinecraftEULA',
    )

    await user.click(screen.getByRole('button', { name: 'Server anlegen' }))
    const sudo = await screen.findByRole('dialog', { name: 'Aktion bestätigen' })
    await user.type(within(sudo).getByLabelText('Code aus der Authenticator-App'), MOCK_TOTP_CODE)
    await user.click(within(sudo).getByRole('button', { name: 'Bestätigen' }))

    await waitFor(() => expect(onCreated).toHaveBeenCalledWith(expect.objectContaining({ name: 'skyblock' })))
    expect(await screen.findByText('Server „skyblock“ angelegt.')).toBeInTheDocument()
    expect(createdInstance('skyblock')).toMatchObject({
      type: 'paper',
      version: '26.2',
      loader_version: null,
      modpack: null,
      java: 'java25',
      memory_mib: 4096,
    })
  })

  it('shows inline errors and sends nothing while the form is invalid', async () => {
    const user = userEvent.setup()
    const onCreated = vi.fn()
    await renderWithProviders(<CreateServerPage onCreated={onCreated} />, { me: loginAs('admin', { sudo: true }) })
    const name = await screen.findByLabelText('Name')
    await user.type(name, 'Bad Name')
    expect(screen.getByText('Nur Kleinbuchstaben, Ziffern und Bindestrich, 2–32 Zeichen, am Anfang kein Bindestrich.')).toBeInTheDocument()
    expect(name).toHaveAttribute('aria-invalid', 'true')
    const memory = screen.getByRole('spinbutton', { name: 'Arbeitsspeicher (GiB)' })
    await user.clear(memory)
    await user.type(memory, '40')
    expect(screen.getByText('Gib 1 bis 32 GiB in Schritten von 0,25 GiB an.')).toBeInTheDocument()
    expect(screen.queryByText('Ohne Zustimmung zur EULA kann kein Server angelegt werden.')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Server anlegen' }))
    expect(screen.getByText('Bitte korrigiere die markierten Felder.')).toBeInTheDocument()
    expect(screen.getByText('Ohne Zustimmung zur EULA kann kein Server angelegt werden.')).toBeInTheDocument()
    await new Promise((resolve) => setTimeout(resolve, 50))
    expect(db.servers).toHaveLength(3)
    expect(onCreated).not.toHaveBeenCalled()
  })

  it('explains a missing base domain and shows the 409 from the panel', async () => {
    db.serverSettings.base_domain = null
    const user = userEvent.setup()
    const onCreated = vi.fn()
    await renderWithProviders(<CreateServerPage onCreated={onCreated} />, { me: loginAs('admin', { sudo: true }) })
    expect(await screen.findByText('Keine Basisdomain festgelegt')).toBeInTheDocument()
    await fillRequired(user, 'skyblock')
    expect(screen.getByText('Adresse: noch keine – erst wenn eine Basisdomain festgelegt ist.')).toBeInTheDocument()
    await screen.findByRole('option', { name: '26.2' })
    await user.click(screen.getByRole('button', { name: 'Server anlegen' }))
    expect(await screen.findByText('Es ist noch keine Basisdomain festgelegt. Trag sie unter Einstellungen ein.')).toBeInTheDocument()
    expect(onCreated).not.toHaveBeenCalled()
    expect(createdInstance('skyblock')).toBeUndefined()
  })

  it('falls back to free text when the version list is unavailable', async () => {
    delete db.catalogs.vanilla
    const user = userEvent.setup()
    await renderWithProviders(<CreateServerPage onCreated={vi.fn()} />, { me: loginAs('admin', { sudo: true }) })
    await fillRequired(user, 'legacy')
    await user.click(screen.getByRole('radio', { name: 'Vanilla' }))
    const version = await screen.findByRole('textbox', { name: 'Minecraft-Version' })
    expect(
      screen.getByText('Die Versionsliste ist gerade nicht abrufbar. Trag die Version von Hand ein, z. B. 1.21.11 oder LATEST.'),
    ).toBeInTheDocument()
    await user.type(version, '1.20.4')
    await user.click(screen.getByRole('button', { name: 'Erweitert' }))
    expect(screen.getByRole('combobox', { name: 'Java-Version' })).toHaveDisplayValue('Automatisch (Java 17)')
    await user.click(screen.getByRole('button', { name: 'Server anlegen' }))
    await waitFor(() => expect(createdInstance('legacy')).toMatchObject({ type: 'vanilla', version: '1.20.4', java: 'java17' }))
  })

  it('offers CurseForge modpacks only with a stored API key', async () => {
    await renderWithProviders(<CreateServerPage onCreated={vi.fn()} />, { me: loginAs('admin') })
    expect(await screen.findByRole('radio', { name: 'CurseForge-Modpack' })).toBeDisabled()
    expect(screen.getByText('Für CurseForge-Modpacks braucht mc-panel einen API-Key. Hinterlege ihn in den Einstellungen.')).toBeInTheDocument()
  })

  it('asks for the CurseForge modpack once a key is stored', async () => {
    db.serverSettings.curseforge_api_key_set = true
    const user = userEvent.setup()
    await renderWithProviders(<CreateServerPage onCreated={vi.fn()} />, { me: loginAs('admin') })
    await user.click(await screen.findByRole('radio', { name: 'CurseForge-Modpack' }))
    expect(screen.getByRole('textbox', { name: 'CurseForge-Modpack' })).toBeInTheDocument()
    expect(screen.queryByRole('combobox', { name: 'Minecraft-Version' })).not.toBeInTheDocument()
    expect(screen.queryByText('Für CurseForge-Modpacks braucht mc-panel einen API-Key. Hinterlege ihn in den Einstellungen.')).not.toBeInTheDocument()
  })

  it('offers the fabric loader versions from the catalog', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<CreateServerPage onCreated={vi.fn()} />, { me: loginAs('admin', { sudo: true }) })
    await fillRequired(user, 'fabric-test')
    await user.click(screen.getByRole('radio', { name: 'Fabric' }))
    await screen.findByRole('option', { name: '0.19.5' })
    const loader = screen.getByRole('combobox', { name: 'Fabric-Loader' })
    expect(loader).toHaveDisplayValue('Neueste (empfohlen)')
    await user.selectOptions(loader, '0.19.4')
    await user.click(screen.getByRole('button', { name: 'Server anlegen' }))
    await waitFor(() =>
      expect(createdInstance('fabric-test')).toMatchObject({ type: 'fabric', version: '26.3', loader_version: '0.19.4', java: 'java25' }),
    )
  })

  it('asks for a Modrinth project instead of a version', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<CreateServerPage onCreated={vi.fn()} />, { me: loginAs('admin', { sudo: true }) })
    await fillRequired(user, 'cobble')
    await user.click(screen.getByRole('radio', { name: 'Modrinth-Modpack' }))
    expect(screen.queryByRole('combobox', { name: 'Minecraft-Version' })).not.toBeInTheDocument()
    const pack = screen.getByRole('textbox', { name: 'Modrinth-Projekt' })
    await user.type(pack, 'Bad Slug')
    expect(screen.getByText('Nur Kleinbuchstaben, Ziffern und Bindestrich, wie in der Modrinth-Adresse.')).toBeInTheDocument()
    await user.clear(pack)
    await user.type(pack, 'cobblemon-fabric')
    await user.click(screen.getByRole('button', { name: 'Server anlegen' }))
    await waitFor(() =>
      expect(createdInstance('cobble')).toMatchObject({ type: 'modrinth', version: '', modpack: 'cobblemon-fabric', java: 'java21' }),
    )
  })

  it('sends edited properties and warns when online mode is off', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<CreateServerPage onCreated={vi.fn()} />, { me: loginAs('admin', { sudo: true }) })
    await fillRequired(user, 'props')
    await screen.findByRole('option', { name: '26.2' })
    await user.click(screen.getByRole('button', { name: 'Eigenschaften' }))
    const motd = screen.getByLabelText('Nachricht im Serverbrowser (MOTD)')
    expect(motd).toHaveAttribute('placeholder', 'props')
    await user.type(motd, 'Hallo')
    await user.click(screen.getByRole('switch', { name: 'PvP' }))
    await user.click(screen.getByRole('switch', { name: 'Online-Modus (Konten bei Mojang prüfen)' }))
    expect(
      screen.getByText('Ohne Online-Modus kann sich jeder mit beliebigem Namen anmelden – auch mit dem Namen eines Operators.'),
    ).toBeInTheDocument()
    await user.type(screen.getByLabelText('Whitelist'), 'Notch, jeb_')
    await user.click(screen.getByRole('button', { name: 'Server anlegen' }))
    await waitFor(() =>
      expect(createdInstance('props')?.properties).toMatchObject({ motd: 'Hallo', pvp: false, online_mode: false, whitelist: ['Notch', 'jeb_'] }),
    )
  })
})
```

Run: `cd web && npx vitest run src/features/servers/create-server-page.test.tsx`
Expected: FAIL – `Failed to resolve import "./create-server-page"`.

- [ ] **Step 8: `web/src/features/servers/server-form.tsx`**

```tsx
import { useQuery } from '@tanstack/react-query'
import { useId, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { isSudoCancelled } from '@/api/errors'
import type { Catalog, InstanceType, JavaVersion } from '@/api/types'
import { NativeSelect } from '@/components/native-select'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { errorMessage } from '@/lib/error-message'
import { cn } from '@/lib/utils'
import { catalogQueryOptions } from './api'
import { CollapsibleSection } from './collapsible-section'
import { FieldRow } from './field-row'
import {
  effectiveJava,
  effectiveVersion,
  EULA_URL,
  PROPERTY_FIELDS,
  resolveServerForm,
  sameFormValue,
  validateServerForm,
  type FormContext,
  type FormField,
  type ServerFormResult,
  type ServerFormValue,
} from './form-model'
import { javaLabel, serverAddress } from './format'
import { PropertiesFields } from './properties-fields'
import {
  DEFAULT_MEMORY_MIB,
  INSTANCE_TYPES,
  isModpack,
  JAVA_VERSIONS,
  MAX_MEMORY_MIB,
  MEMORY_STEP_MIB,
  memoryMiBFromGiB,
  MIN_MEMORY_MIB,
  type FieldError,
} from './validation'

type Shown = (field: FormField) => FieldError | undefined
type Update = (patch: Partial<ServerFormValue>, ...fields: FormField[]) => void

export interface ServerFormProps {
  mode: 'create' | 'edit'
  initial: ServerFormValue
  /** create only: live address preview */
  baseDomain?: string | null
  /** create only: CurseForge can only be chosen with a stored API key */
  curseforgeKeySet?: boolean
  submitLabel: string
  pending: boolean
  /** Error of the surrounding mutation, rendered below the form (a cancelled sudo dialog is not an error). */
  error: unknown
  notice?: ReactNode
  onSubmit(result: ServerFormResult): void
}

/** Shared form for creating (name, type, EULA) and editing (everything else) a server. */
export function ServerForm({
  mode,
  initial,
  baseDomain = null,
  curseforgeKeySet = false,
  submitLabel,
  pending,
  error,
  notice,
  onSubmit,
}: ServerFormProps) {
  const { t } = useTranslation()
  const id = useId()
  const [value, setValue] = useState<ServerFormValue>(initial)
  const [touched, setTouched] = useState<ReadonlySet<FormField>>(() => new Set())
  const [submitted, setSubmitted] = useState(false)
  const modpack = isModpack(value.type)
  const catalog = useQuery({ ...catalogQueryOptions(value.type), enabled: !modpack })
  const ctx: FormContext = { mode, catalog: catalog.data, curseforgeKeySet }
  const errors = validateServerForm(value, ctx)
  const hasErrors = Object.keys(errors).length > 0
  const dirty = mode === 'create' || !sameFormValue(value, initial)
  const shown: Shown = (field) => (submitted || touched.has(field) ? errors[field] : undefined)

  const update: Update = (patch, ...fields) => {
    setValue({ ...value, ...patch })
    if (fields.length > 0) setTouched(new Set([...touched, ...fields]))
  }

  function changeType(type: InstanceType) {
    // Version, loader, modpack and java override belong to the previous type.
    update({ type, version: '', manualVersion: false, loaderVersion: '', modpack: '', javaOverride: '' })
  }

  return (
    <form
      className="max-w-3xl space-y-6"
      noValidate
      onSubmit={(e) => {
        e.preventDefault()
        setSubmitted(true)
        const result = resolveServerForm(value, ctx)
        if (result) onSubmit(result)
      }}
    >
      {mode === 'create' ? (
        <>
          <NameField id={`${id}-name`} name={value.name} baseDomain={baseDomain} error={shown('name')} onChange={(name) => update({ name }, 'name')} />
          <TypeField id={`${id}-type`} type={value.type} curseforgeKeySet={curseforgeKeySet} error={shown('type')} onChange={changeType} />
        </>
      ) : (
        <p className="text-sm">
          <span className="font-medium">{t('servers.form.type')}:</span> {t(`servers.type.${value.type}`)}
        </p>
      )}
      <VersionFields
        id={id}
        value={value}
        catalog={catalog.data}
        catalogPending={!modpack && catalog.isPending}
        catalogFailed={catalog.isError}
        shown={shown}
        update={update}
      />
      <MemoryField id={`${id}-memory`} text={value.memoryGiB} error={shown('memory')} onChange={(memoryGiB) => update({ memoryGiB }, 'memory')} />
      <CollapsibleSection
        title={t('servers.form.advanced')}
        description={t('servers.form.advanced_description')}
        forceOpen={submitted && (errors.java !== undefined || (value.type === 'neoforge' && errors.loader_version !== undefined))}
      >
        <AdvancedFields id={id} value={value} catalog={catalog.data} shown={shown} update={update} />
      </CollapsibleSection>
      <CollapsibleSection
        title={t('servers.form.properties')}
        description={t('servers.form.properties_description')}
        forceOpen={submitted && PROPERTY_FIELDS.some((f) => errors[f] !== undefined)}
      >
        <PropertiesFields
          idPrefix={`${id}-prop`}
          mode={mode}
          serverName={value.name.trim()}
          value={value.properties}
          error={shown}
          onChange={(patch, field) => update({ properties: { ...value.properties, ...patch } }, ...(field ? [field] : []))}
        />
      </CollapsibleSection>
      {mode === 'create' ? (
        <EulaField id={`${id}-eula`} checked={value.eulaAccepted} error={shown('eula')} onChange={(eulaAccepted) => update({ eulaAccepted }, 'eula')} />
      ) : null}
      <div className="space-y-3">
        {notice ? <p className="text-muted-foreground text-sm">{notice}</p> : null}
        {submitted && hasErrors ? (
          <p role="alert" className="text-destructive text-sm">
            {t('servers.form.fix_errors')}
          </p>
        ) : null}
        {error && !isSudoCancelled(error) ? (
          <p role="alert" className="text-destructive text-sm">
            {errorMessage(error, t)}
          </p>
        ) : null}
        <Button type="submit" disabled={pending || !dirty}>
          {submitLabel}
        </Button>
      </div>
    </form>
  )
}

function NameField({
  id,
  name,
  baseDomain,
  error,
  onChange,
}: {
  id: string
  name: string
  baseDomain: string | null
  error: FieldError | undefined
  onChange(name: string): void
}) {
  const { t } = useTranslation()
  const address = serverAddress(name.trim() || '<name>', baseDomain)
  return (
    <div className="space-y-2">
      <FieldRow id={id} label={t('servers.form.name')} hint={t('servers.form.name_hint')} error={error}>
        {(c) => (
          <Input
            {...c}
            className="sm:w-80"
            value={name}
            autoCapitalize="none"
            autoComplete="off"
            spellCheck={false}
            maxLength={32}
            onChange={(e) => onChange(e.target.value)}
          />
        )}
      </FieldRow>
      <p className="text-sm" aria-live="polite">
        {address ? t('servers.form.address_preview', { address }) : t('servers.form.address_unavailable')}
      </p>
    </div>
  )
}

function TypeField({
  id,
  type,
  curseforgeKeySet,
  error,
  onChange,
}: {
  id: string
  type: InstanceType
  curseforgeKeySet: boolean
  error: FieldError | undefined
  onChange(type: InstanceType): void
}) {
  const { t } = useTranslation()
  return (
    <fieldset className="space-y-2" aria-describedby={error ? `${id}-error` : undefined}>
      <legend className="mb-2 text-sm font-medium">{t('servers.form.type')}</legend>
      <div className="grid gap-2 sm:grid-cols-2">
        {INSTANCE_TYPES.map((option) => {
          const optionId = `${id}-${option}`
          const disabled = option === 'curseforge' && !curseforgeKeySet
          return (
            <div key={option} className={cn('flex items-start gap-3 rounded-md border p-3', type === option && 'border-primary', disabled && 'opacity-60')}>
              <input
                id={optionId}
                type="radio"
                name={id}
                value={option}
                checked={type === option}
                disabled={disabled}
                aria-describedby={`${optionId}-description`}
                className="accent-primary mt-0.5 size-4"
                onChange={() => onChange(option)}
              />
              <div className="space-y-0.5">
                <Label htmlFor={optionId}>{t(`servers.type.${option}`)}</Label>
                <p id={`${optionId}-description`} className="text-muted-foreground text-sm">
                  {t(`servers.type_description.${option}`)}
                </p>
              </div>
            </div>
          )
        })}
      </div>
      {curseforgeKeySet ? null : <p className="text-muted-foreground text-sm">{t('servers.form.curseforge_key_hint')}</p>}
      {error ? (
        <p id={`${id}-error`} className="text-destructive text-sm">
          {t(error.key, error.params ?? {})}
        </p>
      ) : null}
    </fieldset>
  )
}

interface VersionFieldsProps {
  id: string
  value: ServerFormValue
  catalog: Catalog | undefined
  catalogPending: boolean
  catalogFailed: boolean
  shown: Shown
  update: Update
}

function VersionFields({ id, value, catalog, catalogPending, catalogFailed, shown, update }: VersionFieldsProps) {
  const { t } = useTranslation()
  if (isModpack(value.type)) {
    const modrinth = value.type === 'modrinth'
    return (
      <FieldRow
        id={`${id}-modpack`}
        label={modrinth ? t('servers.form.modpack_modrinth') : t('servers.form.modpack_curseforge')}
        hint={modrinth ? t('servers.form.modpack_modrinth_hint') : t('servers.form.modpack_curseforge_hint')}
        error={shown('modpack')}
      >
        {(c) => (
          <Input
            {...c}
            value={value.modpack}
            autoCapitalize="none"
            autoComplete="off"
            spellCheck={false}
            onChange={(e) => update({ modpack: e.target.value }, 'modpack')}
          />
        )}
      </FieldRow>
    )
  }

  const versions = catalog?.versions ?? []
  // A failed or empty catalog turns the version into free text (spec §6: 502 → Freitext).
  const listAvailable = !catalogFailed && (catalogPending || versions.length > 0)
  const current = effectiveVersion(value, catalog)
  const listed = current === 'LATEST' || versions.some((v) => v.id === current)
  const useList = listAvailable && !value.manualVersion && (catalogPending || value.version === '' || listed)
  let hint = t('servers.form.version_manual_hint')
  if (useList) hint = t('servers.form.version_hint')
  else if (!listAvailable) hint = t('servers.form.version_catalog_failed')
  const loaders = catalog?.loaders ?? []
  const loaderOptions = value.loaderVersion !== '' && !loaders.includes(value.loaderVersion) ? [value.loaderVersion, ...loaders] : loaders

  return (
    <div className="space-y-4">
      <div className="space-y-1">
        <FieldRow id={`${id}-version`} label={t('servers.form.version')} hint={hint} error={shown('version')}>
          {(c) =>
            useList ? (
              <NativeSelect
                {...c}
                className="w-full sm:w-64"
                value={catalogPending ? '' : current}
                disabled={catalogPending}
                onChange={(e) => update({ version: e.target.value }, 'version')}
              >
                {catalogPending ? <option value="">{t('servers.form.version_loading')}</option> : null}
                {versions.map((v) => (
                  <option key={v.id} value={v.id}>
                    {v.id}
                  </option>
                ))}
                <option value="LATEST">{t('servers.form.version_latest')}</option>
              </NativeSelect>
            ) : (
              <Input
                {...c}
                className="sm:w-64"
                value={value.version}
                autoComplete="off"
                spellCheck={false}
                placeholder="1.21.11"
                onChange={(e) => update({ version: e.target.value }, 'version')}
              />
            )
          }
        </FieldRow>
        {listAvailable && !catalogPending ? (
          <Button
            type="button"
            variant="link"
            size="sm"
            className="h-auto px-0"
            onClick={() => (useList ? update({ manualVersion: true, version: current }) : update({ manualVersion: false, version: '' }))}
          >
            {useList ? t('servers.form.version_manual') : t('servers.form.version_from_list')}
          </Button>
        ) : null}
      </div>
      {value.type === 'fabric' ? (
        <FieldRow id={`${id}-loader`} label={t('servers.form.fabric_loader')} error={shown('loader_version')}>
          {(c) => (
            <NativeSelect {...c} className="w-full sm:w-64" value={value.loaderVersion} onChange={(e) => update({ loaderVersion: e.target.value }, 'loader_version')}>
              <option value="">{t('servers.form.loader_latest')}</option>
              {loaderOptions.map((loader) => (
                <option key={loader} value={loader}>
                  {loader}
                </option>
              ))}
            </NativeSelect>
          )}
        </FieldRow>
      ) : null}
    </div>
  )
}

function MemoryField({ id, text, error, onChange }: { id: string; text: string; error: FieldError | undefined; onChange(text: string): void }) {
  const { t } = useTranslation()
  const mib = memoryMiBFromGiB(text)
  const slider = mib !== null && mib >= MIN_MEMORY_MIB && mib <= MAX_MEMORY_MIB ? mib / 1024 : DEFAULT_MEMORY_MIB / 1024
  return (
    <FieldRow id={id} label={t('servers.form.memory')} hint={t('servers.form.memory_hint')} error={error}>
      {(c) => (
        <div className="flex items-center gap-3">
          <input
            type="range"
            min={MIN_MEMORY_MIB / 1024}
            max={MAX_MEMORY_MIB / 1024}
            step={MEMORY_STEP_MIB / 1024}
            value={slider}
            aria-label={t('servers.form.memory_slider')}
            aria-valuetext={`${slider} GiB`}
            className="accent-primary h-2 min-w-0 flex-1"
            onChange={(e) => onChange(e.target.value)}
          />
          <Input
            {...c}
            type="number"
            inputMode="decimal"
            min={MIN_MEMORY_MIB / 1024}
            max={MAX_MEMORY_MIB / 1024}
            step={MEMORY_STEP_MIB / 1024}
            className="w-24"
            value={text}
            onChange={(e) => onChange(e.target.value)}
          />
        </div>
      )}
    </FieldRow>
  )
}

function AdvancedFields({
  id,
  value,
  catalog,
  shown,
  update,
}: {
  id: string
  value: ServerFormValue
  catalog: Catalog | undefined
  shown: Shown
  update: Update
}) {
  const { t } = useTranslation()
  const derived = effectiveJava({ ...value, javaOverride: '' }, catalog)
  return (
    <>
      <FieldRow id={`${id}-java`} label={t('servers.form.java')} hint={t('servers.form.java_hint')} error={shown('java')}>
        {(c) => (
          <NativeSelect
            {...c}
            className="w-full sm:w-64"
            value={value.javaOverride}
            onChange={(e) => update({ javaOverride: e.target.value as JavaVersion | '' }, 'java')}
          >
            <option value="">{t('servers.form.java_auto', { java: javaLabel(derived) })}</option>
            {JAVA_VERSIONS.map((java) => (
              <option key={java} value={java}>
                {javaLabel(java)}
              </option>
            ))}
          </NativeSelect>
        )}
      </FieldRow>
      {value.type === 'neoforge' ? (
        <FieldRow id={`${id}-neoforge`} label={t('servers.form.neoforge_version')} hint={t('servers.form.neoforge_version_hint')} error={shown('loader_version')}>
          {(c) => (
            <Input
              {...c}
              className="sm:w-64"
              value={value.loaderVersion}
              autoComplete="off"
              spellCheck={false}
              placeholder="21.11.38"
              onChange={(e) => update({ loaderVersion: e.target.value }, 'loader_version')}
            />
          )}
        </FieldRow>
      ) : null}
    </>
  )
}

function EulaField({ id, checked, error, onChange }: { id: string; checked: boolean; error: FieldError | undefined; onChange(checked: boolean): void }) {
  const { t } = useTranslation()
  return (
    <div className="space-y-1">
      <div className="flex items-start gap-2">
        <input
          id={id}
          type="checkbox"
          className="accent-primary mt-0.5 size-4"
          checked={checked}
          aria-invalid={error ? true : undefined}
          aria-describedby={`${id}-message`}
          onChange={(e) => onChange(e.target.checked)}
        />
        <Label htmlFor={id} className="leading-snug">
          {t('servers.form.eula')}
        </Label>
      </div>
      <p id={`${id}-message`} className={cn('text-sm', error ? 'text-destructive' : 'text-muted-foreground')}>
        {error ? t(error.key, error.params ?? {}) : t('servers.form.eula_hint')}{' '}
        <a href={EULA_URL} target="_blank" rel="noreferrer" className="text-primary underline underline-offset-4">
          {t('servers.form.eula_link')}
        </a>
      </p>
    </div>
  )
}
```

- [ ] **Step 9: `web/src/features/servers/create-server-page.tsx`**

```tsx
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { Server } from '@/api/types'
import { PageHeader } from '@/components/page-header'
import { errorMessage } from '@/lib/error-message'
import { createServer, serverSettingsQueryOptions, serversKey } from './api'
import { emptyServerForm, type ServerFormResult } from './form-model'
import { ServerForm } from './server-form'

export function CreateServerPage({ onCreated }: { onCreated(server: Server): void }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const settings = useQuery(serverSettingsQueryOptions)
  // Errors (409 base_domain_missing, 400 …) are rendered by the form; a cancelled sudo dialog stays silent.
  const create = useMutation({
    mutationFn: (result: ServerFormResult) => createServer({ ...result, eula_accepted: true }),
    onSuccess: (server) => {
      toast.success(t('servers.create.created', { name: server.name }))
      void queryClient.invalidateQueries({ queryKey: serversKey })
      onCreated(server)
    },
  })

  let body: ReactNode
  if (settings.isPending) {
    body = <p className="text-muted-foreground">{t('common.loading')}</p>
  } else if (settings.isError) {
    body = (
      <p role="alert" className="text-destructive">
        {errorMessage(settings.error, t)}
      </p>
    )
  } else {
    body = (
      <>
        {settings.data.base_domain === null ? (
          <div role="status" className="mb-6 max-w-3xl rounded-md border p-4 text-sm">
            <p className="font-medium">{t('servers.create.base_domain_missing_title')}</p>
            <p className="text-muted-foreground mt-1">{t('servers.create.base_domain_missing')}</p>
          </div>
        ) : null}
        <ServerForm
          mode="create"
          initial={emptyServerForm()}
          baseDomain={settings.data.base_domain}
          curseforgeKeySet={settings.data.curseforge_api_key_set}
          submitLabel={t('servers.create.submit')}
          pending={create.isPending}
          error={create.error}
          onSubmit={(result) => create.mutate(result)}
        />
      </>
    )
  }

  return (
    <>
      <PageHeader title={t('servers.create.title')} description={t('servers.create.description')} />
      {body}
    </>
  )
}
```

Run: `cd web && npx vitest run src/features/servers`
Expected: PASS (alle Dateien, `create-server-page.test.tsx` mit 9 Tests).

- [ ] **Step 10: Route `web/src/routes/_app/servers/new.tsx`**

```tsx
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { adminGuard } from '@/features/auth/guards'
import { guardBeforeLoad } from '@/features/auth/route-guards'
import { CreateServerPage } from '@/features/servers/create-server-page'

export const Route = createFileRoute('/_app/servers/new')({
  beforeLoad: guardBeforeLoad(adminGuard),
  component: NewServerRoute,
})

function NewServerRoute() {
  const navigate = useNavigate()
  // The dashboard shows the new server until the detail page exists (task 20 switches to /servers/$id).
  return <CreateServerPage onCreated={() => void navigate({ to: '/' })} />
}
```

Run: `cd web && npx vite build`
Expected: Build erfolgreich; `src/routeTree.gen.ts` enthält jetzt `AppServersNewRoute` mit Pfad `/servers/new`.

In `web/src/app/router.integration.test.tsx` im `describe`-Block ergänzen:

```tsx
  it('keeps regular users out of the create server page', async () => {
    const queryClient = createTestQueryClient()
    applyMe(queryClient, loginAs('alice'))
    const { router } = await mountApp('/servers/new', queryClient)
    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
  })
```

- [ ] **Step 11: Dashboard verlinkt die neue Seite, Dialog entfällt**

Test zuerst: in `web/src/features/dashboard/dashboard-page.test.tsx` den Test `lets admins open server creation when a base domain is set` vollständig ersetzen durch:

```tsx
  it('links admins to the create page when a base domain is set', async () => {
    const user = userEvent.setup()
    const { router } = await renderWithProviders(<DashboardPage />, { me: loginAs('admin') })
    const create = await screen.findByRole('link', { name: 'Server anlegen' })
    expect(create).toHaveAttribute('href', '/servers/new')
    expect(screen.queryByText('Keine Basisdomain festgelegt')).not.toBeInTheDocument()
    await user.click(create)
    await waitFor(() => expect(router.state.location.pathname).toBe('/servers/new'))
  })
```

Run: `cd web && npx vitest run src/features/dashboard`
Expected: FAIL – `Unable to find role="link" and name "Server anlegen"`.

`web/src/features/dashboard/dashboard-page.tsx` vollständig ersetzen:

```tsx
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { PageHeader } from '@/components/page-header'
import { Button } from '@/components/ui/button'
import { useMe } from '@/features/auth/me'
import { serverSettingsQueryOptions, serversQueryOptions } from '@/features/servers/api'
import { pollInterval } from '@/features/servers/status'
import { errorMessage } from '@/lib/error-message'
import { ServerCard } from './server-card'

export function DashboardPage() {
  const { t } = useTranslation()
  const me = useMe()
  const isAdmin = me?.role === 'admin'
  // 3 s while a server is changing state, 15 s otherwise (spec §9).
  const servers = useQuery({ ...serversQueryOptions, refetchInterval: (query) => pollInterval(query.state.data) })
  // Only admins may read the instance settings; without a base domain no server can be created.
  const settings = useQuery({ ...serverSettingsQueryOptions, enabled: isAdmin })
  const baseDomainMissing = isAdmin && settings.data?.base_domain === null

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

  let createAction: ReactNode
  if (isAdmin) {
    createAction =
      settings.isPending || baseDomainMissing ? (
        <Button type="button" disabled>
          {t('dashboard.create_server')}
        </Button>
      ) : (
        <Button asChild>
          <Link to="/servers/new">{t('dashboard.create_server')}</Link>
        </Button>
      )
  }

  return (
    <>
      <PageHeader title={t('dashboard.title')} description={t('dashboard.description')} actions={createAction} />
      {baseDomainMissing ? (
        <div role="status" className="mb-6 rounded-md border p-4 text-sm">
          <p className="font-medium">{t('dashboard.base_domain_missing_title')}</p>
          <p className="text-muted-foreground mt-1">{t('dashboard.base_domain_missing')}</p>
        </div>
      ) : null}
      {content}
    </>
  )
}
```

```bash
git rm -q web/src/features/dashboard/create-server-dialog.tsx
```

Run: `cd web && npx vitest run src/features/dashboard src/app`
Expected: PASS.

- [ ] **Step 12: Smoke-Test ohne alten Dialog**

`web/e2e/smoke.spec.ts`: den Test `sensitive actions ask for sudo once and then succeed` vollständig ersetzen (der alte Dialog existiert nicht mehr; Server deckt `servers.spec.ts` in Aufgabe 22 ab):

```ts
test('sensitive actions ask for sudo once and then succeed', async ({ page }) => {
  await signInAsAdmin(page)
  await page.getByRole('link', { name: 'Benutzer', exact: true }).click()
  await page.getByRole('button', { name: 'Benutzer anlegen' }).click()
  const dialog = page.getByRole('dialog', { name: 'Benutzer anlegen' })
  await dialog.getByLabel('Benutzername').fill('carol')
  await dialog.getByLabel('Initiales Passwort').fill('long-enough-password')
  await dialog.getByRole('button', { name: 'Anlegen' }).click()
  const sudo = page.getByRole('dialog', { name: 'Aktion bestätigen' })
  await sudo.getByLabel('Code aus der Authenticator-App').fill('123456')
  await sudo.getByRole('button', { name: 'Bestätigen' }).click()
  await expect(page.getByRole('row', { name: /carol/ })).toBeVisible()
  await expect(sudo).toBeHidden()
})
```

Kontrolle, dass keine Dialog-Texte übrig sind:

Run: `grep -rnE "dashboard\.(create_description|name_label|name_hint|name_invalid|created)|create-server-dialog|CreateServerDialog" web/src web/e2e`
Expected: keine Treffer.

- [ ] **Step 13: Gesamtprüfung**

Run: `cd web && npm run lint && npm run typecheck && npm test && npm run build`
Expected: alles grün; `git status --porcelain web/src/routeTree.gen.ts` zeigt die Datei als geändert (wird mit committet).

Run: `cd web && npm run e2e`
Expected: PASS (`smoke.spec.ts`, `totp.spec.ts`).

Run: `make test`
Expected: PASS.

- [ ] **Step 14: Commit**

```bash
git add -A web/src/features/servers web/src/features/dashboard web/src/routes/_app/servers web/src/routeTree.gen.ts \
  web/src/i18n/de.ts web/src/i18n/en.ts web/src/i18n/i18n.test.ts web/src/app/router.integration.test.tsx web/e2e/smoke.spec.ts
git commit -m "feat(web): add create server page with catalog-backed form" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 20: Detailseite `/servers/$id` – Übersicht, Einstellungen, Löschen

**Files:**
- Create: `web/src/features/servers/server-overview.tsx`, `web/src/features/servers/server-settings-tab.tsx`, `web/src/features/servers/delete-server-section.tsx`, `web/src/features/servers/server-detail-page.tsx`, `web/src/routes/_app/servers/$id.tsx`
- Modify: `web/src/routes/_app/servers/new.tsx`, `web/src/features/dashboard/server-card.tsx` (neu geschrieben), `web/src/features/dashboard/dashboard-page.tsx`, `web/src/features/dashboard/dashboard-page.test.tsx`, `web/src/i18n/de.ts`, `web/src/i18n/en.ts`, `web/src/i18n/i18n.test.ts`, `web/src/app/router.integration.test.tsx`, `web/src/routeTree.gen.ts` (generiert)
- Test: `web/src/features/servers/server-detail-page.test.tsx`

**Interfaces:**
- Consumes: Aufgabe 17 (`serverQueryOptions`, `serverKey`, `serversKey`, `updateServer`, `deleteServer`, `pollInterval`, `describeInstance`, `formatGiB`, `formatCpu`, `javaLabel`), Aufgabe 18 (`StatusBadge`, `AddressLine`, `PowerButtons`, `usePower`), Aufgabe 19 (`ServerForm`, `formFromInstance`, `ServerFormResult`); `SectionCard` (`@/features/profile/section-card`), shadcn `Tabs`; `useMe`, `useFormat`, `useErrorToast`, `isApiError`
- Produces:
```ts
export function ServerOverview(props: { server: Server; instance: InstanceConfig }): JSX.Element      // Banner „Neustart nötig“, Abschnitt „Zustand“, „Steuerung“
export function ServerSettingsTab(props: { server: Server; instance: InstanceConfig }): JSX.Element   // ServerForm mode="edit", PATCH
export function DeleteServerSection(props: { server: Server }): JSX.Element                           // Namen eintippen → DELETE → navigate('/')
export function ServerDetailPage(props: { serverId: number }): JSX.Element
export function ServerCard(props: { server: Server }): JSX.Element   // ohne Löschen, Name verlinkt /servers/$id
// Route: /servers/$id (Kind von /_app); nicht-numerische ID → 404-Seite. /servers/new navigiert nach dem Anlegen hierher.
```

- [ ] **Step 1: Failing Test `web/src/features/servers/server-detail-page.test.tsx`**

```tsx
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { db, loginAs } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { ServerDetailPage } from './server-detail-page'

const GIB = 1024 ** 3

function mockServer(id: number) {
  const server = db.servers.find((s) => s.id === id)
  if (!server) throw new Error(`mock server ${id} missing`)
  return server
}

describe('ServerDetailPage', () => {
  it('shows status, address, resources and power buttons on the overview', async () => {
    const survival = mockServer(1)
    survival.status = {
      ...survival.status,
      memory_bytes: 2 * GIB,
      memory_limit_bytes: 5 * GIB,
      cpu_percent: 12.5,
      started_at: Date.now() - 5 * 60_000,
    }
    await renderWithProviders(<ServerDetailPage serverId={1} />, { path: '/servers/1', me: loginAs('admin') })

    expect(await screen.findByRole('heading', { name: 'survival', level: 1 })).toBeInTheDocument()
    expect(screen.getAllByRole('tab').map((tab) => tab.textContent)).toEqual(['Übersicht', 'Einstellungen', 'Löschen'])
    const state = screen.getByRole('region', { name: 'Zustand' })
    expect(within(state).getByText('Läuft')).toBeInTheDocument()
    expect(within(state).getByText('survival.mc.example.com')).toBeInTheDocument()
    expect(within(state).getByText('Paper 1.21.11')).toBeInTheDocument()
    expect(within(state).getByText('Java 21')).toBeInTheDocument()
    expect(within(state).getByText('2 / 5 GiB')).toBeInTheDocument()
    expect(within(state).getByText('12,5 %')).toBeInTheDocument()
    expect(within(state).getByText('vor 5 Minuten')).toBeInTheDocument()
    expect(screen.getByRole('group', { name: 'Steuerung für survival' })).toBeInTheDocument()
    expect(screen.queryByText('Neustart nötig')).not.toBeInTheDocument()
  })

  it('shows the last error of a failed server', async () => {
    const survival = mockServer(1)
    survival.status = { ...survival.status, state: 'error', last_error: 'image pull failed: manifest unknown' }
    await renderWithProviders(<ServerDetailPage serverId={1} />, { me: loginAs('alice') })
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Letzter Fehler')
    expect(alert).toHaveTextContent('image pull failed: manifest unknown')
    expect(within(screen.getByRole('region', { name: 'Zustand' })).getByText('Fehler')).toBeInTheDocument()
  })

  it('offers a restart while changes are pending and clears the banner afterwards', async () => {
    const survival = mockServer(1)
    survival.revision += 1
    survival.status = { ...survival.status, restart_required: true }
    const user = userEvent.setup()
    await renderWithProviders(<ServerDetailPage serverId={1} />, { me: loginAs('alice') })
    expect(await screen.findByText('Neustart nötig')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Jetzt neu starten' }))
    expect(await screen.findByText('Neustart für „survival“ angefordert.')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByText('Neustart nötig')).not.toBeInTheDocument())
  })

  it('shows a restricted user only the overview', async () => {
    await renderWithProviders(<ServerDetailPage serverId={2} />, { me: loginAs('alice') })
    expect(await screen.findByRole('heading', { name: 'creative', level: 1 })).toBeInTheDocument()
    expect(screen.getAllByRole('tab').map((tab) => tab.textContent)).toEqual(['Übersicht'])
    expect(screen.queryByRole('group', { name: 'Steuerung für creative' })).not.toBeInTheDocument()
    expect(screen.getByText('Keine Berechtigung für Start/Stopp.')).toBeInTheDocument()
  })

  it('explains a server the user may not see', async () => {
    await renderWithProviders(<ServerDetailPage serverId={3} />, { me: loginAs('alice') })
    expect(await screen.findByText('Du hast keine Berechtigung, diesen Server anzusehen.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Zur Übersicht' })).toHaveAttribute('href', '/')
  })

  it('explains a server that does not exist', async () => {
    await renderWithProviders(<ServerDetailPage serverId={999} />, { me: loginAs('admin') })
    expect(await screen.findByText('Diesen Server gibt es nicht (mehr).')).toBeInTheDocument()
  })

  it('saves settings and points out the pending restart', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<ServerDetailPage serverId={1} />, { me: loginAs('admin', { sudo: true }) })
    await user.click(await screen.findByRole('tab', { name: 'Einstellungen' }))
    expect(screen.getByText('Der Server läuft. Änderungen werden erst nach einem Neustart aktiv.')).toBeInTheDocument()
    const save = screen.getByRole('button', { name: 'Änderungen speichern' })
    expect(save).toBeDisabled()
    await screen.findByRole('option', { name: '1.21.11' })
    expect(screen.getByRole('combobox', { name: 'Minecraft-Version' })).toHaveValue('1.21.11')
    expect(screen.queryByLabelText('Name')).not.toBeInTheDocument()
    expect(screen.queryByRole('radio')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Eigenschaften' }))
    const motd = screen.getByLabelText('Nachricht im Serverbrowser (MOTD)')
    expect(motd).toHaveValue('survival')
    await user.clear(motd)
    await user.type(motd, 'Willkommen')
    expect(save).toBeEnabled()
    await user.click(save)

    expect(await screen.findByText('Änderungen gespeichert. Sie werden beim nächsten Neustart aktiv.')).toBeInTheDocument()
    expect(mockServer(1).instance?.properties.motd).toBe('Willkommen')
    await user.click(screen.getByRole('tab', { name: 'Übersicht' }))
    expect(await screen.findByText('Neustart nötig')).toBeInTheDocument()
  })

  it('deletes the server after typing its name', async () => {
    const user = userEvent.setup()
    const { router } = await renderWithProviders(<ServerDetailPage serverId={1} />, { path: '/servers/1', me: loginAs('admin', { sudo: true }) })
    await user.click(await screen.findByRole('tab', { name: 'Löschen' }))
    expect(screen.getByText(/deleted\/survival-<Zeitpunkt>/)).toBeInTheDocument()
    const submit = screen.getByRole('button', { name: 'Server löschen' })
    expect(submit).toBeDisabled()
    await user.type(screen.getByLabelText('Zum Bestätigen den Namen „survival“ eintippen'), 'survival')
    expect(submit).toBeEnabled()
    await user.click(submit)
    expect(await screen.findByText('Server „survival“ gelöscht.')).toBeInTheDocument()
    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
    expect(db.servers.some((s) => s.id === 1)).toBe(false)
  })

  it('lets admins delete a server that was never set up', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<ServerDetailPage serverId={3} />, { path: '/servers/3', me: loginAs('admin', { sudo: true }) })
    expect(
      await screen.findByText('Dieser Server stammt aus der Zeit vor der Instanzverwaltung und hat noch keine Minecraft-Instanz. Du kannst ihn nur löschen.'),
    ).toBeInTheDocument()
    expect(screen.queryByRole('tab')).not.toBeInTheDocument()
    expect(screen.getByText('Der Eintrag und alle Rechte darauf werden entfernt. Serverdaten gibt es keine.')).toBeInTheDocument()
    await user.type(screen.getByLabelText('Zum Bestätigen den Namen „lobby“ eintippen'), 'lobby')
    await user.click(screen.getByRole('button', { name: 'Server löschen' }))
    expect(await screen.findByText('Server „lobby“ gelöscht.')).toBeInTheDocument()
  })

  it('shows no error after the user cancelled the sudo dialog', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<ServerDetailPage serverId={3} />, { me: loginAs('admin') })
    await user.type(await screen.findByLabelText('Zum Bestätigen den Namen „lobby“ eintippen'), 'lobby')
    await user.click(screen.getByRole('button', { name: 'Server löschen' }))
    const sudo = await screen.findByRole('dialog', { name: 'Aktion bestätigen' })
    await user.click(within(sudo).getByRole('button', { name: 'Abbrechen' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Aktion bestätigen' })).not.toBeInTheDocument())
    await new Promise((resolve) => setTimeout(resolve, 100))
    expect(screen.queryByText('Diese Aktion muss bestätigt werden.')).not.toBeInTheDocument()
    expect(db.servers.some((s) => s.id === 3)).toBe(true)
  })
})
```

Run: `cd web && npx vitest run src/features/servers/server-detail-page.test.tsx`
Expected: FAIL – `Failed to resolve import "./server-detail-page"`.

- [ ] **Step 2: Texte**

`web/src/i18n/de.ts`, im Block `servers` nach `level_type: { … },` einfügen:

```ts
    restart: {
      description: 'Geänderte Einstellungen werden erst nach einem Neustart aktiv. Spieler werden dabei kurz getrennt.',
      now: 'Jetzt neu starten',
    },
    operation: {
      pulling: 'Image wird geladen',
      creating: 'Container wird angelegt',
      starting: 'Wird gestartet',
      stopping: 'Wird gestoppt',
      removing: 'Wird entfernt',
    },
    memory_value: '{{used}} / {{limit}} GiB',
    cpu_value: '{{value}} %',
    detail: {
      tabs: {
        overview: 'Übersicht',
        settings: 'Einstellungen',
        delete: 'Löschen',
      },
      not_found: 'Diesen Server gibt es nicht (mehr).',
      forbidden: 'Du hast keine Berechtigung, diesen Server anzusehen.',
      not_configured: 'Dieser Server stammt aus der Zeit vor der Instanzverwaltung und hat noch keine Minecraft-Instanz. Du kannst ihn nur löschen.',
    },
    overview: {
      title: 'Zustand',
      status: 'Status',
      address: 'Adresse',
      type: 'Art',
      java: 'Java',
      memory: 'Arbeitsspeicher',
      cpu: 'CPU',
      started: 'Gestartet',
      last_error: 'Letzter Fehler',
      controls: 'Steuerung',
      unknown_hint: 'Der Agent antwortet gerade nicht. Angezeigt wird der zuletzt bekannte Soll-Zustand; Aktionen werden nachgeholt, sobald er wieder erreichbar ist.',
    },
    settings: {
      save: 'Änderungen speichern',
      saved: 'Änderungen gespeichert.',
      saved_restart: 'Änderungen gespeichert. Sie werden beim nächsten Neustart aktiv.',
      running_notice: 'Der Server läuft. Änderungen werden erst nach einem Neustart aktiv.',
    },
    delete: {
      title: 'Server löschen',
      description:
        'Der Server wird gestoppt und entfernt, ebenso alle Rechte darauf. Die Welt und alle Dateien verschiebt der Agent auf dem Host nach deleted/{{name}}-<Zeitpunkt> im Instanz-Verzeichnis (Standard: /srv/mc-panel/deleted/). Dort bleiben sie, bis du sie selbst löschst.',
      description_not_configured: 'Der Eintrag und alle Rechte darauf werden entfernt. Serverdaten gibt es keine.',
      confirm_label: 'Zum Bestätigen den Namen „{{name}}“ eintippen',
      submit: 'Server löschen',
      deleted: 'Server „{{name}}“ gelöscht.',
    },
```

`web/src/i18n/en.ts`, gleiche Stelle:

```ts
    restart: {
      description: 'Changed settings only take effect after a restart. Players are briefly disconnected.',
      now: 'Restart now',
    },
    operation: {
      pulling: 'Pulling image',
      creating: 'Creating container',
      starting: 'Starting',
      stopping: 'Stopping',
      removing: 'Removing',
    },
    memory_value: '{{used}} / {{limit}} GiB',
    cpu_value: '{{value}}%',
    detail: {
      tabs: {
        overview: 'Overview',
        settings: 'Settings',
        delete: 'Delete',
      },
      not_found: 'This server does not exist (anymore).',
      forbidden: 'You are not allowed to view this server.',
      not_configured: 'This server predates instance management and has no Minecraft instance yet. You can only delete it.',
    },
    overview: {
      title: 'State',
      status: 'Status',
      address: 'Address',
      type: 'Type',
      java: 'Java',
      memory: 'Memory',
      cpu: 'CPU',
      started: 'Started',
      last_error: 'Last error',
      controls: 'Controls',
      unknown_hint: 'The agent is not responding. Shown is the last known desired state; actions are applied once it is reachable again.',
    },
    settings: {
      save: 'Save changes',
      saved: 'Changes saved.',
      saved_restart: 'Changes saved. They take effect at the next restart.',
      running_notice: 'The server is running. Changes take effect after a restart.',
    },
    delete: {
      title: 'Delete server',
      description:
        'The server is stopped and removed, along with all permissions on it. The agent moves the world and all files on the host to deleted/{{name}}-<timestamp> in the instance directory (default: /srv/mc-panel/deleted/). They stay there until you delete them yourself.',
      description_not_configured: 'The entry and all permissions on it are removed. There is no server data.',
      confirm_label: 'Type “{{name}}” to confirm',
      submit: 'Delete server',
      deleted: 'Server “{{name}}” deleted.',
    },
```

In beiden Dateien im Block `dashboard` die Schlüssel `deleted`, `delete_for`, `delete_title` und `delete_description` entfernen (Löschen gibt es nur noch auf der Detailseite).

`web/src/i18n/i18n.test.ts`, am Ende von `DYNAMIC_KEYS` ergänzen:

```ts
  ...['pulling', 'creating', 'starting', 'stopping', 'removing'].map((o) => `servers.operation.${o}`),
```

- [ ] **Step 3: `web/src/features/servers/server-overview.tsx`**

```tsx
import { useTranslation } from 'react-i18next'
import type { InstanceConfig, Server } from '@/api/types'
import { Button } from '@/components/ui/button'
import { SectionCard } from '@/features/profile/section-card'
import { useFormat } from '@/lib/format'
import { AddressLine } from './address-line'
import { describeInstance, formatCpu, formatGiB, javaLabel } from './format'
import { PowerButtons } from './power-buttons'
import { StatusBadge } from './status-badge'
import { usePower } from './use-power'

export function ServerOverview({ server, instance }: { server: Server; instance: InstanceConfig }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const { status } = server
  const canPower = server.permissions.includes('server.power')
  const running = status.state === 'running'
  const showResources = running && status.memory_limit_bytes > 0

  return (
    <div className="space-y-4">
      {status.restart_required ? <RestartBanner server={server} canPower={canPower} /> : null}
      {status.state === 'unknown' ? <p className="text-muted-foreground text-sm">{t('servers.overview.unknown_hint')}</p> : null}
      <SectionCard title={t('servers.overview.title')}>
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] items-center gap-x-4 gap-y-2 text-sm">
          <dt className="text-muted-foreground">{t('servers.overview.status')}</dt>
          <dd className="flex flex-wrap items-center gap-2">
            <StatusBadge state={status.state} />
            {status.operation ? <span className="text-muted-foreground">{t(`servers.operation.${status.operation}`)}</span> : null}
          </dd>
          <dt className="text-muted-foreground">{t('servers.overview.address')}</dt>
          <dd className="min-w-0">
            <AddressLine address={server.address} />
          </dd>
          <dt className="text-muted-foreground">{t('servers.overview.type')}</dt>
          <dd>{describeInstance(instance, t)}</dd>
          <dt className="text-muted-foreground">{t('servers.overview.java')}</dt>
          <dd>{javaLabel(instance.java)}</dd>
          <dt className="text-muted-foreground">{t('servers.overview.memory')}</dt>
          <dd className="tabular-nums">
            {showResources
              ? t('servers.memory_value', { used: formatGiB(status.memory_bytes, fmt.locale), limit: formatGiB(status.memory_limit_bytes, fmt.locale) })
              : '—'}
          </dd>
          <dt className="text-muted-foreground">{t('servers.overview.cpu')}</dt>
          <dd className="tabular-nums">{showResources ? t('servers.cpu_value', { value: formatCpu(status.cpu_percent, fmt.locale) }) : '—'}</dd>
          <dt className="text-muted-foreground">{t('servers.overview.started')}</dt>
          <dd>{running && status.started_at > 0 ? fmt.relative(status.started_at) : '—'}</dd>
        </dl>
        {status.last_error ? (
          <div role="alert" className="border-destructive/50 mt-4 space-y-1 rounded-md border p-3 text-sm">
            <p className="font-medium">{t('servers.overview.last_error')}</p>
            <p className="font-mono break-words">{status.last_error}</p>
          </div>
        ) : null}
      </SectionCard>
      <SectionCard title={t('servers.overview.controls')}>
        {canPower ? <PowerButtons server={server} /> : <p className="text-muted-foreground text-sm">{t('servers.no_power')}</p>}
      </SectionCard>
    </div>
  )
}

function RestartBanner({ server, canPower }: { server: Server; canPower: boolean }) {
  const { t } = useTranslation()
  const power = usePower(server)
  return (
    <div role="status" className="flex flex-col gap-3 rounded-md border p-4 sm:flex-row sm:items-center sm:justify-between">
      <div className="space-y-1">
        <p className="font-medium">{t('servers.restart_required')}</p>
        <p className="text-muted-foreground text-sm">{t('servers.restart.description')}</p>
      </div>
      {canPower ? (
        <Button type="button" disabled={power.isPending} onClick={() => power.mutate('restart')}>
          {t('servers.restart.now')}
        </Button>
      ) : null}
    </div>
  )
}
```

- [ ] **Step 4: `web/src/features/servers/server-settings-tab.tsx`**

```tsx
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { InstanceConfig, Server, ServerState } from '@/api/types'
import { serverKey, serversKey, updateServer } from './api'
import { formFromInstance, type ServerFormResult } from './form-model'
import { ServerForm } from './server-form'

const RUNNING_STATES: ReadonlySet<ServerState> = new Set<ServerState>(['running', 'starting', 'restarting'])

export function ServerSettingsTab({ server, instance }: { server: Server; instance: InstanceConfig }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  // Errors are rendered by the form; a cancelled sudo dialog stays silent.
  const save = useMutation({
    // java is always sent: an omitted java would stay stale after a version change (V4: omitted = unchanged).
    mutationFn: (result: ServerFormResult) =>
      updateServer(server.id, {
        version: result.version,
        loader_version: result.loader_version,
        modpack: result.modpack,
        java: result.java,
        memory_mib: result.memory_mib,
        properties: result.properties,
      }),
    onSuccess: (updated) => {
      queryClient.setQueryData(serverKey(server.id), updated)
      void queryClient.invalidateQueries({ queryKey: serversKey, exact: true })
      toast.success(updated.status.restart_required ? t('servers.settings.saved_restart') : t('servers.settings.saved'))
    },
  })

  // A changed instance (after saving or by another admin) re-initialises the form via the key.
  return (
    <ServerForm
      key={JSON.stringify(instance)}
      mode="edit"
      initial={formFromInstance(server.name, instance)}
      submitLabel={t('servers.settings.save')}
      pending={save.isPending}
      error={save.error}
      notice={RUNNING_STATES.has(server.status.state) ? t('servers.settings.running_notice') : undefined}
      onSubmit={(result) => save.mutate(result)}
    />
  )
}
```

- [ ] **Step 5: `web/src/features/servers/delete-server-section.tsx`**

```tsx
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { Server } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { SectionCard } from '@/features/profile/section-card'
import { useErrorToast } from '@/lib/error-message'
import { deleteServer, serverKey, serversKey } from './api'

/** Deleting needs the server name typed in; the text names the deleted/ folder on the host (spec §3.3). */
export function DeleteServerSection({ server }: { server: Server }) {
  const { t } = useTranslation()
  const id = useId()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const [typed, setTyped] = useState('')
  const matches = typed.trim() === server.name

  const remove = useMutation({
    mutationFn: () => deleteServer(server.id),
    onSuccess: () => {
      toast.success(t('servers.delete.deleted', { name: server.name }))
      void navigate({ to: '/' })
      queryClient.removeQueries({ queryKey: serverKey(server.id), exact: true })
      void queryClient.invalidateQueries({ queryKey: serversKey, exact: true })
    },
    onError: notifyError,
  })

  return (
    <SectionCard title={t('servers.delete.title')} className="max-w-3xl">
      <form
        className="space-y-4"
        noValidate
        onSubmit={(e) => {
          e.preventDefault()
          if (matches) remove.mutate()
        }}
      >
        <p className="text-sm">
          {server.instance ? t('servers.delete.description', { name: server.name }) : t('servers.delete.description_not_configured')}
        </p>
        <div className="space-y-2">
          <Label htmlFor={`${id}-confirm`}>{t('servers.delete.confirm_label', { name: server.name })}</Label>
          <Input
            id={`${id}-confirm`}
            className="sm:w-80"
            value={typed}
            autoCapitalize="none"
            autoComplete="off"
            spellCheck={false}
            onChange={(e) => setTyped(e.target.value)}
          />
        </div>
        <Button type="submit" variant="destructive" disabled={!matches || remove.isPending}>
          {t('servers.delete.submit')}
        </Button>
      </form>
    </SectionCard>
  )
}
```

- [ ] **Step 6: `web/src/features/servers/server-detail-page.tsx`**

```tsx
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { isApiError } from '@/api/errors'
import { PageHeader } from '@/components/page-header'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useMe } from '@/features/auth/me'
import { errorMessage } from '@/lib/error-message'
import { serverQueryOptions } from './api'
import { DeleteServerSection } from './delete-server-section'
import { describeInstance } from './format'
import { ServerOverview } from './server-overview'
import { ServerSettingsTab } from './server-settings-tab'
import { pollInterval } from './status'
import { StatusBadge } from './status-badge'

export function ServerDetailPage({ serverId }: { serverId: number }) {
  const { t } = useTranslation()
  const me = useMe()
  const isAdmin = me?.role === 'admin'
  const query = useQuery({
    ...serverQueryOptions(serverId),
    // Fast while the server changes state; no polling at all after 403/404.
    refetchInterval: (q) => (q.state.data ? pollInterval([q.state.data]) : false),
  })

  if (query.isPending) return <p className="text-muted-foreground">{t('common.loading')}</p>
  if (!query.data) {
    let message = errorMessage(query.error, t)
    if (isApiError(query.error, 'not_found')) message = t('servers.detail.not_found')
    else if (isApiError(query.error, 'forbidden')) message = t('servers.detail.forbidden')
    return (
      <div className="space-y-3">
        <p role="alert" className="text-destructive">
          {message}
        </p>
        <Link to="/" className="text-primary underline underline-offset-4">
          {t('not_found.home')}
        </Link>
      </div>
    )
  }

  const server = query.data
  const header = (
    <PageHeader
      title={server.name}
      description={server.instance ? describeInstance(server.instance, t) : undefined}
      actions={<StatusBadge state={server.status.state} />}
    />
  )

  if (server.instance === null) {
    return (
      <>
        {header}
        <div className="max-w-3xl space-y-4">
          <p className="text-muted-foreground">{t('servers.detail.not_configured')}</p>
          {isAdmin ? <DeleteServerSection server={server} /> : null}
        </div>
      </>
    )
  }

  const instance = server.instance
  return (
    <>
      {header}
      <Tabs defaultValue="overview">
        <TabsList>
          <TabsTrigger value="overview">{t('servers.detail.tabs.overview')}</TabsTrigger>
          {isAdmin ? <TabsTrigger value="settings">{t('servers.detail.tabs.settings')}</TabsTrigger> : null}
          {isAdmin ? <TabsTrigger value="delete">{t('servers.detail.tabs.delete')}</TabsTrigger> : null}
        </TabsList>
        <TabsContent value="overview" className="pt-4">
          <ServerOverview server={server} instance={instance} />
        </TabsContent>
        {isAdmin ? (
          <TabsContent value="settings" className="pt-4">
            <ServerSettingsTab server={server} instance={instance} />
          </TabsContent>
        ) : null}
        {isAdmin ? (
          <TabsContent value="delete" className="pt-4">
            <DeleteServerSection server={server} />
          </TabsContent>
        ) : null}
      </Tabs>
    </>
  )
}
```

Run: `cd web && npx vitest run src/features/servers/server-detail-page.test.tsx`
Expected: PASS (10 Tests).

- [ ] **Step 7: Route `web/src/routes/_app/servers/$id.tsx`**

```tsx
import { createFileRoute, notFound } from '@tanstack/react-router'
import { ServerDetailPage } from '@/features/servers/server-detail-page'

export const Route = createFileRoute('/_app/servers/$id')({
  beforeLoad: ({ params }) => {
    // `/servers/abc` or `/servers/0` are no server ids – render the 404 page instead of firing requests.
    if (!/^[1-9]\d*$/.test(params.id)) throw notFound()
  },
  component: ServerRoute,
})

function ServerRoute() {
  const { id } = Route.useParams()
  return <ServerDetailPage serverId={Number(id)} />
}
```

`web/src/routes/_app/servers/new.tsx`: `NewServerRoute` ersetzen (nach dem Anlegen direkt auf die Detailseite):

```tsx
function NewServerRoute() {
  const navigate = useNavigate()
  return <CreateServerPage onCreated={(server) => void navigate({ to: '/servers/$id', params: { id: String(server.id) } })} />
}
```

In `web/src/app/router.integration.test.tsx` ergänzen:

```tsx
  it('renders the 404 page for a malformed server id', async () => {
    const queryClient = createTestQueryClient()
    applyMe(queryClient, loginAs('admin'))
    await mountApp('/servers/abc', queryClient)
    expect(await screen.findByText('Seite nicht gefunden')).toBeInTheDocument()
  })

  it('opens the detail page of a server', async () => {
    const queryClient = createTestQueryClient()
    applyMe(queryClient, loginAs('alice'))
    await mountApp('/servers/1', queryClient)
    expect(await screen.findByRole('heading', { name: 'survival', level: 1 })).toBeInTheDocument()
  })
```

Run: `cd web && npx vite build && npx vitest run src/app`
Expected: Build erfolgreich (`routeTree.gen.ts` enthält `/servers/$id`); Router-Tests PASS.

- [ ] **Step 8: Karte verlinkt die Detailseite, Löschen zieht um**

Test zuerst – in `web/src/features/dashboard/dashboard-page.test.tsx`:
- die Tests `shows no error toast after the user cancelled the sudo dialog` und `deletes a server after confirmation and sudo` löschen (beides prüft jetzt `server-detail-page.test.tsx`),
- die Import-Zeile `import { db, loginAs, MOCK_TOTP_CODE } from '@/mocks/db'` durch `import { db, loginAs } from '@/mocks/db'` ersetzen,
- diesen Test ergänzen:

```tsx
  it('links each card to its detail page and offers no delete button', async () => {
    await renderWithProviders(<DashboardPage />, { me: loginAs('admin') })
    const survival = await screen.findByRole('article', { name: 'survival' })
    expect(within(survival).getByRole('link', { name: 'survival' })).toHaveAttribute('href', '/servers/1')
    expect(screen.queryByRole('button', { name: 'lobby löschen' })).not.toBeInTheDocument()
  })
```

Run: `cd web && npx vitest run src/features/dashboard`
Expected: FAIL – `Unable to find role="link" and name "survival"`.

`web/src/features/dashboard/server-card.tsx` vollständig ersetzen:

```tsx
import { Link } from '@tanstack/react-router'
import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import type { Server } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardDescription, CardHeader } from '@/components/ui/card'
import { AddressLine } from '@/features/servers/address-line'
import { describeInstance, formatGiB } from '@/features/servers/format'
import { PowerButtons } from '@/features/servers/power-buttons'
import { StatusBadge } from '@/features/servers/status-badge'
import { useFormat } from '@/lib/format'

export function ServerCard({ server }: { server: Server }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const headingId = useId()
  const { instance, status } = server
  const canPower = server.permissions.includes('server.power')
  const showMemory = status.state === 'running' && status.memory_limit_bytes > 0

  return (
    <article aria-labelledby={headingId} className="h-full">
      <Card className="h-full">
        <CardHeader>
          <div className="flex items-start justify-between gap-2">
            <h2 id={headingId} className="min-w-0 text-lg font-semibold break-words">
              <Link
                to="/servers/$id"
                params={{ id: String(server.id) }}
                className="focus-visible:ring-ring rounded-sm outline-none hover:underline focus-visible:ring-2"
              >
                {server.name}
              </Link>
            </h2>
            <StatusBadge state={status.state} />
          </div>
          <CardDescription>{instance ? describeInstance(instance, t) : t('servers.not_configured_hint')}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          {instance ? <AddressLine address={server.address} /> : null}
          {showMemory ? (
            <p className="text-muted-foreground text-sm tabular-nums">
              {t('servers.memory_usage', { used: formatGiB(status.memory_bytes, fmt.locale), limit: formatGiB(status.memory_limit_bytes, fmt.locale) })}
            </p>
          ) : null}
          {status.restart_required ? <Badge variant="outline">{t('servers.restart_required')}</Badge> : null}
          {instance && canPower ? <PowerButtons server={server} /> : null}
          {instance && !canPower ? <p className="text-muted-foreground text-sm">{t('servers.no_power')}</p> : null}
        </CardContent>
      </Card>
    </article>
  )
}
```

In `web/src/features/dashboard/dashboard-page.tsx` die Zeile

```tsx
            <ServerCard server={server} canDelete={isAdmin} />
```

ersetzen durch

```tsx
            <ServerCard server={server} />
```

Run: `cd web && npx vitest run src/features/dashboard src/features/servers src/i18n`
Expected: PASS.

- [ ] **Step 9: Gesamtprüfung**

Run: `cd web && npm run lint && npm run typecheck && npm test && npm run build`
Expected: alles grün.

Run: `cd web && npm run e2e`
Expected: PASS.

Run: `make test`
Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add -A web/src/features/servers web/src/features/dashboard web/src/routes/_app/servers web/src/routeTree.gen.ts \
  web/src/i18n/de.ts web/src/i18n/en.ts web/src/i18n/i18n.test.ts web/src/app/router.integration.test.tsx
git commit -m "feat(web): add server detail page with overview, settings and delete" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 21: Einstellungsseite `/settings` – Basisdomain, CurseForge-Key, Docker-Status

**Files:**
- Create: `web/src/features/settings/settings-card.tsx`, `web/src/features/settings/base-domain-card.tsx`, `web/src/features/settings/curseforge-card.tsx`, `web/src/features/settings/instance-status-card.tsx`, `web/src/features/settings/settings-page.tsx`, `web/src/routes/_app/settings.tsx`
- Modify: `web/src/features/layout/nav.ts`, `web/src/features/layout/app-layout.test.tsx`, `web/src/features/dashboard/dashboard-page.tsx`, `web/src/features/dashboard/dashboard-page.test.tsx`, `web/src/features/servers/create-server-page.tsx`, `web/src/features/servers/create-server-page.test.tsx`, `web/src/features/servers/server-form.tsx`, `web/src/i18n/de.ts`, `web/src/i18n/en.ts`, `web/src/i18n/i18n.test.ts`, `web/src/app/router.integration.test.tsx`, `web/src/routeTree.gen.ts` (generiert)
- Test: `web/src/features/settings/settings-page.test.tsx`

**Interfaces:**
- Consumes: Aufgabe 17 (`serverSettingsQueryOptions`, `serverSettingsKey`, `serversKey`, `updateServerSettings`, `normalizeBaseDomain`, `validateBaseDomain`, `validateCurseForgeKey`), Aufgabe 19 (`FieldRow`); `ConfirmDialog`, `PageHeader`, shadcn `Card`, `Badge`, `Button`, `Input`; `adminGuard`, `guardBeforeLoad`
- Produces:
```ts
export function SettingsCard(props: { title: string; description?: string; actions?: ReactNode; children: ReactNode }): JSX.Element // <section> mit h3
export function BaseDomainCard(props: { settings: ServerSettings }): JSX.Element
export function CurseForgeCard(props: { settings: ServerSettings }): JSX.Element
export function InstanceStatusCard(props: { settings: ServerSettings }): JSX.Element
export function SettingsPage(): JSX.Element
export type NavPath = '/' | '/users' | '/security' | '/audit' | '/settings' | '/profile'
// Route: /settings (Kind von /_app, adminGuard); Navigationspunkt „Einstellungen“ (lucide Settings, adminOnly)
```

- [ ] **Step 1: Failing Test `web/src/features/settings/settings-page.test.tsx`**

```tsx
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { ServerSettings } from '@/api/types'
import { db, loginAs, MOCK_TOTP_CODE } from '@/mocks/db'
import { server } from '@/mocks/node'
import { renderWithProviders } from '@/test/render'
import { SettingsPage } from './settings-page'

describe('SettingsPage', () => {
  it('saves a new base domain after sudo and shows the DNS record to create', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<SettingsPage />, { me: loginAs('admin') })
    expect(await screen.findByRole('heading', { name: 'Minecraft-Server', level: 2 })).toBeInTheDocument()
    const input = screen.getByLabelText('Basisdomain')
    expect(input).toHaveValue('mc.example.com')
    expect(screen.getByText('*.mc.example.com')).toBeInTheDocument()
    const save = screen.getByRole('button', { name: 'Basisdomain speichern' })
    expect(save).toBeDisabled()

    await user.clear(input)
    await user.type(input, 'Play.Example.org')
    expect(screen.getByText('*.play.example.org')).toBeInTheDocument()
    await user.click(save)
    const sudo = await screen.findByRole('dialog', { name: 'Aktion bestätigen' })
    await user.type(within(sudo).getByLabelText('Code aus der Authenticator-App'), MOCK_TOTP_CODE)
    await user.click(within(sudo).getByRole('button', { name: 'Bestätigen' }))

    expect(await screen.findByText('Basisdomain gespeichert.')).toBeInTheDocument()
    expect(db.serverSettings.base_domain).toBe('play.example.org')
    expect(screen.getByLabelText('Basisdomain')).toHaveValue('play.example.org')
  })

  it('rejects an invalid base domain before sending', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<SettingsPage />, { me: loginAs('admin', { sudo: true }) })
    const input = await screen.findByLabelText('Basisdomain')
    await user.clear(input)
    await user.type(input, 'localhost')
    expect(
      screen.getByText('Ungültige Domain. Beispiel: mc.example.com (mindestens zwei Teile, nur Kleinbuchstaben, Ziffern und Bindestrich).'),
    ).toBeInTheDocument()
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(screen.getByRole('button', { name: 'Basisdomain speichern' })).toBeDisabled()
    expect(db.serverSettings.base_domain).toBe('mc.example.com')
  })

  it('stores and removes the CurseForge key without ever showing it', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<SettingsPage />, { me: loginAs('admin', { sudo: true }) })
    expect(await screen.findByText('Nicht hinterlegt')).toBeInTheDocument()
    const key = screen.getByLabelText('API-Key')
    expect(key).toHaveAttribute('type', 'password')
    await user.type(key, '$2a$10$abcdef')
    await user.click(screen.getByRole('button', { name: 'Key speichern' }))

    expect(await screen.findByText('CurseForge-Key gespeichert.')).toBeInTheDocument()
    expect(screen.getByText('Hinterlegt')).toBeInTheDocument()
    expect(db.serverSettings.curseforge_api_key_set).toBe(true)
    expect(screen.getByLabelText('Neuer API-Key')).toHaveValue('')
    expect(document.body).not.toHaveTextContent('$2a$10$abcdef')

    await user.click(screen.getByRole('button', { name: 'Key entfernen' }))
    const confirm = screen.getByRole('dialog', { name: 'CurseForge-Key entfernen?' })
    await user.click(within(confirm).getByRole('button', { name: 'Key entfernen' }))
    expect(await screen.findByText('CurseForge-Key entfernt.')).toBeInTheDocument()
    expect(screen.getByText('Nicht hinterlegt')).toBeInTheDocument()
    expect(db.serverSettings.curseforge_api_key_set).toBe(false)
  })

  it('shows the docker status and unknown containers', async () => {
    server.use(
      http.get('*/api/servers/settings', () =>
        HttpResponse.json<ServerSettings>({
          base_domain: 'mc.example.com',
          curseforge_api_key_set: false,
          docker: 'unavailable',
          unknown_containers: [{ id: 7, name: 'mcpanel-mc-7' }],
        }),
      ),
    )
    await renderWithProviders(<SettingsPage />, { me: loginAs('admin') })
    const status = await screen.findByRole('region', { name: 'Status' })
    expect(within(status).getByText('Nicht erreichbar')).toBeInTheDocument()
    expect(
      within(status).getByText('Der Agent erreicht Docker nicht. Prüfe auf dem Host, ob Docker läuft (systemctl status docker).'),
    ).toBeInTheDocument()
    expect(within(status).getByText('mcpanel-mc-7 (ID 7)')).toBeInTheDocument()
  })

  it('reports an empty list of unknown containers', async () => {
    await renderWithProviders(<SettingsPage />, { me: loginAs('admin') })
    const status = await screen.findByRole('region', { name: 'Status' })
    expect(within(status).getAllByText('Erreichbar')).toHaveLength(2)
    expect(within(status).getByText('Keine.')).toBeInTheDocument()
  })
})
```

Run: `cd web && npx vitest run src/features/settings`
Expected: FAIL – `Failed to resolve import "./settings-page"`.

- [ ] **Step 2: Texte**

`web/src/i18n/de.ts`: im Block `nav` nach `audit: 'Audit-Log',` die Zeile `settings: 'Einstellungen',` ergänzen. Neuer Top-Level-Block direkt nach `servers: { … },`:

```ts
  settings: {
    title: 'Einstellungen',
    description: 'Einstellungen für das ganze Panel.',
    link: 'Zu den Einstellungen',
    minecraft: {
      title: 'Minecraft-Server',
    },
    base_domain: {
      title: 'Basisdomain',
      description: 'Jeder Server ist unter <name>.<basisdomain> erreichbar.',
      label: 'Basisdomain',
      dns_hint: 'Lege beim DNS-Anbieter einen Wildcard-Eintrag an, der auf die IP-Adresse dieses Servers zeigt (A- bzw. AAAA-Eintrag):',
      dns_target: 'IP-Adresse dieses Servers',
      change_hint: 'Eine Änderung gilt für alle Server. Laufende Server bleiben bis zu ihrem nächsten Neustart unter der alten Adresse erreichbar.',
      save: 'Basisdomain speichern',
      saved: 'Basisdomain gespeichert.',
    },
    curseforge: {
      title: 'CurseForge-API-Key',
      description: 'Nötig für Modpacks von CurseForge.',
      set: 'Hinterlegt',
      not_set: 'Nicht hinterlegt',
      label: 'API-Key',
      label_replace: 'Neuer API-Key',
      hint: 'Den Key erzeugst du in der CurseForge-Konsole. mc-panel speichert ihn verschlüsselt und zeigt ihn nie wieder an.',
      console_link: 'CurseForge-Konsole öffnen (neuer Tab)',
      save: 'Key speichern',
      saved: 'CurseForge-Key gespeichert.',
      remove: 'Key entfernen',
      remove_title: 'CurseForge-Key entfernen?',
      remove_description: 'Danach lassen sich keine CurseForge-Modpacks mehr anlegen.',
      removed: 'CurseForge-Key entfernt.',
    },
    status: {
      title: 'Status',
      agent: 'Agent',
      docker: 'Docker',
      reachable: 'Erreichbar',
      unreachable: 'Nicht erreichbar',
      unavailable_hint: 'Der Agent erreicht Docker nicht. Prüfe auf dem Host, ob Docker läuft (systemctl status docker).',
      unknown_hint: 'Der Agent antwortet nicht. Änderungen werden gespeichert und angewendet, sobald er wieder erreichbar ist.',
      unknown_containers: 'Unbekannte Container',
      unknown_containers_hint:
        'Diese Container tragen ein mc-panel-Label, gehören aber zu keinem Server. mc-panel entfernt sie nicht automatisch; lösche sie bei Bedarf auf dem Host mit docker rm -f <Name>.',
      unknown_containers_none: 'Keine.',
      container: '{{name}} (ID {{id}})',
    },
    docker: {
      ok: 'Erreichbar',
      unavailable: 'Nicht erreichbar',
      unknown: 'Unbekannt',
    },
  },
```

`web/src/i18n/en.ts`: im Block `nav` nach `audit` die Zeile `settings: 'Settings',` ergänzen. Neuer Top-Level-Block nach `servers`:

```ts
  settings: {
    title: 'Settings',
    description: 'Settings for the whole panel.',
    link: 'Go to settings',
    minecraft: {
      title: 'Minecraft servers',
    },
    base_domain: {
      title: 'Base domain',
      description: 'Every server is reachable at <name>.<base domain>.',
      label: 'Base domain',
      dns_hint: 'Create a wildcard record with your DNS provider that points to the IP address of this server (A or AAAA record):',
      dns_target: 'IP address of this server',
      change_hint: 'A change applies to all servers. Running servers stay reachable at the old address until their next restart.',
      save: 'Save base domain',
      saved: 'Base domain saved.',
    },
    curseforge: {
      title: 'CurseForge API key',
      description: 'Required for modpacks from CurseForge.',
      set: 'Stored',
      not_set: 'Not stored',
      label: 'API key',
      label_replace: 'New API key',
      hint: 'Create the key in the CurseForge console. mc-panel stores it encrypted and never shows it again.',
      console_link: 'Open the CurseForge console (new tab)',
      save: 'Save key',
      saved: 'CurseForge key saved.',
      remove: 'Remove key',
      remove_title: 'Remove CurseForge key?',
      remove_description: 'Afterwards no CurseForge modpacks can be created.',
      removed: 'CurseForge key removed.',
    },
    status: {
      title: 'Status',
      agent: 'Agent',
      docker: 'Docker',
      reachable: 'Reachable',
      unreachable: 'Unreachable',
      unavailable_hint: 'The agent cannot reach Docker. Check on the host whether Docker is running (systemctl status docker).',
      unknown_hint: 'The agent is not responding. Changes are saved and applied once it is reachable again.',
      unknown_containers: 'Unknown containers',
      unknown_containers_hint:
        'These containers carry an mc-panel label but belong to no server. mc-panel does not remove them automatically; delete them on the host with docker rm -f <name> if needed.',
      unknown_containers_none: 'None.',
      container: '{{name}} (ID {{id}})',
    },
    docker: {
      ok: 'Reachable',
      unavailable: 'Unreachable',
      unknown: 'Unknown',
    },
  },
```

`web/src/i18n/i18n.test.ts`, in `DYNAMIC_KEYS` die Zeile

```ts
  ...['dashboard', 'users', 'security', 'audit', 'profile'].map((k) => `nav.${k}`),
```

ersetzen durch

```ts
  ...['dashboard', 'users', 'security', 'audit', 'settings', 'profile'].map((k) => `nav.${k}`),
  ...['ok', 'unavailable', 'unknown'].map((d) => `settings.docker.${d}`),
```

- [ ] **Step 3: `web/src/features/settings/settings-card.tsx`**

```tsx
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
```

- [ ] **Step 4: `web/src/features/settings/base-domain-card.tsx`**

```tsx
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { ServerSettings } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { serverSettingsKey, serversKey, updateServerSettings } from '@/features/servers/api'
import { FieldRow } from '@/features/servers/field-row'
import { normalizeBaseDomain, validateBaseDomain } from '@/features/servers/validation'
import { useErrorToast } from '@/lib/error-message'
import { SettingsCard } from './settings-card'

const EXAMPLE_DOMAIN = 'mc.example.com'

export function BaseDomainCard({ settings }: { settings: ServerSettings }) {
  const { t } = useTranslation()
  const id = useId()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  // null = untouched; the input shows the stored value until the admin edits it.
  const [draft, setDraft] = useState<string | null>(null)
  const text = draft ?? settings.base_domain ?? ''
  const normalized = normalizeBaseDomain(text)
  const error = draft === null ? null : validateBaseDomain(normalized)
  const unchanged = normalized === (settings.base_domain ?? '')
  const example = error === null && normalized !== '' ? normalized : EXAMPLE_DOMAIN

  const save = useMutation({
    mutationFn: (baseDomain: string) => updateServerSettings({ base_domain: baseDomain }),
    onSuccess: (result) => {
      queryClient.setQueryData(serverSettingsKey, result)
      // Addresses and restart hints of all servers change with the base domain (spec §8).
      void queryClient.invalidateQueries({ queryKey: serversKey })
      setDraft(null)
      toast.success(t('settings.base_domain.saved'))
    },
    onError: notifyError,
  })

  return (
    <SettingsCard title={t('settings.base_domain.title')} description={t('settings.base_domain.description')}>
      <form
        className="space-y-4"
        noValidate
        onSubmit={(e) => {
          e.preventDefault()
          if (error === null && !unchanged) save.mutate(normalized)
        }}
      >
        <FieldRow id={`${id}-domain`} label={t('settings.base_domain.label')} error={error ?? undefined}>
          {(c) => (
            <Input
              {...c}
              value={text}
              placeholder={EXAMPLE_DOMAIN}
              autoCapitalize="none"
              autoComplete="off"
              spellCheck={false}
              onChange={(e) => setDraft(e.target.value)}
            />
          )}
        </FieldRow>
        <div className="space-y-1">
          <p className="text-muted-foreground">{t('settings.base_domain.dns_hint')}</p>
          <p className="font-mono break-all">
            <code>{`*.${example}`}</code> → {t('settings.base_domain.dns_target')}
          </p>
        </div>
        {settings.base_domain !== null ? <p className="text-muted-foreground">{t('settings.base_domain.change_hint')}</p> : null}
        <Button type="submit" disabled={draft === null || error !== null || unchanged || save.isPending}>
          {t('settings.base_domain.save')}
        </Button>
      </form>
    </SettingsCard>
  )
}
```

- [ ] **Step 5: `web/src/features/settings/curseforge-card.tsx`**

```tsx
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { ServerSettings } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { serverSettingsKey, updateServerSettings } from '@/features/servers/api'
import { FieldRow } from '@/features/servers/field-row'
import { validateCurseForgeKey } from '@/features/servers/validation'
import { useErrorToast } from '@/lib/error-message'
import { SettingsCard } from './settings-card'

const CONSOLE_URL = 'https://console.curseforge.com/'

/** Write-only: the panel only reports whether a key is stored, never the key itself. */
export function CurseForgeCard({ settings }: { settings: ServerSettings }) {
  const { t } = useTranslation()
  const id = useId()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const [key, setKey] = useState('')
  const [confirmRemove, setConfirmRemove] = useState(false)
  const error = key === '' ? null : validateCurseForgeKey(key)
  const stored = settings.curseforge_api_key_set

  const save = useMutation({
    mutationFn: (value: string | null) => updateServerSettings({ curseforge_api_key: value }),
    onSuccess: (result, value) => {
      queryClient.setQueryData(serverSettingsKey, result)
      setKey('')
      setConfirmRemove(false)
      toast.success(value === null ? t('settings.curseforge.removed') : t('settings.curseforge.saved'))
    },
    onError: notifyError,
  })

  return (
    <SettingsCard
      title={t('settings.curseforge.title')}
      description={t('settings.curseforge.description')}
      actions={<Badge variant={stored ? 'secondary' : 'outline'}>{stored ? t('settings.curseforge.set') : t('settings.curseforge.not_set')}</Badge>}
    >
      <form
        className="space-y-4"
        noValidate
        onSubmit={(e) => {
          e.preventDefault()
          if (key !== '' && error === null) save.mutate(key)
        }}
      >
        <FieldRow
          id={`${id}-key`}
          label={stored ? t('settings.curseforge.label_replace') : t('settings.curseforge.label')}
          hint={t('settings.curseforge.hint')}
          error={error ?? undefined}
        >
          {(c) => <Input {...c} type="password" value={key} autoComplete="off" spellCheck={false} onChange={(e) => setKey(e.target.value)} />}
        </FieldRow>
        <a href={CONSOLE_URL} target="_blank" rel="noreferrer" className="text-primary inline-block underline underline-offset-4">
          {t('settings.curseforge.console_link')}
        </a>
        <div className="flex flex-wrap gap-2">
          <Button type="submit" disabled={key === '' || error !== null || save.isPending}>
            {t('settings.curseforge.save')}
          </Button>
          {stored ? (
            <Button type="button" variant="outline" disabled={save.isPending} onClick={() => setConfirmRemove(true)}>
              {t('settings.curseforge.remove')}
            </Button>
          ) : null}
        </div>
      </form>
      <ConfirmDialog
        open={confirmRemove}
        onOpenChange={setConfirmRemove}
        title={t('settings.curseforge.remove_title')}
        description={t('settings.curseforge.remove_description')}
        confirmLabel={t('settings.curseforge.remove')}
        destructive
        pending={save.isPending}
        onConfirm={() => save.mutate(null)}
      />
    </SettingsCard>
  )
}
```

- [ ] **Step 6: `web/src/features/settings/instance-status-card.tsx`**

```tsx
import { useTranslation } from 'react-i18next'
import type { ServerSettings } from '@/api/types'
import { SettingsCard } from './settings-card'

/** docker "unknown" means the panel got no answer from the agent at all. */
export function InstanceStatusCard({ settings }: { settings: ServerSettings }) {
  const { t } = useTranslation()
  const agentReachable = settings.docker !== 'unknown'
  return (
    <SettingsCard title={t('settings.status.title')}>
      <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
        <dt className="text-muted-foreground">{t('settings.status.agent')}</dt>
        <dd>{agentReachable ? t('settings.status.reachable') : t('settings.status.unreachable')}</dd>
        <dt className="text-muted-foreground">{t('settings.status.docker')}</dt>
        <dd>{t(`settings.docker.${settings.docker}`)}</dd>
      </dl>
      {settings.docker === 'unavailable' ? <p className="text-muted-foreground">{t('settings.status.unavailable_hint')}</p> : null}
      {settings.docker === 'unknown' ? <p className="text-muted-foreground">{t('settings.status.unknown_hint')}</p> : null}
      <div className="space-y-2">
        <h4 className="font-medium">{t('settings.status.unknown_containers')}</h4>
        {settings.unknown_containers.length === 0 ? (
          <p className="text-muted-foreground">{t('settings.status.unknown_containers_none')}</p>
        ) : (
          <>
            <ul className="list-disc space-y-1 pl-5 font-mono">
              {settings.unknown_containers.map((c) => (
                <li key={c.id}>{t('settings.status.container', { name: c.name, id: c.id })}</li>
              ))}
            </ul>
            <p className="text-muted-foreground">{t('settings.status.unknown_containers_hint')}</p>
          </>
        )}
      </div>
    </SettingsCard>
  )
}
```

- [ ] **Step 7: `web/src/features/settings/settings-page.tsx`**

```tsx
import { useQuery } from '@tanstack/react-query'
import { useId, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { PageHeader } from '@/components/page-header'
import { serverSettingsQueryOptions } from '@/features/servers/api'
import { errorMessage } from '@/lib/error-message'
import { BaseDomainCard } from './base-domain-card'
import { CurseForgeCard } from './curseforge-card'
import { InstanceStatusCard } from './instance-status-card'

export function SettingsPage() {
  const { t } = useTranslation()
  const headingId = useId()
  const settings = useQuery(serverSettingsQueryOptions)

  let content: ReactNode
  if (settings.isPending) {
    content = <p className="text-muted-foreground">{t('common.loading')}</p>
  } else if (settings.isError) {
    content = (
      <p role="alert" className="text-destructive">
        {errorMessage(settings.error, t)}
      </p>
    )
  } else {
    content = (
      <div className="grid gap-4 lg:grid-cols-2">
        <BaseDomainCard settings={settings.data} />
        <CurseForgeCard settings={settings.data} />
        <InstanceStatusCard settings={settings.data} />
      </div>
    )
  }

  return (
    <>
      <PageHeader title={t('settings.title')} description={t('settings.description')} />
      <section aria-labelledby={headingId} className="space-y-4">
        <h2 id={headingId} className="text-lg font-semibold">
          {t('settings.minecraft.title')}
        </h2>
        {content}
      </section>
    </>
  )
}
```

Run: `cd web && npx vitest run src/features/settings src/i18n`
Expected: PASS (Settings 5 Tests, i18n grün).

- [ ] **Step 8: Route und Navigation**

`web/src/routes/_app/settings.tsx`:

```tsx
import { createFileRoute } from '@tanstack/react-router'
import { adminGuard } from '@/features/auth/guards'
import { guardBeforeLoad } from '@/features/auth/route-guards'
import { SettingsPage } from '@/features/settings/settings-page'

export const Route = createFileRoute('/_app/settings')({
  beforeLoad: guardBeforeLoad(adminGuard),
  component: SettingsPage,
})
```

Test zuerst – in `web/src/features/layout/app-layout.test.tsx` im Test `shows the full navigation to admins on desktop` die erwartete Liste ersetzen durch:

```tsx
    expect(within(nav).getAllByRole('link').map((l) => l.textContent)).toEqual([
      'Übersicht',
      'Benutzer',
      'Security Center',
      'Audit-Log',
      'Einstellungen',
      'Profil',
    ])
```

(Der Test `hides admin areas from regular users` erwartet weiter `['Übersicht', 'Profil']`.)

In `web/src/app/router.integration.test.tsx` ergänzen:

```tsx
  it('keeps regular users out of the settings page', async () => {
    const queryClient = createTestQueryClient()
    applyMe(queryClient, loginAs('alice'))
    const { router } = await mountApp('/settings', queryClient)
    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
  })
```

Run: `cd web && npx vitest run src/features/layout`
Expected: FAIL – Liste ohne `Einstellungen`.

`web/src/features/layout/nav.ts` vollständig ersetzen:

```ts
import { LayoutDashboard, ScrollText, Settings, ShieldCheck, UserRound, Users, type LucideIcon } from 'lucide-react'
import type { Role } from '@/api/types'

export type NavPath = '/' | '/users' | '/security' | '/audit' | '/settings' | '/profile'

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
  { to: '/settings', labelKey: 'nav.settings', icon: Settings, adminOnly: true },
  { to: '/profile', labelKey: 'nav.profile', icon: UserRound, adminOnly: false },
]

export function navItemsFor(role: Role): NavItem[] {
  return NAV_ITEMS.filter((item) => !item.adminOnly || role === 'admin')
}
```

Run: `cd web && npx vite build && npx vitest run src/features/layout src/app`
Expected: Build erfolgreich (`routeTree.gen.ts` enthält `/settings`); Tests PASS.

- [ ] **Step 9: Links auf `/settings` nachziehen**

Tests zuerst:

In `web/src/features/dashboard/dashboard-page.test.tsx`, Test `blocks server creation for admins until a base domain is set`, nach der `toBeDisabled()`-Zeile ergänzen:

```tsx
    expect(screen.getByRole('link', { name: 'Zu den Einstellungen' })).toHaveAttribute('href', '/settings')
```

In `web/src/features/servers/create-server-page.test.tsx`:
- im Test `explains a missing base domain and shows the 409 from the panel` nach der ersten `expect`-Zeile ergänzen:

```tsx
    expect(screen.getAllByRole('link', { name: 'Zu den Einstellungen' })[0]).toHaveAttribute('href', '/settings')
```

- im Test `offers CurseForge modpacks only with a stored API key` am Ende ergänzen:

```tsx
    expect(screen.getByRole('link', { name: 'Zu den Einstellungen' })).toHaveAttribute('href', '/settings')
```

Run: `cd web && npx vitest run src/features/dashboard src/features/servers/create-server-page.test.tsx`
Expected: FAIL – `Unable to find role="link" and name "Zu den Einstellungen"`.

`web/src/features/dashboard/dashboard-page.tsx`: im Hinweisblock nach dem `<p className="text-muted-foreground mt-1">…</p>` ergänzen:

```tsx
          <Link to="/settings" className="text-primary mt-2 inline-block underline underline-offset-4">
            {t('settings.link')}
          </Link>
```

`web/src/features/servers/create-server-page.tsx`: `import { Link } from '@tanstack/react-router'` ergänzen und im Hinweisblock nach `<p className="text-muted-foreground mt-1">{t('servers.create.base_domain_missing')}</p>` ergänzen:

```tsx
            <Link to="/settings" className="text-primary mt-2 inline-block underline underline-offset-4">
              {t('settings.link')}
            </Link>
```

`web/src/features/servers/server-form.tsx`: `import { Link } from '@tanstack/react-router'` ergänzen und in `TypeField` die Zeile

```tsx
      {curseforgeKeySet ? null : <p className="text-muted-foreground text-sm">{t('servers.form.curseforge_key_hint')}</p>}
```

ersetzen durch

```tsx
      {curseforgeKeySet ? null : (
        <p className="text-muted-foreground text-sm">
          {t('servers.form.curseforge_key_hint')}{' '}
          <Link to="/settings" className="text-primary underline underline-offset-4">
            {t('settings.link')}
          </Link>
        </p>
      )}
```

Der Hinweis-Absatz enthält jetzt zusätzlich den Link; `getByText` mit dem vollständigen Satz passt deshalb nicht mehr auf den Absatz. In `create-server-page.test.tsx` im Test `offers CurseForge modpacks only with a stored API key` den Matcher ersetzen durch:

```tsx
    expect(screen.getByText(/^Für CurseForge-Modpacks braucht mc-panel einen API-Key\./)).toBeInTheDocument()
```

und im Test `asks for the CurseForge modpack once a key is stored` durch:

```tsx
    expect(screen.queryByText(/^Für CurseForge-Modpacks braucht mc-panel einen API-Key\./)).not.toBeInTheDocument()
```

Run: `cd web && npx vitest run src/features/dashboard src/features/servers`
Expected: PASS.

- [ ] **Step 10: Gesamtprüfung**

Run: `cd web && npm run lint && npm run typecheck && npm test && npm run build`
Expected: alles grün.

Run: `cd web && npm run e2e`
Expected: PASS.

Run: `make test`
Expected: PASS.

- [ ] **Step 11: Commit**

```bash
git add -A web/src/features/settings web/src/features/layout web/src/features/dashboard web/src/features/servers \
  web/src/routes/_app/settings.tsx web/src/routeTree.gen.ts web/src/i18n/de.ts web/src/i18n/en.ts web/src/i18n/i18n.test.ts \
  web/src/app/router.integration.test.tsx
git commit -m "feat(web): add settings page for base domain, CurseForge key and docker status" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 22: Playwright gegen den Mock – Server-Lebenszyklus und eingeschränkter Nutzer

**Files:**
- Create: `web/e2e/servers.spec.ts`
- Modify: `web/playwright.config.ts` (mock-Projekt nimmt `servers.spec.ts` auf)

**Interfaces:**
- Consumes: Aufgaben 17–21 (UI), V5-Mock im Browser-Worker (`npm run dev:mock`, Port 5174 über `webServer` der Playwright-Konfiguration); Login wie `web/e2e/smoke.spec.ts` (Mock-Passwort `correct-horse-battery`, TOTP `123456`; `admin` mit TOTP, `alice` ohne 2FA)
- Produces: zwei E2E-Tests im Projekt `mock`. Wichtig: Der MSW-Zustand lebt in der Seite – ein `page.goto` nach dem Login (voller Reload) setzt die Mock-Datenbank und die Sitzung zurück. Nach dem Login wird deshalb nur noch über Links und Buttons navigiert.

- [ ] **Step 1: Playwright-Konfiguration**

In `web/playwright.config.ts` im Projekt `mock` die Zeile

```ts
      testMatch: /(?<![\w-])(totp|smoke)\.spec\.ts$/,
```

ersetzen durch

```ts
      testMatch: /(?<![\w-])(totp|smoke|servers)\.spec\.ts$/,
```

- [ ] **Step 2: `web/e2e/servers.spec.ts`**

```ts
import { expect, test, type Page } from '@playwright/test'

const PASSWORD = 'correct-horse-battery'
const TOTP_CODE = '123456'

/** Signs in via the UI; the mock state lives in the page, so the test must not reload afterwards. */
async function signIn(page: Page, username: string, withTotp: boolean) {
  await page.goto('/')
  await expect(page).toHaveURL(/\/login$/)
  await page.getByLabel('Benutzername').fill(username)
  await page.getByLabel('Passwort').fill(PASSWORD)
  await page.getByRole('button', { name: 'Anmelden' }).click()
  if (withTotp) {
    await expect(page).toHaveURL(/\/login\/2fa$/)
    await page.getByLabel('Code aus der Authenticator-App').fill(TOTP_CODE)
    await page.getByRole('button', { name: 'Bestätigen' }).click()
  }
  await expect(page.getByRole('heading', { name: 'Server', level: 1 })).toBeVisible()
}

async function confirmSudo(page: Page) {
  const sudo = page.getByRole('dialog', { name: 'Aktion bestätigen' })
  await sudo.getByLabel('Code aus der Authenticator-App').fill(TOTP_CODE)
  await sudo.getByRole('button', { name: 'Bestätigen' }).click()
  await expect(sudo).toBeHidden()
}

test('admin creates a paper server, changes the MOTD and restarts it', async ({ page }) => {
  await signIn(page, 'admin', true)

  await page.getByRole('link', { name: 'Server anlegen' }).click()
  await expect(page).toHaveURL(/\/servers\/new$/)
  await page.getByLabel('Name', { exact: true }).fill('skyblock')
  await expect(page.getByText('Adresse: skyblock.mc.example.com')).toBeVisible()
  await expect(page.getByRole('radio', { name: 'Paper' })).toBeChecked()
  await expect(page.getByRole('combobox', { name: 'Minecraft-Version' })).toHaveValue('26.2')
  await page.getByRole('checkbox', { name: 'Ich akzeptiere die Minecraft-EULA.' }).check()
  await page.getByRole('button', { name: 'Server anlegen' }).click()
  await confirmSudo(page)

  await expect(page).toHaveURL(/\/servers\/\d+$/)
  await expect(page.getByRole('heading', { name: 'skyblock', level: 1 })).toBeVisible()
  const state = page.getByRole('region', { name: 'Zustand' })
  await expect(state.getByText('Läuft')).toBeVisible()
  await expect(state.getByText('skyblock.mc.example.com')).toBeVisible()

  await page.getByRole('tab', { name: 'Einstellungen' }).click()
  await expect(page.getByText('Der Server läuft. Änderungen werden erst nach einem Neustart aktiv.')).toBeVisible()
  await page.getByRole('button', { name: 'Eigenschaften' }).click()
  await page.getByLabel('Nachricht im Serverbrowser (MOTD)').fill('Willkommen auf Skyblock')
  // Sudo is still valid (10 min), so saving asks for no second confirmation.
  await page.getByRole('button', { name: 'Änderungen speichern' }).click()
  await expect(page.getByText('Änderungen gespeichert. Sie werden beim nächsten Neustart aktiv.')).toBeVisible()

  await page.getByRole('tab', { name: 'Übersicht' }).click()
  await expect(page.getByText('Neustart nötig')).toBeVisible()
  await page.getByRole('button', { name: 'Jetzt neu starten' }).click()
  await expect(page.getByText('Neustart für „skyblock“ angefordert.')).toBeVisible()
  await expect(page.getByText('Neustart nötig')).toBeHidden()
  await expect(state.getByText('Läuft')).toBeVisible()
})

test('a restricted user sees only permitted servers and no admin areas', async ({ page }) => {
  await signIn(page, 'alice', false)

  await expect(page.getByRole('article', { name: 'survival' })).toBeVisible()
  await expect(page.getByRole('article', { name: 'creative' })).toBeVisible()
  await expect(page.getByRole('article', { name: 'lobby' })).toHaveCount(0)
  await expect(page.getByRole('link', { name: 'Server anlegen' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Server anlegen' })).toHaveCount(0)
  const nav = page.getByRole('navigation', { name: 'Hauptnavigation' })
  await expect(nav.getByRole('link', { name: 'Einstellungen' })).toHaveCount(0)

  await page.getByRole('link', { name: 'survival', exact: true }).click()
  await expect(page).toHaveURL(/\/servers\/1$/)
  await expect(page.getByRole('heading', { name: 'survival', level: 1 })).toBeVisible()
  await expect(page.getByRole('tab', { name: 'Übersicht' })).toBeVisible()
  await expect(page.getByRole('tab', { name: 'Einstellungen' })).toHaveCount(0)
  await expect(page.getByRole('tab', { name: 'Löschen' })).toHaveCount(0)
  await expect(page.getByRole('group', { name: 'Steuerung für survival' })).toBeVisible()
})
```

- [ ] **Step 3: Laufen lassen**

Run: `cd web && npx playwright install chromium && npm run e2e`
Expected: PASS – `totp.spec.ts`, `smoke.spec.ts` und beide Tests aus `servers.spec.ts`. Schlägt `servers.spec.ts` beim Polling fehl (z. B. `Läuft` erscheint nicht), mit `npx playwright test --project=mock e2e/servers.spec.ts --trace on` den Trace prüfen; die Ursache liegt dann im Mock-Übergang `creating → running` (V5) oder in `pollInterval`, nicht im Test-Timeout.

- [ ] **Step 4: Gesamtprüfung**

Run: `cd web && npm run lint && npm run typecheck && npm test && npm run build`
Expected: alles grün (Lint deckt `e2e/` mit ab).

Run: `make test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/e2e/servers.spec.ts web/playwright.config.ts
git commit -m "test(web): cover the server lifecycle in Playwright against the mock" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Self-Review

**Spec-Abdeckung (§9 und angrenzend):**

| Anforderung | Aufgabe |
|---|---|
| Dashboard: Name, Adresse mit Kopieren, Status-Badge (alle Zustände), Art/Version, RAM, Power-Buttons nur mit `server.power`, „Neustart nötig“, Link zur Detailseite | 18 (Link: 20) |
| Admin-Hinweis ohne Basisdomain, „Server anlegen“ gesperrt, Link zu den Einstellungen | 18 (Link: 21) |
| `/servers/new`: Name + Live-Adresse, Art mit Kurzbeschreibung, Version aus Katalog mit Freitext-Fallback, Loader (Fabric Auswahl, NeoForge Freitext unter „Erweitert“), Modpack, Java abgeleitet + Override, RAM 1–32 GiB (Schritt 0,25, Default 4), Eigenschaften (eingeklappt, Defaults §7), EULA mit Link, Sudo, Detailseite | 19 (Navigation auf Detailseite: 20) |
| CurseForge nur mit hinterlegtem Key | 19 (Link: 21) |
| `/servers/$id`: Übersicht (Status, Adresse, RAM/CPU, Startzeit relativ, `last_error`, Power, Banner + „Jetzt neu starten“), Einstellungen (Admin, „Änderungen speichern“, Hinweis bei laufendem Server), Löschen (Namen eintippen, Zielordner) | 20 |
| Nicht eingerichteter Server: Erklärung + nur Löschen; 403/404 | 20 |
| `/settings` + Navigationspunkt: Basisdomain mit DNS-Beispiel, CurseForge-Key (nur schreibbar, „hinterlegt“, Entfernen), Docker-/Agent-Status, unbekannte Container | 21 |
| Polling 3 s / 15 s | 17 (`pollInterval`), 18 (Dashboard), 20 (Detail) |
| Java-Ableitung mit gemeinsamen Testfällen (Spec §6.1, §11) | 17 |
| Formular-Validierung, Status-Badge (Spec §11 Vitest) | 17, 19 |
| Playwright: anlegen → Detail → Einstellung ändern → „Neustart nötig“ → Neustart (Spec §11) | 22 |
| `de`/`en`, mobil bedienbar (Grid/Flex-Umbrüche wie bestehende Seiten, Tab-Leiste und Formular einspaltig unter `sm`) | 17–21 |

**Typkonsistenz:** `ServerFormResult` (Aufgabe 19) wird in Aufgabe 19 zu `CreateServerRequest` (`{ ...result, eula_accepted: true }`) und in Aufgabe 20 feldweise zu `UpdateServerRequest`. `PowerButtons`/`usePower` (18) werden in 20 ohne Änderung wiederverwendet. `FieldRow` (19) wird in 21 wiederverwendet. Query-Keys: `serversKey` ist Präfix von `serverKey(id)`; Katalog und Einstellungen haben eigene Wurzeln (`server-catalog`, `server-settings`), damit `invalidateQueries({ queryKey: serversKey })` sie nicht neu lädt.

**Reihenfolge:** Jede Aufgabe lässt `npm test`, `npm run typecheck`, `npm run lint`, `npm run build` (mit eingechecktem `routeTree.gen.ts`), `npm run e2e` und `make test` grün. Links auf noch nicht existierende Routen gibt es zu keinem Zeitpunkt.

**Bewusst offen gelassen:** Kein Entfernen der Basisdomain im UI (PUT mit `null` ist möglich, aber ohne Basisdomain sind alle Server unerreichbar – das gehört nicht auf einen einzelnen Button); keine Anzeige der Router-Container-Zustände (V4 liefert sie nicht).
