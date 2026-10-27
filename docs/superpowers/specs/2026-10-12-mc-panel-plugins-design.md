# mc-panel – Sub-project 3c: Plugins and mods (design spec)

- **Date:** 2026-10-12
- **Status:** implemented (PRs #36–#46, 2026-10-12)
- **Author:** Jannes (with Claude)
- **Builds on:** `2026-10-08-mc-panel-foundation-design.md` (sub-project 1), `2026-10-10-mc-panel-instances-design.md` (sub-project 2), `2026-10-11-mc-panel-console-players-design.md` (sub-project 3a), `2026-10-11-mc-panel-files-design.md` (sub-project 3b)

## 1. Goal and context

Sub-project 3 ("live operation") has three parts: 3a console/logs/players and 3b file browser are implemented, **3c plugins/mods** is this document.

### 1.1 Success

A user with `server.plugins` searches Modrinth and Hangar for plugins on a Paper or Purpur server, or Modrinth for mods on a Fabric or NeoForge server, and installs them together with their required dependencies in one click. The user sees all installed jars, including ones uploaded by hand, checks for and installs updates, and disables and removes jars.

### 1.2 Guiding principle

Unchanged: a compromised panel must not gain root on the host (sub-project 1 §3), and only the unprivileged service `mcpanel-files` writes files in data directories (sub-project 3b §1.2). The panel downloads jars from the internet and passes them to the service as a stream; the service gets no network access, and the root agent does not change.

### 1.3 Non-goals (3c)

- CurseForge mods, Spigot/SpigotMC, Bukkit.org and any source other than Modrinth and Hangar.
- Plugins/mods on vanilla and modpack servers (`modrinth`, `curseforge`): there the pack decides the content.
- Datapacks, resource packs, shaders, proxy plugins (Velocity, BungeeCord).
- Downloading files that are only linked externally (Hangar `externalUrl`): no checksum, arbitrary hosts.
- Editing plugin configuration (the Files tab exists for that), loading or unloading plugins in a running server (no hot reload).
- Automatic updates without a click.

## 2. Decisions

| Area | Decision | Reason |
|---|---|---|
| Sources | paper/purpur: plugins from Modrinth and Hangar; fabric/neoforge: mods from Modrinth | Operator's request; Hangar has no mods |
| Download | The panel downloads from fixed CDN hosts and streams to `mcpanel-files`; the service checks the checksum before the `rename` | The only way that covers Hangar and takes effect immediately without giving a service new rights (§2.1) |
| Source of truth | The disk (`plugins/` or `mods/`); the DB only remembers the origin of panel-installed jars | Jars can also come and go through the Files tab, in game or by the server itself |
| Matching | DB entry (filename + sha512), else Modrinth by hash, else "manual" with metadata from the jar | Hangar has no hash lookup; Modrinth recognises most manually uploaded jars |
| Flow | Install and update run as a job in the panel (one per server), progress is polled | Several downloads including dependencies do not fit in one request |
| Running server | Everything allowed at any time; banner "restart required" | Jars are only loaded at start; Linux replaces files by `rename` without taking the open file away from the running server |
| Permission | `server.plugins` (already created in sub-project 1), full trust, warning when granting | A plugin runs code in the container |
| Icons | Proxy in the panel | CSP (`img-src 'self'`) stays unchanged, the browser contacts no third parties |

### 2.1 Rejected alternatives

- **Declarative through itzg (`MODRINTH_PROJECTS`):** fits the desired-state model, but itzg does not know Hangar, changes only take effect at restart, and manual jars would be left out. Also, sub-project 2 §5.4 deliberately allows no further itzg variables.
- **`mcpanel-files` downloads itself:** would need network access (today `RestrictAddressFamilies=AF_UNIX`) and would turn the file service into an internet client.
- **Checksum only in the panel:** the panel would have to hold back the last byte until the checksum matches. Checking in the service before the `rename` is simpler and also protects against errors on the way from panel to service.

## 3. Architecture

```
Browser ──/api/servers/{id}/plugins/*──▶ panel: internal/plugins ──HTTPS, fixed hosts──▶ api.modrinth.com, hangar.papermc.io
                                           │ search, resolve, jobs, inventory,          ◀── jar/icon from cdn.modrinth.com,
                                           │ DB, audit, icon proxy                          hangarcdn.papermc.io
                                           └──Unix socket──▶ mcpanel-files: /v1/jars, /v1/upload (+checksum), rename, delete, mkdir
```

- **panel:** new package `internal/plugins` (handler, routes, inventory, resolving, jobs, icon proxy) with sub-package `internal/plugins/upstream` (clients for Modrinth and Hangar, download, cache). Wired in `cmd/panel/app.go`.
- **Service:** new operation `jars` in `internal/files` (listing with checksums and metadata), `upload` extended with a checksum; wire types and client in `internal/files/filesapi`.
- **agent:** no change. The panel only uses the existing actual state (`state`, `started_at`).

### 3.1 Server types and compatibility

| Type | Folder | Type in UI | Modrinth `project_type` | Modrinth loader | Hangar |
|---|---|---|---|---|---|
| paper | `plugins/` | Plugins | `plugin` | `paper`, `spigot`, `bukkit` | platform `PAPER` |
| purpur | `plugins/` | Plugins | `plugin` | `purpur`, `paper`, `spigot`, `bukkit` | platform `PAPER` |
| fabric | `mods/` | Mods | `mod` | `fabric` | – |
| neoforge | `mods/` | Mods | `mod` | `neoforge` | – |
| vanilla, modrinth, curseforge | – | no tab | – | – | – |

- **Game version** = the instance's `version`. The panel resolves `LATEST` through the existing version catalog (`internal/servers/catalog`, newest version of the type); if the catalog is unreachable, search, versions, resolve, install and updates return `502 catalog_unavailable`, and the inventory keeps working (`game_version: null`).
- **Client mods:** for mods the search filters on `server_side` ∈ `required|optional`; projects with `server_side: unsupported` count as incompatible (also as a dependency).
- **Channels:** Modrinth `version_type` (`release|beta|alpha`) is taken over; for Hangar the channel `Release` (case-insensitive) becomes `release`, every other one `beta`.

## 4. Upstreams (`internal/plugins/upstream`)

Fixed base URLs (overridable in tests), checked against the live services on 2026-10-12; trimmed real responses are in `testdata/`. User-Agent as for the catalogs: `mc-panel/<version> (https://github.com/Bernotese/mc-panel)`.

| Purpose | Modrinth (`https://api.modrinth.com`) | Hangar (`https://hangar.papermc.io`) |
|---|---|---|
| Search | `GET /v2/search?query=&facets=&index=&offset=&limit=20`; facets `project_type:<t>`, `categories:<loader>` (OR across the loaders), `versions:<gv>`, for mods `server_side:required` OR `server_side:optional`; `index=relevance` with a search term, else `downloads` | `GET /api/v1/projects?q=&platform=PAPER&version=<gv>&sort=-downloads&offset=&limit=20` |
| Project | `GET /v2/project/{id}` | `GET /api/v1/projects/{slugOrId}` |
| Versions | `GET /v2/project/{id}/version?loaders=[…]&game_versions=[<gv>]` | `GET /api/v1/projects/{slugOrId}/versions?platform=PAPER&platformVersion=<gv>&limit=25` |
| Version | `GET /v2/version/{id}` | `GET /api/v1/projects/{slugOrId}/versions/{name}` (the version name is the ID) |
| Hash matching | `POST /v2/version_files {hashes, algorithm: "sha512"}` (up to 500 hashes) | – |
| Updates | `POST /v2/version_files/update {hashes, algorithm, loaders, game_versions, version_types}` | Version list, newest in the matching channel |
| Dependencies | `dependencies[]` with `dependency_type = required`, `project_id`, optional `version_id` | `pluginDependencies.PAPER[]` with `required = true`; `projectId` (Hangar) or `externalUrl` (external) |
| File | `files[]`: `primary = true`, else the first `.jar`; `url`, `filename`, `size`, `hashes.sha512` | `downloads.PAPER.fileInfo` (`name`, `sizeBytes`, `sha256Hash`) and `downloadUrl`; `fileInfo = null` with `externalUrl` → external only |

- **Limits:** timeout 10 s per API request, responses ≤ 5 MiB, error/status ≠ 2xx/broken JSON → `502 plugin_source_unavailable`. The Modrinth rate limit (300/min per IP) is respected: an upstream `429` → also `502` with a message.
- **In-memory cache:** search 5 min, projects and version lists 15 min, hash matching 1 h (including "unknown"), updates 1 h per server. Concurrent identical queries share one call (like `catalog`). At most 1000 entries per cache, oldest evicted first.
- **Downloads and icons** only from `https://cdn.modrinth.com/` and `https://hangarcdn.papermc.io/`; redirects only to these hosts and only over HTTPS (at most 3). Other hosts (also from upstream responses) → step error or `400 validation_failed`.
- **Jar download:** ≤ 256 MiB (`size` from the API, else abort), deadline 60 s per read, no overall deadline. If the CDN's `Content-Length` differs from the API size, the panel aborts before passing anything on.
- **Filename:** the upstream name must match `^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}\.jar$` and pass `filesapi.CleanPath`; otherwise `<slug>-<version>.jar`, with invalid characters replaced by `-` and cut to 128 characters.

## 5. Service `mcpanel-files`

### 5.1 `POST /v1/jars`

Body `{id, dir}`, `dir` ∈ `plugins|mods`. Response `{jars: [{name, enabled, size, mtime, sha256, sha512, meta}], truncated}`.

- Lists regular files at the top level of `dir` ending in `.jar` (`enabled: true`) or `.jar.disabled` (`enabled: false`), case-insensitive; symlinks, folders and special files are skipped (rules from sub-project 3b §3.2: `Lstat`, `O_NOFOLLOW|O_NONBLOCK`, `os.SameFile`). At most 500 jars (`truncated: true`). If `dir` is missing or not a folder → empty list.
- **Checksums** sha256 and sha512 in one pass; jars > 256 MiB, sparse files (allocated blocks < size) and everything beyond the budget of 2 GiB read bytes per request get no checksums (`""`) (cache hits cost no budget; jars without a checksum are not cached). In-memory cache keyed by (device, inode, size, mtime, ctime) – the container cannot reset the ctime –, at most 4096 entries; if a file changes while it is read, there are neither checksums nor a cache entry. At most one hash pass per instance and 2 in the service (otherwise the request waits); client deadline 2 min.
- **Metadata** (`meta`, best-effort, else `null`): the service opens the jar as a zip (central directory ≤ 100 000 entries and ≤ 16 MiB; extraction keeps 64 MiB) and reads at most one file ≤ 64 KiB, in this order:

| File | `kind` | Fields |
|---|---|---|
| `paper-plugin.yml`, `plugin.yml` | `plugin` | `name`, `version` (top-level keys, simple `key: value` lines, quotes removed; no YAML parser) |
| `fabric.mod.json` | `mod` | `id`, `name`, `version` |
| `META-INF/neoforge.mods.toml` | `mod` | first `[[mods]]` entry: `modId`, `displayName`, `version`; `${file.jarVersion}` → `Implementation-Version` from `META-INF/MANIFEST.MF` (≤ 64 KiB), else empty |

  Values are cut to 128 characters, control characters removed. A broken jar yields `meta: null`, never an error for the whole list.

### 5.2 Upload with checksum

`PUT /v1/upload` gets the optional query parameters `sha512` (128 hex characters) and `sha256` (64 hex characters), at most one of them. If one is set, the service computes sha256 and sha512 while writing the temp file, compares before `fsync`/`rename` and answers `400 hash_mismatch` on a mismatch (temp file gone, target unchanged). Success: `201 {size, sha256, sha512}` (without a parameter `{size}` as before). New service error code `hash_mismatch` (400).

## 6. panel

### 6.1 Data model

Migration `internal/store/migrations/00005_plugins.sql`, queries in `internal/store/queries/plugins.sql` (prefix `Plugins…`):

```sql
CREATE TABLE plugin_installs (
    server_id    INTEGER NOT NULL REFERENCES servers (id) ON DELETE CASCADE,
    filename     TEXT    NOT NULL,   -- filename without ".disabled"
    sha512       TEXT    NOT NULL,
    source       TEXT    NOT NULL CHECK (source IN ('modrinth', 'hangar')),
    project_id   TEXT    NOT NULL,   -- Modrinth ID or Hangar ID (number as text)
    project_slug TEXT    NOT NULL,
    title        TEXT    NOT NULL,
    version_id   TEXT    NOT NULL,   -- Modrinth version ID or Hangar version name
    version_name TEXT    NOT NULL,
    channel      TEXT    NOT NULL CHECK (channel IN ('release', 'beta', 'alpha')),
    installed_at INTEGER NOT NULL,
    PRIMARY KEY (server_id, filename)
);
```

An entry only counts if a jar of that name (with or without `.disabled`) with exactly this sha512 is on disk; otherwise the inventory deletes it when reading.

### 6.2 Inventory

`GET /servers/{id}/plugins` queries `/v1/jars` and matches each jar:

1. DB entry with the same name and the same sha512 → source from the DB (`installed_by_panel: true`).
2. Otherwise Modrinth `version_files` by sha512 (cache 1 h) → source `modrinth` with project (title, slug via `GET /v2/project`, cached) and version.
3. Otherwise `manual` with `meta`.

If Modrinth is unreachable, the affected jars stay `manual` and the response carries `source_errors: ["modrinth"]`. `restart_needed` is `true` if the server is running and the panel has performed a plugin change for it since the container's `started_at` (a `started_at` of 0 counts as before). The time of the last change is stored per server in `plugin_changes` (never moves back, never reset), recorded as soon as the files service confirmed a change on disk (install/update steps also when a later step fails, toggle, delete), so the notice survives a panel restart and clears with the first container start after the change. Without a recorded change, or if the server does not run, it is `false`; after a change with the agent or Docker unavailable it is `null` (unknown, shown as its own notice without a restart button), never `false`. Dev instances: a dev sync stops the dev before it replaces its data directory, so its next start (wake) is after every earlier change; the sync copies `plugin_installs` but not `plugin_changes`, and a sleeping (stopped) dev needs no notice.

### 6.3 Resolving

`resolve` and every job build the same plan from the project and the requested version (if missing: newest compatible `release`, else the newest compatible one at all):

- Required dependencies recursively up to depth 5, at most 20 packages in the plan; cycles are detected. Depth, plan size and already seen projects are checked before every upstream call; per version at most 50 required dependencies count (the rest becomes an `incompatible` entry with `too_many`), and one resolution makes at most 80 upstream calls. Modrinth dependency with `version_id` → exactly that version (must be compatible), else newest compatible `release`. Hangar dependency with `projectId` → newest compatible version in channel `Release`, else the newest.
- A project counts as already installed if the inventory has a jar with the same source and project ID (enabled or disabled) → `already`, left untouched. Dependencies are not matched across sources. For the **requested** project: without `version` or with the installed version → `already`; with a different `version` → plan entry that replaces the existing jar (version change, flow like an update in §6.4).
- Externally linked dependency (Hangar `externalUrl`) → `external` with name and link. Dependency without a compatible downloadable version → `incompatible`; then the plan cannot run (`409 plugin_incompatible`). Requested version only external → `409 plugin_not_downloadable`.
- Plan entry: `{source, project_id, slug, title, version_id, version_name, channel, filename, size, required_by}`.

### 6.4 Jobs

- In memory, at most one per server (otherwise `409 conflict`) and four in the panel (otherwise `429 rate_limited`); the last result stays until the next job or a panel restart. A panel restart aborts running jobs; the service cleans up temp files anyway.
- **install:** recompute the plan (the preview is not trusted), create the target folder with `mkdir` if needed, then for each plan entry (dependencies first): download → `PUT /v1/upload` with checksum (Modrinth `sha512`, Hangar `sha256`) → DB entry with the sha512 from the response. If the filename already exists (with or without `.disabled`) and belongs to another project or is manual → step error `file_exists`, the job aborts. Steps already installed stay installed. If the service is busy (`busy`), a step retries download and upload up to three times (wait from 2 s, doubled).
- **update:** per selected jar (empty = all with an update): download the new version, upload it under the new name (same name → `overwrite=1`), disabled jars stay disabled (upload as `<name>.jar.disabled`), then delete the old jar and replace the DB entry. Before the download the step checks the sha512 of the jar to be replaced (if it changed since the update check → `file_exists`, updates cache discarded); if the old jar cannot be deleted, it takes the new one back (if that fails too, the message names both files). New required dependencies of the new version are installed along as with `install`; each update group is resolved on its own, and a project that the same job already installed is not downloaded a second time. Duplicate names in `files` count once. An error affects only its step; the job continues and ends `failed` if a step failed.
- States: job `running|done|failed`, step `pending|downloading|done|failed|skipped`; step errors as a code (`download_failed`, `hash_mismatch`, `file_exists`, `insufficient_storage`, `files_unavailable`, `source_unavailable`) plus text without host paths.

### 6.5 Updates

`GET /servers/{id}/plugins/updates` (cache 1 h per server, `?refresh=true` or `?refresh=1` bypasses it; jobs, toggling and deleting discard it) returns `{updates: [{file, from: {id, name, channel}, to: {id, name, channel}}], checked_at}`. Modrinth jars (DB or hash matching) through `version_files/update` with `version_types = [release]`, `[release, beta]` for a beta jar, or all three for alpha; Hangar jars from the DB through the version list, newest downloadable version in the same or a more stable channel, only if it was published later than the installed one. Manual jars get no updates.

### 6.6 API

All routes through `*rbac.Router` with `rbac.Server(rbac.PermServerPlugins, "id")`, described in `api/openapi.yaml`, with an MSW mock. Server without an instance → `404 not_found`; type without plugins/mods → `409 plugins_unsupported` (all routes except `icon`).

| Route | Content |
|---|---|
| `GET /servers/{id}/plugins` | `{kind: plugin\|mod, dir, game_version, sources, jars: [{file, enabled, size, mtime, source: modrinth\|hangar\|manual, project: {id, slug, title, page_url, icon_url}?, version: {id, name, channel}?, meta?, installed_by_panel}], truncated, restart_needed, source_errors}` |
| `GET /servers/{id}/plugins/search?source=&q=&offset=` | `{hits: [{source, project_id, slug, title, description, author, downloads, icon_url, page_url, installed}], total, offset}`; `q` ≤ 100 characters, `offset` ≤ 1000 |
| `GET /servers/{id}/plugins/versions?source=&project=` | `{versions: [{id, name, channel, published_at, downloadable, external_url?}]}` (compatible, newest first, at most 25) |
| `GET /servers/{id}/plugins/resolve?source=&project=&version=` | `{install: [PlanEntry], already: [...], external: [{name, url}], incompatible: [{title, reason}]}` |
| `POST /servers/{id}/plugins/install` | `{source, project, version?}` → `202` with the job (`PluginJob`); nothing to do (everything already installed) → `409 conflict` |
| `GET /servers/{id}/plugins/updates` | §6.5 |
| `POST /servers/{id}/plugins/update` | `{files: [string]}` → `202` with the job; file without an update or no update at all → `400 validation_failed` |
| `POST /servers/{id}/plugins/toggle` | `{file, enabled}` → `204`; `rename` in the service |
| `POST /servers/{id}/plugins/delete` | `{file}` → `204`; also deletes the DB entry |
| `GET /servers/{id}/plugins/jobs` | `{job: null \| {id, kind: install\|update, state, steps: [{title, file, state, error_code, error}], started_at, finished_at}}` |
| `GET /servers/{id}/plugins/icon?url=` | Image (§6.8) |

- `source` ∈ `modrinth|hangar` (`hangar` only for plugins, otherwise `400 validation_failed`); `project`/`version` ≤ 128 characters, `^[A-Za-z0-9._+-]+$`. `file` is a plain filename without `/`, ends in `.jar` or `.jar.disabled` and passes `filesapi.CleanPath`; the path in the service is always `<dir>/<file>`. `toggle` and `delete` only act on names that `/v1/jars` lists (otherwise `404`, also for a truncated list); values for `project`/`version` that consist only of `.` are invalid.
- `icon_url` and `project.icon_url` in responses already point to the icon proxy (`/api/servers/{id}/plugins/icon?url=…`), `page_url` to the project page (`https://modrinth.com/<plugin|mod>/<slug>` or `https://hangar.papermc.io/<owner>/<slug>`).
- **CSRF** applies to all `POST`. **Rate limit** per account across `search`, `versions`, `resolve`, and `updates` with `refresh`: token bucket with 1 call/s and burst 20, so about 60 per minute (otherwise `429 rate_limited` with `Retry-After`); an update check that does not come from the cache counts as well (also for `POST …/update`).
- Unknown project or unknown version at the upstream → `404 not_found` (as a dependency: `incompatible`). The panel only passes on external links (`external_url`, `external[].url`) with `http`/`https`. (Now, SEC-30: the UI links only `https` URLs and shows the target host next to each link; `http` links appear as text.)
- **Error mapping** of the service: `exists` → `409 file_exists`, `not_found` → `404 not_found`, `not_regular`/`invalid` → `400 validation_failed`, everything else (including `busy`, `insufficient_storage`, `too_large`, `hash_mismatch`, an old service without `/v1/jars`) → `503 files_unavailable`; in jobs they appear as step errors.

### 6.7 Error codes

New in `internal/httpx/errors.go` and in the contract's enum:

| Code | Status | When |
|---|---|---|
| `plugins_unsupported` | 409 | Server type without plugins/mods |
| `plugin_source_unavailable` | 502 | Modrinth or Hangar unreachable or answering with something unusable |
| `plugin_not_downloadable` | 409 | Selected version only available externally |
| `plugin_incompatible` | 409 | Project or required dependency without a compatible version |

Existing and used: `conflict` (job running), `catalog_unavailable` (`LATEST` cannot be resolved), `rate_limited`, `validation_failed`, `not_found`.

### 6.8 Icon proxy

- `url` must start with `https://cdn.modrinth.com/` or `https://hangarcdn.papermc.io/` (≤ 512 characters), otherwise `400 validation_failed`; redirects as in §4.
- Image ≤ 256 KiB, type by magic bytes: only PNG, JPEG, WebP, GIF (no SVG) → otherwise `502 plugin_source_unavailable`. Response with the detected `Content-Type`, `X-Content-Type-Options: nosniff`, `Content-Security-Policy: default-src 'none'`, `Cache-Control: private, max-age=86400`.
- In-memory cache (LRU, 32 MiB, 24 h). Not part of the rate limit from §6.6, but at most 8 concurrent upstream fetches in the panel.

### 6.9 Audit

New actions in `internal/audit/types.go`: `plugin.install` `{source, project, version, file}`, `plugin.update` `{file: new name, from, to: version names}` (likewise a version change through `install`, then `plugin.install` with `from`), `plugin.remove` `{file}`, `plugin.toggle` `{file, enabled}`. Target `server:<id>`, result `success`/`failure`, one entry per jar (also for dependencies); the actor is the user who started the job. Inventory, search, versions, resolve, update check and icons are not audited, nor are rejections by permissions, validation and rate limit (as in sub-projects 1–3b).

## 7. Frontend

Existing shadcn components; texts `de`/`en`; usable on mobile.

### 7.1 Tab "Plugins" or "Mods"

Visible with `server.plugins` on paper, purpur, fabric and neoforge; title according to `kind`.

- **Installed:** list with icon, name (project title, else `meta.name`, else filename), version, source (Modrinth/Hangar/manual), enabled/disabled switch, menu (open project page, change version → install dialog with the version preselected, remove with confirmation), update badge. Manual jars without a match: hint "Not matched – updates not possible". Header: "Check for updates", "Update all (n)". On a phone cards instead of a table. `source_errors` → hint "Modrinth currently unreachable – matching incomplete".
- **Discover:** search field (debounced), source toggle Modrinth | Hangar (Modrinth only for mods), hits with icon, title, author, downloads, short description, badge "installed", button "Install"; without a search term the most popular matching projects; "Load more". Descriptions (Markdown/HTML) are not rendered, only the short description and a link to the project page.
- **Install dialog:** version selection (default: newest `release`; beta/alpha marked; versions only available externally greyed out with "Only available externally", a link and a pointer to the Files tab), below it the preview from `resolve`: "Installed along", "Already present", "Needs manual install" (with links), "Incompatible" (blocks "Install").
- **Job:** progress per jar (waiting, downloading, done, skipped, error with translated reason `plugins.step.error.*`), polled every 2 s while `running`; install, update, toggle and remove buttons disabled. When it ends the inventory is reloaded.
- **Banner** on `restart_needed`: "Restart required for the changes to take effect", with a restart button for `server.power`.
- Upstream unreachable: Discover shows an error state with "Try again"; Installed stays usable.

(Now, issue #85: Discover shows a source badge on each hit and a "Results from <source>" link, the source toggle is a tab bar, "Hide installed" is on by default (stored in `localStorage` `mcpanel.plugins.hideInstalled`) and loads further pages by itself (at most 4 per step) plus infinite scroll with "Load more" as a fallback. Installed has a name filter and status and origin filters with a counter and "Reset filters". Issue #78 adds "Import modpack (.mrpack)" for mods; see `2026-10-14-mc-panel-mrpack-import-design.md`.)

### 7.2 User management

When ticking `server.plugins`: hint "'Plugins/mods' allows running code in the Minecraft server. Grant it only to people you fully trust."

### 7.3 Mocks

MSW mocks per operation: small catalog (Modrinth and Hangar, one Hangar project external only, one with a required dependency, one beta version, one client-only mod that does not appear), inventory with a panel-installed, a hash-matched and a manual jar, simulated job with progress, one available update, icons as small PNGs from the mock. `make preview` shows everything without a backend.

## 8. Development, operation and CI

- **Mac:** only `make check` (CLAUDE.md). `make dev` needs nothing new; in dev mode the panel talks to the real upstreams.
- **Test VM / production:** no new configuration. The panel needs outbound HTTPS to `api.modrinth.com`, `cdn.modrinth.com`, `hangar.papermc.io`, `hangarcdn.papermc.io` (as for the catalogs).
- **Release:** unchanged.

## 9. Tests

| Level | Content |
|---|---|
| `plugins/upstream` | `httptest` with real trimmed responses from `testdata/` (search of both sources, versions, `version_files`, `version_files/update`, Hangar with internal and external download and dependencies); facets and query parameters, host allowlist including redirects to foreign hosts and `http`, size limit, differing `Content-Length`, timeout, cache and shared calls, filename fallback |
| Resolving | Depth and package limit, cycle, already installed (also disabled), external, incompatible, fixed `version_id`, channel choice, client-only mod |
| `files` | `/v1/jars`: `.jar`/`.jar.disabled`, symlinked jar and folder skipped, limit 500, checksums and cache (changed mtime → recomputed), metadata from `paper-plugin.yml`, `plugin.yml`, `fabric.mod.json`, `neoforge.mods.toml` with `${file.jarVersion}`, broken and oversized jar → `meta: null`; upload with a wrong checksum leaves no file and the target unchanged, both parameters at once → `400 invalid` |
| panel `plugins` | Fake upstream + fake service + `storetest`: permissions (`plugins`, only `view`, admin, foreign server), CSRF, type without plugins, `LATEST` resolution, inventory matching (DB, hash, manual, orphaned entry), install job with dependency, abort on `file_exists`, update with a new filename and a disabled jar, one job per server, job limit, `restart_needed`, audit entries, rate limit, icon proxy (SVG, foreign host, too large) |
| Contract | OpenAPI ↔ routes, error codes ↔ enum, mock per operation |
| Frontend | Vitest: inventory display (name fallback, badges), resolve preview, job progress; Playwright against the mock: search → install with dependency → disable → update → remove |
| CI acceptance | `scripts/ci-instances.sh` against the real upstreams and the Paper server: install a small Modrinth plugin and a Hangar plugin with a required dependency, restart the server, check via RCON `plugins`, disable a jar and check again. Upstream errors are retried once and then abort with a clear message (no silent skipping). |

Conventions from sub-projects 1–3b apply unchanged.

## 10. Acceptance criteria

Automatically in CI: 1 plus the service side of 2 and 5. Manually by the user on the test VM: 2–7 (`docs/dev/acceptance.md` ("Plugins and mods")).

1. All CI jobs green.
2. Install LuckPerms (Modrinth) and ViaBackwards (Hangar, pulls in ViaVersion) on a Paper server; after the restart they are active.
3. Install a mod on a Fabric server that brings Fabric API along; the server starts with it.
4. A Modrinth jar of an older version uploaded through the Files tab is matched and can be updated.
5. Disable a jar; after the restart it is not loaded; enabling it again loads it again.
6. A user with only `server.view` sees no Plugins tab, the API answers `403`.
7. All installs, updates, toggles and removals are in the audit log; `panel audit verify` reports OK.
