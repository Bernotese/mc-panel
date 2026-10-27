# mc-panel – Sub-project 3c addendum: mrpack import (design spec)

- **Date:** 2026-10-14
- **Status:** implemented (PRs #93, #94, 2026-10-15)
- **Author:** Jannes (with Claude)
- **Builds on:** `2026-10-12-mc-panel-plugins-design.md` (sub-project 3c)

## 1. Goal

A user with `server.plugins` picks a `.mrpack` file (an export of a Modrinth instance) in the mods area of an existing **Fabric or NeoForge server**. The panel shows what changes: new mods, updates, unchanged ones, skipped ones, and mods that are not in the pack and stay installed. After confirmation it installs the **server-side mods** of the pack as a job.

This is different from the server type "Modrinth modpack": that type installs a pack by project ID through itzg when the server is created.

### 1.1 Decisions from issue #78

| Question | Decision |
|---|---|
| Scope | Mods only. `overrides/` and `server-overrides/` (configs etc.) are ignored entirely. |
| Server-side | Only mods that run on the server (pack field `env.server` and Modrinth `server_side`, §3.2). Client mods are skipped and shown as "client only". |
| Download sources | Modrinth only. The panel never downloads from a URL in the pack, but through the Modrinth API by hash (§3.1). Files Modrinth does not know are listed as "upload manually". |
| Loader does not match | Import refused. |
| Minecraft/loader version does not match | Warning. Admins get the button "Switch server version to X"; the import is possible anyway. |
| Mods already installed | Stay installed. Mods from the pack in another version are brought to the pack version. A diff comes first. |
| Running server | Allowed, afterwards the banner "restart required" as in sub-project 3c. |

### 1.2 Non-goals

- Configs, resource packs, shader packs, datapacks or files outside `mods/` from the pack.
- Downloads from GitHub/GitLab or other hosts from `downloads[]`.
- Server types other than fabric/neoforge (paper/purpur → `409 plugins_unsupported`), packs for Forge or Quilt.
- Resolving dependencies: the pack is complete. What it does not contain, the import does not install.
- Removing mods that are not in the pack (no "sync").
- Creating new servers from a local `.mrpack`.

## 2. Flow

```
Browser: pick file ─▶ read the ZIP locally, unpack only modrinth.index.json (fflate)
        ─▶ POST /servers/{id}/plugins/mrpack/preview {index}      ─▶ show diff
        ─▶ [admin, optional] PATCH /servers/{id} (switch version) ─▶ preview again
        ─▶ POST /servers/{id}/plugins/mrpack/import {index, exclude[], accept_mismatch}
        ─▶ 202 PluginJob ─▶ progress as with install/update (GET …/plugins/jobs)
```

**Why unpack in the browser:** a `.mrpack` with overrides can be hundreds of MB, but only the index file is needed (usually < 500 KiB). This way no large file is uploaded, the panel parses no foreign ZIP and the 1 MiB limit for request bodies stays. The panel still does not trust the index: it uses only file names, hashes and the `env` fields, and only after strict validation (§4.1).

## 3. Plan (identical for preview and import)

`preview` and `import` compute the same plan. The import computes it again and does not trust the preview.

1. **Validate the index** (§4.1). Only entries with `path` = `mods/<name>.jar` count. Other paths (e.g. `resourcepacks/`, `config/`) are silently ignored and only counted (`ignored_files`).
2. **Compatibility:** the pack's `dependencies` against the server:
   - Loader: pack `fabric-loader` ↔ server `fabric`, pack `neoforge` ↔ server `neoforge`. If that does not match, or the pack has `forge`/`quilt-loader` → `409 mrpack_incompatible`. (Now: a pack that names no loader is refused the same way.)
   - `minecraft` ≠ the resolved game version of the server (`LATEST` through the catalog, as in sub-project 3c §3.1) → `version_mismatch.minecraft = {pack, server}`.
   - Loader version ≠ the server's `loader_version` (only if the instance has a fixed version) → `version_mismatch.loader = {pack, server}`.

   (Now: the plan reports this as `game_version: {pack, server}`, `loader: {name, pack, server}` (`server` null when the instance has no fixed loader version) and a single `version_mismatch: bool`, see §4.)
3. **Matching by hash:** all sha512 of the mod entries in one call of `POST /v2/version_files` (existing client, cached as for the inventory matching, up to 1000 hashes per call, split otherwise). Hits return project, version and the file with its **Modrinth URL** (`cdn.modrinth.com`, as in sub-project 3c). For the `server_side` check the projects are added (`GET /v2/projects?ids=…`, batch, cached). (Now: the batches are 500 hashes per `version_files` call and 100 IDs per `projects` call. If the Modrinth matching of the installed jars fails, the plan fails as well (`plugin_source_unavailable`), because an update would otherwise look like a name clash.)
4. **Classify** each mod entry (in this order):
   - `env.server == "unsupported"` or project `server_side == "unsupported"` → `skip` (`client_only`).
   - no Modrinth hit → `skip` (`not_on_modrinth`), with the file name so the user can upload it through the Files tab. (Now: also when Modrinth names a version none of whose files carries the hash.)
   - Modrinth version without the server's loader (`fabric` or `neoforge`) → `skip` (`incompatible`).
   - the same sha512 is already in `mods/` (enabled or disabled) → `unchanged`.
   - the same Modrinth project is in `mods/` in another version (inventory matching from sub-project 3c §6.2) → `update` (`replaces: <old file name>`). A disabled jar stays disabled.
   - the target file name exists and belongs to another project or is manual → `skip` (`file_exists`). (Now: also when an earlier entry of the same pack already claimed the target name.)
   - otherwise → `install`.
   - `optional: true` if `env.server == "optional"`. Such entries are preselected and can be deselected.
5. **Stays installed:** all jars in `mods/` that no plan entry matches → `kept[]` (display only).

### 3.1 Why by hash instead of `downloads[]`

According to the format, `downloads[]` may also point to GitHub/GitLab, and the URL would come unchecked from a user's file. Through `version_files` the URL comes from Modrinth itself. The same call also returns project and version for the diff, the DB (`plugin_installs`) and later updates. The sha512 from the pack is the same as on Modrinth. `UploadChecked` checks it in the files service before the `rename` (sub-project 3c §5.2).

### 3.2 Server-side

Modrinth has `client_side`/`server_side` (`required|optional|unsupported`) per project, the pack has `env.server` per file. An entry is skipped as soon as either of the two says `unsupported`. If `env` is missing, the project alone decides.

## 4. API (`internal/plugins`, permission `server.plugins`, CSRF)

| Route | Behaviour |
|---|---|
| `POST /servers/{id}/plugins/mrpack/preview` | Body `MrpackIndex` → `200 MrpackPlan` |
| `POST /servers/{id}/plugins/mrpack/import` | Body `{index: MrpackIndex, exclude: [sha512], accept_mismatch: bool}` → `202 PluginJob` |

- `MrpackPlan`: `{pack: {name, version_id}, game_version: {pack, server}, loader: {name, pack, server}, version_mismatch: bool, entries: [{file, sha512, action: install|update|unchanged|skip, reason?, optional, project?: {id, title, icon_url}, version?: {id, name}, replaces?}], kept: [file], ignored_files: int}`. (Now: `project` and `version` are the `PluginProjectRef`/`PluginVersionRef` of sub-project 3c and carry more fields (slug, page URL, channel); optional fields are `null` rather than absent.)
- Errors: `409 plugins_unsupported` (server type), `409 mrpack_incompatible` (loader), `409 mrpack_version_mismatch` (import with a mismatch and without `accept_mismatch`), `409 conflict` (a job is running or there is nothing to do), `400 validation_failed` (invalid index, `exclude` names a hash that is not an optional plan entry), `429`/`502`/`503` as in sub-project 3c. New codes go into `internal/httpx/errors.go` and the contract. (Now: a body above 1 MiB answers `413 payload_too_large`.)
- Rate limit as for `resolve` or `install` (existing limiter).

### 4.1 Index validation (strict; unknown fields are ignored because the format may grow)

- `formatVersion == 1`, `game == "minecraft"`, `name` 1–128 characters, `versionId` ≤ 128 characters.
- `files` at most 2000 entries. `path` relative, without `..`, without backslash, without leading `/`, ≤ 255 bytes. For mods exactly `mods/<name>` applies, with a `<name>` that passes the file name check of sub-project 3c (`upstream/filename.go`) and ends in `.jar`. (Now: paths with control characters or empty or `.` segments are refused too. A path that is not exactly `mods/<name>.jar` (e.g. a subdirectory of `mods/`) is not refused but counted in `ignored_files`. A `<name>` that fails the file name check is not refused either: the target file becomes `<slug>-<version>.jar` (`upstream.FileName`), as for regular installs.)
- `hashes.sha512` required (128 hex characters, normalised to lowercase), `hashes.sha1` optional. Duplicate sha512 or duplicate target names → `400`. (Now: target names are compared case-insensitively; a present `sha1` must be 40 hex characters.)
- `env.server`/`env.client` ∈ `required|optional|unsupported`, if present. `downloads` and `fileSize` are not evaluated.
- `dependencies`: map from key to version, key ∈ `minecraft|fabric-loader|neoforge|forge|quilt-loader`, versions per instancespec's `versionRe`. (Now: `files` and `dependencies` are required, `minecraft` is required, and at most one loader may be named; an unknown key gives `400`.)

### 4.2 Job

New job kind `mrpack` in the existing registry (one per server, four in the panel). The steps are the `install` and `update` entries without the deselected optional ones. Mechanics per step as in sub-project 3c §6.4: `install` like install, `update` like update (upload the new jar, delete the old one, replace the DB entry, disabled stays disabled). A failed step does not end the job; the job then ends `failed`. Audit per jar `plugin.install` or `plugin.update` with `details.via = "mrpack"`, plus **one** entry `plugin.mrpack_import` at the end with pack name, version and counters (installed/updated/skipped/failed). The updates cache and the inventory are discarded as with the other jobs. (Now: the counters also include `unchanged`; `skipped` counts the skipped and the deselected optional entries.)

## 5. Frontend (`web/src/features/plugins`)

- Button **"Import modpack (.mrpack)"** in the header of the mods area, only for fabric/neoforge. It opens a file dialog (`accept=".mrpack"`).
- Reading with `fflate` (new dependency, small, no worker required): only `modrinth.index.json` is unpacked (filter before unpacking). Index > 5 MiB, missing index, invalid JSON or the file is not a ZIP → error message in the dialog. All other entries are never unpacked. (Now: `web/src/features/plugins/mrpack-read.ts` does not unzip with an fflate filter. It reads the end of the file and the ZIP central directory, then loads only the bytes of `modrinth.index.json` (`Blob.slice`) and inflates them with fflate `Inflate`; the 5 MiB limit is checked in the central directory and again while inflating. ZIP64 archives and entries, encrypted entries and compression methods other than stored and deflate are refused as unsupported. Before sending, `downloads` and `fileSize` are stripped from `files[]`; a body still above 1 MiB gives the error `too_large_to_send`.)
- **Diff dialog** with groups: *New* (n), *Updated* (old → new), *Unchanged*, *Skipped* (reason: client only / not on Modrinth – please upload manually / incompatible / file name taken), *Stays installed (not in the pack)*, note "x files outside mods/ are ignored". Optional entries have a checkbox (preselected). Icons through the existing proxy.
- **Version mismatch:** warning box "Pack for Minecraft X / Fabric Loader Y, server: …". Admins see "Switch server version to X": `PATCH /servers/{id}` with `version` (and `loader_version`), sudo dialog as usual, then the preview is loaded again. Without switching, the import needs the checkbox "Import anyway" (`accept_mismatch`).
- Afterwards progress with `job-progress` and the banner "restart required" as with install/update.
- MSW mocks for both routes, Vitest for the parser, the diff dialog and error cases, one Playwright test against the mock (fixture `.mrpack` under `web/e2e/fixtures/`).

## 6. Tests and acceptance

- Go: index validation (table tests incl. path attacks), classification with a fake upstream (all `action`/`reason` cases), import job (install + update + disabled + failing step), error codes, route rule and OpenAPI comparison (existing tests).
- CI acceptance (`scripts/ci-instances.sh`): for the existing Fabric test server the script builds a small `modrinth.index.json` (Fabric API = `install`, a client mod such as Sodium = `skip client_only`, an unknown hash = `skip not_on_modrinth`) and sends it to `preview` and `import`. Afterwards it checks `mods/` through the inventory. (Now: the script creates its own Fabric server `cimrpack` for this, builds the index from the real Modrinth versions with `downloads` pointing elsewhere on purpose, and also checks that a NeoForge pack answers `409 mrpack_incompatible`, that the jar was written by the files service and that `plugin.mrpack_import` is audited.)
- Manual acceptance: `docs/dev/acceptance.md` ("Plugins and mods").
