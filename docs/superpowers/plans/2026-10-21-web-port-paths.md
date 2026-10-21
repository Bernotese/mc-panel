# Web port paths Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. In this repo, tasks go to agent-team teammates (CLAUDE.md, "Implementation with agent teams").

**Goal:** A server publishes up to 8 HTTP ports: one at the root of `https://<name>.<base domain>/` and the others under `/<path>/`, with the prefix stripped.

**Architecture:** The agent contract (`agentapi.WebRoute`) gains a sorted list of `{path, port}` targets per server. `mcpanel-web` renders one host-matched subroute per server (path prefixes first, then the root), and the bridge port rule of each server network allows a set of ports. The panel replaces `instances.web_port` with a `web_routes` table and `PATCH`/`GET /servers` with a `web_routes` list. The settings section becomes a root slot plus path rows.

**Tech Stack:** Go (sqlc, goose, google/nftables, Caddy JSON config), React + TS (TanStack Query, react-i18next, MSW, Vitest, Playwright), Bash (CI shard).

**Spec:** `docs/superpowers/specs/2026-10-21-mc-panel-web-port-paths-design.md` (addendum to `docs/superpowers/specs/2026-10-17-mc-panel-web-ports-design.md`). Read both.

## Global Constraints

- Path: `""` (root) or 1–32 characters of `[a-z0-9-]`, first and last a letter or digit: regex `^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`.
- Port: `agentapi.ValidWebPort` (1024–65535, not 25565 or 25575).
- Per server: 1–8 targets on the wire (`agentapi.MaxWebTargets = 8`); 0–8 routes in the panel (`[]` = off); unique paths, unique ports, at most one root; sorted by path ascending (root `""` therefore first).
- The agent **rejects** an unsorted target list; the panel **sorts** the client's list before validating.
- Prefix stripped (`rewrite.strip_path_prefix "/<p>"`), request header `X-Forwarded-Prefix: /<p>`, `/<p>` → `308` with `Location: /<p>/`.
- Audit action `server.web_routes_update` with `name`, `old` and `new` (lists of `{path, port}`); `server.web_port_update` stays defined in `internal/audit/types.go` but is no longer written.
- Error codes: `400 validation_failed`, `403`, `409 conflict` with message `base domain required`.
- Checks per CLAUDE.md: `make check` on the Mac, `make test-vm SUITE=<go|lint|web|e2e>` before reporting a task done; never `make test` on the Mac.
- Commit `make generate` output (`internal/store/*.sql.go`, `web/src/api/schema.d.ts`).

## Review Focus

1. **A stored no-root list** (only `afk-smoke`): `/` must answer the static 404, `web_url` must be absent, and the UI must still let the admin switch the root on. Tests: Task 2 golden case "no root", Task 3 `TestWebRoutesView` "no root", Task 4 Vitest "root off then on again".
2. **Prefix look-alikes:** `/afk-smokeX/` and `/afk-smoke-old/` must not hit the `afk-smoke` route. Caddy's `path` matcher `/afk-smoke/*` already ensures this; Task 2 pins it with a check on the rendered JSON (the matcher strings are exactly `/afk-smoke` and `/afk-smoke/*`).
3. **Migration of a published server:** `web_port = 8123` must become exactly one root route with 8123, and `web_status` must carry on without a pending blip caused by a changed comparison. Tests: Task 3 `TestMigrationWebPortToRoutes`, and `TestWebRoutesView` comparing the agent entry with `Equal`.
4. **The client sends an unsorted list or the same list twice:** `[{afk-smoke,8200},{"",8100}]` is accepted and stored sorted. Sending the identical list again writes no audit entry. Test: Task 3 `TestWebRoutesPatchRules`.
5. **Port set shrinks while mcpanel-web stays on a busy network:** the old rule must be kept as a whole, not merged with the new one (rules are compared with `WebRule.Equal`, and only one rule per source exists). Test: Task 2 extends the existing "stay" case in `web_manager_test.go` with a two-port rule.

---

## Team and order

| Task | Teammate | Branch (worktree `../mc-panel-wt/<branch>`) | Starts from |
|---|---|---|---|
| 1 | lead | `feat/web-port-paths` | `origin/main` (spec commit is on it) |
| 2 | `agent` | `feat/web-port-paths-agent` | `feat/web-port-paths` after Task 1 |
| 3 | `panel` | `feat/web-port-paths-panel` | `feat/web-port-paths` after Task 1 |
| 4 | `web` | `feat/web-port-paths-web` | `feat/web-port-paths` after Task 1 |
| 5 | lead | `feat/web-port-paths` | after 2–4 are merged into it |

Tasks 2, 3 and 4 run in parallel and their file sets are disjoint. Task 1 keeps the whole tree compiling by adapting call sites minimally, so the parallel tasks start from a green base. Task 4 owns `api/openapi.yaml` and `web/src/api/schema.d.ts`. Task 3 does not edit them, because `cmd/panel/openapi_test.go` checks routes and error codes only, not schemas.

---

### Task 1: Agent contract

**Files:**
- Modify: `internal/agent/agentapi/web.go`, `internal/agent/agentapi/web_test.go`
- Minimal compile fixes only (behaviour unchanged, root-only): `internal/agent/instances/web.go` (`RenderWebConfig` reads `r.Targets[0].Port`), `internal/agent/instances/web_manager.go` (rule `Ports: []uint16{…}`, the three comparisons below), `internal/agent/firewall/webrules.go`, `internal/agent/firewall/web_linux.go`, `internal/agent/firewall/noop.go`, `internal/servers/web.go` (`Targets: []agentapi.WebTarget{{Path: "", Port: …}}`, `e.route.Equal(route)`), and the tests that construct `WebRoute{…Port…}`/`WebRule{…Port…}` (`internal/agent/client_web_test.go`, `internal/agent/server_web_test.go`, `internal/agent/instances/web_manager_test.go`, `internal/agent/instances/web_test.go`, `internal/agent/firewall/*_test.go`, `internal/servers/web_test.go`).

**Interfaces (produces):**
```go
const MaxWebTargets = 8
const MaxWebRequestBytes = 1 << 20
type WebTarget struct { Path string `json:"path"`; Port int `json:"port"` }
type WebRoute struct { ID int64 `json:"id"`; FQDN string `json:"fqdn"`; Targets []WebTarget `json:"targets"` }
func (r WebRoute) Equal(o WebRoute) bool            // ID, FQDN, slices.Equal(Targets)
func ValidWebPath(p string) bool                    // "" or the Global Constraints regex
func CheckWebTargets(ts []WebTarget) error          // 1..MaxWebTargets, ValidWebPath, ValidWebPort, strictly ascending by Path, unique Port; wraps instancespec.ErrInvalid; message names targets[i]
type WebRule struct { Source netip.Addr; Dest netip.Prefix; Ports []uint16 }
func (r WebRule) Equal(o WebRule) bool
```
`WebRoutesRequest.Validate` calls `CheckWebTargets` per route (error prefix `routes[%d].`) instead of the port check.

- [ ] **Step 1: Write failing tests** in `agentapi/web_test.go`: extend the table of `TestWebRoutesRequestValidate` (cases: `targets` missing/`[]`; 9 targets; path `Afk`, `-a`, `a-`, `a/b`, `a.b`, 33 chars; duplicate path; two roots; unsorted `[{afk,8200},{"",8100}]`; duplicate port; port 25575; valid `[{"",8100},{"afk-smoke",8200}]`). In `internal/agent/server_web_test.go`, add one request that still sends the old `"port"` field and expect it to be rejected as an unknown field). Add `TestValidWebPath` (`""`, `a`, `0`, `afk-smoke`, 32×`a` true; 33×`a`, `A`, `-`, `a_b` false), `TestWebRouteEqual` and `TestWebRuleEqual` (different target order or port set = not equal).
- [ ] **Step 2:** `make check` fails on the missing names. That is expected.
- [ ] **Step 3: Implement the interfaces above** in `agentapi/web.go`. Then apply the minimal call-site fixes. In `web_manager.go`: `st.route == r` → `st.route.Equal(r)`, `slices.Equal(rules, m.webRules)` → `slices.EqualFunc(…, WebRule.Equal)`, `slices.Contains(rules, r)` → `slices.ContainsFunc(rules, r.Equal)`, and the log key `"port"` → `"targets"`. In `firewall/webrules.go`, check `len(r.Ports) == 1 && r.Ports[0] >= minWebPort` for now; `web_linux.go` uses `r.Ports[0]`. Task 2 replaces both.
- [ ] **Step 4:** `make check` passes, then `make test-vm SUITE=go`; everything passes, golden files unchanged.
- [ ] **Step 5: Commit and push**
```bash
git commit -am "feat(agent): web route targets in the agent contract"
git push -u origin feat/web-port-paths
```

---

### Task 2: Agent: path routing and multi-port bridge rules (teammate `agent`)

**Files:**
- Modify: `internal/agent/instances/web.go`, `internal/agent/instances/web_manager.go`, `internal/agent/instances/testdata/web-config.golden.json`, `internal/agent/instances/web_test.go`, `internal/agent/instances/web_manager_test.go`
- Modify: `internal/agent/firewall/webrules.go`, `internal/agent/firewall/web_linux.go`, `internal/agent/firewall/webrules_test.go`, `internal/agent/firewall/web_linux_test.go`, `internal/agent/firewall/nftables_integration_test.go`, `internal/agent/firewall/noop.go` (if its copy of the rules needs `slices.Clone` of `Ports`)

**Interfaces:** consumes Task 1. Produces no new exported names.

- [ ] **Step 1: Failing firewall tests.** In `TestSortedWebRulesRejectsInvalid`, add the cases `Ports` empty, 9 ports, a port < 1024 and a duplicate port. Add `TestSortedWebRulesSortsPorts`: input `Ports: {8200, 8100}` comes out as `{8100, 8200}`, and the caller's slice is unchanged. Add `TestWebRuleExprsMultiPort` in `web_linux_test.go`: a rule with ports `{8100, 8200}` gives three expression lists (accept 8100, accept 8200, drop), in that order.
- [ ] **Step 2:** `make test-vm SUITE=go` shows those failing.
- [ ] **Step 3: Implement.** `sortedWebRules` validates `1 ≤ len(Ports) ≤ agentapi.MaxWebTargets`, each ≥ `minWebPort`, no duplicates, and stores a sorted clone. `webRuleExprs` emits per rule `webAcceptExprs(r, port)` for each port, then `webDropExprs(r)`. `webAcceptExprs` gains the `port uint16` parameter. The comments on `SetWebRules` and `webAcceptExprs` say "Ports".
- [ ] **Step 4: Failing config tests.** Rewrite `TestRenderWebConfigGolden` with three routes: id 3 `survival.example.com` targets `{"",8100},{"afk-smoke",8200}`; id 12 `xn--mnchen-3ya.0-9.example.com` target `{"",65535}`; id 20 `paths.example.com` target `{"map",8300}` (no root). Add `TestRenderWebConfigPathMatchers`: unmarshal the output and assert that route 3's subroute has, in order, the matchers `path ["/afk-smoke"]` (handler `static_response` 308, header `Location: ["/afk-smoke/"]`), `path ["/afk-smoke/*"]` (handlers `rewrite` with `strip_path_prefix "/afk-smoke"`, then `headers` setting `X-Forwarded-Prefix: ["/afk-smoke"]`, then `reverse_proxy` dial `mcpanel-mc-3:8200`), and a matcher-less `reverse_proxy` to `mcpanel-mc-3:8100`. Route 20 ends in `static_response` 404 with `webNotFoundBody`. No matcher string other than `/<p>` and `/<p>/*` appears. The id-order and no-reorder checks stay.
- [ ] **Step 5: Implement `RenderWebConfig`.** Each server gets `caddyRoute{Match: host, Handle: [{Handler: "subroute", Routes: …}], Terminal: true}`, ordered as in spec §4.2. Extend the JSON structs with `caddyMatch.Path []string`, `caddyHandler.Routes []caddyRoute` (`json:"routes,omitempty"`), `StripPathPrefix string` (`json:"strip_path_prefix,omitempty"`), `Headers map[string][]string` (`json:"headers,omitempty"`, for static_response), and `Request *caddyHeaderOps` with `Set map[string][]string` (`json:"set"`). Regenerate the golden file from the output, read it line by line against spec §4.2, then commit it. The ask server is unchanged.
- [ ] **Step 6: Manager.** `webTargets` builds `Ports` from every target of the route (sorted ascending). Extend the existing "stay on busy network" test so the old rule has two ports, the new desired route keeps only one, and the kept rule is the old two-port rule (Review Focus 5). Update `TestNFTablesWebRules` to use a two-port rule plus the denied third port in its expression check.
- [ ] **Step 7:** `make check`, then `make test-vm SUITE=go` and `SUITE=lint`; all pass. The nftables integration test runs only in CI.
- [ ] **Step 8: Commit** `feat(agent): path routes and multi-port rules for mcpanel-web`, then push the branch and report to the lead.

---

### Task 3: Panel: storage, API handler, reconcile (teammate `panel`)

**Files:**
- Create: `internal/store/migrations/00011_web_routes.sql`
- Modify: `internal/store/queries/servers.sql`, generated `internal/store/servers.sql.go` and `internal/store/models.go` (via `make generate`), `internal/store/servers_test.go`
- Modify: `internal/servers/web.go`, `internal/servers/view.go`, `internal/servers/servers.go`, `internal/servers/config.go`, `internal/servers/web_test.go`, `internal/audit/types.go`
- Do **not** edit `api/openapi.yaml` or `web/` (Task 4).

**Interfaces:**
- Consumes: `agentapi.WebTarget`, `CheckWebTargets`, `ValidWebPath`, `WebRoute.Equal`, `MaxWebTargets` (Task 1).
- Produces the JSON that Task 4 mirrors:
  - Server: `web_routes` (admins only, always present for admins, `[]` when none) = `[{path, port, url?}]`; `url` is `https://<fqdn>/` or `https://<fqdn>/<path>/` and absent without a base domain. `web_url` is set only with a root route and a base domain. `web_port` is gone.
  - UpdateServerRequest: `web_routes: [{path, port}]` (not nullable).
- Store (sqlc): `ServersListWebRoutes(ctx) ([]WebRoute, error)` (ordered by server_id, path); `ServersListServerWebRoutes(ctx, serverID int64) ([]WebRoute, error)`; `ServersDeleteWebRoutes(ctx, serverID int64) error`; `ServersInsertWebRoute(ctx, ServersInsertWebRouteParams{ServerID, Path, Port}) error`. `ServersSetWebPort` is removed, and `ServersListInstances` no longer selects `web_port`.
- `audit.ActionServerWebRoutesUpdate = "server.web_routes_update"`.

- [ ] **Step 1: Migration test** `TestMigrationWebPortToRoutes` in `internal/store/servers_test.go`. Migrate to 00010, insert two instances (one with `web_port` 8123, one NULL), migrate up, and assert exactly one row `(id, '', 8123)`. Then migrate down to 00010 with an extra row `afk-smoke` and assert `web_port` = 8123 for the first instance and NULL for the second. Use goose's `UpTo`/`DownTo`; look at how `storetest` or the existing store tests reach a specific version. Also add CHECK cases: inserting path `Afk`, `-a`, port 25565, or a duplicate port for one server each fails; deleting the server cascades.
- [ ] **Step 2: Write the migration.** The table matches spec §3.1. The path CHECK is `path = '' OR (length(path) BETWEEN 1 AND 32 AND path NOT GLOB '*[^a-z0-9-]*' AND substr(path, 1, 1) <> '-' AND substr(path, -1, 1) <> '-')`, and the port CHECK is copied from 00010. Then `INSERT … SELECT server_id, '', web_port FROM instances WHERE web_port IS NOT NULL;` and `ALTER TABLE instances DROP COLUMN web_port;`. Down: `ADD COLUMN web_port` with the 00010 CHECK, copy back the root port, `DROP TABLE web_routes`. Add the queries and run `make generate`.
- [ ] **Step 3: Failing handler tests** in `internal/servers/web_test.go`, adapting the existing ones:
  - `TestWebRoutesPatchRules`: non-admin → 403; admin without sudo → `sudo_required`; `null` → 400; 9 routes, a bad path, a duplicate port, two roots → 400 `validation_failed`; non-empty without base domain → 409 `base domain required`; `[]` without base domain → 200; the unsorted list `[{afk-smoke,8200},{"",8100}]` → 200 and stored sorted; the same list again → no second audit entry; audit `server.web_routes_update` with `old: []` and `new` as the list of `{path, port}`.
  - `TestWebRoutesView`: admin sees `web_routes` with urls `https://survival.mc.example.com/` and `…/afk-smoke/`; no root → no `web_url`, routes still listed; no base domain → status off, routes without `url`; viewer sees neither `web_routes` nor `web_error`; status `active` only when the agent entry `Equal`s the current route, `pending` after a port change.
  - `TestWebRoutesReconcile`: the agent receives one route per server with all targets sorted; rename, delete and base domain change are reflected.
- [ ] **Step 4: Implement.**
  - `updateServerRequest.WebRoutes optional[[]agentapi.WebTarget]` (`json:"web_routes"`, null → `errNotNullable`). Validate after `slices.SortFunc` by path: empty is fine; otherwise `CheckWebTargets`, whose message goes into `writeValidation` prefixed `web_routes: `.
  - `updateWebRoutes(w, r, s, next []agentapi.WebTarget) bool` loads the current list, returns early when `slices.Equal`, otherwise replaces it in `h.inTx` (delete + inserts), audits outside the transaction, and calls `h.Trigger()`.
  - `view(s, perms, in, routes []agentapi.WebTarget, baseDomain, snap)`; `viewData.webRoutes map[int64][]agentapi.WebTarget`, filled in `loadViewData` from `ServersListWebRoutes` via `webTargetsByServer(rows []store.WebRoute) map[int64][]agentapi.WebTarget`. The two single-server callers in `servers.go` load `ServersListServerWebRoutes`.
  - `serverJSON.WebRoutes *[]webRouteJSON` (`json:"web_routes,omitempty"`), where `webRouteJSON{Path string; Port int; URL *string \`json:"url,omitempty"\`}`. `redactFor` sets it to nil.
  - `webRoutes(rows, routes map[int64][]agentapi.WebTarget, baseDomain string) []agentapi.WebRoute` (servers without an instance row are skipped). `syncWeb` loads the routes.
  - Remove `webPortDetails`, `WebPort` in `config.go`, and `ServersSetWebPort` users.
- [ ] **Step 5:** `make check`, `make test-vm SUITE=go`, `SUITE=lint`; all pass, including `cmd/panel/openapi_test.go`.
- [ ] **Step 6: Commit** `feat(servers): several web routes per server`, then push and report.

---

### Task 4: Contract, UI, mock (teammate `web`)

**Files:**
- Modify: `api/openapi.yaml`, `web/src/api/schema.d.ts` (`make generate`)
- Create: `web/src/features/servers/web-routes.ts`, `web/src/features/servers/web-routes.test.ts`, `web/src/features/servers/web-routes-section.tsx`, `web/src/features/servers/web-routes-section.test.tsx`
- Delete: `web/src/features/servers/web-port-section.tsx`, `web/src/features/servers/web-port-section.test.tsx`
- Modify: `web/src/features/servers/server-settings-tab.tsx`, `web/src/features/servers/server-layout.test.tsx`, `web/src/mocks/db.ts`, `web/src/mocks/handlers/servers.ts`, `web/src/mocks/handlers.test.ts`, `web/src/i18n/de.ts`, `web/src/i18n/en.ts`, `web/e2e/web-port.spec.ts`, any `web/e2e/shots/*.shots.ts` that use `webPort`

**Interfaces:**
- Consumes the JSON contract from Task 3's Interfaces block.
- OpenAPI: a new schema `WebRoute` (`required: [path, port]`; `path` string with the Global Constraints pattern, also matching `""`; `port` integer 1024–65535; `url` uri, read-only) and `WebRouteInput` (same without `url`, `additionalProperties: false`). `Server.web_routes: { type: array, maxItems: 8, items: WebRoute }` replaces `web_port`, with a description saying it is admins only and absent for others. `UpdateServerRequest.web_routes: { type: array, maxItems: 8, items: WebRouteInput }` replaces `web_port`, with a description covering admin + sudo, 409 without base domain, `[]` = off, sorted by the panel, no revision bump. Update the `web_url` and `web_status` descriptions to match spec §3.2.
- `web-routes.ts` exports `MAX_WEB_ROUTES = 8`, `DEFAULT_ROOT_PORT = 8100`, `webPortError(raw: string): 'invalid' | 'reserved' | null` (moved), `webPathError(raw: string, taken: string[]): 'invalid' | 'duplicate' | null`, and `toWebRoutes(rootOn: boolean, rootPort: string, rows: {path: string; port: string}[]): {path: string; port: number}[]` (root first, rows sorted by path).

- [ ] **Step 1: Contract.** Edit `api/openapi.yaml`, run `make generate`. Then update `web/src/mocks/db.ts` (`webRoutes?: {path: string; port: number}[]`, seed server 2 with `[{path: '', port: 8100}]` where `webPort: 8100` was) and `handlers/servers.ts`: the same validation and sort as the panel, 409 rule, audit `server.web_routes_update`, `webFields` mirroring the Task 3 view rules. Rewrite the web part of `handlers.test.ts` with the Task 3 cases (400 for a bad path and for 9 routes, 409, viewer redaction, `web_url` only with a root, unsorted input stored sorted).
- [ ] **Step 2: Helper tests** `web-routes.test.ts`: `webPathError('afk-smoke', [])` null; `'Afk'`, `''`, `'-a'`, `'a-'`, 33×`a` → `'invalid'`; `('map', ['map'])` → `'duplicate'`; `toWebRoutes(true, '8100', [{path:'b',port:'1'},{path:'a',port:'2'}])` → root, a, b; `toWebRoutes(false, '', [])` → `[]`. Implement `web-routes.ts`.
- [ ] **Step 3: Section tests** `web-routes-section.test.tsx` via `src/test/render-section.tsx`, carrying over the still-valid cases of the old test (admin only, sudo dialog, base-domain hint and its 409, status dot and error):
  - root off → switch on → port prefilled 8100 → save sends `{web_routes: [{path: '', port: 8100}]}`;
  - add a row `afk-smoke` 8200 → body has both, root first, and the URLs show;
  - a bad path shows the error and disables Save;
  - with root on and 7 rows, "Add port" is disabled; with root off and 7 rows, it is enabled;
  - stored `[{path: 'afk-smoke', port: 8200}]` (no root) → the root slot shows off, switching it on adds the root on save;
  - turning the section off sends `{web_routes: []}`;
  - the hint sentence from spec §5 is present.
- [ ] **Step 4: Implement `WebRoutesSection`** following the layout in spec §5. Reuse the existing section's structure, status dots and mutation handling, plus `Section`, `Switch`, `Input`, `Button` and `Label`. Settings tab: `<WebRoutesSection key={JSON.stringify(server.web_routes ?? [])} server={server} />`. i18n: rename the section title to "Web interfaces" / "Weboberflächen" and add keys for path, add port, remove, root, and the prefix hint, in de and en.
- [ ] **Step 5: e2e** `web/e2e/web-port.spec.ts`: publish the root plus `afk-smoke`, see both URLs and the header link, remove the path row, unpublish. Adapt any shots files that set `webPort`.
- [ ] **Step 6:** `make check`, then `make test-vm SUITE=web`, `SUITE=lint`, `SUITE=e2e`; all pass. Optionally `SUITE=shots` to look at the section in both themes and on mobile.
- [ ] **Step 7: Commit** `feat(web): web interfaces section with path routes`, then push and report.

---

### Task 5: Integration, CI acceptance, docs (lead)

**Files:**
- Modify: `scripts/ci-instances/web.sh`, `docs/install.md`, `docs/dev/acceptance.md`, `CLAUDE.md`, `docs/superpowers/specs/2026-10-17-mc-panel-web-ports-design.md` (§1.2 note), `docs/superpowers/specs/2026-10-21-mc-panel-web-port-paths-design.md` (status line → implemented)

- [ ] **Step 1: Merge** the Task 2, 3 and 4 branches into `feat/web-port-paths`, then run `make check` and `make test-vm SUITE=all`.
- [ ] **Step 2: CI shard.** In `web.sh`, add a third responder `web_responder 8102 extra`, whose directory holds a file `x` with body `ci extra body`. Every `web_port` request body becomes `web_routes`:
  - invalid cases: `[{"path":"Bad","port":8102}]`, nine routes, duplicate port, `null`, each → 400 `validation_failed`;
  - publish `[{"path":"","port":8100},{"path":"extra","port":8102}]`;
  - `web_url` and `web_routes` (with urls) in `GET`;
  - the bridge dump contains `dport 8100 accept` and `dport 8102 accept`, and nothing for 8101;
  - `https://<fqdn>/extra/x` → `ci extra body`;
  - `https://<fqdn>/extra` → 308 with `location: /extra/`;
  - `/` still answers the root body;
  - `mcpanel-web` reaches 8102 and not 8101;
  - publish root only → `/extra/x` answers 404 from the root responder;
  - unpublish `[]` → no `web_url`, empty `web_routes`, status off;
  - the audit query uses `server.web_routes_update`, and its count matches the number of successful PATCHes.
  
  Keep the existing 502, isolation and certificate checks.
- [ ] **Step 3: Docs.**
  - `docs/install.md` "Web interfaces": several ports, the path rules, stripping, the AFK-smoke link example `https://survival.mc.example.com/afk-smoke/pack.zip`.
  - `docs/dev/acceptance.md`: BlueMap at the root plus a second mod under a path; the path loads with its prefix stripped; `/extra` redirects.
  - `CLAUDE.md`: the architecture sentence about web ports, and the spec list entry for the addendum.
  - WP spec §1.2: a note pointing to the addendum.
- [ ] **Step 4:** `make check` and `make test-vm SUITE=all`. Then open the PR with `gh pr create` and `gh pr ready`, and watch `gh pr checks <n> --watch` until `CI OK`; the `instances` job runs the shard. Then `make deploy-testvm REF=feat/web-port-paths` for the manual check.
- [ ] **Step 5: Commit** `test(ci): web route paths in the web shard` and `docs: web port paths`, then push.
