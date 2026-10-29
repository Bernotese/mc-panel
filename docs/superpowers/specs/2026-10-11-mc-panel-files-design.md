# mc-panel – Sub-project 3b: File browser (design spec)

- **Date:** 2026-10-11
- **Status:** implemented (PRs #26–#33, 2026-10-11)
- **Author:** Jannes (with Claude)
- **Builds on:** `2026-10-08-mc-panel-foundation-design.md` (sub-project 1), `2026-10-10-mc-panel-instances-design.md` (sub-project 2), `2026-10-11-mc-panel-console-players-design.md` (sub-project 3a)

## 1. Goal and context

Sub-project 3 ("live operation") is split in three: 3a console/logs/players (implemented), **3b file browser** (this document), 3c plugins/mods (Modrinth, Hangar). Each has its own spec, plan and PRs. (Now: 3c is done as well.)

### 1.1 Success

A user with `server.files` browses a server's data directory, edits configuration files in the browser, uploads and downloads files, creates, renames, moves and deletes. With the server stopped, they upload a world as a zip and extract it; they download folders as a zip.

### 1.2 Guideline

A compromised panel must not get root on the host (sub-project 1 §3). File operations on data that a (possibly compromised) Minecraft server controls therefore **never run as root**: a separate service `mcpanel-files` works as user `mcpanel-mc` – the same user that owns the data anyway. The root agent stays narrow as before and touches no paths, uploads or archives from user hands.

### 1.3 Non-goals (3b)

- Plugins/mods with search and installation from Modrinth/Hangar (3c). (Now: done in sub-project 3c.)
- Resumable uploads (an upload is a stream; if it is aborted, it starts over).
- Changing file permissions or owners (`chmod`/`chown`), creating symlinks.
- Archive formats other than zip (no tar.gz, 7z, rar).
- Copying between servers, a trash bin (deleting is final, with confirmation).
- Backups (sub-project 4). (Now: done in sub-project 4; the files service also runs the backups.)

## 2. Decisions

| Area | Decision | Rationale |
|---|---|---|
| Executing process | Separate service `mcpanel-files` as `mcpanel-mc`, without capabilities, hardened with systemd | Write access as root would be needlessly powerful; a hole in the service yields at most the rights a Minecraft server already has on its data |
| Confinement | Per request `os.Root` on exactly `<instances_dir>/instances/<id>` | All servers share the user `mcpanel-mc`; without `os.Root` a path or symlink trick could reach other servers |
| Permissions | `server.files` = read and write, full trust; a note when granting it | Write access allows code execution in the container (plugin jar); a read/write split would not be a real boundary |
| Running server | Editing/uploading/managing at any time; extracting only when stopped | Configs are often changed during operation; swapping a world during operation destroys data |
| Sizes | Upload ≤ 4 GiB as one stream; extract ≤ 20 GiB / 100 000 entries; 2 GiB reserve | Enough for common worlds, without a chunking protocol |
| Editor | CodeMirror 6, lazy-loaded | Lighter than Monaco, no workers, CSP-compatible (no CDN) |

### 2.1 Rejected alternatives

- **Helper process started by the root agent per request with dropped privileges:** protects the file work just as well, but the root agent would have to accept file requests and start processes – more attack surface at the most important place.
- **agent as root with `os.Root`:** only one layer of protection; a forgotten `os.Open` or an archive path trick would run with root rights.
- **Docker archive API (`docker cp`):** needs an existing container, writes as root with UIDs from the tar, had symlink CVEs and offers no rename/delete.

## 3. Architecture

```
Browser ──/api/servers/{id}/files/*──▶ panel (mcpanel) ──HTTP/Unix socket──▶ mcpanel-files (mcpanel-mc, no caps)
                                         │ permissions, CSRF, audit,            │ os.Root(<instances_dir>/instances/<id>)
                                         │ streaming without buffering          ▼
                                         └──▶ agent (root): actual state   /srv/mc-panel/instances/<id>  (= /data in the container)
```

- **panel:** new package `internal/serverfiles` (handler, routes, audit), client and wire types in `internal/files/filesapi` (shared with the service, like `agentapi`). Checks the server, the permission and – for extracting – the actual state at the agent.
- **Service:** new binary `cmd/files`, package `internal/files` (server, operations, extracting, zip download, limits).
- **agent:** the only change: permissions of `<instances_dir>/instances` (§3.3).

### 3.1 Process and configuration

- Binary `files`, systemd unit `mcpanel-files.service`: `User=mcpanel-mc`, `Group=mcpanel-mc`, `NoNewPrivileges=yes`, `CapabilityBoundingSet=` (empty), `AmbientCapabilities=` (empty), `ProtectSystem=strict`, `ReadWritePaths=<instances_dir>/instances`, `ProtectHome=yes`, `PrivateTmp=yes`, `PrivateDevices=yes`, `RestrictAddressFamilies=AF_UNIX`, `SystemCallArchitectures=native`, `UMask=0027`, `MemoryMax=512M`, `TasksMax=256` (a bound against crafted archives; the service streams, the names in a folder zip are limited to 16 MiB, see §4.1). (Now: the installer's unit `deploy/systemd/mcpanel-files.service` also has `<instances_dir>/backups` in `ReadWritePaths` for the backups of sub-project 4, and uses `MemoryMax=1G`, `TasksMax=512`, `IOWeight=50` and `CPUWeight=50`.)
- `files.toml` in `/etc/mc-panel-files/` (directory `root:mcpanel-mc 0750`, file 0640 – `mcpanel-mc` cannot and should not read `/etc/mc-panel`); unknown keys are rejected, as with the agent:

```toml
socket        = "/run/mc-panel-files/files.sock" # only used without systemd socket activation
socket_mode   = "0660"                           # ditto; group = primary group of the service
panel_uid     = 998                            # UID of the user mcpanel; required in production (allowed: this UID and root)
instances_dir = "/srv/mc-panel"
dev           = false
[limits]
max_upload_bytes   = 4294967296    # 4 GiB
max_extract_bytes  = 21474836480   # 20 GiB
max_extract_entries = 100000
reserve_bytes      = 2147483648    # 2 GiB
max_text_bytes     = 5242880       # 5 MiB
max_list_entries   = 10000
```

(Now: since sub-project 4 the file also takes `restic_binary`; the installer writes it from `deploy/config/files.toml`.)

- **Socket:** in production through systemd socket activation: `mcpanel-files.socket` with `ListenStream=/run/mc-panel-files/files.sock`, `SocketUser=mcpanel-mc`, `SocketGroup=mcpanel`, `SocketMode=0660` – for this the service does not have to be a member of the group `mcpanel` and does not see `/etc/mc-panel`. The service takes over the socket via `LISTEN_FDS`/`LISTEN_PID`; without activation (dev, CI) it creates `socket` itself with `socket_mode`. Its own directory `/run/mc-panel-files` (not `/run/mc-panel`), so that stopping one unit never deletes the other's socket.
- The peer UID check through the existing package `internal/agent/peercred` (Linux `SO_PEERCRED`, macOS `LOCAL_PEERCRED`) is always active; it is the actual access control. Allowed are the panel user and – as with the agent – root (operator, scripts); the service itself never runs as root.
- Missing keys under `[limits]` get the defaults above.
- `panel.toml` gets `files_socket` (default `/run/mc-panel-files/files.sock`, dev `.dev/files.sock`).
- **Dev mode** (`make dev`, macOS): the service runs as the current user with `instances_dir = .dev/mc` (like the dev agent), peer UID = current user; if `instances/<id>` is missing, it creates it (so that the file browser works without Docker).

### 3.2 Confinement per request

- Every request names `id` (int64 > 0). The service opens `<instances_dir>/instances/<id>` with `os.OpenRoot`; if it is missing (server never started) → `404 not_found` (except in dev). All further access only through the `*os.Root`.
- Symlinks are never followed: before every open `root.Lstat`; after opening `f.Stat` and an `os.SameFile` comparison (as in sub-project 3a §5.4). Reading/writing/downloading a symlink or a special file → `400 not_regular`; uploading/creating into a path whose folder component is a symlink → `400 not_regular`. Renaming and deleting a symlink act on the link itself.
- Opening for reading with `O_NONBLOCK` (no hanging FIFO).

### 3.3 File system

- The agent now creates `<instances_dir>/instances` as `root:mcpanel-mc 0750` (instead of `root:root 0700`), so that the service can get in; `deleted/` stays `root:root 0700`. `ensureLayout` (sub-project 2) is adjusted accordingly: the group may read/enter, never write (`CheckInstancesDir` checks only `instances_dir` and its parents and stays unchanged).
- The containers see none of this (only their `/data`).
- Temp files for upload, write and extract live in the target folder as `.mcpanel-tmp-<process token>-<random>` (same file system → atomic `rename`). The token is random per process start. After the start, a background pass (the service already accepts requests) removes temp entries in all instance directories down to depth 64; it leaves entries with the token of its own process untouched, and it aborts on shutdown.

### 3.4 Path rules (shared code `filesapi.CleanPath`, panel and service)

- Relative, separator `/`; `""` or `.` = root; no leading `/`, no `\`, no component `..`, no empty components (`a//b`), no control characters (`< 0x20`, `0x7f`), valid UTF-8; total ≤ 4096 bytes, component ≤ 255 bytes.
- **Hidden** (not visible in lists, every access → `404 not_found`): `.rcon-cli.env` and `.rcon-cli.yaml` at the top level as well as every component with the prefix `.mcpanel-`.
- **Redacted:** when `server.properties` (top level) is read and downloaded, the value of every line `rcon.password=…` is replaced by `********`. Folder zip downloads redact as well. When saving in the editor, a redacted line has no effect because itzg rewrites the file from the environment at start (`OVERRIDE_SERVER_PROPERTIES=true`); on write the service replaces a line `rcon.password=********` with the current value from the file, so that a running server keeps its password. (Now, SEC-37: keys written with escapes, e.g. `rcon\.password` or `rcon\u002Epassword`, which Java's `Properties` reads as `rcon.password`, are recognised as well.)

## 4. API of the service (Unix socket)

Fixed paths, `DisallowUnknownFields`, errors `{error, code}` as with the agent. JSON bodies ≤ 6 MiB (editor content + margin), otherwise 64 KiB.

| Method, path | Body / query | Response |
|---|---|---|
| `POST /v1/list` | `{id, path}` | `{entries: [{name, type, size, mtime}], truncated}`; `type` ∈ `file\|dir\|symlink\|other`; `mtime` Unix ms; sorted: folders first, then name; at most `max_list_entries` |
| `POST /v1/read` | `{id, path}` | `{content, mtime, size, version}` (now, #149: `version` is an opaque token from device, inode, size, mtime and ctime in ns); only regular files ≤ `max_text_bytes` with valid UTF-8 and without NUL, otherwise `415 not_text` |
| `POST /v1/write` | `{id, path, content, expected_mtime}` | `{mtime, size}`; `expected_mtime` = `null` means "new file" (if it exists → `409 exists`), otherwise it must match the current value (`409 modified`); atomic: temp file, `fsync`, `rename`, mode 0644. (Now, #149: optional `expected_version`, which must match as well, and `version` in the response; the base is checked again under a per-server commit lock right before the rename, so of two saves with the same base exactly one succeeds; a commit always moves the millisecond mtime past the replaced file's. The lock covers the service's own requests, not a running server writing the file itself.) |
| `PUT /v1/upload` | Query `id`, `path`, `overwrite` (`0\|1`); body = file, `Content-Length` required, ≤ `max_upload_bytes` | `201 {size}`; without `overwrite` and with an existing target → `409 exists`; abort/error → temp file gone, target unchanged |
| `GET /v1/download` | Query `id`, `path` | File: stream with `Content-Length`; folder: zip stream (`application/zip`, on the fly, without a temp file; symlinks and special files are left out, hidden files as well, and also names that cannot be stored safely in a zip: components with `:`, with a trailing `.` or space, and Windows device names such as `CON`, `NUL`, `COM1`–`COM9`, `LPT1`–`LPT9`, also with an extension). Folder with more than `max_extract_entries` entries or 16 MiB of names → `413 too_large`, folder with an invalid name of its own → `400 invalid`, each before the first byte; too many concurrent downloads → `429 busy` |
| `POST /v1/mkdir` | `{id, path}` | `201`; exists → `409 exists` |
| `POST /v1/rename` | `{id, from, to}` | `204`; target exists → `409 exists`; folder not into itself. (Now, #149: the rename itself never replaces a target that appeared meanwhile: `renameat2(RENAME_NOREPLACE)` on Linux, `renameatx_np(RENAME_EXCL)` on macOS, on the parent folders opened without following symlinks; on other platforms or a file system without the flag, a check and rename under the commit lock.) |
| `POST /v1/delete` | `{id, path}` | `204`; folders recursively (`root.RemoveAll`); the root cannot be deleted (`400`) |
| `POST /v1/extract` | `{id, path, dest}` | Pre-check synchronous (§4.2): errors → `400 invalid` (archive invalid/not allowed), `409 exists` (`dest` not empty), `413 too_large`, `507`; otherwise `202 {job}`; `path` must be a regular `.zip`, `dest` a new or empty folder |
| `GET /v1/jobs` | Query `id` | `{job: null \| {id, state, path, dest, bytes, entries, total_bytes, total_entries, error, started_at, finished_at}}`; `state` ∈ `running\|done\|failed` |
| `GET /v1/health` | – | `{status, version}` |

Error codes of the service: `not_found` (404), `exists` (409), `modified` (409), `not_regular` (400), `not_text` (415), `too_large` (413), `insufficient_storage` (507), `busy` (429, job and download limit), `invalid` (400). Error messages and `job.error` never contain host paths: the service replaces `<instances_dir>/instances/<id>/` with a relative path (otherwise `<instances_dir>`); the log keeps the full text. An aborted or stalling upload body (silence > 60 s, dropped connection) → `400 invalid`.

### 4.1 Limits and disk space

- Before an upload (`Content-Length`), a write and an extract (`total_bytes` from the central directory), the service checks via `statfs` on the instance directory that `reserve_bytes` are still free afterwards, otherwise `507 insufficient_storage`. During an upload it also counts and aborts on more bytes than announced.
  (Now, security review SEC-04: the reserve is the larger of `reserve_bytes` (2 GiB) and `reserve_percent` (new key under `[limits]`, default 5, 0..50) of the file system. Only writes that **grow** data are refused below it: uploads, plugin installs (`UploadChecked`), extracts, restores, new backups and editor saves that make a file larger. Reading, deleting, renaming, shrinking a file and starting the server keep working on a full disk.)
  (Now, #153: an editor save reserves its whole temp copy, the peak while old and new file both exist, so a save that shrinks a file or keeps its size is refused with `507` too instead of eating into the reserve (the old file stays, the temp file is removed); only emptying a file stages no data. Reading, deleting and renaming keep working. The free space is sampled under the same lock as the pending bytes of running writes, so a write that lands meanwhile is counted exactly once.)
- Streams: deadline per read/write operation 60 s; no overall deadline.
  (Now, SEC-24: a client must also move at least 64 KiB per minute spent waiting for it, after a grace of 2 minutes; an upload ends after at most 6 h, a download after 12 h. Slow clients can no longer hold the global slots.)
- At most 4 concurrent uploads in the service (otherwise `429 busy`). (Now: uploads with a checksum, i.e. plugin installs, have their own 4 slots.)
- Downloads: at most 4 concurrent folder zips and 16 file downloads (otherwise `429 busy`). A folder zip keeps its central directory in memory (the names are limited to 16 MiB, `maxZipNameBytes`, plus at most `max_extract_entries` entries); the service therefore counts entries and names in a first pass and takes the zip slot only afterwards, so that only zips that are actually streaming hold the 4 slots.
- (Now, SEC-23: every walk over a folder tree (folder zip, backup, delete, temp cleanup) has a budget of max(1,000,000, 2 × max(`max_backup_entries`, `max_extract_entries`) + 10,000) names and max(256 MiB, 2 × the backup name-byte limit) of names; a tree beyond it fails instead of exhausting the service's memory. Unlocking a tree before deleting it counts only folders, so the temp folder of a restore or extract can always be removed. The temp cleanup at start skips such a data directory with a warning.)
- `delete` and the extract pre-check are explicitly exempt from the server's 30 s write deadline (deleting a large world, central directory with up to 100 000 entries); the client deadlines are long: delete 10 min, extract pre-check 5 min, waiting for download headers 2 min (small calls 10 s).
- Shutdown: the service first aborts running jobs (`svc.Close`), then stops HTTP (`Shutdown`). If the caller has left the pre-check of an extract, no job starts.

### 4.2 Extracting

- At most one job per instance and two in the service in total (otherwise `429 busy`); jobs live in memory, the last result per instance stays until the next job or a restart of the service.
- Pre-check **synchronously in the request** over the central directory, before a byte is written (errors come as HTTP errors, not as a job): every entry name satisfies the path rules (§3.4, without the hidden exception: hidden names → rejected), no symlink entries (mode in the external attribute), no encryption, compression methods only store/deflate, sum of the declared sizes ≤ `max_extract_bytes` and count ≤ `max_extract_entries` (both otherwise `413 too_large`), no duplicate names (otherwise, like all rule violations, `400 invalid`).
- Extracting into a temp folder `<dest-parent>/.mcpanel-tmp-<random>`, re-counting the bytes actually written (protection against forged sizes); exceeding the limit or an error → delete the temp folder, `state: failed`. Success → `rename` of the temp folder to `dest` (if `dest` exists and is empty, it is removed first).
- Files 0644, folders 0755 (the service's umask 0027 applies on top), timestamps from the zip.

## 5. panel

### 5.1 API

All routes through `*rbac.Router` with `rbac.Server(rbac.PermServerFiles, "id")`, described in `api/openapi.yaml`, with an MSW mock. Servers that are not set up → `404 not_found`.

| Route | Content |
|---|---|
| `GET /servers/{id}/files?path=` | List |
| `GET /servers/{id}/files/content?path=` | `{content, mtime, size}` (now also `version`, #149) |
| `PUT /servers/{id}/files/content` | `{path, content, expected_mtime}` → `{mtime, size}` (now, #149: optional `expected_version` with a non-null `expected_mtime`, and `version` in the response; the editor sends the version it loaded) |
| `POST /servers/{id}/files/upload?path=&overwrite=` | raw body (`application/octet-stream`), `Content-Length` required → `201` |
| `GET /servers/{id}/files/download?path=` | Stream; `Content-Disposition: attachment; filename*=UTF-8''<name>` (folder: `<name>.zip`; now without Unicode format characters (category Cf, e.g. right-to-left overrides), SEC-38; the UI shows them visibly as ⟨U+…⟩) |
| `POST /servers/{id}/files/mkdir` | `{path}` |
| `POST /servers/{id}/files/rename` | `{from, to}` |
| `POST /servers/{id}/files/delete` | `{path}` |
| `POST /servers/{id}/files/extract` | `{path, dest}` → `202 {job}` |
| `GET /servers/{id}/files/jobs` | `{job}` |

- **Streaming:** upload and download are exempt from the body limit (1 MiB), `ReadTimeout` and `WriteTimeout`; the handler sets deadlines per operation via `http.ResponseController` (60 s of silence → abort) and passes bytes to the service or back without buffering. Limits per account (otherwise `429 rate_limited`): 2 uploads, 4 downloads, of which 1 folder zip; in addition at most 8 concurrent editor calls (read and save) in the whole panel, of which at most 2 per account (with `Retry-After`). A browser upload that stalls (60 s without data) answers `400 validation_failed` (audited as a failure); with a dropped connection there is no client left to receive a response. Saving in the editor is, like upload and download, exempt from `ReadTimeout` (60 s per read), but the body must arrive within 2 min in total, otherwise `400`. The browser's `Content-Length` is passed on to the service; if it is missing → `400 validation_failed`.
- **CSRF** applies to all writing routes including upload (the browser sends `X-CSRF-Token` via `XMLHttpRequest`). Download is `GET` (cookie `SameSite=Strict`).
- **Extracting:** before the start the panel asks the agent for the actual state; state ≠ `exited`/`created`/`missing` → `409 instance_running`; agent unreachable → `503 agent_unavailable`. As long as a job is `running` for the server, `POST /servers/{id}/power` with `start`/`restart` is rejected with `409 conflict` ("extract running"), and the reconcile loop (sub-project 2 §5.3) does not start the server (desired state `running` is remembered and applied afterwards). The panel learns about running jobs via `GET /v1/jobs` (queried in the reconcile pass and before power actions; if the service is unreachable, "no job" applies). (Now: this gate is the shared data gate `servers.DataGate`, which restores in sub-project 4 also hold.)
- **Error mapping:** service unreachable → `503 files_unavailable`; `exists` → `409 file_exists`; `modified` → `409 file_modified`; `not_text` → `415 not_text`; `not_regular` → `400 not_regular`; `too_large` → `413 payload_too_large`; `insufficient_storage` → `507 insufficient_storage`; `busy` → `429 rate_limited`; `invalid` → `400 validation_failed`; `not_found` → `404 not_found`. For `400` and `413` the panel passes on the service's message (without a technical prefix). Which route can return which status (in particular `413`, `429`, `507`) is listed per operation in `api/openapi.yaml`; `TestFilesErrorsMatchOpenAPI` keeps both in sync.

### 5.2 Error codes

New in `internal/httpx/errors.go` and in the contract's enum:

| Code | Status | When |
|---|---|---|
| `files_unavailable` | 503 | Service `mcpanel-files` unreachable |
| `file_exists` | 409 | Target exists (upload without overwrite, create, rename) |
| `file_modified` | 409 | File was changed since it was opened |
| `not_text` | 415 | Too large for the editor or not text |
| `not_regular` | 400 | Symlink, folder or special file where a file is expected |
| `instance_running` | 409 | Extracting while the server is running |
| `insufficient_storage` | 507 | Less than the reserve free afterwards |

### 5.3 Audit

New actions in `internal/audit/types.go`: `file.write`, `file.upload`, `file.mkdir`, `file.rename`, `file.delete`, `file.extract`, `file.download`. Target `server:<id>`, details `{path}` (for `rename` `{from, to}`, for `upload`/`extract` additionally `{size}` or `{dest}`), result `success`/`failure`. Listing and reading in the editor are not audited; neither are rejections by permissions, validation and rate limit (as in sub-projects 1–3a); nor busy services (`429 busy`) and downloads rejected with `404`, `400 not_regular` or `400 invalid`. A download rejected with `413` (folder too large) is audited as `failure`.

## 6. Frontend

Existing shadcn components; texts `de`/`en`; usable on mobile.

### 6.1 "Files" tab (`server.files`)

(Now: since sub-project 7 this is the `files` area, a route under `routes/_app/servers/$id`, instead of a tab.)

- Breadcrumb path; list with name, size, modification date (sortable, folders first); symlinks and special files marked; cards instead of a table on phones.
- Per entry (menu): open/edit (text), download (folders as zip), rename/move (dialog with target path), delete (confirmation; for folders "including contents, final"), for `.zip` "Extract …".
- Header: "New folder", "New file", "Upload" (also drag and drop). Upload queue with progress and cancel per file. If the loaded list already shows the file, the tab asks with a dialog "<name> already exists" before sending ("Overwrite the existing file?", buttons "Overwrite" / "Skip"; "Skip" sends nothing); `409 file_exists` triggers the same question afterwards. After the last byte it shows "Processing …" until the service answers; if the connection drops before the last byte, it shows "Upload aborted – connection lost (…)".
- **Leaving:** switching tabs, changing routes and reloading with running uploads or unsaved editor changes ask first ("Cancel running uploads?" / "Running uploads will be cancelled."); a single blocker in the tab covers both.
- Never started (no data): no "New folder", "New file", "Upload" buttons, drops are ignored.
- Error dialogs for `413` (with the service's message), `429` and `file_exists` have their own texts.
- Notice bar while the server is running: "Changes usually take effect after a restart." Empty state for data that does not exist yet ("The server has never been started").

### 6.2 Editor

- CodeMirror 6 in a lazy-loaded component; highlighting for YAML, JSON, TOML, properties/INI, otherwise text; line numbers, search.
- Save with Ctrl/Cmd+S (also while focus is on a button of the editor) and a button; warning when leaving with unsaved changes. CRLF line endings are kept. Dragging files onto the editor does nothing. `429 rate_limited` on opening or saving → "Too many files are being edited at the same time right now – please try again in a moment."
- `409 file_modified` → dialog "File was changed: reload (discard own changes) or overwrite".
- For `server.properties`: note "is overwritten from the panel settings at start" with a link to the settings (admin); `rcon.password` appears as `********`.
- `415 not_text` → a download button instead of the editor.

### 6.3 Extracting

Dialog with target folder (suggestion: zip name without extension); disabled with a note while the server is running; also offered in state `error` (the panel decides by the container state, e.g. after a crash). During the job a progress bar (bytes, entries; queried every 2 s); the power buttons of the detail page are disabled. A failed job shows a notice whose title carries the translated reason ("Extracting "x" failed: <reason>", texts `files.extract.reason.*`) and whose description is the service's `job.error`; only the job title is announced to screen readers.

### 6.4 User management

When `server.files` is ticked: note ""Files" allows code execution in the Minecraft server (e.g. through plugins). Only grant it to people you fully trust."

### 6.5 Mocks

MSW mocks with a small in-memory file tree per server (folders, `server.properties` with a redacted password, YAML configs, a zip, a symlink), upload with simulated progress, extract job with progress. `make preview` shows everything without a backend.

## 7. Development, operation and CI

- **Mac:** only `make check` (CLAUDE.md). `make dev` additionally starts `files` in dev mode (§3.1).
- **Test VM:** `scripts/testvm-deploy.sh` builds `files`, creates `/etc/mc-panel/files.toml`, `mcpanel-files.socket` and `mcpanel-files.service` (§3.1) and adds `files_socket` to `panel.toml`. (Now: `scripts/testvm-deploy.sh` builds the binaries and hands them to the installer, which creates `/etc/mc-panel-files/files.toml`, the units and `files_socket`.)
- **Release:** GoReleaser builds `files` for `linux/amd64` and `linux/arm64`.
- **Installer (sub-project 6):** takes over the unit, the configuration and the permissions of `instances/` as described in §3.1/§3.3. (Now: done; units in `deploy/systemd/`, config template in `deploy/config/files.toml`.)
- **CI:** unit tests as before; Linux-specific file system tests (FIFO, `SO_PEERCRED`) run in the Linux job; `scripts/ci-instances.sh` is extended (§8).

## 8. Tests

| Level | Content |
|---|---|
| `filesapi` | Table tests for `CleanPath`: `..`, absolute, `\`, empty components, `.`, control characters, invalid UTF-8, length, hidden names, Unicode |
| `files` operations | Real file system (`t.TempDir`): symlink outward/inward, symlink as a folder component, FIFO, directory instead of file; atomic write, `expected_mtime`; upload with abort (no partial file), too large, more bytes than announced; redacting and writing back `rcon.password`; list with limit; deleting the root rejected; rename into itself |
| `files` extracting | Zip slip (`../`, absolute, `\`), symlink entry, encrypted, unknown compression, forged sizes (zip bomb), too many entries, duplicate names, non-empty target, abort cleans up, job limits |
| `files` download | Folder zip leaves out symlinks, special files and hidden files and redacts `server.properties` |
| `files` server | Peer UID, unknown fields, deadlines, disk space through a fake `statfs` interface, temp cleanup at start |
| agent | `instances/` with `root:mcpanel-mc 0750`, `deleted/` still `0700`; `CheckInstancesDir` accepts group read, rejects group write |
| panel `serverfiles` | Fake service + `storetest`: permissions (`files`, only `view`, admin, foreign server), CSRF, streaming without buffering (large upload body with bounded memory), upload limit per user, error mapping, audit entries, extracting only when stopped, power lock and reconcile pause during a job |
| Contract | OpenAPI ↔ routes, error codes ↔ enum, mock per operation |
| Frontend | Vitest: path helpers, upload queue, conflict dialog, file type detection for the editor; Playwright against the mock: open folder → edit config → save; upload; rename; delete; extract zip with the server stopped |
| CI acceptance | `scripts/ci-instances.sh`: service as `mcpanel-mc` against the Paper server; upload a plugin jar; read `server.properties` (password redacted); change a config; stop the server, upload and extract a world zip; zip slip archive rejected; symlink to `/etc/shadow` returns no content; process runs as `mcpanel-mc` (`/proc/<pid>/status`); write attempt to `<instances_dir>/router` fails |

Conventions from sub-projects 1–3a apply unchanged.

## 9. Acceptance criteria

Automated in CI: 1 as well as the service side of 2–4. Manually by the user on the test VM: 2–7 (`docs/dev/acceptance.md`, "Files").

1. All CI jobs green.
2. Upload a world as a zip and extract it with the server stopped; after switching over (e.g. renaming the folder to `world`) the server starts with it.
3. Upload a plugin jar to `plugins/`; after the restart it is active.
4. Change a config in the editor and save it; a second tab with an outdated state gets the conflict dialog.
5. Download a folder (e.g. `world`) as a zip.
6. A user with only `server.view` sees no files tab, the API answers `403`.
7. All write actions and downloads are in the audit log; `panel audit verify` reports OK.
