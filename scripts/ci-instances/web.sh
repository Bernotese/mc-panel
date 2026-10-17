# shellcheck shell=bash
# shellcheck disable=SC2154 # work, psrv, pcode and the helpers come from lib.sh
# Shard web, sourced by scripts/ci-instances.sh (web ports spec §7, CI acceptance): a host Caddy
# with the installer's catch-all site (deploy/caddy/mc-panel-web.caddy, tls internal on demand,
# the ask of mcpanel-web in its global options) in front of the agent's mcpanel-web. On the Paper
# server ciplugins a minimal HTTP responder (python3 http.server, no BlueMap download) serves a
# known body on 8100 and another one on 8101. Published through the panel (admin + sudo), the
# name answers through Caddy with the body; an unknown name gets no certificate; a second server
# cannot reach the web port; mcpanel-web reaches only the published port; a stopped responder
# gives the 502 notice; unpublishing removes the route, and with zero routes the container,
# the network mcpanel-web and the table bridge mcpanel_web are gone.

web_caddy_pid=""
wo="$work/wout"
# web_cleanup runs before lib.sh's cleanup: mcpanel-web is attached to the server networks,
# which cleanup removes, and carries none of the labels cleanup looks for.
web_cleanup() {
  if [ -n "$web_caddy_pid" ]; then
    kill "$web_caddy_pid" 2>/dev/null || true
    wait "$web_caddy_pid" 2>/dev/null || true
  fi
  ids="$(docker ps -aq --filter label=mcpanel.web)"
  if [ -n "$ids" ]; then
    # shellcheck disable=SC2086 # one container id per word
    docker rm -f $ids >/dev/null 2>&1 || true
  fi
  nft delete table bridge mcpanel_web 2>/dev/null || true
}
trap 'web_cleanup; cleanup' EXIT

setup_agent_files
setup_panel_server
install -d -m 0755 "$wo"
wfqdn="ciplugins.mc.localhost" # name + base domain of setup_panel_server

echo "== web: host Caddy with the installer's catch-all site"
# The caddy binary of the pinned mcpanel-web image (internal/config): no package, no extra pin.
web_image="$(grep -oE '"caddy:[^"]*@sha256:[0-9a-f]{64}"' internal/config/config.go | tr -d '"')"
[ -n "$web_image" ] || fail "caddy image ref not found in internal/config/config.go"
docker create --name mcpanel-ci-caddy "$web_image" >/dev/null || fail "docker create $web_image"
docker cp mcpanel-ci-caddy:/usr/bin/caddy "$work/bin/caddy" >/dev/null || fail "docker cp caddy"
docker rm mcpanel-ci-caddy >/dev/null
install -d -m 0755 "$work/caddy" "$work/caddy/web.d"
# Rendered by the installer's own render_template (in a subshell: main.sh's functions stay out
# of this shell); only the log and the operator directory move below $work.
(
  export MCPANEL_INSTALL_NO_MAIN=1 MCPANEL_INSTALL_DRYRUN=1
  # shellcheck source=scripts/install/main.sh
  . scripts/install/main.sh
  TEMPLATE_DIR="$PWD/deploy"
  export KEY_VERSION=ci KEY_WEB_PROXY_PORT=18100 KEY_TLS_ON_DEMAND=$'internal {\n\t\ton_demand\n\t}'
  render_template caddy/mc-panel-web.caddy
) >"$work/caddy/mc-panel-web.caddy.tpl" || fail "render mc-panel-web.caddy"
sed -e "s#/var/log/caddy/#$work/caddy/#" -e "s#/etc/caddy/mc-panel-web.d/#$work/caddy/web.d/#" \
  "$work/caddy/mc-panel-web.caddy.tpl" >"$work/caddy/mc-panel-web.caddy"
grep -q 'reverse_proxy 127.0.0.1:18100' "$work/caddy/mc-panel-web.caddy" || fail "rendered site: $(cat "$work/caddy/mc-panel-web.caddy")"
# An addition of the operator (mc-panel-web.d) reaches every web interface.
printf 'header X-Robots-Tag "noindex"\n' >"$work/caddy/web.d/robots.caddy"
# The global options as a Caddyfile of the installer has them (cad_on_demand_block), plus what a
# runner needs; the named panel site must win over the catch-all.
cat >"$work/caddy/Caddyfile" <<EOF
{
	admin off
	http_port 18180
	storage file_system $work/caddy/storage
	skip_install_trust
	on_demand_tls {
		ask http://127.0.0.1:18101/
	}
}

panel.mc.localhost {
	tls internal
	respond "PANEL"
}

import mc-panel-web.caddy
EOF
HOME="$work/caddy" "$work/bin/caddy" validate --config "$work/caddy/Caddyfile" --adapter caddyfile >"$wo/validate.log" 2>&1 ||
  fail "caddy validate: $(cat "$wo/validate.log")"
HOME="$work/caddy" "$work/bin/caddy" run --config "$work/caddy/Caddyfile" --adapter caddyfile >"$work/caddy.log" 2>&1 &
web_caddy_pid=$!
caddy_up() { curl -sk --max-time 5 --resolve panel.mc.localhost:443:127.0.0.1 https://panel.mc.localhost/ 2>/dev/null | grep -qx PANEL; }
pwait_for 30 "host Caddy (panel site)" caddy_up
echo "OK: host Caddy runs $("$work/bin/caddy" version | cut -d' ' -f1) with mc-panel-web.caddy"

# web_get <name> <outfile>: GET https://<name>/ through the host Caddy; prints the HTTP status
# ("000" when TLS fails: no certificate), headers in <outfile>.headers.
web_get() {
  curl -sk --max-time 10 --resolve "$1:443:127.0.0.1" -D "$2.headers" -o "$2" -w '%{http_code}' "https://$1/" 2>/dev/null || true
}
# ask <domain>: the HTTP status of mcpanel-web's ask server ("000" when nothing listens).
ask() { curl -s --max-time 5 -o /dev/null -w '%{http_code}' "http://127.0.0.1:18101/?domain=$1" 2>/dev/null || true; }

echo "== web: HTTP responders inside ciplugins (8100 published, 8101 not)"
docker exec "mcpanel-mc-$psrv" sh -c 'mkdir -p /tmp/ciweb/pub /tmp/ciweb/priv &&
  printf "ci web body\n" >/tmp/ciweb/pub/index.html && printf "ci private body\n" >/tmp/ciweb/priv/index.html' ||
  fail "responder files"
# web_responder <port> <dir>: python3 http.server in the container; its pid in /tmp/ciweb/<port>.pid.
web_responder() {
  docker exec -d "mcpanel-mc-$psrv" sh -c "echo \$\$ >/tmp/ciweb/$1.pid; exec python3 -m http.server $1 --bind 0.0.0.0 --directory /tmp/ciweb/$2" ||
    fail "start responder $1"
}
responder_up() { docker exec "mcpanel-mc-$psrv" curl -fsS --max-time 3 "http://127.0.0.1:$1/" >/dev/null 2>&1; }
web_responder 8100 pub
web_responder 8101 priv
pwait_for 30 "responder 8100" responder_up 8100
pwait_for 30 "responder 8101" responder_up 8101
echo "OK: responders on 8100 and 8101"

echo "== web: a second server (ciweb2)"
papi POST /servers "$wo/server2.json" '{"name": "ciweb2", "type": "paper", "version": "1.21.11", "java": "java21", "memory_mib": 1024, "eula_accepted": true}'
[ "$pcode" = 201 ] || fail "create ciweb2: HTTP $pcode $(cat "$wo/server2.json")"
wsrv2="$(jget "$wo/server2.json" 'd["id"]')"
papi POST "/servers/$wsrv2/power" "$wo/power2.json" '{"action": "start"}'
[ "$pcode" = 202 ] || fail "start ciweb2: HTTP $pcode $(cat "$wo/power2.json")"
w2_running() { [ "$(inst_field "$wsrv2" 'i["state"]')" = running ]; }
pwait_for 600 "ciweb2 running" w2_running
echo "OK: ciweb2 ($wsrv2) running"

echo "== web: publish 8100 on ciplugins (admin + sudo)"
p_sudo
for v in 70000 25565 25575 80; do
  papi PATCH "/servers/$psrv" "$wo/invalid.json" "{\"web_port\": $v}"
  is_perror "$wo/invalid.json" "$pcode" 400 validation_failed || fail "web_port $v: HTTP $pcode $(cat "$wo/invalid.json")"
done
papi PATCH "/servers/$psrv" "$wo/publish.json" '{"web_port": 8100}'
[ "$pcode" = 200 ] || fail "publish: HTTP $pcode $(cat "$wo/publish.json")"
web_server() {
  papi GET "/servers/$psrv" "$wo/srv.json"
  [ "$pcode" = 200 ] || fail "GET server: HTTP $pcode $(cat "$wo/srv.json")"
}
web_active() { web_server; [ "$(jget "$wo/srv.json" 'd.get("web_status")')" = active ]; }
pwait_for 300 "web_status active (mcpanel-web created, config loaded)" web_active
jcheck "$wo/srv.json" "web_url and web_port of the published server" \
  'd.get("web_url") == "https://" + a[0] and d.get("web_port") == 8100 and "web_error" not in d' "$wfqdn"
echo "OK: web_status active, web_url https://$wfqdn"

echo "== web: mcpanel-web container, ports and port restriction"
[ "$(docker inspect -f '{{index .Config.Labels "mcpanel.web"}} {{.State.Running}}' mcpanel-web)" = "1 true" ] ||
  fail "mcpanel-web: $(docker inspect -f '{{json .Config.Labels}} {{.State.Status}}' mcpanel-web 2>&1)"
docker port mcpanel-web >"$wo/ports.txt"
if ! [ -s "$wo/ports.txt" ] || grep -qv -- '-> 127\.0\.0\.1:' "$wo/ports.txt"; then
  fail "mcpanel-web publishes beyond 127.0.0.1: $(cat "$wo/ports.txt")"
fi
nft list table bridge mcpanel_web >"$wo/bridge.nft" || fail "table bridge mcpanel_web missing"
grep -q 'dport 8100 accept' "$wo/bridge.nft" || fail "no accept for port 8100: $(cat "$wo/bridge.nft")"
echo "OK: mcpanel-web runs, publishes on 127.0.0.1 only, bridge rules in place"

echo "== web: the ask server"
[ "$(ask "$wfqdn")" = 200 ] || fail "ask $wfqdn: $(ask "$wfqdn")"
for n in "nothere.mc.localhost" "CIPLUGINS.mc.localhost" "$wfqdn." "mc.localhost"; do
  [ "$(ask "$n")" = 404 ] || fail "ask $n: $(ask "$n") (want 404)"
done
echo "OK: ask allows exactly $wfqdn"

echo "== web: through host Caddy"
code="$(web_get "$wfqdn" "$wo/page.html")"
if ! { [ "$code" = 200 ] && grep -qx 'ci web body' "$wo/page.html"; }; then
  fail "https://$wfqdn/: HTTP $code $(cat "$wo/page.html" 2>/dev/null)"
fi
grep -qi '^x-robots-tag: noindex' "$wo/page.html.headers" || fail "mc-panel-web.d addition missing: $(cat "$wo/page.html.headers")"
code="$(web_get nothere.mc.localhost "$wo/unknown.html")"
[ "$code" = 000 ] || fail "unknown name: HTTP $code (want a TLS failure, no certificate) $(cat "$wo/unknown.html" 2>/dev/null)"
if ! { [ "$(web_get panel.mc.localhost "$wo/panel.html")" = 200 ] && grep -qx PANEL "$wo/panel.html"; }; then
  fail "the named panel site no longer wins over the catch-all"
fi
echo "OK: $wfqdn serves the responder (with the operator's header), an unknown name gets no certificate"

echo "== web: isolation"
a_ip="$(docker inspect -f "{{(index .NetworkSettings.Networks \"mcpanel-net-$psrv\").IPAddress}}" "mcpanel-mc-$psrv")"
[ -n "$a_ip" ] || fail "no address of ciplugins on mcpanel-net-$psrv"
if docker exec "mcpanel-mc-$wsrv2" curl -fsS --max-time 5 "http://$a_ip:8100/" >"$wo/b.txt" 2>&1; then
  fail "ciweb2 reaches ciplugins' web port at $a_ip:8100: $(cat "$wo/b.txt")"
fi
if docker exec "mcpanel-mc-$wsrv2" curl -fsS --max-time 5 "http://mcpanel-mc-$psrv:8100/" >"$wo/b2.txt" 2>&1; then
  fail "ciweb2 reaches mcpanel-mc-$psrv:8100 by name: $(cat "$wo/b2.txt")"
fi
if ! { docker exec mcpanel-web wget -T 5 -qO- "http://mcpanel-mc-$psrv:8100/" >"$wo/w.txt" 2>&1 && grep -qx 'ci web body' "$wo/w.txt"; }; then
  fail "mcpanel-web cannot reach the published port: $(cat "$wo/w.txt")"
fi
if docker exec mcpanel-web wget -T 5 -qO- "http://mcpanel-mc-$psrv:8101/" >"$wo/w2.txt" 2>&1; then
  fail "mcpanel-web reaches the unpublished port 8101: $(cat "$wo/w2.txt")"
fi
if docker exec mcpanel-web wget -T 5 -qO- "http://mcpanel-mc-$psrv:25575/" >"$wo/w3.txt" 2>&1; then
  fail "mcpanel-web reaches RCON: $(cat "$wo/w3.txt")"
fi
echo "OK: ciweb2 cannot reach the web port; mcpanel-web reaches 8100 only (not 8101, not RCON)"

echo "== web: responder down gives the 502 notice, and back"
docker exec "mcpanel-mc-$psrv" sh -c 'kill "$(cat /tmp/ciweb/8100.pid)"' || fail "stop responder 8100"
web_502() { [ "$(web_get "$wfqdn" "$wo/down.html")" = 502 ]; }
pwait_for 30 "502 while nothing listens on 8100" web_502
grep -q 'not reachable' "$wo/down.html" || fail "502 page: $(cat "$wo/down.html")"
web_server
jcheck "$wo/srv.json" "web_status stays active while the plugin is down" 'd.get("web_status") == "active"'
web_responder 8100 pub
web_back() { [ "$(web_get "$wfqdn" "$wo/back.html")" = 200 ] && grep -qx 'ci web body' "$wo/back.html"; }
pwait_for 30 "the page is back with the responder" web_back
echo "OK: 502 notice while the plugin is down, back without any action"

echo "== web: unpublish"
p_sudo
papi PATCH "/servers/$psrv" "$wo/unpublish.json" '{"web_port": null}'
[ "$pcode" = 200 ] || fail "unpublish: HTTP $pcode $(cat "$wo/unpublish.json")"
web_server
jcheck "$wo/srv.json" "no web_url and no web_port after unpublishing" \
  '"web_url" not in d and "web_port" not in d and d.get("web_status") == "off"'
web_gone() {
  ! docker inspect mcpanel-web >/dev/null 2>&1 && ! docker network inspect mcpanel-web >/dev/null 2>&1 &&
    ! nft list table bridge mcpanel_web >/dev/null 2>&1
}
pwait_for 120 "mcpanel-web, its network and table bridge mcpanel_web removed (zero routes)" web_gone
[ "$(ask "$wfqdn")" = 000 ] || fail "the ask server still answers: $(ask "$wfqdn")"
# Caddy keeps the certificate it issued, so the name may still complete TLS; it never reaches
# the responder again.
code="$(web_get "$wfqdn" "$wo/after.html")"
if [ "$code" = 200 ] || grep -q 'ci web body' "$wo/after.html" 2>/dev/null; then
  fail "after unpublishing: HTTP $code $(cat "$wo/after.html")"
fi
[ "$(web_get other.mc.localhost "$wo/other.html")" = 000 ] || fail "a new name after unpublishing got a certificate"
echo "OK: unpublished, container, network and table gone"

papi GET "/audit?action=server.web_port_update" "$wo/audit.json"
[ "$pcode" = 200 ] || fail "audit: HTTP $pcode $(cat "$wo/audit.json")"
jcheck "$wo/audit.json" "publish and unpublish audited as server.web_port_update" \
  'len([e for e in d["items"] if e["action"] == "server.web_port_update" and e["result"] == "success"]) == 2'
echo "OK: publish and unpublish audited"
