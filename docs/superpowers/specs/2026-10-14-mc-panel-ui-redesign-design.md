# mc-panel – Teilprojekt 7: UI-Redesign (Design-Spec)

- **Datum:** 2026-10-14
- **Status:** entworfen
- **Autor:** Jannes (mit Claude)
- **Baut auf:** allen Teilprojekten 1–6; ändert nur das Frontend (`web/`)

## 1. Ziel und Kontext

Alle Grundfunktionen stehen. Das Frontend ist aber fast unverändert die shadcn-Vorlage (neutrales Grau ohne Akzent, Geist, schlichte Sidebar, `max-w-6xl`, neun Tabs nebeneinander auf der Serverseite) und wirkt dadurch generisch. Teilprojekt 7 gibt dem Panel ein eigenes, modernes Design mit klarer Richtung „nerdy / Minecraft / Gaming“, macht die Seiten übersichtlicher, nutzt die Breite besser und vereinheitlicht die Abstände.

### 1.1 Erfolg

- Das Panel sieht wie ein eigenes Produkt aus, nicht wie eine shadcn-Demo; Minecraft ist auf den ersten Blick erkennbar (Grasblock, Block-Abschrägungen, Pixel-Überschriften, Mehrspieler-Liste), ohne die Lesbarkeit zu opfern.
- Dark und Light Mode sind gleichwertig ausgearbeitet.
- Zwischen Servern und ihren Bereichen springt man mit einem Klick; Status, Adresse, Start/Stopp und Kennzahlen eines Servers sind in jedem Bereich sichtbar.
- Alle Seiten folgen derselben Spacing-Skala und nutzen die verfügbare Breite.
- Kein Verlust an Funktion, Barrierefreiheit (WCAG 2.1 AA) oder Sicherheit (CSP).

### 1.2 Nicht-Ziele

- Keine Änderungen an API, Backend, OpenAPI, Datenbank oder Berechtigungen.
- Keine neuen Funktionen (außer den in §5 genannten Navigationsänderungen, z. B. URLs pro Server-Bereich).
- Keine Bilder oder Texturen von Mojang, keine echten Minecraft-Assets; alles Thematische ist eigenes CSS/SVG.
- Kein dritter Theme-Modus, keine frei wählbaren Akzentfarben.

## 2. Entscheidungen

| Bereich | Entscheidung | Begründung |
|---|---|---|
| Stilrichtung | „M2 – ausgewogen“: dunkle Launcher-Basis, fast eckig, Buttons und Badges mit Block-Abschrägung, Kennzahlen als vertiefte Inventar-Slots, Pixel-Schrift für Überschriften, normaler Fließtext | Deutlich Minecraft, aber lesbar; gewählt aus Blocky/Pixel (B) und Launcher (C) |
| Modi | Dark (M2) und Light („Clean“: warmes Off-White, Abschrägungen nur angedeutet) gleichwertig; Konsole in beiden dunkel | Nutzerwunsch; Konsole ist ein Terminal |
| Navigation | „L3“: Icon-Leiste + Server-Baum mit aufgeklapptem aktivem Server | Schnelles Springen zwischen Servern und Bereichen |
| Server-Bereiche | Echte Routen `/servers/$id/<bereich>` statt Tab-State | Teilbar, überlebt Neuladen, nötig für den Baum |
| Startseite | „D2“: Liste im Stil des Minecraft-Mehrspieler-Menüs | Sofort wiedererkennbar, skaliert auf viele Server |
| Schriften | Space Grotesk (UI), Silkscreen (Überschriften/Labels), Pixelify Sans (Zahlen/MOTD), JetBrains Mono (Code) – selbst gehostet via fontsource, alle OFL | CSP erlaubt keine externen Fonts |
| Komponenten | shadcn-Komponenten behalten ihre API, bekommen nur neues Styling | Seiten müssen für den neuen Look nichts ändern; parallele Umsetzung |

### 2.1 Verworfene Alternativen

- **Terminal/Dev-Tool (A):** sehr modern, aber Minecraft nur angedeutet.
- **Reines Blocky/Pixel (B), „mehr Minecraft“ (M3), Light „Stein“:** zu schwer lesbar über lange Sitzungen, wirkt wie ein Fan-Projekt.
- **Tabs behalten (L1), Sidebar wechselt in den Server (L2):** L1 löst die Tab-Flut nicht, L2 macht das Springen zwischen Servern umständlicher.
- **Kacheln auf der Startseite (D1):** bei vielen Servern unübersichtlicher.

## 3. Design-System

### 3.1 Tokens (`web/src/index.css`)

Die shadcn-Variablen (`--background`, `--foreground`, `--card`, `--primary`, `--muted`, `--border`, `--ring`, `--chart-*`, `--sidebar-*` …) bleiben als Namen bestehen und bekommen neue Werte. Richtwerte (der Lead legt die finalen Werte in Welle 0 fest und prüft den Kontrast):

| Token | Dark | Light |
|---|---|---|
| `--background` | `#101114` | `#f6f6f4` |
| `--card` / Flächen | `#16171b`, `#1a1c21` | `#efefec`, `#ffffff` |
| `--border` | `#23252b` | `#dcdcd8` |
| `--foreground` | `#d4d6dc` | `#1f2024` |
| `--muted-foreground` | `#7c7f88` | `#5a5b61` (AA auf Hintergrund) |
| `--primary` | `#4fa83f` (Hover `#5cc14a`) | `#3f9a2f` oder dunkler, AA mit weißem Text |
| `--destructive` | Redstone-Rot | Redstone-Rot, dunkler |

Neue Tokens:

- `--bevel-hi`, `--bevel-lo`: helle und dunkle Kante der Abschrägung (je Modus und je Fläche: primär, sekundär, destruktiv).
- `--slot-bg`, `--slot-hi`, `--slot-lo`: vertiefter Inventar-Slot.
- `--glow`: Leucht-Schatten des Akzents (Dark kräftig, Light sehr schwach).
- `--console-bg`, `--console-fg`, `--console-border`: Konsole, in beiden Modi dunkel.
- `--status-online`, `--status-offline`, `--status-starting`, `--status-error`.
- `--mc-0` … `--mc-f`: die 16 Minecraft-Farbcodes (für `§`-Codes in MOTD-Zeile und Konsole; Light-Varianten mit ausreichendem Kontrast).
- `--radius: 0.125rem` (fast eckig).

### 3.2 Schriften

- Pakete: `@fontsource-variable/space-grotesk`, `@fontsource/silkscreen`, `@fontsource-variable/pixelify-sans`, `@fontsource-variable/jetbrains-mono`; `@fontsource-variable/geist` entfällt.
- `--font-sans` = Space Grotesk, `--font-heading` = Silkscreen, `--font-pixel` = Pixelify Sans, `--font-mono` = JetBrains Mono.
- Silkscreen nur für Logo, Seiten- und Abschnittsüberschriften, Badges und kurze Labels in Versalien; nie für Fließtext, nie unter 12 px. Pixelify Sans nur für Kennzahlen, Spielerzahlen und die MOTD-Zeile.
- Umlaute und `ß` müssen in allen vier Schriften vorhanden sein (in Welle 0 geprüft; fehlt ein Glyph, fällt die Stelle auf Space Grotesk zurück).

### 3.3 Utilities (Tailwind `@utility` in `index.css`)

- `bevel` (erhaben, Buttons/Badges), `bevel-inset` (vertieft, Slots/Inputs), `pixel-heading` (Silkscreen + harter Textschatten im Dark Mode), `glow` (Akzent-Schatten).
- Bei `prefers-reduced-motion: reduce` keine Animationen am Glow; bei `forced-colors: active` fallen Abschrägungen und Glow weg, Ränder werden echte `border`s.

### 3.4 Spacing-Skala

| Stelle | Wert |
|---|---|
| Seitenrand | 16 px mobil, 24 px ab `md` |
| Zwischen Sections einer Seite | 24 px (`gap-6`) |
| Innerhalb einer Section | 16 px (`gap-4`) |
| Formularfelder untereinander | 12 px (`gap-3`) |
| Abstand Überschrift → Inhalt | 12 px |
| Tabellenzeilen | dicht: 8 px vertikal |

Seiten nutzen die volle Breite des Inhaltsbereichs. Formulare und Textseiten (Server anlegen, Einstellungen, Profil, Login) bekommen eine lesbare Maximalbreite (`max-w-3xl` bzw. `max-w-md` für Login).

### 3.5 Basiskomponenten (`web/src/components/ui/*`)

API unverändert, neues Styling:

- **Button:** `default` grün mit `bevel` + `glow`, `secondary`/`outline` grau mit `bevel`, `destructive` rot mit `bevel`, `ghost` und `link` flach. Gedrückt: Abschrägung invertiert.
- **Badge:** `bevel`, Silkscreen; Statusvarianten über die `--status-*`-Tokens.
- **Input, Select, Textarea, Switch:** `bevel-inset`.
- **Card:** flache Fläche mit 1-px-Rand (keine runden Schatten).
- **Tabs:** Unterstrich-Stil mit grünem Balken (bleiben für Unter-Tabs wie Sicherheit und Plugins).
- **Dialog, Sheet, Dropdown:** Fläche mit `bevel`, Titel als `pixel-heading`.
- **Table:** dichte Zeilen, Kopf in Silkscreen-Labels.

### 3.6 Neue gemeinsame Bausteine (`web/src/components/`)

| Baustein | Props (fest ab Welle 0) | Zweck |
|---|---|---|
| `PageHeader` | `title`, `description?`, `actions?`, `breadcrumb?` | Seitenkopf mit `pixel-heading` |
| `Section` | `title?`, `description?`, `actions?`, `children`, `aria-labelledby` automatisch | einheitlicher Abschnitts-Container; ersetzt `SectionCard` und ad-hoc `Card`s |
| `StatSlot` | `label`, `value`, `hint?`, `bar?: number (0–1)`, `tone?: 'default' \| 'warn' \| 'danger'` | Kennzahl als Inventar-Slot |
| `PixelIcon` | `kind: 'grass' \| 'server'`, `state?`, `size?` | Grasblock-Logo und Server-Icon (CSS/Inline-SVG) |
| `PingBars` | `level: 0–5 \| null`, `label` | Erreichbarkeit im Stil der Mehrspieler-Liste, mit Text für Screenreader |
| `McText` | `text` | rendert `§`-Farbcodes als Spans (Logik aus `features/console/ansi.ts` wiederverwenden), nie HTML |

`SectionCard` (`features/profile/section-card.tsx`) wird in Welle 1 durch `Section` ersetzt und danach gelöscht.

## 4. App-Rahmen und Navigation

### 4.1 Aufbau

- **Icon-Leiste** (56 px, links): oben `PixelIcon grass` als Link zur Startseite; darunter Server, Benutzer, Sicherheit, Audit, Einstellungen (Admin-Einträge nur für Admins, Logik aus `nav.ts`); unten Profil, Theme-Umschalter, Logout. Jedes Icon mit `aria-label` und Tooltip; aktiver Eintrag mit `aria-current="page"` und grünem `bevel`.
- **Server-Baum** (ca. 220 px): Überschrift „SERVER“, darunter alle sichtbaren Server mit Status-Punkt (aus der vorhandenen Servers-Query, gleiche Poll-Logik wie die Startseite). Der aktive Server ist aufgeklappt und zeigt seine Bereiche (§4.2). Admins sehen unten „+ Server anlegen“. Einklappbar; Zustand in `localStorage` unter `mcpanel.tree` (try/catch, Standard: offen).
- **Topbar** (dünn): Brotkrume (z. B. `survival / Konsole`), mobil zusätzlich der Menü-Knopf.
- **Inhalt:** volle Breite, Seitenrand nach §3.4.

### 4.2 Responsiv

| Breite | Verhalten |
|---|---|
| ≥ 1024 px (`lg`) | Leiste + Baum dauerhaft |
| 768–1023 px | nur Leiste; Server-Icon öffnet den Baum als Flyout (Sheet) |
| < 768 px | Topbar mit Menü-Knopf; Drawer enthält Leiste und Baum |

Der Skip-Link „Zum Inhalt“ bleibt erhalten.

### 4.3 Server-Bereiche als Routen

- Layout-Route `routes/_app/servers/$id.tsx` (Server-Kopf + `<Outlet/>`), Kind-Routen: Index = Übersicht, `console`, `players`, `files`, `plugins`, `backups`, `monitoring`, `settings`. `servers/new` bleibt.
- Eine reine Funktion `serverSections(server, isAdmin)` in `features/servers/sections.ts` liefert die erlaubten Bereiche in fester Reihenfolge (heutige Logik aus `server-detail-page.tsx`: Übersicht und Monitoring immer; Konsole und Spieler mit `server.console`; Dateien mit `server.files`; Plugins/Mods mit `server.plugins` und passendem Typ; Backups mit `server.backups`; Einstellungen für Admins; ohne Instanz nur Übersicht, dort für Admins das Löschformular). Baum, Mobilmenü und Routen-Guards nutzen dieselbe Funktion. Unit-Tests decken jede Kombination ab.
- Wird ein nicht erlaubter Bereich direkt aufgerufen (oder das Recht entzogen, während er offen ist), leitet die Route zur Übersicht weiter (`replace`).
- Wechselt man den Server, bleibt der Bereich erhalten, wenn er beim Ziel erlaubt ist, sonst Übersicht.
- „Löschen“ ist kein eigener Bereich mehr, sondern eine Gefahrenzone am Ende der Einstellungen (unverändert mit Bestätigung und sudo).
- **Ungespeicherte Änderungen / laufende Uploads im Dateien-Bereich:** `FilesTab` schützt sich selbst mit `useBlocker` von TanStack Router (gleiche Texte wie heute, `leaveTexts`) und mit `beforeunload`. Das greift bei jedem Navigieren weg, nicht nur beim Tab-Wechsel. Die Props `onBusyChange` entfallen.
- Verweise zwischen Bereichen (heute `onOpenSettings`) werden echte Links auf `/servers/$id/settings`.

### 4.4 Server-Kopf

Über allen Bereichen eines Servers:

- Name als `pixel-heading`, `StatusBadge`, kopierbare Adresse (`AddressLine`, Mono), Power-Buttons (rechts; ohne `server.power` ausgeblendet mit dem heutigen Hinweis in der Übersicht).
- Vier `StatSlot`s: Spieler (`x/y`), TPS, RAM (mit Balken), CPU – aus Monitoring-Übersicht und Server-Status; fehlende Werte zeigen „–“, nie 0.
- Darunter bei Bedarf: Banner „Neustart nötig“ (mit Knopf), Hinweis bei laufendem Entpacken, letzter Fehler (nur Admins bekommen den Text).
- Mobil: Slots als 2×2-Raster, Power-Buttons unter dem Namen.
- Server ohne Instanz: nur Name und „Nicht eingerichtet“.

Die **Übersicht** enthält danach nur noch Details (Typ, Version, Java, Speicher, Startzeit) und die Online-Spieler.

## 5. Seiten

### 5.1 Startseite (Mehrspieler-Liste)

- Admins: oben `StatSlot`-Leiste des Hosts (CPU, RAM, Load, Disk frei mit Balken; Disk-Warnung als `tone="danger"` mit dem heutigen Text).
- Hinweis „Keine Basisdomain“ unverändert, als Section.
- Liste: eine Zeile pro Server (`article`, Name als Link auf `/servers/$id`):
  - links `PixelIcon server` (grau bei gestoppt/nicht eingerichtet),
  - Zeile 1: Name, Adresse (Mono, kopierbar), rechts Spielerzahl und `PingBars` (5 Balken bei erreichbar, 0 + „Nicht erreichbar“ sonst),
  - Zeile 2 im MOTD-Stil (Pixelify Sans): Typ + Version, TPS, RAM, CPU, „Neustart nötig“ (gold), Alerts (rot),
  - rechts Power-Buttons als Icons (mit `aria-label`, gleiche Gruppe „Steuerung für …“),
  - ab `lg` die Spieler-Sparkline in der Zeile.
- „Server anlegen“ im Seitenkopf (unverändert gesperrt ohne Basisdomain).
- Alle Texte, Rollen und Namen, die `dashboard-page.test.tsx` prüft, bleiben gleich.

### 5.2 Server-Bereiche

- **Konsole:** füllt die verfügbare Höhe (Viewport minus Rahmen und Server-Kopf, mindestens 320 px); Eingabe im Terminal-Stil mit `>`-Prompt; Farben über `--console-*` und `--mc-*`.
- **Spieler:** Listen als dichte Tabellen in Sections; Aktionen als Icon-Buttons mit Labels.
- **Dateien:** ab `xl` Liste und Editor nebeneinander (Liste ca. 40 %), darunter wie heute untereinander; Upload-Warteschlange als Section.
- **Plugins/Mods:** „Entdecken“ als Kachel-Raster (Icon, Name, Autor, Downloads), „Installiert“ als dichte Liste; Unter-Tabs bleiben.
- **Backups:** Schedule und Snapshot-Liste als Sections; Snapshot-Liste als Tabelle.
- **Monitoring:** Kennzahlen als `StatSlot`s, Diagramme mit Token-Farben (`--chart-*`, Grün als Hauptserie).
- **Einstellungen:** Server-Formular in Sections mit `max-w-3xl`, Gefahrenzone „Server löschen“ am Ende (rote Section).

### 5.3 Admin- und Auth-Seiten

- **Benutzer, Rechte, Audit:** dichte Tabellen, Aktionen rechts, Filter in einer Zeile über der Tabelle.
- **Sicherheit:** Unter-Tabs bleiben; Übersicht mit `StatSlot`s; Balkendiagramm mit Token-Farben.
- **Einstellungen, Profil:** Sections mit `max-w-3xl`.
- **Login, 2FA, Setup-2FA:** zentriertes Panel (`max-w-md`) mit `PixelIcon grass` und `pixel-heading`, Hintergrund ein dezentes Pixelmuster aus CSS-Gradienten (kein Bild).
- **Sudo-Dialog, Bestätigungsdialoge, Toaster, 404/Fehlerseite:** neuer Stil, Verhalten unverändert.

## 6. Umsetzung mit Agent-Teams

### 6.1 Wellen

- **Welle 0 – Lead allein, kurz** (Branch `feat/ui-foundation`): Tokens Dark/Light (§3.1), Schriften (§3.2), Utilities (§3.3), Spacing-Regeln, die Bausteine aus §3.6 mit festen Props (einfache, aber funktionierende Umsetzung), `.gitignore`-Eintrag `.superpowers/`. Danach ist der Commit der Ausgangspunkt für alle Teammates.
- **Welle 1 – fünf Teammates parallel**, jeder im eigenen Worktree auf eigenem Branch ab dem Welle-0-Commit, mit disjunkten Dateien:

| Teammate | Branch | Umfang | Dateien |
|---|---|---|---|
| T1 Primitives | `feat/ui-primitives` | §3.5, Diagramm-Farben, ConfirmDialog, ThemeToggle, Toaster, NotFound/RouteError, DataList, NativeSelect, CodeForm | `web/src/components/ui/*`, `web/src/components/charts/*`, übrige `web/src/components/*.tsx` außer den Welle-0-Bausteinen |
| T2 Shell | `feat/ui-shell` | §4 komplett, Übersicht, E2E-Navigation | `features/layout/*`, `routes/**`, `features/servers/{server-detail-page,server-overview,sections,server-header,status-badge,address-line,power-buttons}.*`, `e2e/*` |
| T3 Startseite + Live | `feat/ui-live` | §5.1, Konsole, Spieler, Monitoring | `features/dashboard/*`, `features/console/*`, `features/players/*`, `features/monitoring/*` |
| T4 Server-Verwaltung | `feat/ui-manage` | Dateien (inkl. `useBlocker`), Plugins, Backups, Server-Formular, Einstellungen + Gefahrenzone | `features/files/*`, `features/plugins/*`, `features/backups/*`, `features/servers/{server-form,server-settings-tab,delete-server-section,properties-fields,…}.*` (alles in `features/servers` außer T2s Dateien) |
| T5 Admin + Auth | `feat/ui-admin` | §5.3 | `features/{users,security,audit,settings,profile,auth,setup,sudo,webauthn}/*` |

Die genaue Dateizuordnung (insbesondere in `features/servers/`) legt der Plan fest; jede Datei hat genau einen Besitzer.

### 6.2 Verträge zwischen den Teammates

- Bereichs-Komponenten (`ServerOverview`, `MonitoringTab`, `ConsoleTab`, `PlayersTab`, `FilesTab`, `PluginsTab`, `BackupsTab`, `ServerSettingsTab`) nehmen ab Welle 1 nur noch `{ server: Server }` (Plugins zusätzlich `kind`; Übersicht und Einstellungen zusätzlich `instance`). T2 rendert sie in den Routen; T3/T4 ändern ihr Inneres. Links auf andere Bereiche bauen sie selbst mit `<Link to="/servers/$id/settings">`.
- Der Server-Kopf (T2) liest „Entpacken läuft“ über `useFileJob` aus `features/files` (API von T4 unverändert).
- Nur der Lead ändert `web/src/index.css`, `web/package.json`/`package-lock.json` und die Welle-0-Bausteine. Bedarf meldet ein Teammate dem Lead; der Lead pusht auf `feat/ui-foundation`, die Teammates rebasen.
- **i18n:** neue Schlüssel nur im Namensraum des eigenen Features; keine bestehenden Schlüssel umbenennen oder löschen, solange ein anderer Teammate sie noch nutzen könnte (Aufräumen macht der Lead nach der Integration). Konflikte in `de.ts`/`en.ts` löst der Lead.
- **E2E:** Nur T2 ändert Navigation in `e2e/*` (Tab → Link über einen Helfer `openServerSection(page, name)` in `e2e/helpers.ts`). Brauchen T3–T5 Änderungen an E2E-Specs, melden sie diese T2 bzw. dem Lead.
- **Mocks:** keine Änderungen nötig; falls doch, nur nach Absprache mit dem Lead.

### 6.3 Tests und Prüfung

- Jeder Teammate: `make check`, dann `make test-vm SUITE=web` (Vitest + tsc) für seine Änderung; T2 zusätzlich `SUITE=e2e`. Der VM-Lock ist geteilt, deshalb `SUITE=all` nur einmal pro PR durch den Lead.
- Unit-Tests: `serverSections` (alle Rechte-Kombinationen), Routen-Guards und Weiterleitung, Blocker im Dateien-Bereich, `McText`, `PingBars`, `StatSlot` (fehlende Werte → „–“), Baum-Zustand in `localStorage`. Bestehende Tests werden an die neue Struktur angepasst, nicht gelöscht; Semantik (Rollen, Namen, Texte) bleibt, wo die Spec sie nicht ausdrücklich ändert.
- **Screenshots:** Jeder Teammate erzeugt mit `make test-vm SUITE=shots` (Playwright-Projekt `screenshots`, eigene Datei `web/e2e/shots/<bereich>.shots.ts`, nicht in CI) Screenshots seiner Seiten gegen den Mock in Dark und Light (Desktop 1440 px und Mobil 390 px); sie landen lokal in `web/screenshots/` (gitignored), der Teammate prüft sie selbst und der Lead zeigt eine Auswahl dem Nutzer. Die Infrastruktur gehört zu Welle 0.
- Kontrast: Der Lead prüft die Token-Paare aus §3.1 in Welle 0 rechnerisch (AA: 4,5 : 1 Text, 3 : 1 große Schrift/UI-Ränder).

### 6.4 PRs

1. **PR 1 – Fundament:** Welle 0 + T1, gegen `main`.
2. **PR 2 – Navigation:** T2, gestapelt auf `feat/ui-foundation`; nach Merge von PR 1 Basis auf `main` umstellen.
3. **PR 3 – Seiten:** der Lead integriert T3, T4, T5 auf `feat/ui-pages` (gestapelt auf PR 2) und löst Konflikte.

Merges nur nach Freigabe durch den Nutzer. Nach dem letzten Merge: `make deploy-testvm` und Abnahme (§7); `CLAUDE.md` (Frontend-Abschnitt) und die Roadmap im Foundation-Spec werden um Teilprojekt 7 ergänzt.

## 7. Abnahmekriterien

- Dark und Light entsprechen §3 auf allen Seiten; keine Reste des neutralen shadcn-Graus.
- Navigation nach §4 auf Desktop, Tablet und Mobil; jeder Server-Bereich hat eine eigene URL, Neuladen bleibt im Bereich; unerlaubte Bereiche leiten zur Übersicht.
- Server-Kopf mit Status, Adresse, Power und Kennzahlen in jedem Bereich.
- Startseite als Mehrspieler-Liste nach §5.1.
- Ungespeicherte Änderungen im Dateien-Bereich werden bei jedem Wegnavigieren abgefragt.
- Spacing nach §3.4 auf allen Seiten; keine horizontale Scrollleiste bei 390 px Breite (außer in Tabellen/Konsole, die selbst scrollen).
- Tastaturbedienung und sichtbarer Fokus überall; Kontraste AA; `forced-colors` und `prefers-reduced-motion` berücksichtigt.
- CSP unverändert, keine externen Ressourcen; `npm run build` ohne Warnungen über fehlende Glyphen.
- CI grün (inkl. E2E gegen Mock und echtes Backend).
