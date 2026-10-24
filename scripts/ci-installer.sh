#!/usr/bin/env bash
# Installer acceptance for a disposable Linux CI runner (sub-project 6, spec §8, §10 criteria
# 1-5 without Let's Encrypt): builds three staging directories in the format of --from-dir
# (v1 0.0.1-ci, v2 0.0.2-ci, broken 0.0.3-ci whose panel exits at once), then runs the whole
# lifecycle of install.sh as root: install (tls internal, firewall on), doctor without
# failures, a second install that changes nothing, sign-in through Caddy, doctor's audit
# check (ok, then fail for a changed audit entry), update to v2,
# update to the broken build (health check fails, automatic rollback to v2 with the same
# database), update to a signed release downloaded over HTTPS (a local release server with a
# throwaway key, through the hand-over; a release whose checksums.txt was changed after signing
# is refused), uninstall (services, units, Caddy site, nftables tables and mc-panel containers
# gone, data and config kept, foreign container and foreign table untouched), install again
# over the kept data (no admin prompt, old password still valid), and uninstall --purge with
# the domain typed in (a wrong answer keeps everything).
#
# Usage (as root): scripts/ci-installer.sh REPO_DIR
# Changes the host (packages, users, systemd units, Caddy, nftables, /etc/hosts, the CA store,
# ports 80, 443, 25565 and 8443): disposable CI runners only.
set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "usage: scripts/ci-installer.sh REPO_DIR" >&2
  exit 2
fi
if [ "$(id -u)" -ne 0 ] || [ "$(uname -s)" != Linux ]; then
  echo "ci-installer: Linux and root only" >&2
  exit 1
fi
repo="$(cd "$1" && pwd)"
work="$(mktemp -d)"
chmod 0755 "$work"
domain="panel.ci.test"
admin="ci-admin"
admin_pw='ci installer admin password 1'
ca=/var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt
changed_re='^\[changed\]'
export MCPANEL_INSTALL_ALLOW_UNSUPPORTED=1

release_ca=/usr/local/share/ca-certificates/mc-panel-ci-release.crt
release_port=8443
release_pid=""

cleanup() {
  if [ -n "$release_pid" ]; then kill "$release_pid" 2>/dev/null || true; fi
  if [ -e "$release_ca" ]; then
    rm -f "$release_ca"
    update-ca-certificates --fresh >/dev/null 2>&1 || true
  fi
  docker rm -f ci-foreign ci-fake-instance >/dev/null 2>&1 || true
  nft delete table inet ci_foreign 2>/dev/null || true
  rm -rf "$work"
}
trap cleanup EXIT

fail() {
  echo "FAIL: $*" >&2
  echo "--- install log (last 80 lines)" >&2
  tail -n 80 /var/log/mc-panel-install.log >&2 2>/dev/null || true
  echo "--- units" >&2
  systemctl --no-pager --lines=30 status 'mcpanel-*' caddy >&2 2>/dev/null || true
  # status only shows loaded units; after a failed rollback they are stopped
  echo "--- journal of the mc-panel units (last 150 lines)" >&2
  journalctl --no-pager -o short-iso -u 'mcpanel-*' -n 150 >&2 2>/dev/null || true
  exit 1
}

# stage <dir> <version> [broken]: binaries, deploy/ and the assembled install.sh in the format
# of --from-dir. "broken" replaces panel with a script that reports the version but whose
# serve exits at once, so the update health check fails.
stage() {
  local dir="$1" version="$2"
  mkdir -p "$dir"
  (cd "$repo" && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$dir/" ./cmd/agent ./cmd/files ./cmd/panel)
  cp -r "$repo/deploy" "$dir/deploy"
  bash "$repo/scripts/install/assemble.sh" "$version" "$dir/install.sh" # ASSEMBLE_* from stage_signed
  chmod 0755 "$dir/install.sh"
  if [ "${3:-}" = broken ]; then
    mv "$dir/panel" "$dir/panel.real"
    cat >"$dir/panel" <<EOF
#!/bin/sh
case "\$1" in
  version) echo "$version" ;;
  serve) echo "ci-installer: broken panel" >&2; exit 1 ;;
  *) exec "$dir/panel.real" "\$@" ;;
esac
EOF
    chmod 0755 "$dir/panel"
  fi
}

# release_server: a GitHub-like release download tree in $work/releases/download/<tag>/,
# served over HTTPS on 127.0.0.1:$release_port with a certificate of a throwaway CA that the
# system trusts (curl in install.sh accepts HTTPS only), and a throwaway signing key whose
# allowed_signers line the signed staging builds embed (ASSEMBLE_SIGNERS).
release_server() {
  local d="$work/release-ca" _
  mkdir -p "$d" "$work/releases/download"
  openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 1 -subj "/CN=mc-panel CI release CA" \
    -addext basicConstraints=critical,CA:TRUE -addext keyUsage=critical,keyCertSign \
    -keyout "$d/ca.key" -out "$d/ca.crt" 2>/dev/null
  openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -subj "/CN=127.0.0.1" \
    -keyout "$d/srv.key" -out "$d/srv.csr" 2>/dev/null
  printf 'subjectAltName=IP:127.0.0.1\nextendedKeyUsage=serverAuth\n' >"$d/srv.ext"
  openssl x509 -req -in "$d/srv.csr" -CA "$d/ca.crt" -CAkey "$d/ca.key" -CAcreateserial -days 1 \
    -extfile "$d/srv.ext" -out "$d/srv.crt" 2>/dev/null
  cp "$d/ca.crt" "$release_ca"
  update-ca-certificates >/dev/null
  python3 -I - "$work/releases" "$d/srv.crt" "$d/srv.key" "$release_port" >"$work/release-server.log" 2>&1 <<'PY' &
import functools, http.server, ssl, sys
root, cert, key, port = sys.argv[1:5]
handler = functools.partial(http.server.SimpleHTTPRequestHandler, directory=root)
srv = http.server.ThreadingHTTPServer(("127.0.0.1", int(port)), handler)
ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
ctx.load_cert_chain(cert, key)
srv.socket = ctx.wrap_socket(srv.socket, server_side=True)
srv.serve_forever()
PY
  release_pid=$!
  for _ in $(seq 1 20); do
    curl -fsS -o /dev/null "https://127.0.0.1:$release_port/" 2>/dev/null && break
    sleep 0.5
  done
  curl -fsS -o /dev/null "https://127.0.0.1:$release_port/" || fail "release server: $(cat "$work/release-server.log")"
  ssh-keygen -q -t ed25519 -N '' -C mc-panel-ci-release -f "$work/release-key" </dev/null
  printf 'mc-panel-release namespaces="mc-panel-release" %s\n' "$(cut -d' ' -f1,2 "$work/release-key.pub")" >"$work/release-signers"
}

# publish <staging dir> <version>: the staging build as a release like goreleaser makes it
# (tarball of the binaries and deploy/, install.sh, checksums.txt), signed with the CI key.
publish() {
  local dir="$1" v="$2" out="$work/releases/download/v$2" arch
  arch="$(dpkg --print-architecture)"
  mkdir -p "$out"
  tar -C "$dir" -czf "$out/mc-panel_${v}_linux_${arch}.tar.gz" panel agent files deploy
  cp "$dir/install.sh" "$out/install.sh"
  (cd "$out" && sha256sum "mc-panel_${v}_linux_${arch}.tar.gz" install.sh >checksums.txt)
  ssh-keygen -q -Y sign -f "$work/release-key" -n mc-panel-release "$out/checksums.txt" </dev/null 2>/dev/null
}

# stage_signed <dir> <version>: a staging build whose install.sh trusts only the CI key and
# downloads from the local release server (assemble.sh's test-only inputs).
stage_signed() {
  ASSEMBLE_SIGNERS="$work/release-signers" ASSEMBLE_RELEASE_BASE="https://127.0.0.1:$release_port" stage "$@"
}

# inst <dir> <args…>: runs install.sh of a staging directory; output also in $work/last.out.
inst() {
  local dir="$1"
  shift
  "$dir/install.sh" "$@" 2>&1 | tee "$work/last.out"
  return "${PIPESTATUS[0]}"
}

# https <path> [curl args…]: request through Caddy with its internal CA.
https() {
  local p="$1"
  shift
  curl -sS --max-time 20 --cacert "$ca" --resolve "$domain:443:127.0.0.1" "$@" "https://$domain$p"
}

wait_health() {
  local _
  for _ in $(seq 1 30); do
    if https /api/health -o /dev/null -f 2>/dev/null; then
      return 0
    fi
    sleep 1
  done
  https /api/health -o /dev/null -f || fail "health through Caddy"
}

login_ok() {
  local out="$work/login.json" code
  code="$(https /api/auth/login -o "$out" -w '%{http_code}' -X POST \
    -H "Origin: https://$domain" -H 'Sec-Fetch-Site: same-origin' -H 'Content-Type: application/json' \
    --data-binary "$(python3 -I -c 'import json, sys; print(json.dumps({"username": sys.argv[1], "password": sys.argv[2]}))' "$admin" "$admin_pw")")"
  [ "$code" = 200 ] || fail "login through Caddy: HTTP $code $(cat "$out")"
  python3 -I -c 'import json, sys; d = json.load(open(sys.argv[1])); sys.exit(0 if d.get("stage") == "setup" and d.get("csrf_token") else 1)' "$out" ||
    fail "login answer: $(cat "$out")"
}

# doctor_fails: number of failed doctor checks (doctor --json: {"status", "checks": [...]}).
doctor_fails() {
  "$v1/install.sh" doctor --json >"$work/doctor.json" 2>"$work/doctor.err" || true
  python3 -I -c 'import json, sys; d = json.load(open(sys.argv[1])); print(sum(c["status"] == "fail" for c in d["checks"]))' "$work/doctor.json" ||
    fail "doctor --json: $(cat "$work/doctor.err") $(cat "$work/doctor.json")"
}

# doctor_check ID: the status of one doctor check (doctor --json).
doctor_check() {
  "$v1/install.sh" doctor --json >"$work/doctor.json" 2>"$work/doctor.err" || true
  python3 -I -c 'import json, sys; d = json.load(open(sys.argv[1])); print([c["status"] for c in d["checks"] if c["id"] == sys.argv[2]][0])' "$work/doctor.json" "$1" ||
    fail "doctor --json: $(cat "$work/doctor.err") $(cat "$work/doctor.json")"
}

# audit_tamper EXPR: sets actor_name of the first audit entry to EXPR the way an attacker with
# the database could (the append-only trigger dropped and created again from its own SQL, in
# one transaction). Run as mcpanel, like db_fingerprint.
audit_tamper() {
  local trig
  trig="$(runuser -u mcpanel -- sqlite3 /var/lib/mc-panel/panel.db "SELECT sql FROM sqlite_master WHERE name = 'audit_log_no_update'")"
  [ -n "$trig" ] || fail "no trigger audit_log_no_update"
  runuser -u mcpanel -- sqlite3 -bail /var/lib/mc-panel/panel.db <<EOF || fail "audit_tamper $1"
.timeout 10000
BEGIN IMMEDIATE;
DROP TRIGGER audit_log_no_update;
UPDATE audit_log SET actor_name = $1 WHERE id = (SELECT min(id) FROM audit_log);
$trig;
COMMIT;
EOF
}

# db_fingerprint: users and schema version, stable across restarts. Read as mcpanel, so root
# never creates a -shm or -wal file in the panel's data directory.
db_fingerprint() {
  runuser -u mcpanel -- sqlite3 -readonly /var/lib/mc-panel/panel.db \
    'SELECT group_concat(username) FROM (SELECT username FROM users ORDER BY username); SELECT max(version_id) FROM goose_db_version;'
}

active() { systemctl is-active --quiet "$1"; }

echo "== prepare: hosts entry, restic, staging"
grep -q " $domain\$" /etc/hosts || echo "127.0.0.1 $domain" >>/etc/hosts
# GitHub's runner image leaves /usr/local/bin and /usr/share/keyrings group/world-writable; the
# installer rightly refuses to put root's binaries or apt's Caddy key there (another user could
# swap them), so the runner gets the Ubuntu default first.
chmod go-w /usr/local/bin /usr/share/keyrings /etc/apt/sources.list.d
bash "$repo/scripts/install-restic.sh"
v1="$work/v1" v2="$work/v2" broken="$work/broken"
stage "$v1" 0.0.1-ci
stage "$v2" 0.0.2-ci
stage "$broken" 0.0.3-ci broken
echo "OK: staged 0.0.1-ci, 0.0.2-ci and the broken 0.0.3-ci"

echo "== install (tls internal, firewall on)"
printf '%s\n' "$admin_pw" | inst "$v1" install --from-dir "$v1" --tls internal --domain "$domain" --ssh-port 22 --admin-user "$admin" --yes ||
  fail "install: exit $?"
for u in mcpanel-agent.service mcpanel-files.socket mcpanel-files.service mcpanel-panel.socket mcpanel-panel.service mcpanel-fw.service caddy.service; do
  active "$u" || fail "$u not active after install"
done
[ "$(stat -c '%U:%G %a' /run/mc-panel-web/panel.sock)" = "mcpanel:caddy 660" ] ||
  fail "panel socket: $(stat -c '%U:%G %a' /run/mc-panel-web/panel.sock)"
[ "$(stat -c '%U:%G %a' /run/mc-panel-files/files.sock)" = "mcpanel-mc:mcpanel 660" ] ||
  fail "files socket: $(stat -c '%U:%G %a' /run/mc-panel-files/files.sock)"
[ "$(/usr/local/bin/panel version)" = 0.0.1-ci ] || fail "installed panel version: $(/usr/local/bin/panel version)"
[ "$(runuser -u mcpanel -- /usr/local/bin/panel admin list -config /etc/mc-panel/panel.toml)" = "$admin" ] || fail "admin list"
grep -qx 'import mc-panel.caddy # managed by mc-panel' /etc/caddy/Caddyfile || fail "Caddyfile without the managed import line"
nft list table inet mcpanel_fw >"$work/fw.nft" || fail "table inet mcpanel_fw missing"
grep -q 'policy drop' "$work/fw.nft" || fail "mcpanel_fw input policy is not drop"
for p in 22 80 443 25565; do
  grep -qE "tcp dport .*\\b$p\\b" "$work/fw.nft" || fail "mcpanel_fw does not allow tcp $p"
done
curl -sS --max-time 20 -o /dev/null https://github.com/ || fail "outbound HTTPS broken by the firewall"
echo "OK: installed, units active, sockets and firewall as specified"

echo "== Caddy from its repository, admin API on a socket, sandboxed services"
apt-cache policy caddy | awk '/^ [*][*][*] /{i=1; next} /^     [^ ]/{i=0} i' | grep -q 'dl.cloudsmith.io/public/caddy/stable' ||
  fail "caddy does not come from the Caddy repository: $(apt-cache policy caddy)"
cv="$(caddy version | awk '{ sub(/^v/, "", $1); print $1 }')"
[ "$(printf '%s\n' 2.10.0 "$cv" | sort -V | head -n 1)" = 2.10.0 ] || fail "caddy $cv is older than 2.10"
[ -S /run/caddy/admin.sock ] || fail "no Caddy admin socket"
if runuser -u mcpanel -- curl -sS -m 2 -o /dev/null http://127.0.0.1:2019/config/ 2>/dev/null; then
  fail "the panel user reaches Caddy's admin API on 127.0.0.1:2019"
fi
systemctl reload caddy.service || fail "systemctl reload caddy with the admin socket"
for u in mcpanel-panel mcpanel-agent mcpanel-files; do
  systemd-analyze security --no-pager "$u.service" | tail -n 1
done
echo "OK: caddy $cv from its repository, admin socket only, reload works"
[ "$(stat -c '%U:%G %a' /etc/caddy/mc-panel.d)" = "root:root 755" ] || fail "/etc/caddy/mc-panel.d: $(stat -c '%U:%G %a' /etc/caddy/mc-panel.d)"
printf 'respond /ci-site-hook 204\n' >/etc/caddy/mc-panel.d/ci.caddy
systemctl reload caddy.service || fail "reload with a file in mc-panel.d"
code="$(https /ci-site-hook -o /dev/null -w '%{http_code}')"
[ "$code" = 204 ] || fail "the panel site does not import mc-panel.d (HTTP $code)"
rm /etc/caddy/mc-panel.d/ci.caddy
systemctl reload caddy.service || fail "reload after removing the mc-panel.d file"
echo "OK: the panel site imports /etc/caddy/mc-panel.d/*.caddy"

echo "== doctor"
n="$(doctor_fails)"
[ "$n" = 0 ] || fail "doctor reports $n failures: $(cat "$work/doctor.json")"
echo "OK: doctor without failures"

echo "== second install changes nothing"
inst "$v1" install --from-dir "$v1" --tls internal --domain "$domain" --ssh-port 22 --yes </dev/null || fail "second install: exit $?"
if grep -E "$changed_re" "$work/last.out"; then
  fail "second install reported changes (see above)"
fi
echo "OK: idempotent"

echo "== sign-in through Caddy"
wait_health
login_ok
grep -q '"uri":"/api/auth/login"' /var/log/caddy/mc-panel.log || fail "the login is not in the access log /var/log/caddy/mc-panel.log"
echo "OK: panel reachable through Caddy, admin signs in, access log written"

echo "== doctor checks the audit log against the journal"
# the sign-in above wrote audit entries, and the panel logged their anchors to its journal
[ "$(runuser -u mcpanel -- sqlite3 -readonly /var/lib/mc-panel/panel.db 'SELECT count(*) FROM audit_log')" -gt 0 ] ||
  fail "no audit entries after the sign-in"
[ "$(doctor_check audit_journal)" = ok ] || fail "doctor audit_journal: $(cat "$work/doctor.json")"
audit_tamper "actor_name || '~'"
[ "$(doctor_check audit_journal)" = fail ] || fail "doctor audit_journal after a changed entry: $(cat "$work/doctor.json")"
audit_tamper "substr(actor_name, 1, length(actor_name) - 1)"
[ "$(doctor_check audit_journal)" = ok ] || fail "doctor audit_journal after undoing the change: $(cat "$work/doctor.json")"
echo "OK: doctor audit_journal is ok and fails for a changed entry"

echo "== update to 0.0.2-ci"
before="$(db_fingerprint)"
inst "$v2" update --from-dir "$v2" --yes </dev/null || fail "update: exit $?"
[ "$(/usr/local/bin/panel version)" = 0.0.2-ci ] || fail "panel version after update: $(/usr/local/bin/panel version)"
for b in agent files; do
  [ "$("/usr/local/bin/$b" -version)" = 0.0.2-ci ] || fail "$b version after update"
done
[ "$(db_fingerprint)" = "$before" ] || fail "database changed by the update"
ls -d /var/lib/mc-panel-rollback/0.0.1-ci-* >/dev/null 2>&1 || fail "no rollback backup of 0.0.1-ci"
[ "$(stat -c '%U:%G %a' /var/lib/mc-panel-rollback)" = "root:root 700" ] ||
  fail "rollback dir: $(stat -c '%U:%G %a' /var/lib/mc-panel-rollback)"
active mcpanel-panel.service || fail "panel not active after update"
wait_health
login_ok
echo "OK: updated, data kept, backup written"

echo "== update to a broken build rolls back"
before="$(db_fingerprint)"
set +e
inst "$broken" update --from-dir "$broken" --yes </dev/null
rc=$?
set -e
[ "$rc" = 3 ] || fail "broken update: exit $rc, want 3 (rolled back)"
[ "$(/usr/local/bin/panel version)" = 0.0.2-ci ] || fail "panel version after rollback: $(/usr/local/bin/panel version)"
active mcpanel-panel.service || fail "panel not active after rollback"
[ "$(db_fingerprint)" = "$before" ] || fail "database differs after rollback"
wait_health
login_ok
n="$(doctor_fails)"
[ "$n" = 0 ] || fail "doctor reports $n failures after the rollback: $(cat "$work/doctor.json")"
echo "OK: failed health check rolled back to 0.0.2-ci"

echo "== update to a signed release over HTTPS"
release_server
launcher="$work/launcher" signed="$work/signed" tampered="$work/tampered"
# launcher: 0.0.2-ci again, but trusting the CI key and the local server; it hands over to the
# install.sh of 9.0.0-ci after checking the signature, and that one downloads the tarball.
stage_signed "$launcher" 0.0.2-ci
stage_signed "$signed" 9.0.0-ci
stage_signed "$tampered" 9.0.1-ci
publish "$signed" 9.0.0-ci
publish "$tampered" 9.0.1-ci
# after signing: a changed install.sh with a matching checksums.txt (an attacker with the
# release but without the key)
td="$work/releases/download/v9.0.1-ci"
printf '\necho tampered\n' >>"$td/install.sh"
(cd "$td" && sed -i '/ install.sh$/d' checksums.txt && sha256sum install.sh >>checksums.txt)
before="$(db_fingerprint)"
inst "$launcher" update --version 9.0.0-ci --yes </dev/null || fail "signed update: exit $?"
grep -q 'release v9.0.0-ci: signed' "$work/last.out" || fail "the hand-over did not report the signature"
[ "$(/usr/local/bin/panel version)" = 9.0.0-ci ] || fail "panel version after the signed update: $(/usr/local/bin/panel version)"
[ "$(db_fingerprint)" = "$before" ] || fail "database changed by the signed update"
grep -qx 'verified=signed' /etc/mc-panel/release || fail "release record: $(cat /etc/mc-panel/release)"
"$signed/install.sh" doctor --json >"$work/doctor.json" 2>/dev/null || true
python3 -I -c 'import json, sys; d = json.load(open(sys.argv[1])); c = [c for c in d["checks"] if c["id"] == "release_signature"][0]; sys.exit(0 if c["status"] == "ok" and "signature-checked" in c["message"] else 1)' "$work/doctor.json" ||
  fail "doctor release_signature: $(cat "$work/doctor.json")"
wait_health
login_ok
set +e
inst "$launcher" update --version 9.0.1-ci --yes </dev/null
rc=$?
set -e
[ "$rc" = 1 ] || fail "tampered update: exit $rc, want 1"
grep -q 'does not verify' "$work/last.out" || fail "the tampered release was not refused for its signature"
[ "$(/usr/local/bin/panel version)" = 9.0.0-ci ] || fail "panel version after the tampered update: $(/usr/local/bin/panel version)"
active mcpanel-panel.service || fail "panel not active after the refused update"
echo "OK: signed release installed through the hand-over, tampered release refused"

echo "== uninstall keeps data"
# --init: sleep as PID 1 would ignore SIGTERM, and uninstall's docker stop would wait 60 s.
docker run -d --init --name ci-foreign --label ci.foreign=1 busybox:1.37 sleep 600 >/dev/null
docker run -d --init --name ci-fake-instance --label mcpanel.instance=999 busybox:1.37 sleep 600 >/dev/null
nft add table inet ci_foreign
inst "$v2" uninstall --yes </dev/null || fail "uninstall: exit $?"
for u in mcpanel-agent.service mcpanel-files.socket mcpanel-files.service mcpanel-panel.socket mcpanel-panel.service mcpanel-fw.service; do
  [ ! -e "/etc/systemd/system/$u" ] || fail "$u still installed"
  ! active "$u" || fail "$u still active"
done
for b in panel agent files; do
  [ ! -e "/usr/local/bin/$b" ] || fail "/usr/local/bin/$b still there"
done
[ ! -e /etc/caddy/mc-panel.caddy ] || fail "mc-panel.caddy still there"
! grep -q '^import mc-panel.caddy' /etc/caddy/Caddyfile || fail "import line still in the Caddyfile"
active caddy.service || fail "caddy stopped by uninstall"
! nft list table inet mcpanel_fw >/dev/null 2>&1 || fail "table inet mcpanel_fw still there"
! nft list table inet mcpanel >/dev/null 2>&1 || fail "table inet mcpanel still there"
nft list table inet ci_foreign >/dev/null || fail "foreign nftables table removed"
[ -z "$(docker ps -aq --filter label=mcpanel.instance)" ] || fail "mc-panel containers left"
[ -n "$(docker ps -q --filter name=ci-foreign)" ] || fail "foreign container removed"
for f in /var/lib/mc-panel/panel.db /etc/mc-panel/panel.toml /etc/mc-panel/agent.toml /etc/mc-panel-files/files.toml; do
  [ -e "$f" ] || fail "$f removed by uninstall"
done
if ! id mcpanel >/dev/null 2>&1 || ! id mcpanel-mc >/dev/null 2>&1; then
  fail "users removed by uninstall"
fi
echo "OK: uninstall removed the program only"

echo "== install again over kept data"
inst "$v2" install --from-dir "$v2" --tls internal --domain "$domain" --ssh-port 22 --yes </dev/null || fail "reinstall: exit $?"
grep -q '^\[skip\] admin exists' "$work/last.out" || fail "reinstall did not find the admin"
wait_health
login_ok
echo "OK: reinstall found DB and config, old password valid"

echo "== uninstall --purge needs the domain"
set +e
printf 'wrong.example\n' | script -qec "$v2/install.sh uninstall --purge --yes" /dev/null >"$work/purge1.out" 2>&1
rc=$?
set -e
cat "$work/purge1.out"
[ "$rc" != 0 ] || fail "purge with a wrong confirmation succeeded"
[ -e /var/lib/mc-panel/panel.db ] || fail "data removed despite a wrong confirmation"
id mcpanel >/dev/null 2>&1 || fail "user mcpanel removed despite a wrong confirmation"
printf '%s\n' "$domain" | script -qec "$v2/install.sh uninstall --purge" /dev/null >"$work/purge2.out" 2>&1 ||
  fail "purge: $(cat "$work/purge2.out")"
cat "$work/purge2.out"
for p in /var/lib/mc-panel /var/lib/mc-panel-rollback /srv/mc-panel /etc/mc-panel /etc/mc-panel-files; do
  [ ! -e "$p" ] || fail "$p still there after --purge"
done
! id mcpanel >/dev/null 2>&1 || fail "user mcpanel still there after --purge"
! id mcpanel-mc >/dev/null 2>&1 || fail "user mcpanel-mc still there after --purge"
docker image inspect busybox:1.37 >/dev/null || fail "--purge removed a foreign image"
[ -n "$(docker ps -q --filter name=ci-foreign)" ] || fail "foreign container removed by --purge"
echo "OK: purge only with the typed domain, then everything gone"

echo "PASS: installer lifecycle (install, doctor, idempotence, login, update, rollback, uninstall, reinstall, purge)"
