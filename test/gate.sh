#!/usr/bin/env bash
# The full merge gate, run concurrently: validate-skills, the doc lints
# (test/doclint.sh), the grep gate (test/grep-gate.sh), gofmt, go vet, go test -race and the smoke suite split into N shards, all at once (#82). One serial gate
# is several times slower; this takes about 2-3 minutes.
# Failed Go packages and smoke shards retry once after all initial checks exit.
#
# Usage: bash test/gate.sh [--smoke-only] [--random] [--shards N]
#   --smoke-only  run only the sharded smoke suite (the CI shard guard); skips
#                 the doc lints too: they run in the Skill Format workflow
#   --random      deal the sections to shards at random, so a section
#                 that quietly needs another's state fails (shard-safety guard)
#   --shards N    shard count; default ROTA_SMOKE_SHARDS, else one per
#                 two cores (nproc/2, 1 to 4)
# Env: ROTA_GATE_LOGS=<dir> keeps the per-check logs there (else a tmp dir that
#      is kept only when a check fails); ROTA_GATE_LOCK=<path> moves the lock.
#
# Only one gate runs per machine: a second one waits for the first. Two gates at
# once starve the Go tests of CPU and produce time-budget false reds. The gate
# also takes a slot of the repo's test lock (test.slots), the one `rota test run`
# and `rota proof record` queue on, so a worker proof does not run beside it.
set -uo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
# Run under the test lock: re-enter through `rota test lock`, which marks the
# child (ROTA_TEST_LOCK_HELD) so this branch runs once.
if [ -z "${ROTA_TEST_LOCK_HELD:-}" ]; then
  LOCK_BIN="${ROTA_BIN:-}"
  if [ -z "$LOCK_BIN" ]; then
    LOCK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/rota-gate-bin-XXXXXX")" || exit 1
    (cd "$REPO" && go build -trimpath -o "$LOCK_DIR/rota" ./cmd/rota) || { rm -rf "$LOCK_DIR"; echo "gate: go build ./cmd/rota failed" >&2; exit 2; }
    LOCK_BIN="$LOCK_DIR/rota"
  fi
  rc=0
  (cd "$REPO" && "$LOCK_BIN" test lock -- bash "$REPO/test/gate.sh" "$@") || rc=$?
  [ -z "${LOCK_DIR:-}" ] || rm -rf "$LOCK_DIR"
  exit "$rc"
fi
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
  # Nothing set it: one shard per two cores, at least one, at most four, so a
  # gate takes about half the machine (the same sum as worker.DefaultSmokeShards).
  CORES="$(nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 2)"
  SHARDS=$((CORES / 2)); [ "$SHARDS" -ge 1 ] || SHARDS=1; [ "$SHARDS" -le 4 ] || SHARDS=4
fi
case "$SHARDS" in ''|*[!0-9]*|0) echo "gate: shard count must be an integer >= 1, got: $SHARDS" >&2; exit 2 ;; esac

# Machine-wide lock (test/lib/gate-lock.sh): a stale lock from a killed gate is taken over.
. "$REPO/test/lib/gate-lock.sh"
LOCK="${ROTA_GATE_LOCK:-/tmp/rota-gate.lock}"
gate_lock_acquire "$LOCK"

LOGS="${ROTA_GATE_LOGS:-$(mktemp -d "${TMPDIR:-/tmp}/rota-gate-logs-XXXXXX")}"
mkdir -p "$LOGS"
# Smoke differences retain snapshots beside the logs, outside GATE_TMP (which
# cleanup always removes). Resolve relative log paths before runners change cwd.
SMOKE_DIAGNOSTICS="$(cd "$LOGS" && pwd -P)"
# Every check makes its temp files under one gate-owned root, so a leak is
# countable: the root must be empty once the checks have exited (#85).
GATE_TMP="$(mktemp -d "$LOGS/tmp.XXXXXX")" || exit 1  # fresh: LOGS may be shared, and GATE_TMP is rm -rf'd
export TMPDIR="$GATE_TMP"
# The gate runs in a fresh scratch worktree each time. Without -trimpath the Go
# build cache keys every compile on the source directory, so each path adds a
# full copy (~210 MB measured, 85 GB over a round, #709); with it, entries are
# reused across paths. Covers go vet, go test and the smoke runner's go build.
case " ${GOFLAGS:-} " in *" -trimpath "*) ;; *) export GOFLAGS="${GOFLAGS:+$GOFLAGS }-trimpath" ;; esac
# Every check runs scrubbed (#320): no live herdr/tmux identity, no ssh-agent, and
# HOME/XDG pinned to a gate-owned dir, since go test, grep-gate and doclint run
# rota verbs outside the smoke runner's own scrub. The worktree list of this repo
# must come out unchanged, whatever a check does.
. "$REPO/test/lib/isolate.sh"
ISO_DIR="$LOGS/iso"
isolate_env "$ISO_DIR"
WORKTREES_BEFORE="$GATE_TMP.worktrees"
worktree_snapshot "$REPO" > "$WORKTREES_BEFORE"
PIDS=()
cleanup() { gate_lock_release "$LOCK"; rm -rf "$GATE_TMP" "$ISO_DIR" "$WORKTREES_BEFORE"; }
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

# A recovery is valid only at the original clean checkout, matching the
# verifier's same-tree policy. Never excuse a check that repaired its inputs.
RETRY_HEAD="$(git -C "$REPO" rev-parse HEAD 2>/dev/null)"
RETRY_CLEAN=0
[ -n "$RETRY_HEAD" ] && [ -z "$(git -C "$REPO" status --porcelain)" ] && RETRY_CLEAN=1
retry_tree_unchanged() {
  [ "$RETRY_CLEAN" = 1 ] &&
    [ "$(git -C "$REPO" rev-parse HEAD 2>/dev/null)" = "$RETRY_HEAD" ] &&
    [ -z "$(git -C "$REPO" status --porcelain)" ]
}

# Keep the JSON side channel for package selection, while ordinary failure
# logs retain go test's human assertion lines.
run_go_tests() {
  local rc=0
  go test -json -race -timeout 30m -p "$SHARDS" ./... >"$LOGS/gotest.json" 2>&1 || rc=$?
  python3 "$REPO/test/gate-go.py" output "$LOGS/gotest.json" || return 2
  return "$rc"
}

go_retry_budget() {
  local count
  count="$(wc -l <"$LOGS/go-failed.list")"
  if [ "$count" -gt 3 ]; then
    echo "gate: skipping Go retries ($count failed packages; limit 3)"
    return 1
  fi
  return 0
}

TIMEFORMAT='%R'
START=$SECONDS
if [ "$SMOKE_ONLY" = 0 ]; then
  launch validate python3 test/validate-skills.py
  launch doclint bash test/doclint.sh
  launch grepgate bash test/grep-gate.sh
  launch gofmt bash test/gofmt.sh
  launch vet go vet ./...
  launch gotest run_go_tests
fi
N="$(partition)" || { echo "gate: partition failed" >&2; exit 2; }
for i in $(seq 1 "$N"); do
  launch "smoke-shard$i" env ROTA_SMOKE_DIAGNOSTICS="$SMOKE_DIAGNOSTICS" SECTION_LIST="$(cat "$LOGS/shard$i.list")" bash test/runner.sh
done
echo "gate: ${#NAMES[@]} checks running ($N smoke shards); logs in $LOGS"

# Reap every initial check before retrying anything: a retry runs alone.
RCS=()
for idx in "${!PIDS[@]}"; do
  rc=0; wait "${PIDS[$idx]}" || rc=$?
  RCS+=("$rc")
done
fail=0
for idx in "${!PIDS[@]}"; do
  name="${NAMES[$idx]}"
  rc="${RCS[$idx]}"
  if [ "$name" = gotest ] && [ "$rc" = 1 ] && retry_tree_unchanged &&
     python3 "$REPO/test/gate-go.py" failed "$LOGS/gotest.json" >"$LOGS/go-failed.list" && go_retry_budget; then
    go_failed=0
    pkg_idx=0
    module="$(awk '$1 == "module" {print $2}' "$REPO/go.mod")"
    while IFS= read -r pkg; do
      pkg_idx=$((pkg_idx + 1))
      retry_log="$LOGS/go-retry$pkg_idx.log"
      if ! retry_tree_unchanged; then
        go_failed=1
        break
      fi
      retry_rc=0
      { time { (cd "$REPO" && go test -race -timeout 30m -count=1 "$pkg") >"$retry_log" 2>&1; }; } \
        2>"$retry_log.time" || retry_rc=$?
      check="go:${pkg#"$module"/}"
      if [ "$retry_rc" = 0 ] && retry_tree_unchanged; then
        first_log="$LOGS/go-first$pkg_idx.log"
        if python3 "$REPO/test/gate-go.py" output "$LOGS/gotest.json" "$pkg" >"$first_log" &&
           python3 "$REPO/test/gate-flake.py" "$check" "$first_log" "$retry_log" "$retry_log.time"; then
          echo "FLAKY $check (failed once, passed isolated retry)"
        else
          echo "FAIL $check (could not report recovery)"
          go_failed=1
        fi
      else
        echo "FAIL $check isolated retry (exit $retry_rc)"
        tail -n 25 "$retry_log" | sed 's/^/     /'
        go_failed=1
      fi
    done <"$LOGS/go-failed.list"
    [ "$go_failed" = 0 ] && continue
  fi
  case "$name" in smoke-*)
    if [ "$rc" = 0 ] && ! grep -q 'All smoke tests passed.' "$LOGS/$name.log"; then
      echo "FAIL $name (no pass line)"
      fail=1
      continue
    fi
    if [ "$SMOKE_ONLY" = 0 ] && [ "$rc" = 1 ] && retry_tree_unchanged &&
       python3 "$REPO/test/gate-smoke.py" "$LOGS/$name.log"; then
      retry_rc=0
      shard="${name#smoke-shard}"
      { time { (cd "$REPO" && env ROTA_SMOKE_DIAGNOSTICS="$SMOKE_DIAGNOSTICS" \
        SECTION_LIST="$(cat "$LOGS/shard$shard.list")" bash test/runner.sh) \
        >"$LOGS/$name.retry.log" 2>&1; }; } 2>"$LOGS/$name.retry.log.time" || retry_rc=$?
      if [ "$retry_rc" = 0 ] && retry_tree_unchanged && grep -q 'All smoke tests passed.' "$LOGS/$name.retry.log"; then
        if python3 "$REPO/test/gate-flake.py" "$name" "$LOGS/$name.log" "$LOGS/$name.retry.log" "$LOGS/$name.retry.log.time"; then
          echo "FLAKY $name (failed once, passed isolated retry)"
        else
          echo "FAIL $name (could not report recovery)"
          fail=1
        fi
        continue
      fi
      echo "FAIL $name isolated retry (exit $retry_rc)"
      tail -n 25 "$LOGS/$name.retry.log" | sed 's/^/     /'
    fi
    ;;
  esac
  if [ "$rc" = 0 ]; then
    echo "ok   $name"
  else
    echo "FAIL $name (exit $rc)"; tail -n 25 "$LOGS/$name.log" | sed 's/^/     /'
    # A shard that dies without a summary line (set -e, SIGPIPE 141, a kill) leaves only the
    # last section header behind; name it so the death is not a bare FAILED (#1242).
    case "$name" in smoke-*)
      grep -qE 'All smoke tests passed\.|^Smoke assertions failed\.$' "$LOGS/$name.log" || \
        echo "     $name died without a summary line: exit $rc, last output: $(tail -n 1 "$LOGS/$name.log")" ;; esac
    fail=1
  fi
done
echo "gate: $((SECONDS - START)) s"
# No-leak check (#85): a check that exited cleanly removed what it created.
if [ -n "$(ls -A "$GATE_TMP" 2>/dev/null)" ]; then
  echo "FAIL leak: checks left temp entries under $GATE_TMP:"; ls -A "$GATE_TMP" | head -20 | sed 's/^/     /'
  fail=1
fi
worktree_guard_check "$WORKTREES_BEFORE" "$REPO" || { echo "FAIL worktree guard"; fail=1; }
if [ "$fail" = 0 ]; then
  [ -n "${ROTA_GATE_LOGS:-}" ] || rm -rf "$LOGS"
  echo "All smoke tests passed."
  exit 0
fi
echo "gate: FAILED; full logs kept in $LOGS" >&2
exit 1
