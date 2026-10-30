#!/usr/bin/env bash
# Measures how limits.restic_gomaxprocs in files.toml (MC-PERF-04, issue #161) changes the tick
# time of a running server during a backup. For the test VM or a staging host with a deployed
# panel, never production: it rewrites files.toml, restarts mcpanel-files, deletes the backups of
# the benchmark server (initial runs) and toggles saving over RCON. Run it only while nothing
# else uses the host (no test runs), with a fixed workload on the server (docs/dev/README.md,
# "Backup CPU budget"). Needs root, curl, python3 and a paper/purpur/vanilla server >= 1.20.3
# (`tick query` with percentiles).
#
#   sudo bash scripts/bench-restic-budget.sh <server id> [budget ...]   # default budgets: 0 1 2
#
# Environment: RUNS (repetitions per budget and kind, default 3), BASELINE_S (seconds of tick
# samples without a backup per round, default 120), OUT (results directory, default
# ./bench-restic-<date>). Results: OUT/results.csv (one line per backup), OUT/ticks-*.txt (the raw
# tick samples) and a summary on stdout. The budgets run interleaved (0 1 2 0 1 2 …) so that a
# growing world or a warming cache does not favour one of them; files.toml is restored at the end.
set -euo pipefail

FILES_TOML=/etc/mc-panel-files/files.toml
FILES_SOCK=/run/mc-panel-files/files.sock
FILES_UNIT=mcpanel-files.service
INSTANCES_DIR=/srv/mc-panel
SAMPLE_S=5 # `tick query` covers the last 100 ticks = 5 s at 20 TPS

die() {
  echo "bench-restic-budget: $*" >&2
  exit 1
}
log() { echo "[$(date +%T)] $*" >&2; }

[[ $(id -u) -eq 0 ]] || die "run it as root (sudo)"
[[ $# -ge 1 && $1 =~ ^[1-9][0-9]*$ ]] || die "usage: $0 <server id> [budget ...]"
ID=$1
shift
BUDGETS=(0 1 2)
(($# == 0)) || BUDGETS=("$@")
for b in "${BUDGETS[@]}"; do
  if ! [[ $b =~ ^[0-9]+$ ]] || ((b > $(nproc))); then
    die "budget $b is not between 0 and $(nproc)"
  fi
done
RUNS=${RUNS:-3}
BASELINE_S=${BASELINE_S:-120}
OUT=${OUT:-./bench-restic-$(date +%Y%m%d-%H%M%S)}
CONTAINER=mcpanel-mc-$ID
for c in curl python3 docker; do
  command -v "$c" > /dev/null || die "$c is missing"
done
docker inspect -f '{{.State.Running}}' "$CONTAINER" 2> /dev/null | grep -qx true || die "$CONTAINER is not running"
mkdir -p "$OUT"

rcon() { docker exec "$CONTAINER" rcon-cli "$@"; }

files_api() { # METHOD PATH [JSON]
  local args=(-sS --fail-with-body --unix-socket "$FILES_SOCK" -X "$1" "http://files$2")
  [[ $# -lt 3 ]] || args+=(-H 'Content-Type: application/json' -d "$3")
  curl "${args[@]}"
}

# field NAME prints a top-level field of the JSON object on stdin.
field() { python3 -I -c 'import json, sys; print(json.load(sys.stdin)[sys.argv[1]])' "$1"; }
# current_job prints the job of the benchmark server as a JSON object.
current_job() { files_api GET "/v1/jobs?id=$ID" | python3 -I -c 'import json, sys; print(json.dumps(json.load(sys.stdin)["job"]))'; }

# Configuration: the original files.toml comes back on every exit, saving is switched on again.
ORIG=$(mktemp)
cp -p "$FILES_TOML" "$ORIG"
cleanup() {
  rcon save-on > /dev/null 2>&1 || true
  if ! cmp -s "$ORIG" "$FILES_TOML"; then
    cp -p "$ORIG" "$FILES_TOML"
    systemctl restart "$FILES_UNIT" || true
  fi
  rm -f "$ORIG"
}
trap cleanup EXIT

# set_budget N: restic_gomaxprocs = N under [limits] (0 = the key removed), then a restart of the
# files service, which also starts a new cgroup (memory.peak counts from here).
set_budget() {
  local tmp
  tmp=$(mktemp)
  awk -v n="$1" '
    /^[[:space:]]*restic_gomaxprocs[[:space:]]*=/ { next }
    { print }
    /^[[:space:]]*\[limits\][[:space:]]*$/ && n > 0 { print "restic_gomaxprocs = " n; done = 1 }
    END { if (!done && n > 0) print "\n[limits]\nrestic_gomaxprocs = " n }' "$ORIG" > "$tmp"
  cat "$tmp" > "$FILES_TOML" # keeps owner and mode
  rm -f "$tmp"
  systemctl restart "$FILES_UNIT"
  for _ in $(seq 50); do
    files_api GET /v1/backups/status > /dev/null 2>&1 && return 0
    sleep 0.2
  done
  die "$FILES_UNIT did not come back with restic_gomaxprocs = $1 (journalctl -u $FILES_UNIT)"
}

# sample_ticks FILE: one line "epoch p50 p95 p99" (ms) every SAMPLE_S seconds until killed.
sample_ticks() {
  while :; do
    rcon tick query 2> /dev/null | python3 -I -c '
import re, sys, time
s = re.sub(r"\x1b\[[0-9;]*[A-Za-z]|§.", "", sys.stdin.read())
s = re.sub(r"(\d),(\d)", r"\1.\2", s)
p = [re.search(r"P%s: (\d+(?:\.\d+)?)" % q, s) for q in ("50", "95", "99")]
if all(p):
    print("%d %s %s %s" % (time.time(), *(m.group(1) for m in p)), flush=True)' >> "$1" || true
    sleep "$SAMPLE_S"
  done
}

# sample_restic_rss FILE: the largest RSS (KiB) of all restic processes, sampled every 0.5 s.
sample_restic_rss() {
  local peak=0 r
  while :; do
    # ps exits 1 while no restic runs; under pipefail and set -e that would end the sampler
    r=$(ps -C restic -o rss= 2> /dev/null | sort -n | tail -1 | tr -d ' ') || true
    if [[ -n $r ]] && ((r > peak)); then
      peak=$r
      echo "$peak" > "$1"
    fi
    sleep 0.5
  done
}

cg_cpu_usec() { awk '$1 == "usage_usec" { print $2 }' "$1/cpu.stat"; }

# disk_io_ms DEV: completed I/Os and the ms spent on them (/proc/diskstats) of the disk that holds
# INSTANCES_DIR, for the average latency of a run.
DISK=$(lsblk -no PKNAME "$(findmnt -no SOURCE --target "$INSTANCES_DIR")" 2> /dev/null | head -1)
[[ -n $DISK ]] || DISK=$(basename "$(findmnt -no SOURCE --target "$INSTANCES_DIR")")
disk_io_ms() { awk -v d="$DISK" '$3 == d { print $4 + $8, $7 + $11 }' /proc/diskstats; }

host_cpu() { awk '/^cpu / { print $2 + $3 + $4 + $7 + $8, $2 + $3 + $4 + $5 + $6 + $7 + $8 }' /proc/stat; }

FILES_CG=/sys/fs/cgroup/system.slice/$FILES_UNIT
MC_CG=/sys/fs/cgroup/system.slice/docker-$(docker inspect -f '{{.Id}}' "$CONTAINER").scope
[[ -d $MC_CG ]] || die "no cgroup $MC_CG (Docker with the systemd cgroup driver expected)"
REPO=$INSTANCES_DIR/backups/repos/$ID

baseline() { # FILE
  log "baseline: ${BASELINE_S}s of tick samples without a backup"
  sample_ticks "$1" &
  local pid=$!
  sleep "$BASELINE_S"
  kill "$pid" 2> /dev/null || true
  wait "$pid" 2> /dev/null || true
}

# run_backup BUDGET KIND RUN: one backup the way the panel takes it (save-off, save-all flush,
# backup, save-on), with all measurements; appends a line to results.csv.
run_backup() {
  local budget=$1 kind=$2 run=$3 ticks="$OUT/ticks-b$1-$2-$3.txt" rss="$OUT/.rss" tp rp
  set_budget "$budget"
  if [[ $kind == initial ]]; then
    files_api POST /v1/backups/purge "{\"id\":$ID}" > /dev/null
  fi
  local repo_before cpu0 mc0 host0 disk0 t0 t1 t2 job
  repo_before=$(du -sb "$REPO" 2> /dev/null | cut -f1 || echo 0)
  echo 0 > "$rss"
  sample_ticks "$ticks" &
  tp=$!
  sample_restic_rss "$rss" &
  rp=$!
  cpu0=$(cg_cpu_usec "$FILES_CG")
  mc0=$(cg_cpu_usec "$MC_CG")
  host0=$(host_cpu)
  disk0=$(disk_io_ms)
  t0=$(date +%s.%N)
  rcon save-off > /dev/null
  rcon save-all flush > /dev/null
  t1=$(date +%s.%N)
  job=$(files_api POST /v1/backups/create "{\"id\":$ID,\"kind\":\"manual\",\"note\":\"bench\"}")
  while [[ $(field state <<< "$job") == running ]]; do
    sleep 1
    job=$(current_job)
  done
  rcon save-on > /dev/null
  t2=$(date +%s.%N)
  kill "$tp" "$rp" 2> /dev/null || true
  wait "$tp" "$rp" 2> /dev/null || true
  [[ $(field state <<< "$job") == "done" ]] || die "backup failed: $job"
  python3 -I - "$OUT/results.csv" "$budget" "$kind" "$run" "$t0" "$t1" "$t2" "$job" \
    "$cpu0" "$(cg_cpu_usec "$FILES_CG")" "$mc0" "$(cg_cpu_usec "$MC_CG")" \
    "$host0" "$(host_cpu)" "$disk0" "$(disk_io_ms)" \
    "$(cat "$FILES_CG/memory.peak")" "$(cat "$rss")" "$repo_before" "$(du -sb "$REPO" | cut -f1)" "$ticks" << 'EOF'
import json, os, sys
(out, budget, kind, run, t0, t1, t2, job, cpu0, cpu1, mc0, mc1, host0, host1, disk0, disk1,
 peak, rss, repo0, repo1, ticks) = sys.argv[1:]
job = json.loads(job)
t0, t1, t2 = float(t0), float(t1), float(t2)
h0, h1 = [list(map(float, x.split())) for x in (host0, host1)]
d0, d1 = [list(map(float, x.split())) for x in (disk0, disk1)]
p95, p99 = [], []
for line in open(ticks):
    _, _, a, b = line.split()
    p95.append(float(a)); p99.append(float(b))
q = lambda v, f: max(v) if f == 1 else sorted(v)[int(f * (len(v) - 1))] if v else float("nan")
row = {
    "budget": budget, "kind": kind, "run": run,
    "backup_s": round((job["finished_at"] - job["started_at"]) / 1000, 1),
    "saveoff_s": round(t2 - t0, 1),
    "flush_s": round(t1 - t0, 1),
    "bytes": job["bytes"], "entries": job["entries"],
    "files_cpu_s": round((int(cpu1) - int(cpu0)) / 1e6, 1),
    "mc_cpu_s": round((int(mc1) - int(mc0)) / 1e6, 1),
    "host_cpu_pct": round(100 * (h1[0] - h0[0]) / max(h1[1] - h0[1], 1), 1),
    "disk_await_ms": round((d1[1] - d0[1]) / max(d1[0] - d0[0], 1), 2),
    "files_mem_peak_mib": round(int(peak) / 2**20, 1),
    "restic_rss_peak_mib": round(int(rss) / 1024, 1),
    "repo_growth_mib": round((int(repo1) - int(repo0)) / 2**20, 1),
    "windows": len(p95),
    "tick_p95_median_ms": q(p95, 0.5), "tick_p95_max_ms": q(p95, 1),
    "tick_p99_median_ms": q(p99, 0.5), "tick_p99_max_ms": q(p99, 1),
}
new = not os.path.exists(out)
with open(out, "a") as f:
    if new:
        f.write(",".join(row) + "\n")
    f.write(",".join(str(v) for v in row.values()) + "\n")
print(row)
EOF
}

log "server $ID, budgets ${BUDGETS[*]}, $RUNS runs, results in $OUT ($(nproc) CPUs, disk $DISK)"
for run in $(seq "$RUNS"); do
  baseline "$OUT/ticks-baseline-$run.txt"
  for kind in initial incremental; do
    for b in "${BUDGETS[@]}"; do
      log "run $run: $kind backup with restic_gomaxprocs = $b"
      run_backup "$b" "$kind" "$run"
    done
  done
done

python3 -I - "$OUT" << 'EOF'
import csv, glob, statistics, sys
out = sys.argv[1]
base = [float(l.split()[2]) for f in glob.glob(out + "/ticks-baseline-*.txt") for l in open(f)]
base99 = [float(l.split()[3]) for f in glob.glob(out + "/ticks-baseline-*.txt") for l in open(f)]
if base:
    print("baseline (no backup): tick P95 median %.1f ms max %.1f ms, P99 median %.1f ms max %.1f ms"
          % (statistics.median(base), max(base), statistics.median(base99), max(base99)))
rows = list(csv.DictReader(open(out + "/results.csv")))
cols = ["backup_s", "saveoff_s", "files_cpu_s", "host_cpu_pct", "disk_await_ms", "restic_rss_peak_mib",
        "files_mem_peak_mib", "repo_growth_mib", "tick_p95_median_ms", "tick_p95_max_ms", "tick_p99_max_ms"]
print("median over runs:")
print("%-12s %-7s " % ("kind", "budget") + " ".join("%12s" % c[:12] for c in cols))
for kind in ("initial", "incremental"):
    for b in dict.fromkeys(r["budget"] for r in rows):
        sel = [r for r in rows if r["kind"] == kind and r["budget"] == b]
        if sel:
            print("%-12s %-7s " % (kind, b) + " ".join("%12.1f" % statistics.median(float(r[c]) for r in sel) for c in cols))
EOF
