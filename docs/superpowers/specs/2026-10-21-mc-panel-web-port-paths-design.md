# mc-panel: several web ports per server under paths (design spec)

- **Date:** 2026-10-21
- **Status:** implemented
- **Author:** Jannes Göbel (with Claude)
- **Addendum to:** `2026-10-17-mc-panel-web-ports-design.md` (web ports). Everything there stays in force unless this document changes it. Section numbers like "WP §4.2" refer to that spec.

## 1. Goal

Today a server publishes exactly one HTTP port, at `https://<name>.<base domain>` (BlueMap). Mods beyond BlueMap also bring their own small web servers, and they should be publishable next to it, under the same name:

| Port in the container | Mod | Public URL |
|---|---|---|
| 8100 | BlueMap | `https://survival.example.com/` |
| 8200 | AFK-smoke resource pack | `https://survival.example.com/afk-smoke/` |

The motivating case is the AFK-smoke mod. Its web server answers only `GET`/`HEAD` on `/pack.zip` and on `/afk-smoke/pack.zip` (so it works with and without prefix stripping) and 404 on everything else. The operator puts the public link (`https://survival.example.com/afk-smoke/pack.zip`) into `config/afk-smoke.json`, and Minecraft clients download the pack from there. With an empty link the mod sends no pack.

### 1.1 Decisions

| Question | Decision |
|---|---|
| Shape of a route | A server has a list of **web routes**, each a **path** and a **port**. Path `""` is the root (`https://<fqdn>/`); any other path `p` is published at `https://<fqdn>/<p>/`. |
| The first route | Defaults to the root: the UI prefills the first row with the root and port 8100. |
| Who names the path | **The admin types it** per route (for example `afk-smoke`). |
| Prefix | **Stripped** before forwarding: `/afk-smoke/pack.zip` reaches the mod as `/pack.zip`. |
| Who may publish, who may open | Unchanged (WP §1.1): admins with sudo publish; the pages are public for anyone with the link. |
| Names and certificates | Unchanged: everything stays under the one name `<name>.<base domain>`, so there are no new subdomains, no new certificates and no change to the ask endpoint (WP §4.3). |
| Existing servers | A stored `web_port` becomes the root route with that port. Nothing changes for them. |

This reverses two non-goals of WP §1.2 on purpose: "more than one web port per server" and "path prefixes (`/map/`)". The other non-goals stay, including extra subdomains like `map.survival.example.com`.

### 1.2 Non-goals

- Making apps work under a sub-path when they do not support it. Many web UIs link their assets absolutely (`/style.css`), which breaks under a prefix. The root route is there for such apps (BlueMap); the UI says so (§5).
- Nested paths (`/a/b/`), per-route access control, per-route status, and non-HTTP ports.
- Configuring the mods themselves (for example writing `config/afk-smoke.json`).

## 2. Rules for routes

These rules apply in the panel (API validation, DB CHECKs) and again in the agent (`agentapi`), which never trusts the panel.

- **Path:** `""` (root), or 1–32 characters of `[a-z0-9-]` that start and end with a letter or digit. It is a single segment: no `/`, no dots, no uppercase, so the path cannot carry traversal or injection.
- **Port:** the existing rule `agentapi.ValidWebPort` (1024–65535, not 25565 or 25575).
- **Per server:** at most **8 routes** (builder's call: the bound only needs to be small, and 8 is plenty); paths unique; ports unique (two paths to one port add nothing and would complicate the port restriction); at most one root. A server without a root route is allowed: then `https://<fqdn>/` answers the static 404 of `mcpanel-web`.
- **Order:** the panel stores and sends routes sorted by path, root first. Every list on the wire and in the DB is in that order, so diffs, audit entries and golden configs are stable.

## 3. Panel

### 3.1 Data

A new migration:

1. creates `web_routes (server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE, path TEXT NOT NULL, port INTEGER NOT NULL, PRIMARY KEY (server_id, path), UNIQUE (server_id, port))` with CHECKs for the path and port rules of §2 (the 8-route bound is enforced in the handler, inside the same transaction as the write);
2. copies every `instances.web_port IS NOT NULL` into `web_routes (server_id, '', web_port)`;
3. drops `instances.web_port`.

The Down migration reverses it, keeping only the root route's port. As before, routes are not part of the instance spec and never bump `revision`. New sqlc queries in `internal/store/queries/servers.sql` with the `Servers…` prefix: list all routes (for the reconcile), list a server's routes, and replace a server's routes (delete and insert in one transaction).

### 3.2 API

`PATCH /servers/{id}` replaces `web_port` with **`web_routes`**: an array of `{path, port}`, the complete desired list for that server. `[]` turns publishing off. The rules about who may send it are unchanged (admin plus sudo, `403` otherwise; `409 conflict` "base domain required" when the list is not empty and no base domain is set). The panel sorts the list by path before it validates, so clients need not send it in order (the agent, in contrast, rejects an unsorted list). An invalid list answers `400 validation_failed` naming the first broken route.

`GET /servers` and `GET /servers/{id}`:

- `web_status` and `web_error`: unchanged, one status per server (the agent applies a server's routes all or nothing, §4).
- `web_url`: `https://<fqdn>` when a **root route** exists and a base domain is set; absent otherwise. This is the link in the server header and the overview ("Open web map"), visible to everyone who can see the server, as today.
- `web_routes` (admins only, replaces `web_port`): `[{path, port, url}]`, where `url` is `https://<fqdn>/` or `https://<fqdn>/<path>/`; `url` is absent while no base domain is set. Present as `[]` when there are no routes.

Users with only `server.view` see neither the extra routes nor their ports. Builder's call: the extra routes are mostly machine endpoints (a pack download), so the header keeps one link.

Audit: a new action `server.web_routes_update` with `old` and `new` (both route lists). The action `server.web_port_update` stays in `internal/audit/types.go` so that existing entries still verify and display, but nothing writes it any more.

OpenAPI, generated TS types and the MSW mock change with it, as for every API change.

### 3.3 Reconcile

`webRoutes` in `internal/servers/web.go` builds one agent route per server that has at least one web route: `{id, fqdn, targets: [{path, port}, …]}`. Everything else in WP §3.3 holds: the complete list on every pass and after every relevant change, and routes are independent of the power state.

## 4. Agent and `mcpanel-web`

### 4.1 Agent API

`PUT /v1/web/routes` keeps one entry per server and gains a list of targets:

```json
{"routes": [{"id": 7, "fqdn": "survival.example.com",
             "targets": [{"path": "", "port": 8100}, {"path": "afk-smoke", "port": 8200}]}]}
```

`WebRoute.Port` is replaced by `Targets []WebTarget` (`Path string`, `Port int`). Validation adds the rules of §2 (1–8 targets, path syntax, unique paths and ports, at most one root, sorted as in §2; a list in another order is rejected rather than sorted, so the agent never has to guess). `MaxWebRoutes` stays 500 servers; `MaxWebRequestBytes` grows to fit 500 × 8 targets (builder's call, about 1 MiB). The reply stays one status per id.

Panel and agent ship in the same release and `install.sh update` swaps them together, so there is no compatibility shim for the old `port` field. During the swap the agent may briefly reject the panel's old-shaped request; the next reconcile pass after the swap fixes that.

### 4.2 Caddy config

For each route, `RenderWebConfig` emits one host-matched route whose handler is a subroute, in this order:

1. for every non-root target `p`:
   - path exactly `/<p>` → `308` redirect to `/<p>/` (a query string on the bare prefix is dropped, builder's call);
   - path `/<p>/*` → `rewrite` with `strip_path_prefix: "/<p>"`, set the request header `X-Forwarded-Prefix: /<p>`, then `reverse_proxy mcpanel-mc-<id>:<port>`;
2. the root target, if any → `reverse_proxy mcpanel-mc-<id>:<port>` for every other path;
3. otherwise the static 404 page.

Non-root paths therefore always win over the root app for their prefix. Only validated paths go into matchers and headers (no user text, as before). The golden test covers a root-only route (unchanged output apart from the subroute wrapper), a mixed route, a route without root, and that a path like `afk-smokeX` does not match `/afk-smoke/`.

### 4.3 Port restriction

`agentapi.WebRule.Port` becomes `Ports []uint16`: still one rule per source address (one per attached server network; `sortedWebRules` keeps rejecting duplicate sources), now allowing TCP to the subnet on any of the server's published ports, as an anonymous set or a set of `dport` matches. Everything else from that address is dropped, and the fail-closed ordering of WP §4.2 holds unchanged. A newly added port enters the rule before the new config routes to it. For a removed port the order does not matter for safety, because dropping more is always safe. The nftables integration test (root, CI) covers two allowed ports and one denied port on the same network.

## 5. UI

The server settings section "Web interface" becomes **"Web interfaces"** (admins only):

- the **root row** is a fixed slot that is always shown first: path `/` (not editable), a port field, the resulting URL, and its own switch instead of a remove button. Switched off, the root is not published, and the next save has no root. Switched on with an empty port, the field is prefilled with 8100. Builder's call: a fixed slot means the root can always be turned on again, also for a server whose stored routes have no root;
- below it, the **path rows**, each with a **path** field, a **port** field, the resulting URL, and a remove button;
- "Add port" adds a path row with an empty path, required, validated live with the rules of §2. It is disabled once the published routes (the root, if switched on, plus the path rows) reach 8;
- one "Save" for the whole list, through the global sudo dialog; turning the section off sends `[]`;
- status, error and the base-domain hint as today;
- the hint gains one sentence: "Under a path, the prefix is stripped before the request reaches the mod (`/afk-smoke/pack.zip` arrives as `/pack.zip`). Apps that link their files absolutely only work at the root." The BlueMap hint stays.

The header and overview link is unchanged (root only). de and en strings; design system as in the UI redesign spec. Vitest covers adding and removing path rows, path validation, the 8-route limit (root counted only when on), switching the root off and on again (also for stored routes without a root), and the request body; the mock e2e test publishes a root and a path route and sees both URLs.

## 6. Error handling

Changes to WP §6 only:

| Case | Behaviour |
|---|---|
| A mod does not listen on its port | `502` for that path only; the other routes of the server keep working; `web_status` stays `active`. |
| `/afk-smoke` without trailing slash | `308` to `/afk-smoke/`. |
| A path no route covers, no root | Static 404 from `mcpanel-web`. |
| The agent rejects a server's targets | The whole server's routes report `error` (all or nothing per server); other servers are unaffected. |

## 7. Testing

- **Go, agent:** target validation (syntax, limits, duplicates, order, unknown fields); golden configs of §4.2; bridge rules with several ports per network (unit and nftables integration test as root in CI).
- **Go, panel:** the migration (a stored `web_port` becomes a root route; Down keeps the root); `PATCH` validation, the 8-route bound, `403`/`409`, audit with old and new lists; `webRoutes` builds targets; viewers see neither `web_routes` nor extra paths; `web_url` only with a root route.
- **Web:** as in §5, plus the mock handlers and `handlers.test.ts`.
- **CI acceptance (`scripts/ci-instances/web.sh`):** the minimal responder listens on two ports with different bodies. Through host Caddy:
  - `/` answers the root port's body;
  - `/extra/x` reaches the second port as `/x` (the responder serves a file `x`; without stripping it would answer 404). `X-Forwarded-Prefix` is covered by the golden config test;
  - `/extra` redirects with `308`;
  - a third, unpublished port stays unreachable from `mcpanel-web`;
  - removing the extra route makes `/extra/x` fall through to the root.
- **Installer:** no change (the Caddy file and `agent.toml` keys stay); `doctor` is unchanged.

## 8. Documentation

- **WP spec:** a note at §1.2 that the two reversed non-goals are now covered by this addendum.
- **`docs/install.md`**, section "Web interfaces": several ports per server, the path rules, prefix stripping, and the AFK-smoke example link.
- **`docs/dev/acceptance.md`:** a manual check with BlueMap at the root and a second mod under a path.
- **`CLAUDE.md`:** "one published HTTP port per server" becomes "published HTTP ports per server, the root plus paths", and this spec goes into the spec list.
