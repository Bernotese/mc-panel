# UI-Redesign – Lead: Team-Steuerung und Integration

> **Für den Lead.** Ergänzt `2026-10-14-ui-wave0-foundation.md` und die fünf Welle-1-Pläne (`2026-10-14-ui-wave1-t{1..5}-*.md`). Spec: `docs/superpowers/specs/2026-10-14-mc-panel-ui-redesign-design.md`.

## Entscheidungen des Leads zu den „Requests to lead“ der Pläne

| Anfrage | Entscheidung |
|---|---|
| T2: `web/src/test/media.ts` um Viewport `'tablet'` (900 px) erweitern | **Erlaubt**, die Datei gehört in Welle 1 T2. Andere Teammates ändern sie nicht. |
| T2: Wert von `nav.dashboard` → „Server“ / „Servers“ | **Erlaubt** (nur T2). |
| T2: Server ohne Instanz → nur Bereich „Übersicht“, Löschformular für Admins dort | **Erlaubt**, Spec §4.3 ist entsprechend angepasst. |
| T3/T4: typisierter Link auf `/servers/$id/settings` existiert vor T2 nicht | Übergangslösungen bleiben (T3 Cast in `settingsLink()`, T4 `ServerSettingsLink` mit `navigate({ href })`). Bei der Integration auf ein gemeinsames typisiertes `<Link to="/servers/$id/settings" params>` umstellen. |
| T3/T4: veraltete optionale Props (`onOpenSettings`, `onBusyChange`) | Bleiben in Welle 1 optional und ungenutzt; Entfernen ist Integrationsschritt 3. |
| T3 `renderSection` / T4 `renderServerTab` – doppelte Test-Helfer | Bei der Integration zu einem Helfer in `web/src/test/render-section.tsx` zusammenführen. |
| T3: `PowerIconButtons` wiederholt Stopp-Bestätigung aus `PowerButtons` | Nach der Integration als `compact`-Prop von `PowerButtons` zusammenführen (eigener Commit in PR 3). |
| T5: `features/profile/section-card.tsx` löschen | Integrationsschritt 4. |
| T1: `bevel!` (important) mit eigenen `@utility`s | Tailwind v4 unterstützt `!` auf `@utility`s; nur falls der Toast-Screenshot es widerlegt, ergänzt der Lead auf `feat/ui-foundation` eine Variante. |
| Neue i18n-Schlüssel (`dashboard.list_label`, `servers.form.section.*`, `audit.filters_label`, …) | Jeder im eigenen Namensraum; Konflikte in `de.ts`/`en.ts` löst der Lead beim Mergen. |

## Teammates starten (nach Welle 0)

Für jeden Teammate `T1`…`T5` (Agent-Team, je ein Spawn, alle in einer Nachricht):

```bash
git fetch origin
git worktree add ../mc-panel-wt/<branch> -b <branch> origin/feat/ui-foundation
(cd ../mc-panel-wt/<branch>/web && npm ci)
```

| Teammate | Branch | Plan |
|---|---|---|
| T1 | `feat/ui-primitives` | `2026-10-14-ui-wave1-t1-primitives.md` |
| T2 | `feat/ui-shell` | `2026-10-14-ui-wave1-t2-shell.md` |
| T3 | `feat/ui-live` | `2026-10-14-ui-wave1-t3-live.md` |
| T4 | `feat/ui-manage` | `2026-10-14-ui-wave1-t4-manage.md` |
| T5 | `feat/ui-admin` | `2026-10-14-ui-wave1-t5-admin.md` |

Spawn-Prompt je Teammate: Plan-Datei, Branch, Worktree-Pfad, „alle Tasks des Plans“, Hinweis auf diese Datei (Abschnitt „Entscheidungen“), Regel „nur eigene Dateien; Bedarf an gemeinsamen Dateien an den Lead melden“, „`make check` + die im Plan genannten `make test-vm`-Suites vor jedem Fertig-Melden; Screenshots selbst ansehen“.

Ändert der Lead während Welle 1 etwas auf `feat/ui-foundation`, informiert er alle Teammates, die dann `git rebase origin/feat/ui-foundation` ausführen.

## Integration

1. **PR 1:** `feat/ui-primitives` in `feat/ui-foundation` mergen (fast-forward oder Merge-Commit), `make check`, `make test-vm SUITE=all`, Push, PR `feat/ui-foundation` → `main` öffnen, sofort `gh pr ready`, `gh pr checks <n> --watch`.
2. **PR 2:** `feat/ui-shell` auf `feat/ui-foundation` rebasen, PR mit Basis `feat/ui-foundation` öffnen; nach Merge von PR 1 Basis auf `main` umstellen (`gh pr edit <n> --base main`).
3. **PR 3:** Branch `feat/ui-pages` ab `feat/ui-shell`; `feat/ui-live`, `feat/ui-manage`, `feat/ui-admin` nacheinander mergen, Konflikte (i18n) lösen, dann Aufräum-Commits:
   - veraltete Props entfernen: `onOpenSettings` (PlayersTab, FilesTab), `onBusyChange` (FilesTab) und die Übergangsstellen in den T2-Routen (Files-Route-Blocker, `onOpenSettings`-Übergaben, `<DeleteServerSection/>` in der Settings-Route – Test „renders exactly one delete form“ muss grün sein);
   - Settings-Links auf typisiertes `<Link>` umstellen (T3-Cast, T4 `ServerSettingsLink`);
   - Test-Helfer zusammenführen (`web/src/test/render-section.tsx`);
   - `features/profile/section-card.tsx` löschen (`grep -rn SectionCard web/src` muss leer sein);
   - `PowerIconButtons` → `PowerButtons compact`.
   PR mit Basis `feat/ui-shell` öffnen, nach Merge von PR 2 auf `main` umstellen.
4. Je PR: `make check`, `make test-vm SUITE=all`, `make test-vm SUITE=shots` – Auswahl an Screenshots (Startseite, Konsole, Dateien, Login; Dark/Light; Desktop/Mobil) dem Nutzer zeigen.
5. Nach dem letzten Merge (nur nach Freigabe): `make deploy-testvm`, Abnahme nach Spec §7, `CLAUDE.md` (Frontend-Abschnitt: Navigation, Routen pro Server-Bereich, Design-Tokens/Utilities, Schriften, Suite `shots`) und Roadmap im Foundation-Spec um Teilprojekt 7 ergänzen, Spec-Status auf „umgesetzt“, Pläne löschen.
6. Teammates herunterfahren, Worktrees entfernen (`git worktree remove`).
