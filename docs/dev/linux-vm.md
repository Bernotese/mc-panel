# Linux-VM für produktionsnahe Tests

macOS hat weder nftables noch systemd. Für Spec §3.5 und die Abnahmekriterien 5 und 7 läuft mc-panel deshalb in einer Ubuntu-LTS-VM unter Lima (Standard) oder OrbStack.

## Was die Skripte aufbauen

```
macOS                         VM (Ubuntu LTS, Architektur = Mac: arm64 bzw. amd64)
─────                         ───────────────────────────────────────────────────
make release → dist/*.tar.gz  /usr/local/bin/{panel,agent}  (aus genau einem Snapshot-Archiv)
scripts/vm-setup.sh ───────▶  mcpanel-agent.service  root, Gruppe mcpanel, /etc/mc-panel/agent.toml
                              mcpanel-panel.service  User mcpanel, /etc/mc-panel/panel.toml (Prod-Modus)
                              /run/mc-panel/agent.sock (0660 root:mcpanel), /var/lib/mc-panel (0700)
                              nftables: table inet mcpanel, sets mcpanel_jail4 / mcpanel_jail6
scripts/vm-test.sh ────────▶  netns mcpanel-client 192.0.2.2 ──veth──▶ 192.0.2.1:8080 (panel)
```

- Das panel läuft **ohne** `-dev`. `public_url = "https://mcpanel.test"` erfüllt die Prod-Validierung; TLS terminiert in Produktion Caddy, nicht das panel. Der Test sendet deshalb `Origin: https://mcpanel.test`.
- Der agent läuft ohne `-dev`, also mit `firewall = "nftables"` und `panel_uid` = UID von `mcpanel`.
- Testclient ist ein eigener Netz-Namespace mit `192.0.2.2`: Loopback-Adressen dürfen nicht gebannt werden, und über Lima-Portweiterleitungen käme jede Anfrage als `127.0.0.1` an.
- Jeder Lauf von `vm-setup.sh` (ohne `--provision-only`) setzt den Zustand zurück: DB gelöscht, Jail-Sets geleert, Units neu gestartet.
- Die Units sind transient (`systemd-run`) und verschwinden beim VM-Neustart; danach `scripts/vm-setup.sh --no-build` erneut ausführen. Dauerhafte Units liefert später der Installer (Teilprojekt 6).

## Ablauf

```bash
brew install lima
scripts/vm-setup.sh --provision-only   # VM anlegen (erstes Mal: Ubuntu-Image ~600 MB), Pakete, User, Testnetz
make release                           # Snapshot-Archive nach dist/
scripts/vm-setup.sh --no-build         # installieren + agent und panel starten
scripts/vm-test.sh                     # Abnahmekriterium 5 (benötigt Phase 2)
```

`scripts/vm-setup.sh` ohne Flags führt `make release` selbst aus. Erwartete Ausgabe am Ende von `vm-setup.sh`:

```
{"status":"ok"}
socket: 660 root:mcpanel /run/mc-panel/agent.sock
active
active
nftables: table inet mcpanel present
== done: agent + panel running in VM mcpanel (panel at http://192.0.2.1:8080 inside the VM)
```

`scripts/vm-test.sh` endet mit `PASS: acceptance check 5 (nftables part)` und zeigt vorher das Set mit `192.0.2.2`. Es funktioniert erst nach der Phase-2-Integration (Jail → agent-Ban); vorher bricht es beim Warten auf das Set mit `FAIL: 192.0.2.2 not in inet mcpanel mcpanel_jail4 after 30s` ab.

## OrbStack statt Lima

```bash
VM_TOOL=orb scripts/vm-setup.sh --provision-only
VM_TOOL=orb scripts/vm-setup.sh --no-build
VM_TOOL=orb scripts/vm-test.sh
```

Die Skripte nutzen dann `orb create ubuntu mcpanel`, `orb -m mcpanel -u root …` und `orb push -m mcpanel …`.

## Nützliche Befehle

```bash
limactl shell mcpanel                                         # Shell in der VM
limactl shell mcpanel sudo journalctl -u mcpanel-panel -u mcpanel-agent -f
limactl shell mcpanel sudo nft list ruleset
limactl shell mcpanel sudo nft list set inet mcpanel mcpanel_jail4
limactl shell mcpanel sudo ip netns exec mcpanel-client curl -s http://192.0.2.1:8080/api/health
limactl stop mcpanel
limactl delete --force mcpanel                                # VM komplett entfernen
```

## Fehlersuche

| Symptom | Ursache / Abhilfe |
|---|---|
| `expected exactly one dist/mc-panel_*_linux_arm64.tar.gz, found 0` | `make release` fehlt oder ist fehlgeschlagen |
| `… found 2` | alte Snapshots in `dist/`: `make clean && make release` |
| `agent socket /run/mc-panel/agent.sock missing` | agent-Log steht direkt darüber; häufig falscher `panel_uid` oder fehlende Root-Rechte |
| `panel health check failed` | panel-Log steht darüber; häufig Validierungsfehler in `/etc/mc-panel/panel.toml` |
| `WARNING: table inet mcpanel not present yet` | agent legt Tabelle/Sets beim Start an (Stream C); vor dessen Merge erwartbar |
| Login liefert `403` statt `401` | CSRF/Origin-Prüfung: `public_url` in `panel.toml` und `Origin`-Header in `vm-test.sh` müssen übereinstimmen |
| Jail aus früherem Lauf blockiert | `scripts/vm-setup.sh --no-build` setzt DB und Sets zurück (macht `vm-test.sh` automatisch) |
