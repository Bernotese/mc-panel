# Entwicklung

Lokale Entwicklung auf macOS (Apple Silicon), Release-Ziele `linux/amd64` und `linux/arm64`.
Architektur und Anforderungen: `docs/superpowers/specs/2026-10-08-mc-panel-foundation-design.md`.

## Toolchain

```bash
brew install go node sqlc goose golangci-lint goreleaser jq shellcheck lima
```

| Werkzeug | Version | Hinweis |
|---|---|---|
| Go | laut `go.mod` | CI liest dieselbe Datei (`go-version-file`) |
| Node.js | 24 (Active LTS) | CI pinnt die Hauptversion `24`; Node 26 wird erst am 2026-10-28 LTS. Beim Wechsel auf die nächste LTS-Linie alle `node-version`-Einträge in `.github/workflows/*.yml` gemeinsam anheben |
| golangci-lint | v2.14.0 | CI-Version; exakt dieselbe lokal: `GOBIN="$PWD/.dev/tools" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0` |
| GoReleaser | v2.18.2 (CI), lokal ≥ v2.8 | `make release` nutzt das lokal installierte `goreleaser` |
| sqlc | Version im Header von `internal/store/db.go` | CI installiert genau diese Version; wer mit einer neueren Version generiert, committet den geänderten Header mit |
| govulncheck | v1.8.0 | `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...` |
| Redocly CLI | 2.54.2 | `npx --yes @redocly/cli@2.54.2 lint api/openapi.yaml --extends=minimal` |
| actionlint | v1.7.12 | `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12` (nutzt `shellcheck`, falls installiert) |
| Lima | aktuell | nur für die Linux-VM, siehe `docs/dev/linux-vm.md` |

## Schnellstart

```bash
(cd web && npm ci)
make dev        # agent + panel (-dev) + Vite; UI unter http://localhost:5173
```

## Make-Targets

| Target | Wirkung |
|---|---|
| `make dev` | agent (falls gebaut) + `panel serve -dev` + Vite-Dev-Server, alles unter `./.dev/` |
| `make test` | Go-Tests (`test-go`) und Frontend-Tests (`test-web`) |
| `make test-race` | Go-Tests mit Race-Detector (`CGO_ENABLED=1`) |
| `make lint` | `golangci-lint run ./...` plus ESLint und TypeScript-Check im Frontend |
| `make generate` | `sqlc generate` und `npm run gen:api` |
| `make build` | Frontend bauen, dann `bin/panel` (und `bin/agent`) für das Host-System |
| `make release` | GoReleaser-Snapshot nach `dist/`: Archive für linux/amd64 und linux/arm64 plus `checksums.txt` |
| `make clean` | entfernt `bin/`, `dist/`, `.dev/` und das gebaute Frontend |

## CI lokal nachstellen

| CI-Job | Lokal |
|---|---|
| `go` | `go mod verify && go mod tidy -diff`, `sqlc generate && git status --porcelain internal/store`, `make test-go`, `make test-race`, `.dev/tools/golangci-lint run ./...`, `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...` |
| `web` | `cd web && npm ci && npm run lint && npm run typecheck && npm test && npm run build && npm run gen:api && git status --porcelain . && npm audit --audit-level=high` |
| `openapi` | `npx --yes @redocly/cli@2.54.2 lint api/openapi.yaml --extends=minimal` |
| `nftables` | nur unter Linux als root: in der VM oder per CI (`go test -tags nftintegration -exec sudo ./internal/agent/...`) |
| `build` | `make build`, `make release`, dann `file dist/*/panel dist/*/agent` |

Linux-spezifische Dateien lokal linten: `GOOS=linux .dev/tools/golangci-lint run ./...`.

## Lint-Konventionen

- Die Konfiguration steht in `.golangci.yml`; gosec läuft als Teil von golangci-lint.
- Ausnahmen gehören gezielt in `.golangci.yml` (Pfad + Linter + Text, mit Begründung) oder als `//nolint:<linter> // <Begründung>` direkt an die Zeile. Nie einen Linter global abschalten.
- Aktive Exclusion-Presets: nur `std-error-handling` (ignoriert ungeprüfte Fehler von `Close`, `Flush`, `os.Remove`, `fmt.Fprint*` u. Ä.). `common-false-positives` ist bewusst **nicht** aktiv: gosec G103 (`unsafe`), G204 (Subprozess mit Variable) und G304 (Datei-Pfad aus Variable) bleiben sichtbar und brauchen im Einzelfall eine gezielte Regel oder `//nolint:gosec // Begründung`.
- Global abgeschaltete gosec-Regeln (`linters.settings.gosec.excludes`): G104 (ungeprüfte Fehler, macht errcheck), G115 (Integer-Konvertierung, feuert bei jeder `uint32(len(x))`/`int(uid)`-Konvertierung; Grenzen werden dort geprüft, wo Eingaben nicht vertrauenswürdig sind) und G117 (Feldnamen wie `password`/`secret` in API-DTOs sind Teil des OpenAPI-Vertrags).
- Requests und DB-Zugriffe immer mit Kontext: `http.NewRequestWithContext`, `QueryContext`/`ExecContext`/`PingContext`. Ausnahme ist `net.Listen` im agent (`cmd/agent/`, `internal/agent/`).
- Dateimodus `0660` (agent-Socket, Spec §3) ist nur in `cmd/agent/` und `internal/agent/` für gosec G302 freigegeben, sofern er als Literal (`0o660`) in der Zeile steht; überall sonst und für andere Modi über `0600` ist eine Begründung nötig.
- In Tests sind feste Test-Passwörter, `math/rand` und lockere Dateimodi erlaubt.
- Formatierung: `gofmt` + `goimports` mit lokalem Präfix `mcpanel` (`golangci-lint fmt ./...`).

## Release

```bash
git switch main && git pull --ff-only
make test && make lint
git tag -a v0.1.0 -m "mc-panel v0.1.0"
git push origin v0.1.0
```

Der Workflow `.github/workflows/release.yml` prüft zuerst, dass der getaggte Commit in `main` enthalten ist (sonst Abbruch), führt `make lint`, `make test` und `make test-race` aus, baut das Frontend und veröffentlicht mit GoReleaser (nur dieser letzte Schritt sieht `GITHUB_TOKEN`):

- `mc-panel_<version>_linux_amd64.tar.gz`, `mc-panel_<version>_linux_arm64.tar.gz` (je `panel` und `agent`, ohne Unterordner)
- `checksums.txt` (SHA-256, Format `<hash>  <datei>`)

`<version>` ist der Tag ohne führendes `v`. Tags mit Suffix (z. B. `v0.2.0-rc.1`) werden als Pre-Release markiert.
Vor dem Taggen lokal prüfen: `make release && (cd dist && shasum -a 256 -c checksums.txt)`.

Hinweise zu GoReleaser:

- Meilenstein-Tags wie `phase0` sind kein SemVer und werden per `git.ignore_tags: ["phase*"]` in `.goreleaser.yaml` ignoriert.
- `goreleaser check` und die Git-Statusermittlung brauchen einen konfigurierten Git-Remote. Ohne Remote funktioniert `make release` im Snapshot-Modus trotzdem (Version `0.0.1-snapshot+none`); `goreleaser check` bricht mit `no remote configured` ab. Ohne den Repo-Remote zu ändern, hilft: `GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=remote.origin.url GIT_CONFIG_VALUE_0=https://github.com/example/mc-panel.git goreleaser check`.
- Ab Go 1.27 speichert `go version -m` die `-ldflags` nicht mehr. Ob die Version eingelinkt wurde, zeigt nur der String selbst: `grep -aF "$(jq -r .version dist/metadata.json)" dist/agent_linux_arm64_v8.0/agent` (setzt `var version` in `package main` voraus).

## Linux-VM

Produktionsnahe Tests (nftables, systemd, Benutzer `mcpanel`): siehe `docs/dev/linux-vm.md`.
