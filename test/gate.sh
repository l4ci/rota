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
# Env: ROTA_GATE_LOGS=<dir> keeps the per-check logs there (else a dir under the
#      project temp root, <main checkout>/.rota/tmp, or the system temp dir
#      outside a rota project, kept only when a check fails); ROTA_GATE_LOCK=<path>
#      moves the lock. TMPDIR and the isolated HOME stay in the system temp dir:
#      under the checkout, a fixture walking up would find the project's .rota/.
#
# Only one gate runs per machine: a second one waits for the first. Two gates at
# once starve the Go tests of CPU and produce time-budget false reds. The gate
# also takes a slot of the repo's test lock (test.slots), the one `rota test run`
# and `rota proof record` queue on, so a worker proof does not run beside it.
set -uo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
. "$REPO/test/lib/project-tmp.sh"
PROJECT_TMP="$(project_tmp "$REPO")" || PROJECT_TMP="${TMPDIR:-/tmp}"
# Run under the test lock: re-enter through `rota test lock`, which marks the
# child (ROTA_TEST_LOCK_HELD) so this branch runs once.
if [ -z "${ROTA_TEST_LOCK_HELD:-}" ]; then
  LOCK_BIN="${ROTA_BIN:-}"
  if [ -z "$LOCK_BIN" ]; then
    LOCK_DIR="$(mktemp -d "$PROJECT_TMP/rota-gate-bin-XXXXXX")" || exit 1
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

LOGS="${ROTA_GATE_LOGS:-$(mktemp -d "$PROJECT_TMP/rota-gate-logs-XXXXXX")}"
mkdir -p "$LOGS"
# Smoke differences retain snapshots beside the logs, outside GATE_TMP (which
# cleanup always removes). Resolve relative log paths before runners change cwd.
SMOKE_DIAGNOSTICS="$(cd "$LOGS" && pwd -P)"
# Every check makes its temp files under one gate-owned root, so a leak is
# countable: the root must be empty once the checks have exited (#85). It is
# fresh and in the system temp dir, never the project's. It sits in one private
# (mktemp, 0700) parent with the isolated HOME and the worktree snapshot, so no
# gate path is a guessable name in the shared temp dir; the parent is rm -rf'd.
GATE_SCRATCH="$(mktemp -d "${TMPDIR:-/tmp}/rota-gate-tmp.XXXXXX")" || exit 1
GATE_TMP="$GATE_SCRATCH/tmp"
mkdir "$GATE_TMP" || exit 1
export TMPDIR="$GATE_TMP"
# Compiled test binaries and the runners' rota builds go under the project temp
# root (#2499), in a private dir this run removes. GOTMPDIR keeps go test's
# binaries out of TMPDIR; t.TempDir() fixtures still use TMPDIR above.
ROTA_GATE_BIN="$(mktemp -d "$PROJECT_TMP/rota-gate-bin-XXXXXX")" || exit 1
mkdir "$ROTA_GATE_BIN/gotmp" || exit 1
export ROTA_GATE_BIN GOTMPDIR="$ROTA_GATE_BIN/gotmp"
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
ISO_DIR="$GATE_SCRATCH/iso"
isolate_env "$ISO_DIR"
WORKTREES_BEFORE="$GATE_SCRATCH/worktrees"
worktree_snapshot "$REPO" > "$WORKTREES_BEFORE"
PIDS=()
# OSC 7501 status (#2642): working/progress while the checks run, then done or
# error, so the tab shows the gate's state. Only when stdout and stderr are both
# terminals; terminals that don't know the sequence ignore it.
GATE_OK=0 STATUS_DONE=0
osc7501() { [ -t 1 ] && [ -t 2 ] && printf '\033]7501;state=%s:app=rota%s\033\\' "$1" "${2:+:progress=$2}" >&2; return 0; }
cleanup() {
  if [ "$STATUS_DONE" = 0 ]; then STATUS_DONE=1; if [ "$GATE_OK" = 1 ]; then osc7501 done; else osc7501 error; fi; fi
  gate_lock_release "$LOCK"; rm -rf "$GATE_SCRATCH" "$ROTA_GATE_BIN"
}
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

# Deal the sections to shards (test/gate-partition.py): weighted, or shuffled under --random.
partition() { python3 "$REPO/test/gate-partition.py" "$REPO" "$SHARDS" "$RANDOM_PART" "$LOGS"; }

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

# The one retry policy, shared by the Go and smoke reruns (a change to the
# unchanged-tree rule or the recovery report lands here once):
#   retry_once <check> <first-log> <retry-log> <require-line> <rerun-cmd...>
# Reruns the command from the repo root, timed into <retry-log>.time. Recovery needs
# exit 0, an unchanged tree and, when <require-line> is not empty, that line in the
# retry log. Returns 0 recovered (FLAKY recorded), 1 stays red (message printed),
# 2 tree changed, so nothing was rerun.
retry_once() {
  local check="$1" first_log="$2" retry_log="$3" require="$4" retry_rc=0
  shift 4
  retry_tree_unchanged || return 2
  { time { (cd "$REPO" && "$@") >"$retry_log" 2>&1; }; } 2>"$retry_log.time" || retry_rc=$?
  if [ "$retry_rc" = 0 ] && retry_tree_unchanged &&
     { [ -z "$require" ] || grep -q "$require" "$retry_log"; }; then
    if python3 "$REPO/test/gate-flake.py" "$check" "$first_log" "$retry_log" "$retry_log.time"; then
      echo "FLAKY $check (failed once, passed isolated retry)"
      return 0
    fi
    echo "FAIL $check (could not report recovery)"
    return 1
  fi
  echo "FAIL $check isolated retry (exit $retry_rc)"
  tail -n 25 "$retry_log" | sed 's/^/     /'
  return 1
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
osc7501 working 0
echo "gate: ${#NAMES[@]} checks running ($N smoke shards); logs in $LOGS"

# Reap every initial check before retrying anything: a retry runs alone.
RCS=()
for idx in "${!PIDS[@]}"; do
  rc=0; wait "${PIDS[$idx]}" || rc=$?
  RCS+=("$rc")
  osc7501 working $(( (idx + 1) * 90 / ${#PIDS[@]} ))  # the retries below get the last tenth
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
      first_log="$LOGS/go-first$pkg_idx.log"
      if ! python3 "$REPO/test/gate-go.py" output "$LOGS/gotest.json" "$pkg" >"$first_log"; then
        echo "FAIL go:${pkg#"$module"/} (could not report recovery)"
        go_failed=1
        continue
      fi
      retry_once "go:${pkg#"$module"/}" "$first_log" "$LOGS/go-retry$pkg_idx.log" "" \
        go test -race -timeout 30m -count=1 "$pkg"
      retry_rc=$?
      [ "$retry_rc" = 0 ] || go_failed=1
      [ "$retry_rc" != 2 ] || break  # tree changed: stop retrying
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
      shard="${name#smoke-shard}"
      if retry_once "$name" "$LOGS/$name.log" "$LOGS/$name.retry.log" 'All smoke tests passed\.' \
         env ROTA_SMOKE_DIAGNOSTICS="$SMOKE_DIAGNOSTICS" SECTION_LIST="$(cat "$LOGS/shard$shard.list")" bash test/runner.sh; then
        continue
      fi
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
  GATE_OK=1
  [ -n "${ROTA_GATE_LOGS:-}" ] || rm -rf "$LOGS"
  echo "All smoke tests passed."
  exit 0
fi
echo "gate: FAILED; full logs kept in $LOGS" >&2
exit 1
