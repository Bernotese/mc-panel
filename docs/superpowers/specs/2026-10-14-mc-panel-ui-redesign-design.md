# mc-panel – Sub-project 7: UI Redesign (design spec)

- **Date:** 2026-10-14
- **Status:** implemented (PRs #80, #81, #82, 2026-10-14)
- **Author:** Jannes (with Claude)
- **Builds on:** all sub-projects 1–6; changes only the frontend (`web/`)

## 1. Goal and context

All basic features are in place. The frontend, however, is still almost unchanged from the shadcn template (neutral grey without an accent, Geist, a plain sidebar, `max-w-6xl`, nine tabs side by side on the server page) and therefore looks generic. Sub-project 7 gives the panel its own modern design with a clear "nerdy / Minecraft / gaming" direction, makes the pages easier to scan, uses the width better and unifies the spacing.

### 1.1 Success

- The panel looks like a product of its own, not like a shadcn demo; Minecraft is recognizable at first glance (grass block, block bevels, pixel headings, multiplayer list) without sacrificing readability.
- Dark and light mode are worked out to the same standard.
- One click moves between servers and their areas; status, address, start/stop and key figures of a server are visible in every area.
- All pages follow the same spacing scale and use the available width.
- No loss of function, accessibility (WCAG 2.1 AA) or security (CSP).

### 1.2 Non-goals

- No changes to the API, backend, OpenAPI, database or permissions.
- No new features (except the navigation changes named in §5, e.g. URLs per server area).
- No images or textures from Mojang, no real Minecraft assets; everything thematic is our own CSS/SVG.
- No third theme mode, no freely selectable accent colours.

## 2. Decisions

| Area | Decision | Reason |
|---|---|---|
| Style direction | "M2 – balanced": dark launcher base, nearly square, buttons and badges with a block bevel, key figures as sunken inventory slots, pixel font for headings, normal body text | Clearly Minecraft, but readable; chosen from Blocky/Pixel (B) and Launcher (C) |
| Modes | Dark (M2) and light ("Clean": warm off-white, bevels only hinted) on equal footing; the console is dark in both | User request; the console is a terminal |
| Navigation | "L3": icon rail + server tree with the active server expanded | Fast jumps between servers and areas |
| Server areas | Real routes `/servers/$id/<area>` instead of tab state | Shareable, survives a reload, needed for the tree |
| Home page | "D2": list in the style of the Minecraft multiplayer menu | Instantly recognizable, scales to many servers |
| Fonts | Space Grotesk (UI), Silkscreen (headings/labels), Pixelify Sans (numbers/MOTD), JetBrains Mono (code), self-hosted via fontsource, all OFL | The CSP allows no external fonts |
| Components | shadcn components keep their API and only get new styling | Pages need no changes for the new look; parallel implementation |

### 2.1 Rejected alternatives

- **Terminal/dev tool (A):** very modern, but Minecraft only hinted at.
- **Pure Blocky/Pixel (B), "more Minecraft" (M3), light "Stone":** too hard to read over long sessions, looks like a fan project.
- **Keep the tabs (L1), sidebar switches into the server (L2):** L1 does not solve the flood of tabs, L2 makes jumping between servers more cumbersome.
- **Tiles on the home page (D1):** harder to scan with many servers.

## 3. Design system

### 3.1 Tokens (`web/src/index.css`)

The shadcn variables (`--background`, `--foreground`, `--card`, `--primary`, `--muted`, `--border`, `--ring`, `--chart-*`, `--sidebar-*` …) keep their names and get new values. Target values (the lead sets the final values in wave 0 and checks the contrast):

| Token | Dark | Light |
|---|---|---|
| `--background` | `#101114` | `#f6f6f4` |
| `--card` / surfaces | `#16171b`, `#1a1c21` | `#efefec`, `#ffffff` |
| `--border` | `#23252b` | `#dcdcd8` |
| `--foreground` | `#d4d6dc` | `#1f2024` |
| `--muted-foreground` | `#7c7f88` | `#5a5b61` (AA on the background) |
| `--primary` | `#4fa83f` (hover `#5cc14a`) | `#3f9a2f` or darker, AA with white text |
| `--destructive` | redstone red | redstone red, darker |

New tokens:

- `--bevel-hi`, `--bevel-lo`: light and dark edge of the bevel (per mode and per surface: primary, secondary, destructive).
- `--slot-bg`, `--slot-hi`, `--slot-lo`: sunken inventory slot.
- `--glow`: glow shadow of the accent (strong in dark, very faint in light).
- `--console-bg`, `--console-fg`, `--console-border`: console, dark in both modes.
- `--status-online`, `--status-offline`, `--status-starting`, `--status-error`.
- `--mc-0` … `--mc-f`: the 16 Minecraft colour codes (for `§` codes in the MOTD line and the console; light variants with sufficient contrast).
- `--radius: 0.125rem` (nearly square).

(Now: the final values are hex tokens in `web/src/index.css`, defined in `:root` and `.dark`. The bevel uses per-surface tokens (`--bevel-primary-hi`/`-lo`, `--bevel-danger-hi`/`-lo`) plus `--bevel-edge`; further tokens include `--slot-edge`, `--surface-2`, `--destructive-solid`, `--pattern`, `--pixel-shadow` and `--rail`. `web/src/styles/tokens.test.ts` checks the AA contrast of the token pairs in both modes.)

### 3.2 Fonts

- Packages: `@fontsource-variable/space-grotesk`, `@fontsource/silkscreen`, `@fontsource-variable/pixelify-sans`, `@fontsource-variable/jetbrains-mono`; `@fontsource-variable/geist` is dropped.
- `--font-sans` = Space Grotesk, `--font-heading` = Silkscreen, `--font-pixel` = Pixelify Sans, `--font-mono` = JetBrains Mono.
- Silkscreen only for the logo, page and section headings, badges and short labels in capitals; never for body text, never below 12 px. Pixelify Sans only for key figures, player counts and the MOTD line. (Now, issue #84: stat values, player counts and the dashboard figures use Space Grotesk (`font-semibold`, `tabular-nums`) for readability; Pixelify Sans stays for player names and the MOTD.)
- Umlauts and `ß` must exist in all four fonts (checked in wave 0; if a glyph is missing, that spot falls back to Space Grotesk).

### 3.3 Utilities (Tailwind `@utility` in `index.css`)

- `bevel` (raised, buttons/badges), `bevel-inset` (sunken, slots/inputs), `pixel-heading` (Silkscreen + hard text shadow in dark mode), `glow` (accent shadow).
- With `prefers-reduced-motion: reduce` the glow has no animations; with `forced-colors: active` bevels and glow are dropped and edges become real `border`s.

(Now: `index.css` also defines `bevel-primary`, `bevel-danger`, `bevel-pressed`, `console-scope` (the always-dark console with its own `§` colours) and `pixel-pattern`.)

### 3.4 Spacing scale

| Place | Value |
|---|---|
| Page margin | 16 px on mobile, 24 px from `md` |
| Between sections of a page | 24 px (`gap-6`) |
| Inside a section | 16 px (`gap-4`) |
| Form fields below each other | 12 px (`gap-3`) |
| Heading → content | 12 px |
| Table rows | dense: 8 px vertical |

Pages use the full width of the content area. Forms and text pages (create server, settings, profile, login) get a readable maximum width (`max-w-3xl`, or `max-w-md` for login).

### 3.5 Base components (`web/src/components/ui/*`)

API unchanged, new styling:

- **Button:** `default` green with `bevel` + `glow`, `secondary`/`outline` grey with `bevel`, `destructive` red with `bevel`, `ghost` and `link` flat. Pressed: bevel inverted.
- **Badge:** `bevel`, Silkscreen; status variants via the `--status-*` tokens.
- **Input, Select, Textarea, Switch:** `bevel-inset`.
- **Card:** flat surface with a 1 px border (no rounded shadows).
- **Tabs:** underline style with a green bar (they stay for sub-tabs such as Security and Plugins).
- **Dialog, Sheet, Dropdown:** surface with `bevel`, title as `pixel-heading`.
- **Table:** dense rows, header in Silkscreen labels.

### 3.6 New shared building blocks (`web/src/components/`)

| Building block | Props (fixed from wave 0) | Purpose |
|---|---|---|
| `PageHeader` | `title`, `description?`, `actions?`, `breadcrumb?` | Page header with `pixel-heading` |
| `Section` | `title?`, `description?`, `actions?`, `children`, `aria-labelledby` automatic | Uniform section container; replaces `SectionCard` and ad-hoc `Card`s |
| `StatSlot` | `label`, `value`, `hint?`, `bar?: number (0–1)`, `tone?: 'default' \| 'warn' \| 'danger'` | Key figure as an inventory slot |
| `PixelIcon` | `kind: 'grass' \| 'server'`, `state?`, `size?` | Grass block logo and server icon (CSS/inline SVG) |
| `PingBars` | `level: 0–5 \| null`, `label` | Reachability in the style of the multiplayer list, with text for screen readers |
| `McText` | `text` | Renders `§` colour codes as spans (reusing the logic from `features/console/ansi.ts`), never HTML |

`SectionCard` (`features/profile/section-card.tsx`) is replaced by `Section` in wave 1 and deleted afterwards. (Now: deleted.)

## 4. App frame and navigation

### 4.1 Layout

- **Icon rail** (56 px, left): at the top `PixelIcon grass` as a link to the home page; below it Servers, Users, Security, Audit, Settings (admin entries only for admins, logic from `nav.ts`); at the bottom Profile, theme toggle, Logout. Every icon has an `aria-label` and a tooltip; the active entry has `aria-current="page"` and a green `bevel`.
- **Server tree** (about 220 px): heading "SERVERS", below it all visible servers with a status dot (from the existing servers query, same polling logic as the home page). The active server is expanded and shows its areas (§4.2). Admins see "+ Create server" at the bottom. Collapsible; state in `localStorage` under `mcpanel.tree` (try/catch, default: open).
- **Top bar** (thin): breadcrumb (e.g. `survival / Console`), on mobile also the menu button.
- **Content:** full width, page margin per §3.4.

(Now, issue #86: on desktop (≥ `lg`) the rail can be expanded to show labels next to the icons, state in `localStorage` `mcpanel.rail` (default: collapsed). All pages share one content area `mx-auto max-w-7xl`; the server form, the panel settings and the profile use two columns on wide screens.)

### 4.2 Responsive

| Width | Behaviour |
|---|---|
| ≥ 1024 px (`lg`) | Rail + tree always visible |
| 768–1023 px | Rail only; the server icon opens the tree as a flyout (Sheet) |
| < 768 px | Top bar with menu button; the drawer contains rail and tree |

The skip link "Skip to content" stays.

### 4.3 Server areas as routes

- Layout route `routes/_app/servers/$id.tsx` (server header + `<Outlet/>`), child routes: index = overview, `console`, `players`, `files`, `plugins`, `backups`, `monitoring`, `settings`. `servers/new` stays.
- A pure function `serverSections(server, isAdmin)` in `features/servers/sections.ts` returns the allowed areas in a fixed order (the logic of the former `server-detail-page.tsx`: overview and monitoring always; console and players with `server.console`; files with `server.files`; plugins/mods with `server.plugins` and a matching type; backups with `server.backups`; settings for admins; without an instance only the overview, which then has the delete form for admins). Tree, mobile menu and route guards use the same function. Unit tests cover every combination. (Now: the fixed order is overview, monitoring, console, players, files, plugins, backups, settings.)
- If a disallowed area is opened directly (or the permission is revoked while it is open), the route redirects to the overview (`replace`).
- When switching servers, the area is kept if it is allowed on the target, otherwise the overview.
- "Delete" is no longer an area of its own but a danger zone at the end of the settings (unchanged with confirmation and sudo).
- **Unsaved changes / running uploads in the files area:** `FilesTab` guards itself with TanStack Router's `useBlocker` (same texts as before, `leaveTexts`) and with `beforeunload`. This applies to every navigation away, not only to a tab switch. The `onBusyChange` props are dropped.
- Links between areas (formerly `onOpenSettings`) become real links to `/servers/$id/settings`.

### 4.4 Server header

Above all areas of a server:

- Name as `pixel-heading`, `StatusBadge`, copyable address (`AddressLine`, mono), power buttons (right; hidden without `server.power`, with the existing hint in the overview).
- Four `StatSlot`s: players (`x/y`), TPS, RAM (with a bar), CPU, from the monitoring overview and the server status; missing values show "–", never 0.
- Below, when needed: banner "Restart required" (with a button), a note while an extract runs, the last error (only admins get the text).
- Mobile: slots as a 2×2 grid, power buttons below the name.
- Server without an instance: only the name and "Not set up".

The **overview** then only contains details (type, version, Java, memory, start time) and the online players.

## 5. Pages

### 5.1 Home page (multiplayer list)

- Admins: at the top a `StatSlot` row for the host (CPU, RAM, load, free disk with a bar; disk warning as `tone="danger"` with the existing text).
- The "No base domain" note unchanged, as a section.
- List: one row per server (`article`, name as a link to `/servers/$id`):
  - left `PixelIcon server` (grey when stopped/not set up),
  - line 1: name, address (mono, copyable), on the right the player count and `PingBars` (5 bars when reachable, 0 + "Not reachable" otherwise),
  - line 2 in MOTD style (Pixelify Sans): type + version, TPS, RAM, CPU, "Restart required" (gold), alerts (red),
  - on the right the power buttons as icons (with `aria-label`, same group "Controls for …"),
  - from `lg` the player sparkline in the row.
- "Create server" in the page header (still disabled without a base domain).
- All texts, roles and names that `dashboard-page.test.tsx` checks stay the same.

### 5.2 Server areas

- **Console:** fills the available height (viewport minus frame and server header, at least 320 px); input in terminal style with a `>` prompt; colours via `--console-*` and `--mc-*`.
- **Players:** lists as dense tables in sections; actions as icon buttons with labels.
- **Files:** from `xl` list and editor side by side (list about 40 %), below that stacked as before; upload queue as a section.
- **Plugins/mods:** "Discover" as a tile grid (icon, name, author, downloads), "Installed" as a dense list; sub-tabs stay.
- **Backups:** schedule and snapshot list as sections; snapshot list as a table.
- **Monitoring:** key figures as `StatSlot`s, charts with token colours (`--chart-*`, green as the main series).
- **Settings:** server form in sections with `max-w-3xl`, danger zone "Delete server" at the end (red section).

### 5.3 Admin and auth pages

- **Users, permissions, audit:** dense tables, actions on the right, filters in one row above the table.
- **Security:** sub-tabs stay; overview with `StatSlot`s; bar chart with token colours.
- **Settings, profile:** sections with `max-w-3xl`.
- **Login, 2FA, setup 2FA:** centred panel (`max-w-md`) with `PixelIcon grass` and `pixel-heading`; the background is a subtle pixel pattern from CSS gradients (no image).
- **Sudo dialog, confirmation dialogs, toaster, 404/error page:** new style, behaviour unchanged.

## 6. Implementation with agent teams

### 6.1 Waves

- **Wave 0 – lead alone, short** (branch `feat/ui-foundation`): tokens dark/light (§3.1), fonts (§3.2), utilities (§3.3), spacing rules, the building blocks from §3.6 with fixed props (simple but working implementation), `.gitignore` entry `.superpowers/`. That commit is then the starting point for all teammates.
- **Wave 1 – five teammates in parallel**, each in their own worktree on their own branch from the wave 0 commit, with disjoint files:

| Teammate | Branch | Scope | Files |
|---|---|---|---|
| T1 Primitives | `feat/ui-primitives` | §3.5, chart colours, ConfirmDialog, ThemeToggle, Toaster, NotFound/RouteError, DataList, NativeSelect, CodeForm | `web/src/components/ui/*`, `web/src/components/charts/*`, the other `web/src/components/*.tsx` except the wave 0 building blocks |
| T2 Shell | `feat/ui-shell` | §4 complete, overview, E2E navigation | `features/layout/*`, `routes/**`, `features/servers/{server-detail-page,server-overview,sections,server-header,status-badge,address-line,power-buttons}.*`, `e2e/*` |
| T3 Home page + live | `feat/ui-live` | §5.1, console, players, monitoring | `features/dashboard/*`, `features/console/*`, `features/players/*`, `features/monitoring/*` |
| T4 Server management | `feat/ui-manage` | Files (incl. `useBlocker`), plugins, backups, server form, settings + danger zone | `features/files/*`, `features/plugins/*`, `features/backups/*`, `features/servers/{server-form,server-settings-tab,delete-server-section,properties-fields,…}.*` (everything in `features/servers` except T2's files) |
| T5 Admin + auth | `feat/ui-admin` | §5.3 | `features/{users,security,audit,settings,profile,auth,setup,sudo,webauthn}/*` |

The plan sets the exact file assignment (in particular in `features/servers/`); every file has exactly one owner. (Now: `server-detail-page` was removed in wave 1; the server areas are the layout route `routes/_app/servers/$id` with `features/servers/server-layout.tsx`.)

### 6.2 Contracts between the teammates

- Area components (`ServerOverview`, `MonitoringTab`, `ConsoleTab`, `PlayersTab`, `FilesTab`, `PluginsTab`, `BackupsTab`, `ServerSettingsTab`) take only `{ server: Server }` from wave 1 on (plugins also `kind`; overview and settings also `instance`). T2 renders them in the routes; T3/T4 change their insides. They build links to other areas themselves with `<Link to="/servers/$id/settings">`.
- The server header (T2) reads "extract running" via `useFileJob` from `features/files` (T4's API unchanged).
- Only the lead changes `web/src/index.css`, `web/package.json`/`package-lock.json` and the wave 0 building blocks. A teammate reports needs to the lead; the lead pushes to `feat/ui-foundation`, the teammates rebase.
- **i18n:** new keys only in the namespace of one's own feature; do not rename or delete existing keys while another teammate might still use them (the lead cleans up after integration). The lead resolves conflicts in `de.ts`/`en.ts`.
- **E2E:** only T2 changes navigation in `e2e/*` (tab → link via a helper `openServerSection(page, name)` in `e2e/helpers.ts`). If T3–T5 need changes to E2E specs, they report them to T2 or the lead. (Now: the helper's signature is `openServerSection(page, server, label)`.)
- **Mocks:** no changes needed; if they are, only after agreeing with the lead.

### 6.3 Tests and checks

- Every teammate: `make check`, then `make test-vm SUITE=web` (Vitest + tsc) for their change; T2 also `SUITE=e2e`. The VM lock is shared, so `SUITE=all` only once per PR, by the lead.
- Unit tests: `serverSections` (all permission combinations), route guards and redirect, the blocker in the files area, `McText`, `PingBars`, `StatSlot` (missing values → "–"), tree state in `localStorage`. Existing tests are adapted to the new structure, not deleted; semantics (roles, names, texts) stay where the spec does not explicitly change them.
- **Screenshots:** every teammate uses `make test-vm SUITE=shots` (Playwright project `screenshots`, own file `web/e2e/shots/<area>.shots.ts`, not in CI) to take screenshots of their pages against the mock in dark and light (desktop 1440 px and mobile 390 px); they land locally in `web/screenshots/` (gitignored), the teammate reviews them, and the lead shows a selection to the user. The infrastructure belongs to wave 0.
- Contrast: in wave 0 the lead checks the token pairs from §3.1 by calculation (AA: 4.5 : 1 for text, 3 : 1 for large text/UI borders).

### 6.4 PRs

1. **PR 1 – foundation:** wave 0 + T1, against `main`.
2. **PR 2 – navigation:** T2, stacked on `feat/ui-foundation`; after PR 1 is merged, rebase onto `main`.
3. **PR 3 – pages:** the lead integrates T3, T4, T5 on `feat/ui-pages` (stacked on PR 2) and resolves conflicts.

Merges only after the user approves. After the last merge: `make deploy-testvm` and acceptance (§7); `CLAUDE.md` (frontend section) and the roadmap in the foundation spec get sub-project 7 added. (Now: done.)

## 7. Acceptance criteria

Manual checklist: `docs/dev/acceptance.md` ("UI").

- Dark and light match §3 on all pages; no remains of the neutral shadcn grey.
- Navigation per §4 on desktop, tablet and mobile; every server area has its own URL, a reload stays in the area; disallowed areas redirect to the overview.
- Server header with status, address, power and key figures in every area.
- Home page as a multiplayer list per §5.1.
- Unsaved changes in the files area prompt on every navigation away.
- Spacing per §3.4 on all pages; no horizontal scrollbar at 390 px width (except in tables/console, which scroll themselves).
- Keyboard operation and visible focus everywhere; AA contrast; `forced-colors` and `prefers-reduced-motion` respected.
- CSP unchanged, no external resources; `npm run build` without warnings about missing glyphs.
- CI green (incl. E2E against the mock and the real backend).
