# Plan: mrpack-Import (Issue #78)

Spec: `docs/superpowers/specs/2026-10-14-mc-panel-mrpack-import-design.md`. Zwei Teammates, zwei Branches, zwei PRs. Die API wird zuerst gemergt.

| Teammate | Branch | Tasks | Dateien |
|---|---|---|---|
| `mrpack-api` | `feat/mrpack-api` (von `origin/main`) | 1–6 | `api/openapi.yaml`, `web/src/api/schema.d.ts`, `internal/httpx/errors.go`, `internal/plugins/**` (Go), `scripts/ci-instances.sh`, `docs/dev/acceptance-tp3c.md`, Stub-Mocks in `web/src/mocks/` (nur Task 1) |
| `plugins` | `feat/mrpack-ui` (von `feat/mrpack-api`, sobald Task 1 gepusht ist) | 7–9 | `web/src/features/plugins/**`, `web/src/mocks/` (Plugin-Mocks), `web/e2e/**` (mrpack-Tests/Fixtures), `web/package.json`/`package-lock.json` (fflate), i18n-Keys `plugins.mrpack.*` |

## Tasks `mrpack-api`

1. **Contract zuerst:** die zwei Routen aus Spec §4 in `api/openapi.yaml` (Schemas `MrpackIndex`, `MrpackImportRequest`, `MrpackPlan`, Fehler-Responses), neue Codes `mrpack_incompatible`, `mrpack_version_mismatch` in `internal/httpx/errors.go` und im Error-Enum, `make generate`, minimale MSW-Handler, damit `handlers.test.ts` grün bleibt. Die Routen registrieren mit Handlern, die vorerst `501`/Stub liefern, damit `openapi_test.go` passt. Commit, Push, dann dem Lead melden. Ab da kann `plugins` darauf aufbauen.
2. **Index-Parsing und -Validierung** (`internal/plugins/mrpack.go`): Typen, `parseIndex` nach Spec §4.1, Tabellen-Tests inkl. Pfad-Angriffe (`../`, absolute Pfade, Backslash, NUL, doppelte Hashes/Namen, zu viele Einträge).
3. **Upstream:** Batch-Abfrage `GET /v2/projects?ids=[…]` in `internal/plugins/upstream` (gecacht wie `modrinthProject`, `server_side` im Typ `Project`), Stückelung von `version_files` bei > 1000 Hashes, Testdaten in `testdata/`.
4. **Plan und Preview:** Kompatibilität (Loader, Minecraft- und Loader-Version gegen Instanz und Katalog), Klassifizierung nach Spec §3 (Reihenfolge einhalten), `kept`, `ignored_files`, Handler `preview`. Tests mit Fake-Upstream für jede `action`/`reason`.
5. **Import-Job:** Job-Art `mrpack`, Plan neu berechnen, `exclude` nur für optionale Einträge, `accept_mismatch`, Schritte über die vorhandene install/update-Mechanik (`runJob` mit `stopOnError=false`), Audit je Jar mit `via: "mrpack"` plus Abschluss-Eintrag `plugin.mrpack_import`, Cache-Invalidierung. Tests inkl. deaktiviertem Jar und fehlgeschlagenem Schritt.
6. **Abnahme:** Erweiterung von `scripts/ci-instances.sh` nach Spec §6 (Fabric-Server, Fabric API installiert, Sodium `client_only`, unbekannter Hash `not_on_modrinth`) und Abschnitt „mrpack-Import“ in `docs/dev/acceptance-tp3c.md`.

## Tasks `plugins`

7. **Parser:** `fflate` als Abhängigkeit (`npm install fflate` im Worktree; das ist eine Projekt-Abhängigkeit, kein globales Paket). `readMrpackIndex(file: File)` entpackt nur `modrinth.index.json` (Filter), Grenze 5 MiB, verständliche Fehler (kein ZIP, kein Index, ungültiges JSON). Vitest mit kleinen, im Test erzeugten ZIPs.
8. **UI:** Knopf „Modpack importieren (.mrpack)“ (nur fabric/neoforge), Diff-Dialog nach Spec §5 (Gruppen, optionale Checkboxen, Gründe, Hinweise), Versionsabweichung mit Admin-Knopf „Server-Version umstellen“ (`PATCH /servers/{id}`, Sudo-Dialog greift automatisch, danach Preview neu) bzw. „Trotzdem importieren“, Start des Jobs, Fortschritt mit `job-progress`, Banner „Neustart nötig“. de/en-Texte.
9. **Mocks und E2E:** realistische MSW-Handler für preview/import (inkl. Mismatch- und Fehlerfall), Playwright-Test mit Fixture `web/e2e/fixtures/test.mrpack` (im Repo, klein, nur Index), Screenshot in `web/e2e/shots/`.

## Für beide

Worktree unter `../mc-panel-wt/<branch>`, `make check` vor jedem Commit. Vor dem Push `make test-vm` mit den betroffenen Suites (api: `go`, `lint`, `web`; ui: `web`, `e2e`, `shots`). PR als Draft mit „Part of #78“ (API) bzw. „Closes #78“ (UI), dann auf `CI OK` warten. Rückfragen als Issue-Kommentar in #78 mit „🤖 **Claude (<name>)**:“.
