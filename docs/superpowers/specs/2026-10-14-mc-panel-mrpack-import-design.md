# mc-panel – mrpack-Import (Design-Spec)

- **Datum:** 2026-10-14
- **Status:** freigegeben über die Rückfragen in Issue #78, in Umsetzung
- **Autor:** Jannes Göbel (mit Claude)
- **Baut auf:** `2026-10-12-mc-panel-plugins-design.md` (TP3c)

## 1. Ziel

Ein Nutzer mit `server.plugins` wählt im Mods-Bereich eines bestehenden **Fabric- oder NeoForge-Servers** eine `.mrpack`-Datei (Export einer Modrinth-Instanz) aus. Das panel zeigt, was sich ändert: neue Mods, Updates, Unverändertes, Übersprungenes und Mods, die nicht im Pack stehen und installiert bleiben. Nach der Bestätigung installiert es die **servertauglichen Mods** des Packs als Job.

Das ist etwas anderes als der Server-Typ „Modrinth-Modpack“: Der installiert beim Anlegen ein Pack per Projekt-ID über itzg.

### 1.1 Entscheidungen aus Issue #78

| Frage | Entscheidung |
|---|---|
| Umfang | Nur Mods. `overrides/` und `server-overrides/` (Configs usw.) werden komplett ignoriert. |
| Servertauglich | Nur Mods, die auf dem Server laufen (Pack-Angabe `env.server` und Modrinth `server_side`, §3.2). Client-Mods werden übersprungen und als „nur Client“ angezeigt. |
| Downloadquellen | Nur Modrinth. Das panel lädt nie von einer URL aus dem Pack, sondern über die Modrinth-API per Hash (§3.1). Dateien, die Modrinth nicht kennt, werden als „manuell hochladen“ aufgeführt. |
| Loader passt nicht | Import abgelehnt. |
| Minecraft-/Loader-Version passt nicht | Warnung. Admins bekommen den Knopf „Server-Version auf X umstellen“, der Import ist trotzdem möglich. |
| Bereits installierte Mods | Bleiben installiert. Mods aus dem Pack in anderer Version werden auf die Pack-Version gebracht. Vorher kommt ein Diff. |
| Laufender Server | Erlaubt, danach Banner „Neustart nötig“ wie bei TP3c. |

### 1.2 Nicht-Ziele

- Configs, Resourcepacks, Shaderpacks, Datapacks oder Dateien außerhalb von `mods/` aus dem Pack.
- Downloads von GitHub/GitLab oder anderen Hosts aus `downloads[]`.
- Server-Arten außer fabric/neoforge (paper/purpur → `409 plugins_unsupported`), Packs für Forge oder Quilt.
- Abhängigkeiten nachauflösen: Das Pack ist vollständig. Was es nicht enthält, installiert der Import nicht.
- Mods entfernen, die nicht im Pack stehen (kein „Sync“).
- Neue Server aus einer lokalen `.mrpack` anlegen.

## 2. Ablauf

```
Browser: Datei wählen ─▶ ZIP lokal lesen, nur modrinth.index.json entpacken (fflate)
        ─▶ POST /servers/{id}/plugins/mrpack/preview {index}      ─▶ Diff anzeigen
        ─▶ [Admin, optional] PATCH /servers/{id} (Version umstellen) ─▶ Preview neu
        ─▶ POST /servers/{id}/plugins/mrpack/import {index, exclude[], accept_mismatch}
        ─▶ 202 PluginJob ─▶ Fortschritt wie bei install/update (GET …/plugins/jobs)
```

**Warum im Browser entpacken:** Eine `.mrpack` mit Overrides kann Hunderte MB groß sein, gebraucht wird aber nur die Index-Datei (meist < 500 KiB). So geht keine große Datei hoch, das panel parst kein fremdes ZIP und die 1-MiB-Grenze für Request-Bodies bleibt. Das panel vertraut dem Index trotzdem nicht: Es nutzt nur Dateinamen, Hashes und die `env`-Angaben, und nur nach strikter Prüfung (§4.1).

## 3. Plan (Preview und Import identisch)

`preview` und `import` berechnen denselben Plan. Der Import rechnet neu und vertraut der Vorschau nicht.

1. **Index prüfen** (§4.1). Nur Einträge mit `path` = `mods/<name>.jar` zählen. Andere Pfade (z. B. `resourcepacks/`, `config/`) werden still ignoriert und nur gezählt (`ignored_files`).
2. **Kompatibilität:** `dependencies` des Packs gegen den Server:
   - Loader: Pack `fabric-loader` ↔ Server `fabric`, Pack `neoforge` ↔ Server `neoforge`. Passt das nicht, oder hat das Pack `forge`/`quilt-loader` → `409 mrpack_incompatible`.
   - `minecraft` ≠ aufgelöste Spielversion des Servers (`LATEST` über den Katalog, wie TP3c §3.1) → `version_mismatch.minecraft = {pack, server}`.
   - Loader-Version ≠ `loader_version` des Servers (nur wenn die Instanz eine feste Version hat) → `version_mismatch.loader = {pack, server}`.
3. **Zuordnung per Hash:** alle sha512 der Mod-Einträge in einem Aufruf von `POST /v2/version_files` (vorhandener Client, gecacht wie bei der Inventar-Zuordnung, bis 1000 Hashes je Aufruf, sonst gestückelt). Treffer liefern Projekt, Version und die Datei mit **Modrinth-URL** (`cdn.modrinth.com`, wie TP3c). Für die `server_side`-Prüfung kommen die Projekte dazu (`GET /v2/projects?ids=…`, Batch, gecacht).
4. **Klassifizieren** je Mod-Eintrag (in dieser Reihenfolge):
   - `env.server == "unsupported"` oder Projekt `server_side == "unsupported"` → `skip` (`client_only`).
   - kein Modrinth-Treffer → `skip` (`not_on_modrinth`), samt Dateiname, damit der Nutzer ihn per Dateien-Tab hochladen kann.
   - Modrinth-Version ohne Loader des Servers (`fabric` bzw. `neoforge`) → `skip` (`incompatible`).
   - gleiche sha512 liegt schon in `mods/` (aktiv oder deaktiviert) → `unchanged`.
   - dasselbe Modrinth-Projekt liegt in anderer Version in `mods/` (Inventar-Zuordnung aus TP3c §6.2) → `update` (`replaces: <alter Dateiname>`). Ein deaktiviertes Jar bleibt deaktiviert.
   - Zieldateiname existiert und gehört zu einem anderen Projekt oder ist manuell → `skip` (`file_exists`).
   - sonst → `install`.
   - `optional: true`, wenn `env.server == "optional"`. Solche Einträge sind vorausgewählt und lassen sich abwählen.
5. **Bleibt installiert:** alle Jars in `mods/`, die keinem Plan-Eintrag entsprechen → `kept[]` (nur Anzeige).

### 3.1 Warum per Hash statt `downloads[]`

`downloads[]` darf laut Format auch auf GitHub/GitLab zeigen, und die URL käme ungeprüft aus einer Datei des Nutzers. Über `version_files` kommt die URL von Modrinth selbst. Zugleich liefert der Aufruf Projekt und Version für den Diff, die DB (`plugin_installs`) und spätere Updates. Die sha512 aus dem Pack ist dieselbe wie bei Modrinth. `UploadChecked` prüft sie im Dateidienst vor dem `rename` (TP3c §5.2).

### 3.2 Servertauglich

Modrinth kennt pro Projekt `client_side`/`server_side` (`required|optional|unsupported`), das Pack pro Datei `env.server`. Übersprungen wird, sobald eine der beiden Angaben `unsupported` sagt. Fehlt `env`, entscheidet allein das Projekt.

## 4. API (`internal/plugins`, Recht `server.plugins`, CSRF)

| Route | Verhalten |
|---|---|
| `POST /servers/{id}/plugins/mrpack/preview` | Body `MrpackIndex` → `200 MrpackPlan` |
| `POST /servers/{id}/plugins/mrpack/import` | Body `{index: MrpackIndex, exclude: [sha512], accept_mismatch: bool}` → `202 PluginJob` |

- `MrpackPlan`: `{pack: {name, version_id}, game_version: {pack, server}, loader: {name, pack, server}, version_mismatch: bool, entries: [{file, sha512, action: install|update|unchanged|skip, reason?, optional, project?: {id, title, icon_url}, version?: {id, name}, replaces?}], kept: [file], ignored_files: int}`.
- Fehler: `409 plugins_unsupported` (Server-Art), `409 mrpack_incompatible` (Loader), `409 mrpack_version_mismatch` (Import mit Abweichung ohne `accept_mismatch`), `409 conflict` (Job läuft oder nichts zu tun), `400 validation_failed` (Index ungültig, `exclude` nennt einen Hash, der kein optionaler Plan-Eintrag ist), `429`/`502`/`503` wie bei TP3c. Neue Codes kommen in `internal/httpx/errors.go` und den Contract.
- Rate-Limit wie `resolve` bzw. `install` (vorhandener Limiter).

### 4.1 Validierung des Index (strikt, unbekannte Felder werden ignoriert, weil das Format wachsen darf)

- `formatVersion == 1`, `game == "minecraft"`, `name` 1–128 Zeichen, `versionId` ≤ 128 Zeichen.
- `files` höchstens 2000 Einträge. `path` relativ, ohne `..`, ohne Backslash, ohne führenden `/`, ≤ 255 Bytes. Für Mods gilt genau `mods/<name>` mit `<name>`, das der Dateinamen-Prüfung von TP3c (`upstream/filename.go`) genügt und auf `.jar` endet.
- `hashes.sha512` Pflicht (128 Hex-Zeichen, klein normalisiert), `hashes.sha1` optional. Doppelte sha512 oder doppelte Zielnamen → `400`.
- `env.server`/`env.client` ∈ `required|optional|unsupported`, falls vorhanden. `downloads` und `fileSize` werden nicht ausgewertet.
- `dependencies`: Map von Schlüssel auf Version, Schlüssel ∈ `minecraft|fabric-loader|neoforge|forge|quilt-loader`, Versionen nach `versionRe` der instancespec.

### 4.2 Job

Neue Job-Art `mrpack` in der vorhandenen Registry (einer pro Server, vier im panel). Die Schritte sind die `install`- und `update`-Einträge ohne die abgewählten optionalen. Mechanik je Schritt wie TP3c §6.4: `install` wie install, `update` wie update (neues Jar hoch, altes löschen, DB-Eintrag ersetzen, deaktiviert bleibt deaktiviert). Ein fehlgeschlagener Schritt beendet den Job nicht, der Job endet dann `failed`. Audit je Jar `plugin.install` bzw. `plugin.update` mit `details.via = "mrpack"`, dazu am Ende **ein** Eintrag `plugin.mrpack_import` mit Pack-Name, Version und Zählern (installiert/aktualisiert/übersprungen/fehlgeschlagen). Updates-Cache und Inventar werden wie bei den anderen Jobs verworfen.

## 5. Frontend (`web/src/features/plugins`)

- Knopf **„Modpack importieren (.mrpack)“** im Kopf des Mods-Bereichs, nur bei fabric/neoforge. Er öffnet einen Datei-Dialog (`accept=".mrpack"`).
- Lesen mit `fflate` (neue Abhängigkeit, klein, ohne Worker-Zwang): Es wird nur `modrinth.index.json` entpackt (Filter vor dem Entpacken). Index > 5 MiB, fehlender Index, ungültiges JSON oder Datei kein ZIP → Fehlermeldung im Dialog. Alle anderen Einträge werden nie entpackt.
- **Diff-Dialog** mit Gruppen: *Neu* (n), *Aktualisiert* (alt → neu), *Unverändert*, *Übersprungen* (Grund: nur Client / nicht auf Modrinth – bitte manuell hochladen / inkompatibel / Dateiname belegt), *Bleibt installiert (nicht im Pack)*, Hinweis „x Dateien außerhalb von mods/ werden ignoriert“. Optionale Einträge haben eine Checkbox (vorausgewählt). Icons über den vorhandenen Proxy.
- **Versionsabweichung:** Warnbox „Pack für Minecraft X / Fabric Loader Y, Server: …“. Admins sehen „Server-Version auf X umstellen“: `PATCH /servers/{id}` mit `version` (und `loader_version`), Sudo-Dialog wie üblich, danach wird die Preview neu geladen. Ohne Umstellung braucht der Import die Checkbox „Trotzdem importieren“ (`accept_mismatch`).
- Danach Fortschritt mit `job-progress` und das Banner „Neustart nötig“ wie bei install/update.
- MSW-Mocks für beide Routen, Vitest für Parser, Diff-Dialog und Fehlerfälle, ein Playwright-Test gegen den Mock (Fixture-`.mrpack` unter `web/e2e/fixtures/`).

## 6. Tests und Abnahme

- Go: Index-Validierung (Tabellen-Tests inkl. Pfad-Angriffe), Klassifizierung mit Fake-Upstream (alle `action`/`reason`-Fälle), Import-Job (install + update + deaktiviert + Fehler-Schritt), Fehlercodes, Routen-Rule und OpenAPI-Abgleich (vorhandene Tests).
- CI-Abnahme (`scripts/ci-instances.sh`): Für den vorhandenen Fabric-Testserver baut das Skript ein kleines `modrinth.index.json` (Fabric API = `install`, ein Client-Mod wie Sodium = `skip client_only`, ein unbekannter Hash = `skip not_on_modrinth`) und schickt es an `preview` und `import`. Danach prüft es `mods/` über das Inventar.
- Manuelle Abnahme: Abschnitt „mrpack-Import“ in der Abnahme-Checkliste (#87 führt die Checklisten zusammen; bis dahin `docs/dev/acceptance-tp3c.md`).
