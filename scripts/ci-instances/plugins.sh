# shellcheck shell=bash
# Shard plugins, sourced by scripts/ci-instances.sh (sub-project 3c, spec §10 criteria 2 and 5 on
# the panel/files-service path): on the panel's Paper server, installs plugins from the real
# Modrinth and Hangar (Chunky; ViaBackwards with its required dependency ViaVersion), checks
# inventory, checksums, icon proxy, loading after a restart, disabling, removal and the audit
# log; then the mrpack import into a Fabric server.

setup_agent_files
setup_panel_server

echo "== plugins: empty inventory; CSRF and foreign icon hosts refused"
papi GET "/servers/$psrv/plugins" "$po/inv.json"
[ "$pcode" = 200 ] || fail "inventory: HTTP $pcode $(cat "$po/inv.json")"
jcheck "$po/inv.json" "empty inventory of a fresh Paper server" '
  d["kind"] == "plugin" and d["dir"] == "plugins" and d["game_version"] == "1.21.11"
  and sorted(d["sources"]) == ["hangar", "modrinth"] and d["jars"] == [] and not d["truncated"]'
code="$(curl -sS --max-time 10 -o "$po/csrf.json" -w '%{http_code}' -X POST -H "Origin: $porigin" \
  -H 'Sec-Fetch-Site: same-origin' -H "Cookie: __Host-sid=$psid" -H 'Content-Type: application/json' \
  --data-binary '{"source": "modrinth", "project": "fALzjamp"}' "$pbase/api/servers/$psrv/plugins/install")" ||
  fail "install without CSRF token: curl exit $?"
is_perror "$po/csrf.json" "$code" 403 csrf_invalid || fail "install without CSRF token: HTTP $code $(cat "$po/csrf.json")"
papi GET "/servers/$psrv/plugins/jobs" "$po/job.json"
jcheck "$po/job.json" "a refused install must not start a job" 'd["job"] is None'
# Spec §6.8: only https://cdn.modrinth.com/ and https://hangarcdn.papermc.io/ (prefix incl. the
# slash), so look-alike hosts, userinfo tricks and plain http are refused before any request.
for u in https://example.com/icon.png http://cdn.modrinth.com/data/fALzjamp/icon.webp \
  https://cdn.modrinth.com.evil.example/icon.png https://hangarcdn.papermc.io@example.com/icon.png; do
  q="$(python3 -I -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1], safe=""))' "$u")"
  papi GET "/servers/$psrv/plugins/icon?url=$q" "$po/icon.json"
  is_perror "$po/icon.json" "$pcode" 400 validation_failed || fail "icon proxy with $u: HTTP $pcode $(head -c 300 "$po/icon.json")"
done
echo "OK: inventory empty (plugin, plugins/, 1.21.11, modrinth+hangar); 403 csrf_invalid without job; icon proxy refuses foreign hosts and http (400)"

echo "== plugins: search Modrinth and Hangar (real upstreams)"
papi_up GET "/servers/$psrv/plugins/search?source=modrinth&q=chunky" "$po/search-m.json"
[ "$pcode" = 200 ] || fail "search modrinth: HTTP $pcode $(cat "$po/search-m.json")"
jcheck "$po/search-m.json" "Modrinth search finds Chunky" '
  any(h["slug"] == "chunky" and h["source"] == "modrinth" and not h["installed"]
      and h["page_url"] == "https://modrinth.com/plugin/chunky"
      and (h["icon_url"] or "").startswith("/api/servers/%s/plugins/icon?url=" % a[0]) for h in d["hits"])' "$psrv"
p_chunky="$(jget "$po/search-m.json" 'next(h["project_id"] for h in d["hits"] if h["slug"] == "chunky")')"
icon_chunky="$(jget "$po/search-m.json" 'next(h["icon_url"] for h in d["hits"] if h["slug"] == "chunky")')"
papi_up GET "/servers/$psrv/plugins/search?source=hangar&q=ViaBackwards" "$po/search-h.json"
[ "$pcode" = 200 ] || fail "search hangar: HTTP $pcode $(cat "$po/search-h.json")"
jcheck "$po/search-h.json" "Hangar search finds ViaBackwards" '
  any(h["slug"].lower() == "viabackwards" and h["source"] == "hangar" and not h["installed"]
      and h["page_url"] == "https://hangar.papermc.io/ViaVersion/ViaBackwards" for h in d["hits"])'
p_viaback="$(jget "$po/search-h.json" 'next(h["project_id"] for h in d["hits"] if h["slug"].lower() == "viabackwards")')"
echo "OK: search hits chunky ($p_chunky, Modrinth) and ViaBackwards ($p_viaback, Hangar) with page and proxied icon URLs"

echo "== plugins: icon proxy delivers the Chunky icon"
papi_up GET "${icon_chunky#/api}" "$po/icon.bin"
[ "$pcode" = 200 ] || fail "icon proxy (Chunky): HTTP $pcode $(head -c 300 "$po/icon.bin")"
tr -d '\r' <"$po/icon.bin.headers" >"$po/icon.h"
grep -qiE '^content-type: image/(png|jpeg|webp|gif)$' "$po/icon.h" || fail "icon content type: $(cat "$po/icon.h")"
grep -qix 'x-content-type-options: nosniff' "$po/icon.h" || fail "icon without nosniff: $(cat "$po/icon.h")"
grep -qix "content-security-policy: default-src 'none'" "$po/icon.h" || fail "icon CSP: $(cat "$po/icon.h")"
[ "$(stat -c %s "$po/icon.bin")" -le 262144 ] || fail "icon larger than 256 KiB"
echo "OK: icon via proxy ($(grep -i '^content-type:' "$po/icon.h"), nosniff, CSP default-src 'none')"

echo "== plugins: resolve (Chunky alone; ViaBackwards pulls in ViaVersion)"
papi_up GET "/servers/$psrv/plugins/resolve?source=modrinth&project=$p_chunky" "$po/resolve-m.json"
[ "$pcode" = 200 ] || fail "resolve chunky: HTTP $pcode $(cat "$po/resolve-m.json")"
jcheck "$po/resolve-m.json" "plan for Chunky" '
  [(e["slug"], e["source"], e["required_by"]) for e in d["install"]] == [("chunky", "modrinth", None)]
  and d["install"][0]["filename"].endswith(".jar") and d["install"][0]["size"] > 0
  and d["already"] == [] and d["external"] == [] and d["incompatible"] == []'
f_chunky="$(plan_field "$po/resolve-m.json" chunky filename)"
s_chunky="$(plan_field "$po/resolve-m.json" chunky size)"
papi_up GET "/servers/$psrv/plugins/resolve?source=hangar&project=$p_viaback" "$po/resolve-h.json"
[ "$pcode" = 200 ] || fail "resolve ViaBackwards: HTTP $pcode $(cat "$po/resolve-h.json")"
jcheck "$po/resolve-h.json" "plan for ViaBackwards with ViaVersion as required dependency" '
  sorted(e["slug"].lower() for e in d["install"]) == ["viabackwards", "viaversion"]
  and all(e["source"] == "hangar" and e["channel"] == "release" and e["size"] > 0 and e["filename"].endswith(".jar") for e in d["install"])
  and [e["required_by"] for e in d["install"] if e["slug"].lower() == "viabackwards"] == [None]
  and [e["required_by"] for e in d["install"] if e["slug"].lower() == "viaversion"] == [e["title"] for e in d["install"] if e["slug"].lower() == "viabackwards"]
  and d["already"] == [] and d["external"] == [] and d["incompatible"] == []'
f_viaver="$(plan_field "$po/resolve-h.json" viaversion filename)"
s_viaver="$(plan_field "$po/resolve-h.json" viaversion size)"
p_viaver="$(plan_field "$po/resolve-h.json" viaversion project_id)"
f_viaback="$(plan_field "$po/resolve-h.json" viabackwards filename)"
s_viaback="$(plan_field "$po/resolve-h.json" viabackwards size)"
echo "OK: plans: $f_chunky; $f_viaver (required by ViaBackwards) + $f_viaback; nothing external or incompatible"

echo "== plugins: install while the server runs (jobs, checksums checked by the files service)"
install_plugin modrinth "$p_chunky" "Chunky (Modrinth)"
install_plugin hangar "$p_viaback" "ViaBackwards (Hangar)"
# Spec §6.4: dependencies first; after an upstream retry only the missing step is left.
jcheck "$po/job.json" "ViaVersion installed before ViaBackwards" '[s["file"] for s in d["job"]["steps"]] in ([a[0], a[1]], [a[1]])' "$f_viaver" "$f_viaback"
for e in "$f_chunky:$s_chunky" "$f_viaver:$s_viaver" "$f_viaback:$s_viaback"; do
  n="${e%%:*}"
  s="${e##*:}"
  [ -f "$pdata/plugins/$n" ] && [ ! -L "$pdata/plugins/$n" ] || fail "jar missing on disk: plugins/$n"
  [ "$(stat -c '%U %s' "$pdata/plugins/$n")" = "mcpanel-mc $s" ] ||
    fail "plugins/$n: $(stat -c '%U %s' "$pdata/plugins/$n") (want mcpanel-mc $s: written by the files service, size from the upstream)"
done
no_tmp_in "$pdata" || fail "install left a .mcpanel-tmp-* file"
papi GET "/servers/$psrv/plugins" "$po/inv.json"
[ "$pcode" = 200 ] || fail "inventory: HTTP $pcode $(cat "$po/inv.json")"
jcheck "$po/inv.json" "inventory after install" '
  sorted(j["file"] for j in d["jars"]) == sorted(a[0:3])
  and all(j["enabled"] and j["installed_by_panel"] and j["project"] and j["version"] for j in d["jars"])
  and {j["file"]: (j["source"], j["project"]["id"]) for j in d["jars"]} == {a[0]: ("modrinth", a[3]), a[1]: ("hangar", a[4]), a[2]: ("hangar", a[5])}
  and d["restart_needed"]' "$f_chunky" "$f_viaver" "$f_viaback" "$p_chunky" "$p_viaver" "$p_viaback"
papi_up GET "/servers/$psrv/plugins/search?source=hangar&q=ViaBackwards" "$po/search-h.json"
[ "$pcode" = 200 ] || fail "search hangar: HTTP $pcode $(cat "$po/search-h.json")"
jcheck "$po/search-h.json" "search marks ViaBackwards as installed" 'any(h["project_id"] == a[0] and h["installed"] for h in d["hits"])' "$p_viaback"
echo "OK: three jars in plugins/ (owner mcpanel-mc, upstream sizes), inventory with source, project, installed_by_panel and restart_needed"

echo "== plugins: files service lists the jars with checksums and metadata"
code="$(fpost /v1/jars "{\"id\": $psrv, \"dir\": \"plugins\"}" "$fo/jars.json")"
[ "$code" = 200 ] || fail "files /v1/jars: HTTP $code $(cat "$fo/jars.json")"
python3 -I -c '
import hashlib, json, os, sys
d = json.load(open(sys.argv[1]))
got = {j["name"]: j for j in d["jars"]}
if d["truncated"]:
    sys.exit("jars truncated")
for name in sys.argv[3:]:
    j = got.get(name)
    if j is None:
        sys.exit("jars lacks %s: %r" % (name, sorted(got)))
    data = open(os.path.join(sys.argv[2], name), "rb").read()
    if (j["sha256"], j["sha512"], j["size"], j["enabled"]) != (hashlib.sha256(data).hexdigest(), hashlib.sha512(data).hexdigest(), len(data), True):
        sys.exit("jars entry %s: checksums, size or enabled differ from the disk" % name)
    m = j["meta"]
    if not m or m["kind"] != "plugin" or not m["name"] or m["name"].lower() not in name.lower():
        sys.exit("jars entry %s: meta %r" % (name, m))' "$fo/jars.json" "$pdata/plugins" "$f_chunky" "$f_viaver" "$f_viaback" ||
  fail "files /v1/jars content"
code="$(fpost /v1/jars "{\"id\": $psrv, \"dir\": \"mods\"}" "$fo/jars.json")"
[ "$code" = 200 ] || fail "files /v1/jars mods: HTTP $code $(cat "$fo/jars.json")"
jcheck "$fo/jars.json" "a missing mods/ is an empty list" 'd["jars"] == [] and not d["truncated"]'
code="$(fpost /v1/jars "{\"id\": $psrv, \"dir\": \"world\"}" "$fo/jars.json")"
is_error "$fo/jars.json" "$code" 400 invalid || fail "files /v1/jars with dir world: HTTP $code $(cat "$fo/jars.json")"
echo "OK: /v1/jars: sha256/sha512/size match the disk, metadata from plugin.yml; mods/ empty; dir world -> 400 invalid"

echo "== plugins: upload with checksum (files service)"
head -c 65536 /dev/urandom >"$work/ci-sum.bin"
head -c 1024 /dev/urandom >"$work/ci-sum.other"
chmod 0644 "$work/ci-sum.bin" "$work/ci-sum.other"
sum256="$(sha256sum "$work/ci-sum.bin" | cut -d' ' -f1)"
sum512="$(sha512sum "$work/ci-sum.bin" | cut -d' ' -f1)"
zero256="$(printf '%064d' 0)"
code="$(fputsum "$work/ci-sum.bin" "overwrite=0&sha256=$zero256" "$fo/sum.json")"
is_error "$fo/sum.json" "$code" 400 hash_mismatch || fail "upload with a wrong sha256: HTTP $code $(cat "$fo/sum.json")"
[ ! -e "$pdata/ci-sum.bin" ] || fail "upload with a wrong checksum left ci-sum.bin behind"
code="$(fputsum "$work/ci-sum.bin" "overwrite=0&sha256=$sum256&sha512=$sum512" "$fo/sum.json")"
is_error "$fo/sum.json" "$code" 400 invalid || fail "upload with sha256 and sha512: HTTP $code $(cat "$fo/sum.json")"
code="$(fputsum "$work/ci-sum.bin" "overwrite=0&sha512=$sum512" "$fo/sum.json")"
[ "$code" = 201 ] || fail "upload with the right sha512: HTTP $code $(cat "$fo/sum.json")"
jcheck "$fo/sum.json" "upload answer with both checksums" 'd["size"] == 65536 and d["sha256"] == a[0] and d["sha512"] == a[1]' "$sum256" "$sum512"
cmp -s "$work/ci-sum.bin" "$pdata/ci-sum.bin" || fail "uploaded ci-sum.bin differs"
code="$(fputsum "$work/ci-sum.other" "overwrite=1&sha256=$sum256" "$fo/sum.json")"
is_error "$fo/sum.json" "$code" 400 hash_mismatch || fail "overwrite with a wrong checksum: HTTP $code $(cat "$fo/sum.json")"
cmp -s "$work/ci-sum.bin" "$pdata/ci-sum.bin" || fail "a refused overwrite changed ci-sum.bin"
no_tmp_in "$pdata" || fail "checksum uploads left a .mcpanel-tmp-* file"
rm -f "$pdata/ci-sum.bin"
echo "OK: wrong checksum -> 400 hash_mismatch (no file, target unchanged), both parameters -> 400 invalid, right one -> 201 with sha256/sha512"

echo "== plugins: restart, all three plugins load"
p_restart "after install"
check_plugins "Chunky,ViaVersion,ViaBackwards" ""
check_enabled_in_log Chunky ViaVersion ViaBackwards
papi GET "/servers/$psrv/plugins" "$po/inv.json"
[ "$pcode" = 200 ] || fail "inventory: HTTP $pcode $(cat "$po/inv.json")"
jcheck "$po/inv.json" "no restart needed after the restart" 'not d["restart_needed"]'
echo "OK: Chunky, ViaVersion and ViaBackwards enabled after the restart (RCON plugins, log); restart_needed false"

echo "== plugins: disable Chunky, restart, it is not loaded"
papi POST "/servers/$psrv/plugins/toggle" "$po/toggle.json" \
  "$(python3 -I -c 'import json, sys; print(json.dumps({"file": sys.argv[1], "enabled": False}))' "$f_chunky")"
[ "$pcode" = 204 ] || fail "disable Chunky: HTTP $pcode $(cat "$po/toggle.json")"
[ -f "$pdata/plugins/$f_chunky.disabled" ] && [ ! -e "$pdata/plugins/$f_chunky" ] || fail "disable not on disk: $(ls -a "$pdata/plugins")"
papi GET "/servers/$psrv/plugins" "$po/inv.json"
[ "$pcode" = 200 ] || fail "inventory: HTTP $pcode $(cat "$po/inv.json")"
jcheck "$po/inv.json" "Chunky disabled in the inventory, origin kept" '
  [(j["enabled"], j["source"], j["installed_by_panel"]) for j in d["jars"] if j["file"] == a[0] + ".disabled"] == [(False, "modrinth", True)]
  and d["restart_needed"]' "$f_chunky"
p_restart "after disabling Chunky"
check_plugins "ViaVersion,ViaBackwards" "Chunky"
echo "OK: $f_chunky.disabled is not loaded after the restart, ViaVersion and ViaBackwards still are"

echo "== plugins: enable and remove Chunky"
papi POST "/servers/$psrv/plugins/toggle" "$po/toggle.json" \
  "$(python3 -I -c 'import json, sys; print(json.dumps({"file": sys.argv[1], "enabled": True}))' "$f_chunky.disabled")"
[ "$pcode" = 204 ] || fail "enable Chunky: HTTP $pcode $(cat "$po/toggle.json")"
[ -f "$pdata/plugins/$f_chunky" ] && [ ! -e "$pdata/plugins/$f_chunky.disabled" ] || fail "enable not on disk: $(ls -a "$pdata/plugins")"
papi POST "/servers/$psrv/plugins/delete" "$po/delete.json" "$(jobj file "$f_chunky")"
[ "$pcode" = 204 ] || fail "delete Chunky: HTTP $pcode $(cat "$po/delete.json")"
[ ! -e "$pdata/plugins/$f_chunky" ] || fail "deleted jar still on disk"
papi GET "/servers/$psrv/plugins" "$po/inv.json"
[ "$pcode" = 200 ] || fail "inventory: HTTP $pcode $(cat "$po/inv.json")"
jcheck "$po/inv.json" "inventory after removal" 'sorted(j["file"] for j in d["jars"]) == sorted(a)' "$f_viaver" "$f_viaback"
echo "OK: toggle back to .jar, delete removes the jar and its inventory entry"

echo "== plugins: update check against the real upstreams"
papi_up GET "/servers/$psrv/plugins/updates?refresh=1" "$po/updates.json"
[ "$pcode" = 200 ] || fail "updates: HTTP $pcode $(cat "$po/updates.json")"
jcheck "$po/updates.json" "updates answer" 'd["checked_at"] > 0 and all(u["file"] in a for u in d["updates"])' "$f_viaver" "$f_viaback"
echo "OK: update check answers ($(jget "$po/updates.json" 'len(d["updates"])') update(s))"

echo "== plugins: audit entries and hash chain"
papi GET "/audit?action=plugin.&limit=200" "$po/audit.json"
[ "$pcode" = 200 ] || fail "audit: HTTP $pcode $(cat "$po/audit.json")"
jcheck "$po/audit.json" "plugin.* audit entries" '
  sorted(e["details"].get("file") for e in d["items"] if e["action"] == "plugin.install" and e["result"] == "success") == sorted(a[0:3])
  and sorted(e["details"].get("enabled") for e in d["items"] if e["action"] == "plugin.toggle") == [False, True]
  and [e["details"].get("file") for e in d["items"] if e["action"] == "plugin.remove"] == [a[0]]
  and all(e["target"] == "server:" + a[3] and e["actor_name"] == "ci-admin" for e in d["items"])' \
  "$f_chunky" "$f_viaver" "$f_viaback" "$psrv"
setpriv --reuid=mcpanel --regid=mcpanel --init-groups \
  "$work/bin/panel" audit verify -config "$work/panel.toml" >"$po/verify.txt" 2>&1 ||
  fail "panel audit verify: $(cat "$po/verify.txt")"
echo "OK: plugin.install x3 (incl. the dependency), plugin.toggle x2, plugin.remove x1 on server:$psrv by ci-admin; audit verify OK"

echo "== plugins: mrpack import into a Fabric server (Fabric API installed, Sodium client_only, unknown hash not_on_modrinth)"
papi POST /servers "$po/fserver.json" '{"name": "cimrpack", "type": "fabric", "version": "1.21.11", "java": "java21", "memory_mib": 2048, "eula_accepted": true}'
[ "$pcode" = 201 ] || fail "create fabric server: HTTP $pcode $(cat "$po/fserver.json")"
fsrv="$(jget "$po/fserver.json" 'd["id"]')"
f_created() { [ "$(inst_field "$fsrv" 'i["state"]')" != absent ]; }
pwait_for 300 "container of the fabric server (reconcile)" f_created
# The index is built from the real Modrinth versions for 1.21.11 (the hashes are what the panel
# matches); downloads point somewhere else on purpose: the panel must never use them.
python3 -I - "$po/mrpack.json" <<'PY' || fail "build modrinth.index.json from Modrinth"
import json, sys, time, urllib.parse, urllib.request

def latest(project):
    q = urllib.parse.urlencode({"loaders": '["fabric"]', "game_versions": '["1.21.11"]'})
    req = urllib.request.Request("https://api.modrinth.com/v2/project/%s/version?%s" % (project, q),
                                 headers={"User-Agent": "mc-panel-ci (https://github.com/Bernotese/mc-panel)"})
    for attempt in range(3):
        try:
            with urllib.request.urlopen(req, timeout=30) as r:
                v = json.load(r)[0]
            break
        except Exception:
            if attempt == 2:
                raise
            time.sleep(20)
    f = next((f for f in v["files"] if f["primary"]), v["files"][0])
    return f["filename"], f["hashes"]["sha512"]

fapi, sod = latest("fabric-api"), latest("sodium")
def entry(name, sha512, server):
    return {"path": "mods/" + name, "hashes": {"sha512": sha512, "sha1": "0" * 40},
            "env": {"client": "required", "server": server}, "downloads": ["https://example.invalid/" + name], "fileSize": 1}
index = {"formatVersion": 1, "game": "minecraft", "versionId": "1.0.0", "name": "CI Pack",
         "dependencies": {"minecraft": "1.21.11", "fabric-loader": "0.17.2"},
         "files": [entry(fapi[0], fapi[1], "required"), entry(sod[0], sod[1], "required"),
                   entry("ci-private.jar", "ab" * 64, "required"),
                   {"path": "config/ci.json", "hashes": {"sha512": "cd" * 64}}]}
json.dump(index, open(sys.argv[1], "w"))
PY
f_fapi="$(jget "$po/mrpack.json" 'd["files"][0]["path"][5:]')"
papi_up POST "/servers/$fsrv/plugins/mrpack/preview" "$po/mpreview.json" "$(cat "$po/mrpack.json")"
[ "$pcode" = 200 ] || fail "mrpack preview: HTTP $pcode $(cat "$po/mpreview.json")"
jcheck "$po/mpreview.json" "mrpack plan" '
  [(e["action"], e["reason"]) for e in d["entries"]] == [("install", None), ("skip", "client_only"), ("skip", "not_on_modrinth")]
  and d["entries"][0]["file"] == a[0] and d["entries"][0]["project"]["slug"] == "fabric-api"
  and d["entries"][1]["project"]["slug"] == "sodium" and d["entries"][2]["project"] is None
  and d["kept"] == [] and d["ignored_files"] == 1 and d["pack"] == {"name": "CI Pack", "version_id": "1.0.0"}
  and d["game_version"] == {"pack": "1.21.11", "server": "1.21.11"} and d["loader"]["name"] == "fabric"' "$f_fapi"
# A NeoForge pack is refused on a Fabric server.
papi POST "/servers/$fsrv/plugins/mrpack/preview" "$po/mneo.json" \
  "$(python3 -I -c 'import json, sys; d = json.load(open(sys.argv[1])); d["dependencies"] = {"minecraft": "1.21.11", "neoforge": "21.11.1"}; print(json.dumps(d))' "$po/mrpack.json")"
is_perror "$po/mneo.json" "$pcode" 409 mrpack_incompatible || fail "neoforge pack on fabric: HTTP $pcode $(cat "$po/mneo.json")"
psrv_saved="$psrv"
for attempt in 1 2; do
  papi_up POST "/servers/$fsrv/plugins/mrpack/import" "$po/mimport.json" \
    "$(python3 -I -c 'import json, sys; print(json.dumps({"index": json.load(open(sys.argv[1])), "exclude": [], "accept_mismatch": False}))' "$po/mrpack.json")"
  [ "$pcode" = 202 ] || fail "mrpack import: HTTP $pcode $(cat "$po/mimport.json")"
  pjob="$(jget "$po/mimport.json" 'd["id"]')"
  psrv="$fsrv" # p_job_finished polls $psrv
  pwait_for 300 "mrpack job" p_job_finished
  psrv="$psrv_saved"
  [ "$pjob_state" = "done" ] && break
  [ "$attempt" = 1 ] || fail "mrpack import failed twice: $(cat "$po/job.json")"
  echo "WARN: mrpack import failed, retrying once in 20 s: $(cat "$po/job.json")" >&2
  sleep 20
done
jcheck "$po/job.json" "mrpack job" 'd["job"]["kind"] == "mrpack" and [(s["file"], s["state"]) for s in d["job"]["steps"]] == [(a[0], "done")]' "$f_fapi"
papi GET "/servers/$fsrv/plugins" "$po/finv.json"
[ "$pcode" = 200 ] || fail "fabric inventory: HTTP $pcode $(cat "$po/finv.json")"
jcheck "$po/finv.json" "mods/ after the import" '
  d["dir"] == "mods" and [(j["file"], j["source"], j["installed_by_panel"], j["project"]["slug"]) for j in d["jars"]] == [(a[0], "modrinth", True, "fabric-api")]' "$f_fapi"
[ "$(stat -c '%U' "$work/mc/instances/$fsrv/mods/$f_fapi")" = mcpanel-mc ] || fail "mods/$f_fapi not written by the files service"
papi GET "/audit?action=plugin.mrpack_import" "$po/maudit.json"
[ "$pcode" = 200 ] || fail "audit: HTTP $pcode $(cat "$po/maudit.json")"
jcheck "$po/maudit.json" "plugin.mrpack_import audited" '
  any(e["action"] == "plugin.mrpack_import" and e["target"] == "server:" + a[0] and e["details"]["installed"] == 1
      and e["details"]["skipped"] == 2 and e["details"]["failed"] == 0 for e in d["items"])' "$fsrv"
p_sudo
papi DELETE "/servers/$fsrv" "$po/fdel.json"
[ "$pcode" = 202 ] || fail "delete fabric server: HTTP $pcode $(cat "$po/fdel.json")"
f_gone() { [ "$(inst_field "$fsrv" i)" = absent ]; }
pwait_for 180 "container of the fabric server removed" f_gone
echo "OK: mrpack preview (install/client_only/not_on_modrinth, 1 ignored), NeoForge pack 409, import job wrote $f_fapi via the files service, audited"
