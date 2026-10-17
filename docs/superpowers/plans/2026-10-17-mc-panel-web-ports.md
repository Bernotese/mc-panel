# Web ports per server: implementation plan

> **For agentic workers:** this plan is implemented by an agent team (CLAUDE.md, "Implementation with agent teams"). Each teammate works only on its own tasks and files, in its own worktree, and uses superpowers:test-driven-development inside each task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** an admin publishes one HTTP port of a Minecraft server (BlueMap first) under the server's own name, `https://<name>.<base domain>`, with an on-demand Let's Encrypt certificate.

**Architecture:** the panel stores `instances.web_port`, builds the complete route list `{id, fqdn, port}` on every reconcile pass and sends it to the agent (`PUT /v1/web/routes`). The agent runs a Caddy container `mcpanel-web` (pinned image, non-root, read-only rootfs), connects it to the networks of the servers with a route, writes its JSON config (file watch, no admin API) and restricts it to the published port with an nftables table `bridge mcpanel_web`. Host Caddy gets a catch-all on-demand-TLS site that forwards to `127.0.0.1:18100` and asks `127.0.0.1:18101` before it issues a certificate.

**Tech stack:** Go (agent, panel, `github.com/google/nftables`), SQLite/goose/sqlc, React/TanStack/MSW, Bash (installer), Caddy 2.

**Spec:** `docs/superpowers/specs/2026-10-17-mc-panel-web-ports-design.md` (read it first; this plan argues from it).

## Team, branches and phases

Integration branch: **`feat/web-ports`** (the lead creates it from `origin/main`; it carries this plan). Every teammate branches from `origin/feat/web-ports` in its own worktree (`git worktree add ../mc-panel-wt/<branch> -b <branch> origin/feat/web-ports`), pushes its branch and reports; the lead merges it into `feat/web-ports`. Only the lead opens the PR (`feat/web-ports` → `main`).

| Phase | Teammate | Branch | Tasks |
|---|---|---|---|
| 1 | `contracts` | `feat/web-ports-contracts` | 1 |
| 2 (parallel, after task 1 is merged) | `agent` | `feat/web-ports-agent` | 2, 3 |
| 2 | `firewall` | `feat/web-ports-firewall` | 4 |
| 2 | `contracts` (continues as panel teammate) | `feat/web-ports-panel` | 5 |
| 2 | `web` | `feat/web-ports-web` | 6 |
| 2 | `installer` | `feat/web-ports-installer` | 7 |
| 3 (after phase 2 is merged) | `installer` | `feat/web-ports-acceptance` | 8 |

Every task ends with `make check` on the Mac and `make test-vm SUITE=<suites the task touches>` before it is reported done (Go: `go`, plus `lint`; web: `web`, `lint`, `e2e`; installer: `lint`). Never run `make test`, `make lint` or Playwright on the Mac.

## Global constraints

- Runtime names (never renamed, CLAUDE.md conventions): container `mcpanel-web`, network `mcpanel-web`, nftables table `bridge mcpanel_web`, label `mcpanel.web=1`, label `mcpanel.web-template=<sha256>`.
- Port rule, everywhere it is checked (DB CHECK, panel, agentapi): `1024 <= port <= 65535` and `port != 25565` and `port != 25575`. UI default: `8100`.
- `agent.toml`: `web_proxy_port` (default `18100`), `web_ask_port` (default `18101`), both 1024–65535, different from each other.
- At most 500 routes per request; ids unique and > 0; fqdns unique, lowercase, ≤ 253 chars, ≥ 3 labels, every label valid per `instancespec` rules.
- `mcpanel-web` publishes only on `127.0.0.1`; inside the container the proxy listens on `:8080`, the ask server on `:8081`.
- Changing `web_port` needs admin + sudo (the route `PATCH /servers/{id}` already is `rbac.Admin().WithSudo()`); audited as `server.web_port_update` with `old`, `new`; does not bump `revision`.
- `web_status` values: `off | pending | active | error`. Agent per-route status: `pending | active | error`.
- Without a base domain: setting a port answers `409 conflict` with message `base domain required`.
- The agent never edits host Caddy files; the panel never proxies web traffic.
- Go tests use only the standard library; all time logic uses `clock.Clock`; every DB/HTTP call takes a context.
- Strings in de and en; colours only via design tokens.

## Review focus

1. **Agent restart with live routes.** After an agent restart the agent has no desired list until the panel's next PUT. Expected: the agent leaves `mcpanel-web`, its networks and rules untouched until it received a list (`desired == nil` ≠ empty list). Test in task 3.
2. **Deleting a server whose network has `mcpanel-web` attached.** `removeInstanceNetworkLocked` disconnects only the router today and `removeOrphanNetwork` refuses networks with containers other than the router; with `mcpanel-web` attached, the network would never be removed. Expected: both treat `mcpanel-web` like the router. Test in task 3.
3. **A slow first apply (image pull) under the panel's 3 s agent timeout.** Expected: `PUT /v1/web/routes` stores the list, triggers the apply asynchronously and answers at once with `pending` for routes not yet applied; it never blocks on Docker pulls. Test in task 3.
4. **Upper-case or trailing-dot names reaching the ask endpoint, and an fqdn that tries to break out of the config.** Expected: the ask server matches exactly the lowercase fqdns; the config is built with `encoding/json` from validated values only (golden test with a hostile-looking but valid name and a rejected one). Test in tasks 1 and 2.
5. **Base domain removed or changed while routes exist.** Expected: the next reconcile sends the list without those routes (empty list → container removed), `web_status` becomes `off`, `web_url` disappears. Test in task 5.

---

### Task 1: Contracts (agentapi, agent endpoint and client, OpenAPI, migration)

Owner: `contracts`. Everything later tasks compile against.

**Files:**
- Create: `internal/agent/agentapi/web.go`, `internal/agent/agentapi/web_test.go`
- Modify: `internal/agent/server.go` (route `PUT /v1/web/routes`, option `WithWeb`, `noWeb`), `internal/agent/server_test.go` or new `internal/agent/server_web_test.go`
- Modify: `internal/agent/client.go` (method `WebRoutes`), new `internal/agent/client_web_test.go`
- Create: `internal/store/migrations/00010_web_port.sql`
- Modify: `internal/store/queries/servers.sql` (query `ServersSetWebPort`), regenerated `internal/store/servers.sql.go`, `internal/store/models.go`
- Modify: `api/openapi.yaml` (fields on `Server`, `UpdateServerRequest`), regenerated `web/src/api/schema.d.ts`
- Modify: `internal/audit` action constant `ActionServerWebPortUpdate = "server.web_port_update"` (where the other `ActionServer*` constants live)

**Interfaces (produces):**

```go
// internal/agent/agentapi/web.go
package agentapi

const (
	PathWebRoutes = "/v1/web/routes"
	// MaxWebRoutes bounds one request (spec §4.1).
	MaxWebRoutes = 500
	// MaxWebRequestBytes is the body limit of PUT /v1/web/routes (500 routes × ~300 bytes).
	MaxWebRequestBytes = 256 << 10
)

type WebRoute struct {
	ID   int64  `json:"id"`
	FQDN string `json:"fqdn"`
	Port int    `json:"port"`
}

type WebRoutesRequest struct {
	Routes []WebRoute `json:"routes"` // never null on the wire; nil is rejected
}

type WebRouteState string

const (
	WebPending WebRouteState = "pending"
	WebActive  WebRouteState = "active"
	WebError   WebRouteState = "error"
)

type WebRouteStatus struct {
	ID     int64         `json:"id"`
	Status WebRouteState `json:"status"`
	Error  string        `json:"error,omitempty"` // sanitized, like last_error
}

type WebRoutesResponse struct {
	Routes []WebRouteStatus `json:"routes"` // one per requested id, in request order
}

// ValidWebPort is the port rule of spec §1.1 (shared by panel and agent).
func ValidWebPort(p int) bool { return p >= 1024 && p <= 65535 && p != 25565 && p != 25575 }

// Validate wraps instancespec.ErrInvalid like EnsureRequest.Validate.
func (r WebRoutesRequest) Validate() error

// WebRule is one allow rule of the bridge port restriction (not on the wire; shared by
// internal/agent/instances and internal/agent/firewall): mcpanel-web at Source may open TCP
// connections to Port on the server network Dest; everything else from Source is dropped.
type WebRule struct {
	Source netip.Addr   // fixed address of mcpanel-web on the server network
	Dest   netip.Prefix // the server network's subnet
	Port   uint16
}
```

`Validate` rules (each its own error message, wrapped with `instancespec.ErrInvalid`): `routes` not nil; `len ≤ MaxWebRoutes`; `id > 0` and unique; `fqdn` unique, `len ≤ 253`, `== strings.ToLower(fqdn)`, no trailing dot, ≥ 3 labels, every label passes the `instancespec` label check (reuse the existing label function behind `instancespec.CheckFQDN`; export a `CheckDNSName(fqdn string) error` there if no suitable exported helper exists); `ValidWebPort(port)`.

Agent side (in `internal/agent/server.go`):

```go
// Web is the web router part of the instance manager (instances.Manager implements it).
type Web interface {
	SetWebRoutes(ctx context.Context, req agentapi.WebRoutesRequest) (agentapi.WebRoutesResponse, error)
}

func WithWeb(w Web) Option
```

Handler: decode with `s.decode(w, r, &req, agentapi.MaxWebRequestBytes)` (unknown fields rejected as everywhere), `req.Validate()` → `400`, `agentapi.ErrDockerUnavailable` → `503 docker_unavailable` (same mapping as `queued`), success → `200` with the response. `noWeb` returns `ErrDockerUnavailable`.

Client: `func (c *Client) WebRoutes(ctx context.Context, req agentapi.WebRoutesRequest) (agentapi.WebRoutesResponse, error)` with `PUT`.

Migration `00010_web_port.sql`:

```sql
-- +goose Up
-- Optional web port per server (web ports spec §3.1); NULL = off. Not part of the instance
-- spec: changing it never bumps revision.
ALTER TABLE instances ADD COLUMN web_port INTEGER NULL
  CHECK (web_port IS NULL OR (web_port BETWEEN 1024 AND 65535 AND web_port NOT IN (25565, 25575)));

-- +goose Down
ALTER TABLE instances DROP COLUMN web_port;
```

Query (no `revision`, no `updated_at` change of the spec):

```sql
-- name: ServersSetWebPort :one
UPDATE instances SET web_port = ? WHERE server_id = ? RETURNING *;
```

Check that `ServersListInstances`/`ServersGetInstance` return `web_port` (they do if they select `*`; otherwise add the column) and that `instanceFromListRow` still compiles.

OpenAPI (`api/openapi.yaml`):
- `UpdateServerRequest.web_port`: `type: [integer, 'null']`, `minimum: 1024`, `maximum: 65535`, description with the 25565/25575 exclusion, admin + sudo.
- `Server`: `web_url` (string, uri, optional), `web_status` (enum `off|pending|active|error`, required), `web_port` (integer, optional, admin only), `web_error` (string, optional, admin only).
- Run `make generate`; commit `schema.d.ts`.

- [ ] **Step 1:** Write `agentapi/web_test.go`: table test of `Validate` (valid list; nil routes; 501 routes; id 0; duplicate id; duplicate fqdn; `Survival.example.com`; `survival.example.com.`; two labels; label with `_`; 254 chars; ports 1023, 1024, 25565, 25575, 65535, 65536) and `ValidWebPort`. Run on the VM: `go test ./internal/agent/agentapi -run Web` → FAIL (undefined).
- [ ] **Step 2:** Implement `web.go`; tests pass.
- [ ] **Step 3:** Server test: unknown field → 400; invalid body → 400; `noWeb` → 503 `docker_unavailable`; a fake `Web` gets the decoded request and its response is returned as JSON. Client test against the server over a socket pair like `client_instances_test.go`. Implement; pass.
- [ ] **Step 4:** Migration + query + `make generate`; a store test that the CHECK rejects 25565 and 80 and accepts NULL and 8100 (`storetest.Open(t)`).
- [ ] **Step 5:** OpenAPI fields + `make generate`. Panel code must still compile (`view` does not fill the new fields yet; `web_status` is required in the schema, so set `WebStatus: "off"` in `serverJSON` for now and leave the rest to task 5). `cmd/panel/openapi_test.go` and the MSW handlers test must stay green: add `web_status: 'off'` to the mock server fixtures in `web/src/mocks/`.
- [ ] **Step 6:** `make check`; `make test-vm SUITE=go` and `SUITE=lint`; commit (`feat(web-ports): agent and panel contracts`), push, report.

---

### Task 2: `mcpanel-web` container, config rendering, agent config keys

Owner: `agent`. Pure functions plus config; no Docker calls yet.

**Files:**
- Create: `internal/agent/instances/web.go` (constants, `WebContainer`, `RenderWebConfig`, `WebAddress`), `internal/agent/instances/web_test.go`, golden file `internal/agent/instances/testdata/web-config.golden.json`
- Modify: `internal/agent/docker/types.go` (`HostConfig.Tmpfs map[string]string \`json:"Tmpfs,omitempty"\``; `EndpointSettings.IPAMConfig *EndpointIPAMConfig` with `IPv4Address string \`json:"IPv4Address,omitempty"\``)
- Modify: `internal/config/config.go`, `internal/config/agent_*_test.go` (`Images.Web`, `WebProxyPort`, `WebAskPort`)
- Modify: `deploy/config/agent.toml` is the installer's file: **do not edit it**; task 7 adds the keys there.

**Interfaces (produces):**

```go
const (
	WebName            = "mcpanel-web"     // container
	WebNetwork         = "mcpanel-web"     // its home network
	LabelWeb           = "mcpanel.web"
	LabelWebTemplate   = "mcpanel.web-template"
	WebMemoryBytes     = 128 << 20
	WebPidsLimit       = 128
	webProxyPort       = "8080/tcp"
	webAskPort         = "8081/tcp"
	webConfigDir       = "/etc/mcpanel-web" // read-only bind of WebDir
	webConfigFile      = "caddy.json"
)

// WebDir is <instances_dir>/web, root-owned 0755, mounted read-only at webConfigDir.
func WebDir(instancesDir string) string

// WebContainer builds the hardened Caddy container (spec §2.1).
func WebContainer(h Host) (docker.ContainerConfig, error)

// WebAddress is mcpanel-web's fixed address on a server network: the last usable host of
// the subnet (10.213.0.16/28 → 10.213.0.30). The bridge rules are written for it before the
// connect, which keeps the port restriction fail-closed.
func WebAddress(subnet netip.Prefix) (netip.Addr, bool)

// RenderWebConfig returns the Caddy JSON config for routes (sorted by id). trusted is the
// subnet of the mcpanel-web network (host Caddy's requests arrive from its gateway), so
// X-Forwarded-For from host Caddy is kept.
func RenderWebConfig(routes []agentapi.WebRoute, trusted netip.Prefix) ([]byte, error)
```

`Host` gains `WebProxyPort, WebAskPort int`. `WebContainer`: image `pinnedImage(h.Images.Web)`; `User: "65532:65532"`; `Cmd: ["caddy", "run", "--config", "/etc/mcpanel-web/caddy.json", "--watch"]`; `Env: ["XDG_DATA_HOME=/data", "XDG_CONFIG_HOME=/config"]`; labels `{LabelWeb: "1"}`; exposed `8080/tcp`, `8081/tcp`; `PortBindings` both with `HostIP: "127.0.0.1"` and the configured host ports; `Binds: [WebDir + ":/etc/mcpanel-web:ro"]` (directory, so the atomic rename stays visible); `Tmpfs: {"/data": "rw,noexec,nosuid,nodev,size=16m", "/config": "rw,noexec,nosuid,nodev,size=1m"}`; `CapDrop ALL`, `no-new-privileges:true`, `ReadonlyRootfs`, memory/swap `WebMemoryBytes`, `PidsLimit`, `unless-stopped`, `logConfig()`, network `WebNetwork`.

Why the file watch and not the admin API (spec §2.1 allows the fallback): the container runs as 65532 and cannot create a socket in a root-only directory; `--watch` reloads through `caddy.Load` without any listener. The config sets `admin.disabled = true` and `admin.config.persist = false` (nothing is written to `/config`).

`RenderWebConfig` builds Go structs and `json.MarshalIndent` (never string templates):

```json
{
  "admin": {"disabled": true, "config": {"persist": false}},
  "apps": {"http": {"servers": {
    "proxy": {
      "listen": [":8080"],
      "automatic_https": {"disable": true},
      "trusted_proxies": {"source": "static", "ranges": ["<trusted subnet>"]},
      "routes": [
        {"match": [{"host": ["survival.example.com"]}],
         "handle": [{"handler": "reverse_proxy", "upstreams": [{"dial": "mcpanel-mc-1:8100"}]}],
         "terminal": true},
        {"handle": [{"handler": "static_response", "status_code": 404, "body": "Not found\n"}]}
      ],
      "errors": {"routes": [{"handle": [{"handler": "static_response", "status_code": 502,
        "body": "The server's web interface is not reachable right now.\n"}]}]}
    },
    "ask": {
      "listen": [":8081"],
      "automatic_https": {"disable": true},
      "routes": [
        {"match": [{"query": {"domain": ["survival.example.com"]}}],
         "handle": [{"handler": "static_response", "status_code": 200}], "terminal": true},
        {"handle": [{"handler": "static_response", "status_code": 404}]}
      ]
    }
  }}}
}
```

With zero routes the function is never called (the container is removed). `Host` header is passed through by Caddy's `reverse_proxy` by default; X-Forwarded-For/Proto are kept because the source is trusted.

Config (`internal/config`): `Images.Web` with toml `web`, default the pinned `caddy:<latest stable 2.x>-alpine@sha256:<index digest>` (determine on the test VM: `docker buildx imagetools inspect caddy:<tag>`; use a tag that is at least two weeks old, note "verified 2026-10-xx" next to it like the others) and add it to `Images.validate`; `WebProxyPort` (`web_proxy_port`, default 18100), `WebAskPort` (`web_ask_port`, default 18101), validated (1024–65535, different, and neither is 25565/25575). Wire them into `instances.Config`/`Host` in `cmd/agent/main.go`.

- [ ] **Step 1:** Tests: `WebAddress` (/28 → base+14; /24 → base+254; IPv6 → false; /31, /32 → false); `WebContainer` hardening (user, caps, ro rootfs, tmpfs, binds ro, ports only on 127.0.0.1, labels, network, unpinned image rejected); golden test of `RenderWebConfig` for two routes in reverse id order (output sorted by id) — update with `-update` flag pattern if the package has one, otherwise compare to the checked-in file; config defaults and validation (equal ports, 80, 25565, missing digest). FAIL.
- [ ] **Step 2:** Implement; PASS on the VM (`go test ./internal/agent/instances ./internal/config -run 'Web|Agent'`).
- [ ] **Step 3:** `make check`, `make test-vm SUITE=go`, `SUITE=lint`; commit (`feat(agent): mcpanel-web container and config rendering`).

---

### Task 3: Applying web routes in the instance manager

Owner: `agent`. Depends on task 2 and on the `WebFilter` interface below (implemented by task 4; tests use a fake).

**Files:**
- Create: `internal/agent/instances/web_manager.go`, `internal/agent/instances/web_manager_test.go`
- Modify: `internal/agent/instances/manager.go` (Config/Manager fields, `Tick`), `internal/agent/instances/network.go` (web network in `managedNetworks`/egress, disconnect `mcpanel-web` in `removeInstanceNetworkLocked`, allow it in `removeOrphanNetwork`, generic subnet allocation reused for the web network)
- Modify: `internal/agent/docker/network.go` (`NetworkConnectIP(ctx, network, container string, ip netip.Addr) error`), `internal/agent/docker/network_test.go`
- Modify: `cmd/agent/main.go` (`Web: fw`, `agent.WithWeb(mgr)`)
- Modify (docs): `docs/superpowers/specs/2026-10-10-mc-panel-instances-design.md` (routing and isolation paragraph on `mcpanel-web`), `CLAUDE.md` (architecture paragraph of the agent: `mcpanel-web` container/network, `bridge mcpanel_web`; runtime names list), and spec §4.2 of the web ports spec: the allow rule targets the server network's subnet on the web port (the only other member, mc-router, listens on 25565 only), not the server's IP, so it can be written before the connect.

**Interfaces:**

```go
// WebFilter is the bridge port restriction of mcpanel-web (firewall.Backend, task 4).
type WebFilter interface {
	SetWebRules(ctx context.Context, rules []agentapi.WebRule) error
	WebRulesInPlace(ctx context.Context, rules []agentapi.WebRule) (bool, error)
}

// Config gains:
//   Web WebFilter // nil = none (tests that do not care)

// SetWebRoutes implements agent.Web. It validates req, stores it as the desired list,
// wakes the apply loop and returns at once with the status per requested id.
func (m *Manager) SetWebRoutes(ctx context.Context, req agentapi.WebRoutesRequest) (agentapi.WebRoutesResponse, error)

// applyWeb is one apply round (called from Tick and when woken by SetWebRoutes; serialised
// with webMu, takes netMu for network changes).
func (m *Manager) applyWeb(ctx context.Context)
```

Manager state (guarded by `webMu`): `webDesired []agentapi.WebRoute` (nil until the first PUT since agent start — **then applyWeb does nothing**), `webStatus map[int64]agentapi.WebRouteStatus`, `webLastConfig []byte`, a `webWake chan struct{}` (buffer 1; a goroutine started in `Run` calls `applyWeb` on wake; `Tick` calls it too).

`SetWebRoutes`: `req.Validate()` → error; Docker down (cached state) → `ErrDockerUnavailable`; store a clone; for each requested id answer the last status if the route (id, fqdn, port) is unchanged since it was applied, else `pending`; non-blocking send on `webWake`.

`applyWeb` order (fail-closed; idempotent; every step re-run each tick):
1. `desired == nil` → return. `len(desired) == 0` → remove container `mcpanel-web` (only if it carries `LabelWeb=1`), remove network `mcpanel-web` (only if labelled), `SetWebRules(nil)`, clear status; return.
2. Ensure the `mcpanel-web` network (labels `{LabelNetwork:"1", LabelWeb:"1"}`, IPv4, subnet from the instance pool via the same allocation code as `createInstanceNetwork`), then `syncEgress` (the network is part of `managedNetworks` now) and check `egressCovers`.
3. For each route: the server container `mcpanel-mc-<id>` must exist and be managed (`managedID`), else status `error` "server container missing"; `ensureInstanceNetwork(ctx, id)` (F1: labels, IPv4, egress) else `error` "server network missing"; compute `WebAddress(subnet)`.
4. `SetWebRules` with one rule per ok route **before** any connect; on error every route is `error` "port restriction not applied" and nothing is connected.
5. Render the config for the ok routes, `writeReadable(WebDir, "caddy.json", data)`.
6. Ensure the container (create/replace/start like `refreshRouter`: template hash label `LabelWebTemplate`, `sameImage`, foreign container named `mcpanel-web` without the label → every route `error`, never touched).
7. Connect: for each ok route not yet connected, `NetworkConnectIP(net, WebName, addr)`; disconnect `mcpanel-web` from every labelled `mcpanel-net-*` it is on that is not ok/desired (skip ids that are `busy`, like `syncRouterNetworks`).
8. Verify: for each ok route `GET http://127.0.0.1:<ask port>/?domain=<fqdn>` (2 s timeout, `NewRequestWithContext`) → 200 means `active`; otherwise `pending` (Caddy's watch polls once per second; the next round retries) and after 3 failed rounds `error` "web router did not load the config". Make the checker injectable (`Config.WebAsk func(ctx, fqdn) (bool, error)`; default does the HTTP request) so tests need no listener.

Network edits in `network.go`:
- `isWebNetwork(n)` (name `mcpanel-web`, bridge, both labels) is included in `managedNetworks` subnets (egress covers it).
- `removeInstanceNetworkLocked` disconnects `WebName` too when it carries `LabelWeb=1`.
- `removeOrphanNetwork` treats `WebName` like `RouterName` in the "other containers" check.

- [ ] **Step 1:** Tests with the Docker mock (`docker/dockertest`) and a fake `WebFilter`:
  - first PUT creates network, writes config, creates container, connects with the fixed IP; rules set before the first connect (record call order);
  - PUT answers in < 1 s while the image pull of the mock blocks (Review focus 3);
  - a second PUT without one route disconnects that network and rewrites the config;
  - empty list removes container and network; `nil` desired (fresh manager, Tick) touches nothing even with an existing `mcpanel-web` (Review focus 1);
  - a foreign `mcpanel-web` container (no label) and an unlabelled `mcpanel-net-7` are never touched;
  - route for an id without container → `error` "server container missing", other routes still `active`;
  - `SetWebRules` failure → nothing connected, all `error`;
  - deleting a server (`Remove`) with `mcpanel-web` attached removes the network; an orphan network with only `mcpanel-web` and the router is removed (Review focus 2);
  - ask check: 200 → active; three failures → error;
  - template change replaces the container.
  FAIL.
- [ ] **Step 2:** `docker.NetworkConnectIP` with a test of the request body (`EndpointConfig.IPAMConfig.IPv4Address`), then implement the manager; PASS.
- [ ] **Step 3:** Docs edits listed above.
- [ ] **Step 4:** `make check`, `make test-vm SUITE=go`, `SUITE=lint`; commit (`feat(agent): apply web routes through mcpanel-web`), push, report.

---

### Task 4: Bridge port restriction `bridge mcpanel_web`

Owner: `firewall`. Only `internal/agent/firewall/`.

**Files:**
- Modify: `internal/agent/firewall/firewall.go` (constants, `Backend` gains the two methods), `nftables_linux.go` (or a new `web_linux.go`), `noop.go`, `noop_test.go`
- Create: `internal/agent/firewall/web_linux_test.go` (rule expressions, no root), extend `nftables_integration_test.go` (root, CI job `nftables`)

**Interfaces:**

```go
const (
	WebTableName     = "mcpanel_web" // family bridge
	WebChainName     = "forward"
	WebChainPriority = -10
)

// Backend gains (same signatures as instances.WebFilter):
SetWebRules(ctx context.Context, rules []agentapi.WebRule) error
WebRulesInPlace(ctx context.Context, rules []agentapi.WebRule) (bool, error)
```

Semantics: `SetWebRules` replaces the whole table `bridge mcpanel_web` in one batch (add table, add chain `forward` type filter hook forward priority -10 policy accept, flush chain, add rules). Per rule, in this order:
1. `meta protocol ip ip saddr <Source> ip daddr <Dest> tcp dport <Port> accept`
2. `meta protocol ip ip saddr <Source> drop`

Rules are sorted (Source, Dest, Port) so equal input gives equal rule sets. Empty `rules` deletes the table (and is a no-op if it does not exist). Stateless on purpose: replies come from the server's side and are not matched; a connection from `mcpanel-web` is only possible to `Dest:Port`. Invalid input (Source not IPv4, Source not inside Dest, Port < 1024) → error, nothing changed. `WebRulesInPlace` lists the chain and compares the rule count and expressions with what `SetWebRules` would write (like `EgressInPlace`). Nothing outside table `bridge mcpanel_web` is created, modified or deleted. The manager (task 3) calls `WebRulesInPlace` every tick and re-applies when it is false.

Noop: stores the rules, logs them, `WebRulesInPlace` → true.

- [ ] **Step 1:** Unit tests of the expression builder (bytes of saddr/daddr/port, order accept before drop, sorting, invalid input). FAIL → implement → PASS (`go test ./internal/agent/firewall` on the VM).
- [ ] **Step 2:** Root integration test (runs in CI job `nftables`; locally only `go vet`): set two rules, list them with `nft -j list table bridge mcpanel_web`, set one rule (the other is gone), `WebRulesInPlace` true, flush from outside → false, empty list removes the table, the tables `inet mcpanel` and a foreign test table stay untouched.
- [ ] **Step 3:** `make check` (vets the Linux-only files), `make test-vm SUITE=go`, `SUITE=lint`; commit (`feat(firewall): bridge port restriction for mcpanel-web`), push, report.

---

### Task 5: Panel: `web_port`, view fields, reconcile of the route list

Owner: `contracts` (as panel teammate). Only `internal/servers/` (and `cmd/panel/app.go` if wiring changes).

**Files:**
- Modify: `internal/servers/servers.go` (`updateServerRequest.WebPort`, `handleUpdateServer`, view), `internal/servers/view*.go` or wherever `serverJSON`/`view` live, `internal/servers/reconcile.go`, `internal/servers/handler.go` (agent interface gains `WebRoutes`, `noAgent`), `internal/servers/settings.go` (trigger after base domain change if it does not already)
- Create: `internal/servers/web.go` (route list builder, status store), `internal/servers/web_test.go`
- Modify: the agent fakes in `internal/servers/helpers_test.go`

**Interfaces:**

```go
// webRoutes builds the complete desired list (spec §3.3) from the instance rows and the
// base domain: every row with web_port set, fqdn = name + "." + baseDomain; empty when
// baseDomain is "". Sorted by id.
func webRoutes(rows []store.ServersListInstancesRow, baseDomain string) []agentapi.WebRoute

// webState is kept in memory (like the panel errors): the agent's last answer per id, or
// "agent unavailable" when the PUT failed.
type webState struct {
	mu   sync.Mutex
	byID map[int64]agentapi.WebRouteStatus
}
```

Request handling (`PATCH /servers/{id}`; already admin + sudo, so non-admins get 403 from rbac): `web_port` is `optional[int]` (null = off). If set and not null: `agentapi.ValidWebPort` else `400 validation`; base domain empty → `409 conflict` "base domain required". If the value differs from the stored one: `ServersSetWebPort` (no revision bump, so it does not count as a spec change in `diff`), audit `server.web_port_update` `{name, old, new}` (old/new as number or null), `h.Trigger()`. A request with only `web_port` must not write `ServersUpdateInstance`.

View: `web_status` = `off` when `web_port` is NULL or base domain empty; otherwise the agent status from `webState` (`pending` when none yet). `web_url` = `https://<name>.<base domain>` when port set and base domain set. `web_port` and `web_error` only for admins (`redactFor` clears them for non-admins).

Reconcile: at the end of `ReconcileOnce` (also on the early return when the agent or Docker is down) send `webRoutes(rows, set.BaseDomain)` via `agent.WebRoutes`; store the answer; on error mark every route `error` "agent unavailable" (sanitized). The list is sent on every pass, so delete, base domain change and port change are covered without extra hooks; `Trigger()` after a `web_port` change and after a base domain change makes it quick.

- [ ] **Step 1:** Tests: non-admin PATCH with `web_port` → 403; admin without sudo → `sudo_required`; admin + sudo → 200, audit entry with old/new, revision unchanged; invalid ports → 400; no base domain → 409; viewer GET sees `web_url`/`web_status`, not `web_port`/`web_error`; `webRoutes` with rename-free cases: delete (row gone), base domain change (fqdn changes), base domain removed (empty list, `web_status` off — Review focus 5); reconcile sends the list even when no instance needs an action; agent error → `error` "agent unavailable". FAIL.
- [ ] **Step 2:** Implement; PASS (`go test ./internal/servers ./cmd/panel` on the VM).
- [ ] **Step 3:** `make check`, `make test-vm SUITE=go`, `SUITE=lint`; commit (`feat(servers): web port per server`), push, report.

---

### Task 6: UI: settings section, header link, mock

Owner: `web`. Only `web/`.

**Files:**
- Create: `web/src/features/servers/web-port-section.tsx`, `web/src/features/servers/web-port-section.test.tsx`
- Modify: `web/src/features/servers/server-settings-tab.tsx` (render the section for admins, before the danger zone), `server-header.tsx` and `server-overview.tsx` (link), `web/src/features/servers/api.ts` (mutation sends `web_port`), `web/src/mocks/` (fixtures with `web_status`, `web_url`, `web_port`; PATCH handler applies `web_port`, answers 409 without base domain, switches to `active` after the update), i18n files (de/en) for all strings below
- Create: `web/e2e/web-port.spec.ts` (mock e2e)

Behaviour (spec §3.4):
- Section "Web interface" / „Weboberfläche“ (`Section` component): switch "Publish web port"; port input (number, prefilled `8100`, client-side check with the global port rule, message for 25565/25575); status badge (`off`/`pending`/`active`/`error` + `web_error`) using `--status-*` tokens; the URL as a link when set; the hint text: "Public for anyone with the link; the plugin is responsible for its own security. BlueMap: set `accept-download: true` in `plugins/BlueMap/core.conf` and keep the web server enabled on this port." (de translation alongside).
- Saving sends `PATCH /servers/{id}` with `{web_port: <n> | null}`; the global sudo dialog handles `sudo_required` (no own sudo code).
- No base domain: the section shows a short explanation and a link to the settings page where the base domain is set; the switch is disabled. A `409` with message `base domain required` shows the same text.
- Header and overview: when `web_status === 'active'` and `web_url` is set, a link "Open web map" / „Webkarte öffnen“ with `target="_blank"` and `rel="noopener noreferrer"`, for everyone who sees the server.
- Non-admins never see the section.

- [ ] **Step 1:** Vitest (through `src/test/render-section.tsx`): admin sees the section, non-admin not; default 8100; 25565 rejected; save sends `{web_port: 8100}`; switch off sends `null`; no base domain → explanation, disabled; header link only for `active` with the right `rel`. FAIL → implement → PASS (`make test-vm SUITE=web`).
- [ ] **Step 2:** Mock e2e: sign in (mock), open a server's settings, publish, see the link in the header, unpublish, link gone (`make test-vm SUITE=e2e`).
- [ ] **Step 3:** `make test-vm SUITE=shots`; look at the settings and header shots in dark/light, desktop/mobile.
- [ ] **Step 4:** `make check`, `make test-vm SUITE=lint`; commit (`feat(web): web port section and web map link`), push, report.

---

### Task 7: Installer: catch-all Caddy site, agent.toml keys, doctor, uninstall, operator docs

Owner: `installer`. Only `scripts/install/`, `deploy/`, `docs/install.md`, `docs/dev/acceptance.md`.

**Files:**
- Create: `deploy/caddy/mc-panel-web.caddy`
- Modify: `scripts/install/caddy.sh` (write/validate/restore the new file, import line, global `on_demand_tls` block in the installer's own Caddyfile), `deploy/config/agent.toml` (`web_proxy_port`, `web_ask_port`, rendered with the same values the Caddy file gets), `scripts/install/config.sh` if the values are rendered there, `scripts/install/doctor.sh` (`web_router`, `web_catchall`), `scripts/install/uninstall.sh` (file, import line, container `mcpanel-web` with label, network `mcpanel-web` with label, table `bridge mcpanel_web`), `scripts/install/update.sh` if it lists managed Caddy files, `scripts/install/tests/*` (Bash tests)
- Modify: `docs/install.md` (section "Web interfaces"), `docs/dev/acceptance.md` (manual BlueMap check in the instances area)

`deploy/caddy/mc-panel-web.caddy` (placeholders like `mc-panel.caddy`):

```caddy
# Managed by mc-panel install.sh {{VERSION}}; install.sh update rewrites this file.
# Web interfaces of the Minecraft servers (https://<server>.<base domain>), routed by the
# agent's mcpanel-web container. Certificates are issued on demand, only for names that
# mcpanel-web allows (on_demand_tls ask in the Caddyfile's global options).
https:// {
	tls {{TLS_ON_DEMAND}}
	log {
		output file /var/log/caddy/mc-panel-web.log {
			roll_size 50MiB
			roll_keep 14
		}
	}
	# header X-Robots-Tag "noindex"
	reverse_proxy 127.0.0.1:{{WEB_PROXY_PORT}}
}
```

`{{TLS_ON_DEMAND}}` is `{ on_demand }` for Let's Encrypt and `internal { on_demand }` when the installer runs with internal TLS (test VM). The installer's own Caddyfile gets a global options block at the top:

```caddy
{
	on_demand_tls {
		ask http://127.0.0.1:{{WEB_ASK_PORT}}/
	}
}
```

plus the line `import mc-panel-web.caddy` next to `import mc-panel.caddy`. Keep the panel site more specific (a named host always wins over `https://`). With a foreign Caddyfile: print the hint with both snippets, as for the panel site. Validate with `caddy validate` before the reload and restore on failure, exactly like `mc-panel.caddy`. All system calls through `sys_*` wrappers; new functions use the `cad_`/`doc_`/`uni_` prefixes.

`doctor`:
- `web_router`: no container `mcpanel-web` → ok ("no web ports published"); otherwise it is running, `docker port mcpanel-web` lists only `127.0.0.1:` bindings, and `nft list table bridge mcpanel_web` succeeds.
- `web_catchall`: `curl -sk --resolve doctor-check.invalid:443:127.0.0.1 https://doctor-check.invalid/` must not answer 200 (no certificate for an unknown name).

- [ ] **Step 1:** Bash tests (`scripts/install/tests`): render with LE and internal TLS, the global block and both import lines in the installer's Caddyfile, restore after a failed validate, foreign Caddyfile hint, doctor results (no container ok; non-local binding fails; missing table fails), uninstall removes file/import/container/network/table and leaves an unlabelled `mcpanel-web` container alone. FAIL → implement → PASS (`make test-vm SUITE=lint` runs `scripts/install/test.sh` and shellcheck).
- [ ] **Step 2:** `docs/install.md` "Web interfaces": wildcard DNS record `*.<base domain>` required; certificates on demand (first request takes a few seconds); BlueMap steps (`accept-download: true` in `plugins/BlueMap/core.conf`, webserver port 8100, publish the port in the server settings); public by design; ports 18100/18101 on 127.0.0.1 and the `agent.toml` keys; `/etc/caddy/mc-panel-web.caddy` and `/var/log/caddy/mc-panel-web.log`; troubleshooting via `journalctl -u caddy`. `docs/dev/acceptance.md`: manual check with a real BlueMap on the test VM.
- [ ] **Step 3:** `make check` (`bash -n`), `make installer`; commit (`feat(installer): catch-all site for server web interfaces`), push, report.

---

### Task 8: CI acceptance shard `web`

Owner: `installer`, phase 3 (after tasks 1–7 are merged into `feat/web-ports`).

**Files:**
- Create: `scripts/ci-instances/web.sh`
- Modify: `scripts/ci-instances.sh` (shard list and header comment), `.github/workflows/ci.yml` (`shard web '^internal/agent/(instances|firewall)/|^internal/servers/|^deploy/caddy/'`)

The shard (spec §7, CI acceptance), using `scripts/ci-instances/lib.sh` helpers like the other shards:
1. Install host Caddy on the runner (Ubuntu package or the official repository as the installer does) and run it with a minimal Caddyfile: global `on_demand_tls { ask http://127.0.0.1:18101/ }` and the rendered `mc-panel-web.caddy` with `tls internal { on_demand }`.
2. Set a base domain, create two Paper servers through the panel API; in server A start a minimal HTTP responder on 8100 inside its container (e.g. `docker exec` a `busybox httpd` or a `python3 -m http.server` if the image has it; no BlueMap download) that serves a known body.
3. Publish 8100 on A (admin + sudo through the API). Wait until `web_status` is `active`.
4. `curl -sk --resolve <a>.<base>:443:127.0.0.1 https://<a>.<base>/` → 200 with the body.
5. `curl -sk --resolve unknown.<base>:443:127.0.0.1 …` → TLS failure (no certificate).
6. From server B's container, connecting to A's IP on 8100 fails (isolation).
7. From `mcpanel-web` (`docker exec mcpanel-web wget -T 3 -qO- http://mcpanel-mc-<a>:25575` or a second responder port) the non-published port is unreachable.
8. Unpublish: the route returns 404/no certificate, and with zero routes the container `mcpanel-web` and network `mcpanel-web` are gone, and table `bridge mcpanel_web` is gone.

- [ ] **Step 1:** Write the shard; `bash -n` and shellcheck through `make test-vm SUITE=lint`.
- [ ] **Step 2:** Push; the PR's CI runs the `web` shard (root job, runs only in CI). Fix failures from `gh run view <id> --log-failed`.
- [ ] **Step 3:** Commit (`test(ci): acceptance shard for web ports`), report.

---

## Integration (lead)

- Merge each teammate branch into `feat/web-ports` after review (`make check`, `make test-vm SUITE=all` on the integrated tree).
- Open the PR `feat/web-ports` → `main`, watch `gh pr checks <n> --watch` until `CI OK`, mark it ready.
- `make deploy-testvm REF=feat/web-ports` and run the manual BlueMap check from `docs/dev/acceptance.md`.
- Delete this plan in the PR before merge (the spec stays).
