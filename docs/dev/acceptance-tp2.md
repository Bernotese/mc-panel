# Abnahme Teilprojekt 2 (manuell, Testserver)

Automatisch geprüft (CI): Unit- und Integrationstests, Docker-Integration (`dockerintegration`), Instanz-Abnahme über den agent (`scripts/ci-instances.sh`: Routing, Härtung, Absturz-Neustart, Löschen), Playwright gegen den Mock.

Manuell auf einem Testserver (Ubuntu 26.04 mit Docker, Release-Archiv aus einem `v*`-Tag oder `make release`, Caddy davor), getrennt von der Entwicklung:

## Testserver einrichten (bis der Installer aus Teilprojekt 6 existiert)

Als root auf einem frischen Ubuntu 26.04 (DNS: `panel.<domain>` und `*.mc.<domain>` zeigen auf den Server; Ports 80, 443 und 25565 offen):

```bash
apt-get update && apt-get install -y docker.io caddy nftables
systemctl enable --now docker
useradd --system --user-group --home-dir /var/lib/mc-panel --no-create-home --shell /usr/sbin/nologin mcpanel
useradd --system --user-group --home-dir /nonexistent --no-create-home --shell /usr/sbin/nologin mcpanel-mc
install -d -m 0755 -o root -g root /srv/mc-panel
install -d -m 0750 -o root -g mcpanel /etc/mc-panel

# Binaries aus dem Release-Archiv (linux_amd64 oder linux_arm64)
tar -xzf mc-panel_*_linux_amd64.tar.gz -C /usr/local/bin panel agent

cat >/etc/mc-panel/agent.toml <<EOF
socket = "/run/mc-panel/agent.sock"
panel_uid = $(id -u mcpanel)
firewall = "nftables"
instances_dir = "/srv/mc-panel"
mc_user = "mcpanel-mc"
EOF
chmod 0600 /etc/mc-panel/agent.toml

cat >/etc/mc-panel/panel.toml <<'EOF'
listen = "127.0.0.1:8080"
public_url = "https://panel.<domain>"
data_dir = "/var/lib/mc-panel"
agent_socket = "/run/mc-panel/agent.sock"
trusted_proxies = ["127.0.0.1/32"]
EOF
chown root:mcpanel /etc/mc-panel/panel.toml && chmod 0640 /etc/mc-panel/panel.toml

systemd-run --unit=mcpanel-agent -p User=root -p Group=mcpanel \
  -p RuntimeDirectory=mc-panel -p RuntimeDirectoryMode=0750 \
  /usr/local/bin/agent -config /etc/mc-panel/agent.toml
systemd-run --unit=mcpanel-panel -p User=mcpanel -p Group=mcpanel \
  -p StateDirectory=mc-panel -p StateDirectoryMode=0700 \
  /usr/local/bin/panel serve -config /etc/mc-panel/panel.toml

printf '%s\n' 'panel.<domain> {' '	reverse_proxy 127.0.0.1:8080' '}' >/etc/caddy/Caddyfile
systemctl reload caddy

printf '%s\n' '<passwort>' | sudo -u mcpanel /usr/local/bin/panel admin create -config /etc/mc-panel/panel.toml -username admin
```

Die `systemd-run`-Units überleben keinen Neustart des Servers. Für Kriterium 6 nach dem Neustart beide `systemd-run`-Befehle erneut ausführen und erst dann prüfen; Docker selbst startet die Minecraft-Container mit `unless-stopped` schon vorher.

Logs: `journalctl -u mcpanel-agent -u mcpanel-panel -f`. Status des agents: `curl -s --unix-socket /run/mc-panel/agent.sock http://agent/v1/instances`.

## Kriterien

| # | Kriterium (Spec §12) | Vorgehen | Erwartet |
|---|---|---|---|
| 2 | Paper-Server, Client verbindet sich | Basisdomain setzen (Wildcard-DNS `*.<domain>` → Server), Server `survival` (Paper) anlegen, mit einem Minecraft-Client `survival.<domain>` betreten | Status „läuft“, Client betritt die Welt |
| 3 | Weitere Arten | je einen Server Vanilla, Fabric, NeoForge und ein kleines Modrinth-Pack anlegen und starten | jeder erreicht „läuft“ und ist per Client erreichbar |
| 4 | Rechte | Benutzer mit `server.power` auf `survival` anlegen und damit anmelden | sieht nur `survival`, kann starten/stoppen, kein Tab „Einstellungen“, `PATCH /api/servers/<id>` → 403 |
| 5 | Änderung + Neustart | MOTD und RAM ändern | „Neustart nötig“; nach „Jetzt neu starten“ neue MOTD im Client, neues Limit in `docker inspect`, Welt erhalten |
| 6 | Neustart des Hosts | Server laufen lassen, Host neu starten | Server mit Soll „läuft“ laufen danach wieder |
| 8 | Löschen + Audit | Server löschen, Audit-Log ansehen, `sudo -u mcpanel panel audit verify -config /etc/mc-panel/panel.toml` | Container und Route weg, Daten unter `deleted/`, Audit vollständig, `verify` OK |

Befunde als GitHub-Issue anlegen (`gh issue create`), mit Schritten zum Nachstellen.
