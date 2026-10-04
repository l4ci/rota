#!/usr/bin/env bash
# The full merge gate, run concurrently: validate-skills, go vet, go test -race
# and the smoke suite split into N shards, all at once (#82). One serial gate
# is ~589 s; this is ~160 s on 8 cores (docs/design/round-speed.md).
#
# Usage: bash test/gate.sh [--smoke-only] [--random] [--shards N]
#   --smoke-only  run only the sharded smoke suite (the CI shard guard)
#   --random      deal the sections to shards at random, so a section
#                 that quietly needs another's state fails (shard-safety guard)
#   --shards N    shard count; default ROTA_SMOKE_SHARDS, else config
#                 gate.smokeShards, else 4
# Env: ROTA_GATE_LOGS=<dir> keeps the per-check logs there (else a tmp dir that
#      is kept only when a check fails); ROTA_GATE_LOCK=<dir> moves the lock.
#
# Only one gate runs per machine: a second one waits for the first. Two gates at
# once starve the Go tests of CPU and produce time-budget false reds.
set -uo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
SMOKE_ONLY=0 RANDOM_PART=0 SHARDS="${ROTA_SMOKE_SHARDS:-}"
while [ $# -gt 0 ]; do
  case "$1" in
    --smoke-only) SMOKE_ONLY=1 ;;
    --random) RANDOM_PART=1 ;;
    --shards) SHARDS="${2:?--shards needs a number}"; shift ;;
    *) echo "gate: unknown argument: $1" >&2; exit 2 ;;
  esac
  shift
done

if [ -z "$SHARDS" ]; then
  SHARDS="$(python3 - "$REPO" <<'PY' 2>/dev/null
import json, sys
try:
    v = json.load(open(sys.argv[1] + "/.rota/config.json"))["gate"]["smokeShards"]
    print(v if isinstance(v, int) and v >= 1 else 4)
except Exception:
    print(4)
PY
)"
  SHARDS="${SHARDS:-4}"
fi
case "$SHARDS" in ''|*[!0-9]*|0) echo "gate: shard count must be an integer >= 1, got: $SHARDS" >&2; exit 2 ;; esac

# Machine-wide lock: mkdir is atomic everywhere (macOS has no flock). The owner
# pid is recorded so a lock left by a killed gate is taken over.
LOCK="${ROTA_GATE_LOCK:-/tmp/rota-gate.lock}"
waited=0
until mkdir "$LOCK" 2>/dev/null; do
  owner="$(cat "$LOCK/pid" 2>/dev/null || true)"
  if [ -n "$owner" ] && ! kill -0 "$owner" 2>/dev/null; then
    rm -rf "$LOCK"; continue
  fi
  [ "$waited" -gt 0 ] || echo "gate: another gate holds $LOCK (pid ${owner:-?}); waiting" >&2
  waited=1; sleep 2
done
echo $$ > "$LOCK/pid"

LOGS="${ROTA_GATE_LOGS:-$(mktemp -d "${TMPDIR:-/tmp}/rota-gate-logs-XXXXXX")}"
mkdir -p "$LOGS"
PIDS=()
cleanup() { rm -rf "$LOCK"; }
trap 'cleanup' EXIT
# On INT/TERM stop the checks too, with their children (the shard runners and go).
stop_checks() { for p in "${PIDS[@]}"; do pkill -P "$p" 2>/dev/null; kill "$p" 2>/dev/null; done; }
trap 'stop_checks; cleanup; exit 130' INT TERM

NAMES=()
launch() { # launch <name> <cmd...>: run in the background, log to $LOGS/<name>.log
  local name="$1"; shift
  ( cd "$REPO" && "$@" ) >"$LOGS/$name.log" 2>&1 &
  PIDS+=($!); NAMES+=("$name")
}

# Deal the sections to shards greedily, heaviest first, by the measured
# seconds in test/shard-weights.txt (unlisted sections count 1). --random
# ignores the weights and deals a shuffle round-robin. Either way a shard runs
# its sections in serial order: only which shard owns a section changes.
partition() {
  python3 - "$REPO" "$SHARDS" "$RANDOM_PART" "$LOGS" <<'PY'
import os, random, sys
repo, n, rnd, logs = sys.argv[1], int(sys.argv[2]), sys.argv[3] == "1", sys.argv[4]
secs = sorted(os.path.join("test/sections", f) for f in os.listdir(os.path.join(repo, "test/sections")) if f.endswith(".sh"))
w = {}
try:
    for line in open(os.path.join(repo, "test/shard-weights.txt")):
        p = line.split()
        if len(p) == 2 and not line.startswith("#"):
            w[p[0]] = float(p[1])
except OSError:
    pass
n = max(1, min(n, len(secs)))
shards = [[] for _ in range(n)]
if rnd:
    random.shuffle(secs)
    for i, s in enumerate(secs):
        shards[i % n].append(s)
    shards = [sorted(sh) for sh in shards]  # shards keep serial order inside; only the split is random
else:
    load = [0.0] * n
    for s in sorted(secs, key=lambda s: -w.get(os.path.basename(s), 1.0)):
        i = load.index(min(load))
        shards[i].append(s); load[i] += w.get(os.path.basename(s), 1.0)
    shards = [sorted(sh) for sh in shards]
for i, sh in enumerate(shards):
    open(os.path.join(logs, "shard%d.list" % (i + 1)), "w").write("\n".join(sh) + "\n")
print(n)
PY
}

START=$SECONDS
if [ "$SMOKE_ONLY" = 0 ]; then
  launch validate python3 test/validate-skills.py
  launch vet go vet ./...
  launch gotest go test -race -timeout 30m ./...
fi
N="$(partition)" || { echo "gate: partition failed" >&2; exit 2; }
for i in $(seq 1 "$N"); do
  launch "smoke-shard$i" env SECTION_LIST="$(cat "$LOGS/shard$i.list")" bash test/runner.sh
done
echo "gate: ${#NAMES[@]} checks running ($N smoke shards); logs in $LOGS"

fail=0
for idx in "${!PIDS[@]}"; do
  name="${NAMES[$idx]}"
  if wait "${PIDS[$idx]}"; then
    case "$name" in smoke-*) grep -q 'All smoke tests passed.' "$LOGS/$name.log" || { echo "FAIL $name (no pass line)"; fail=1; continue; } ;; esac
    echo "ok   $name"
  else
    echo "FAIL $name"; tail -n 25 "$LOGS/$name.log" | sed 's/^/     /'; fail=1
  fi
done
echo "gate: $((SECONDS - START)) s"
if [ "$fail" = 0 ]; then
  [ -n "${ROTA_GATE_LOGS:-}" ] || rm -rf "$LOGS"
  echo "All smoke tests passed."
  exit 0
fi
echo "gate: FAILED; full logs kept in $LOGS" >&2
exit 1
