echo "D1: statusline dump, Stop hook, SessionStart handoff, hook install (#65)"

# Everything lives under $TMP: a git project with .rota/, a fixture Claude config
# dir (CLAUDE_CONFIG_DIR, so no real ~/.claude is read or written) and a fake
# orchestrator process that holds the round lease. ROTA_TEST_HOLDER_PID stands in
# for the hook's nearest non-shell ancestor, which this script cannot arrange.
OH="$(mktemp -d "$TMP/orch-hooks.XXXXXX")"
P="$OH/proj"; CC="$OH/claude"; mkdir -p "$P/.rota" "$CC"
(
  cd "$P" && git init -q -b main . && git -c user.email=a@b -c user.name=n commit -q --allow-empty -m init \
    && printf '{"git":{"baseBranch":"main"}}\n' > .rota/config.json
) || fail "D1 fixture setup failed"
NOW="2026-10-03T12:00:00Z"
NOW_EPOCH="$(python3 -c 'import calendar,time; print(calendar.timegm(time.strptime("'$NOW'","%Y-%m-%dT%H:%M:%SZ")))')"
CD="$(git -C "$P" rev-parse --path-format=absolute --git-common-dir)"
SESS="$CD/rota/session/s1.json"
HANDOFF="$P/.rota/handoff/main.md"

sleep 300 >/dev/null 2>&1 & ORCH=$!
sleep 300 >/dev/null 2>&1 & WORKER=$!
trap 'kill "$ORCH" "$WORKER" 2>/dev/null; rm -rf "$TMP"' EXIT # a mid-section fail() must not leave the sleepers behind
mklease() { # mklease <pid>: a live lease held by that process
  mkdir -p "$CD/rota"
  python3 - "$CD/rota/round-lease.json" "$1" "$P" <<'PY' || fail "could not write the lease"
import json, socket, sys
pid = int(sys.argv[2])
try:
    start = int(open("/proc/%d/stat" % pid).read().rsplit(")", 1)[1].split()[19])
except OSError:
    start = 0
l = {"pid": pid, "host": socket.gethostname(), "root": sys.argv[3], "round": 1, "startedAt": "2026-10-03T00:00:00Z"}
if start:
    l["start"] = start
json.dump(l, open(sys.argv[1], "w"))
PY
}
# oh <holder-pid> <args>...: rota with the fixed clock, the fixture config dir and a stand-in ancestor.
oh() { local h="$1"; shift; env CLAUDE_CONFIG_DIR="$CC" ROTA_TEST_NOW="$NOW" ROTA_TEST_HOLDER_PID="$h" "$ROTA_BIN" "$@"; }
dump_pct() { # dump_pct <pct> [<now>]: one statusline refresh at that context percentage
  printf '{"session_id":"s1","cwd":"%s","context_window":{"used_percentage":%s}}' "$P" "$1" \
    | env ROTA_TEST_NOW="${2:-$NOW}" "$ROTA_BIN" statusline dump || fail "dump exited non-zero"
}
stop_in() { printf '{"session_id":"s1","cwd":"%s","stop_hook_active":%s}' "$P" "$1"; }
sj() { python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))[sys.argv[2]])' "$SESS" "$1"; }

# --- state file ---------------------------------------------------------------
dump_pct 10 "2026-10-03T11:58:00Z"
[ "$(sj updatedAt)" = "2026-10-03T11:58:00Z" ] || fail "D1: updatedAt not recorded"
[ "$(sj contextPct)" = "10" ] || fail "D1: contextPct not recorded"
dump_pct 33 "2026-10-03T11:59:00Z"
[ "$(sj updatedAt)" = "2026-10-03T11:59:00Z" ] && [ "$(sj contextPct)" = "33" ] || fail "D1: second refresh did not update the state"
printf '{"session_id":"s1","cwd":"%s","rate_limits":{"five_hour":{"used_percentage":12,"resets_at":99}},"context_window":{"context_window_size":200000,"current_usage":{"input_tokens":100000,"cache_creation_input_tokens":20000,"cache_read_input_tokens":30000}}}' "$P" \
  | env ROTA_TEST_NOW="$NOW" "$ROTA_BIN" statusline dump
[ "$(sj contextPct)" = "75.0" ] || [ "$(sj contextPct)" = "75" ] || fail "D1: contextPct not derived from current_usage: $(sj contextPct)"
[ "$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["rateLimits"]["five_hour"]["resets_at"])' "$SESS")" = "99" ] || fail "D1: rate_limits not kept verbatim"
pass "D1: the state file follows each refresh and derives the percentage from current_usage"

RC=0; OUT="$(printf '{"session_id":"s1"}' | "$ROTA_BIN" statusline dump --then 'cat; echo " then"; exit 9')" || RC=$?
[ "$RC" -eq 0 ] && [ "$OUT" = '{"session_id":"s1"} then' ] || fail "D1: --then did not pass the input through and exit 0: rc=$RC out=$OUT"
RC=0; OUT="$(printf 'not json' | "$ROTA_BIN" statusline dump 2>&1)" || RC=$?
[ "$RC" -eq 0 ] && [ -z "$OUT" ] || fail "D1: a bad payload must be swallowed silently: rc=$RC out=$OUT"
RC=0; OUT="$(printf 'not json' | ROTA_STATUSLINE_DEBUG=1 "$ROTA_BIN" statusline dump 2>&1)" || RC=$?
[ "$RC" -eq 0 ] && [ -n "$OUT" ] || fail "D1: ROTA_STATUSLINE_DEBUG should print the error: rc=$RC"
RC=0; OUT="$(printf '{}' | "$ROTA_BIN" statusline dump --json 2>/dev/null)" || RC=$?
[ "$RC" -eq 2 ] && [ -z "$OUT" ] || fail "D1: dump must reject --json with exit 2 and no stdout: rc=$RC out=$OUT"
pass "D1: dump passes input to --then, swallows errors, rejects --json"

# --- Stop hook ----------------------------------------------------------------
mklease "$ORCH"
for pct in 74 75 99; do
  dump_pct "$pct"
  RC=0; OUT="$(stop_in false | oh "$ORCH" hook stop)" || RC=$?
  [ "$RC" -eq 0 ] || fail "D1: hook stop exited $RC at $pct"
  if [ "$pct" = 74 ]; then
    [ -z "$OUT" ] || fail "D1: 74% must pass, got: $OUT"
  else
    [ "$(echo "$OUT" | jget decision)" = "block" ] || fail "D1: $pct% must block: $OUT"
    case "$(echo "$OUT" | jget reason)" in *"$HANDOFF"*"/exit"*) ;; *) fail "D1: reason should name the handoff path and /exit: $OUT" ;; esac
  fi
  python3 - "$SESS" <<'PY'
import json, sys
p = sys.argv[1]; s = json.load(open(p))
for k in ("handoffBlocks", "blockedAt", "handoffFailed"): s.pop(k, None)
json.dump(s, open(p, "w"))
PY
done
dump_pct 99
RC=0; OUT="$(stop_in false | oh "$WORKER" hook stop)" || RC=$?
[ "$RC" -eq 0 ] && [ -z "$OUT" ] || fail "D1: a worker (not the lease holder) must pass: rc=$RC out=$OUT"
rm -f "$CD/rota/round-lease.json"
RC=0; OUT="$(stop_in false | oh "$ORCH" hook stop)" || RC=$?
[ "$RC" -eq 0 ] && [ -z "$OUT" ] || fail "D1: no lease must pass at 99%: rc=$RC out=$OUT"
mklease "$ORCH"
dump_pct 99 "2026-10-03T11:50:00Z"
OUT="$(stop_in false | oh "$ORCH" hook stop)"
[ -z "$OUT" ] || fail "D1: a stale reading (10 min old) must not block: $OUT"
printf '{"orchestrator":{"handoffThreshold":500}}\n' > "$P/.rota/config.json"
dump_pct 99
OUT="$(stop_in false | oh "$ORCH" hook stop)"
[ -z "$OUT" ] || fail "D1: an out-of-range threshold must pass, not block: $OUT"
printf '{"git":{"baseBranch":"main"}}\n' > "$P/.rota/config.json"
pass "D1: Stop blocks at 75 and 99, passes at 74, for workers, without a lease, on stale state and on bad config"

# --- loop prevention ----------------------------------------------------------
python3 - "$SESS" <<'PY'
import json, sys
p = sys.argv[1]; s = json.load(open(p))
for k in ("handoffBlocks", "blockedAt", "handoffFailed"): s.pop(k, None)
json.dump(s, open(p, "w"))
PY
dump_pct 90
[ "$(stop_in false | oh "$ORCH" hook stop | jget decision)" = "block" ] || fail "D1: first stop should block"
mkdir -p "$P/.rota/handoff"
printf '<!-- rota-handoff: orchestrator -->\n<!-- written %s -->\n\nbody line\n' "$NOW" > "$HANDOFF"
python3 -c 'import os,sys; os.utime(sys.argv[1], (int(sys.argv[2])+5,)*2)' "$HANDOFF" "$NOW_EPOCH"
OUT="$(stop_in true | oh "$ORCH" hook stop)"
[ -z "$OUT" ] || fail "D1: stop_hook_active with a new handoff must pass: $OUT"
rm -f "$HANDOFF"
for i in 1 2; do
  [ "$(stop_in true | oh "$ORCH" hook stop | jget decision)" = "block" ] || fail "D1: reblock $i without a handoff"
done
OUT="$(stop_in true | oh "$ORCH" hook stop)"
[ -z "$OUT" ] || fail "D1: past handoffMaxBlocks the hook must pass: $OUT"
[ "$(sj handoffFailed)" = "True" ] || fail "D1: handoffFailed should be recorded"
OUT="$(stop_in false | oh "$ORCH" hook stop)"
[ -z "$OUT" ] || fail "D1: a failed session must not be held again: $OUT"
pass "D1: loop prevention passes on a new handoff, caps the re-blocks and records handoffFailed"

# --- SessionStart -------------------------------------------------------------
ss_in() { printf '{"session_id":"s2","cwd":"%s","source":"%s"}' "$P" "$1"; }
printf '<!-- rota-handoff: orchestrator -->\n<!-- written %s -->\n\nround state: ben on #5\n' "$NOW" > "$HANDOFF"
python3 -c 'import os,sys; os.utime(sys.argv[1], (int(sys.argv[2])-30,)*2)' "$HANDOFF" "$NOW_EPOCH"
for src in resume compact; do
  OUT="$(ss_in "$src" | oh "$ORCH" hook session-start)"
  [ -z "$OUT" ] && [ -f "$HANDOFF" ] || fail "D1: $src must keep the handoff and print nothing: $OUT"
done
OUT="$(ss_in startup | oh "$ORCH" hook session-start)"
[ "$(echo "$OUT" | jget hookSpecificOutput.hookEventName)" = "SessionStart" ] || fail "D1: startup should inject: $OUT"
case "$(echo "$OUT" | jget hookSpecificOutput.additionalContext)" in
  "Handoff from the previous orchestrator session, now consumed:"*"round state: ben on #5"*) ;;
  *) fail "D1: injected context is wrong: $OUT" ;;
esac
[ ! -e "$HANDOFF" ] && [ -f "$HANDOFF.consumed" ] || fail "D1: the handoff should be renamed to .consumed"
OUT="$(ss_in startup | oh "$ORCH" hook session-start)"
[ -z "$OUT" ] || fail "D1: a consumed handoff must not inject twice: $OUT"
# No lease at all: a fresh handoff written by the Stop hook still reaches the restarted session.
rm -f "$CD/rota/round-lease.json" "$HANDOFF.consumed"
printf '# a manual pause\n' > "$HANDOFF"
python3 -c 'import os,sys; os.utime(sys.argv[1], (int(sys.argv[2])-30,)*2)' "$HANDOFF" "$NOW_EPOCH"
OUT="$(ss_in startup | oh "$ORCH" hook session-start)"
[ -z "$OUT" ] && [ -f "$HANDOFF" ] || fail "D1: an unmarked handoff must not inject without a lease: $OUT"
printf '<!-- rota-handoff: orchestrator -->\nlate\n' > "$HANDOFF"
python3 -c 'import os,sys; os.utime(sys.argv[1], (int(sys.argv[2])-30,)*2)' "$HANDOFF" "$NOW_EPOCH"
OUT="$(ss_in startup | oh "$ORCH" hook session-start)"
case "$OUT" in *late*) ;; *) fail "D1: a marked fresh handoff should inject without a lease: $OUT" ;; esac
pass "D1: SessionStart injects and renames on startup, keeps the file on resume and compact"

# --- install / uninstall ------------------------------------------------------
SP="$P/.claude/settings.local.json"
mkdir -p "$P/.claude"
printf '{\n  "statusLine": {\n    "type": "command",\n    "command": "~/line.sh"\n  }\n}\n' > "$SP"
cp "$SP" "$OH/settings.orig"
RC=0; OUT="$(oh 0 --json -C "$P" hook install)" || RC=$?
[ "$RC" -eq 4 ] && [ "$(echo "$OUT" | jget data.blockedBy)" = "statusline exists" ] && [ "$(echo "$OUT" | jget data.changed)" = "false" ] \
  || fail "D1: install over a statusline must be refused with exit 4: rc=$RC $OUT"
cmp -s "$SP" "$OH/settings.orig" || fail "D1: a refused install wrote"
OUT="$(oh 0 --json -C "$P" hook install --wrap-statusline)"
[ "$(echo "$OUT" | jget data.statusline)" = "wrapped" ] && [ "$(echo "$OUT" | jget data.changed)" = "true" ] || fail "D1: wrap failed: $OUT"
grep -q 'rota statusline dump --then' "$SP" && grep -q '"rotaWrapped": "~/line.sh"' "$SP" && grep -q '# rota-hook' "$SP" || fail "D1: wrapped settings missing entries"
cp "$SP" "$OH/settings.wrapped"
OUT="$(oh 0 --json -C "$P" hook install --wrap-statusline)"
[ "$(echo "$OUT" | jget data.changed)" = "false" ] && cmp -s "$SP" "$OH/settings.wrapped" || fail "D1: a re-run must change nothing: $OUT"
OUT="$(oh 0 --json -C "$P" hook uninstall)"
[ "$(echo "$OUT" | jget data.changed)" = "true" ] || fail "D1: uninstall changed nothing: $OUT"
cmp -s "$SP" "$OH/settings.orig" || fail "D1: uninstall did not restore the file byte for byte"
OUT="$(oh 0 --json -C "$P" hook uninstall)"
[ "$(echo "$OUT" | jget data.changed)" = "false" ] || fail "D1: a second uninstall must be a no-op: $OUT"
NOHV="$(mktemp -d)" # $TMP itself holds a .rota/, so a dir outside it
RC=0; oh 0 --json -C "$NOHV" hook install --scope project >/dev/null 2>&1 || RC=$?
rm -rf "${NOHV:?}"
[ "$RC" -eq 3 ] || fail "D1: a project scope outside a project must exit 3, got $RC"
pass "D1: install wraps an existing statusline, a re-run changes nothing, uninstall restores the file byte for byte"

# --- doctor -------------------------------------------------------------------
DB="$OH/bin"; mkdir -p "$DB"
ln -s "$(command -v git)" "$DB/git"; printf '#!/bin/sh\nexit 0\n' > "$DB/jq"; chmod +x "$DB/jq"
ln -s "$ROTA_BIN" "$DB/rota"
dd_run() { ROTA_TEST_DOCTOR_PATH="$DB" CLAUDE_CONFIG_DIR="$CC" "$ROTA_BIN" --json -C "$P" doctor 2>/dev/null; }
dd_field() { python3 -c '
import json,sys
cs={c["name"]:c for c in json.load(sys.stdin)["data"]["checks"]}
print(cs[sys.argv[1]].get(sys.argv[2],"ABSENT"))' "$1" "$2"; }
rm -f "$SP"
OUT="$(dd_run || true)"
[ "$(echo "$OUT" | dd_field statusline status)" = "skip" ] || fail "D1: no settings file: statusline should skip: $OUT"
SKIP_DETAIL="$(echo "$OUT" | dd_field stop-hook detail)"
[ "$(echo "$OUT" | dd_field stop-hook status)" = "skip" ] && grep -q 'rota hook install' <<<"$SKIP_DETAIL" || fail "D1: hooks not installed are opt-in and should skip, naming the install command: $OUT"
printf '{"statusLine":{"type":"command","command":"~/line.sh"}}\n' > "$SP"
OUT="$(dd_run || true)"
[ "$(echo "$OUT" | dd_field statusline status)" = "skip" ] && [ "$(echo "$OUT" | dd_field stop-hook status)" = "skip" ] || fail "D1: a user's own statusline is not a rota install, both should skip: $OUT"
# a partial install (one marked hook, no dump) is opted in and fails with the hint
printf '{"statusLine":{"type":"command","command":"~/line.sh"},"hooks":{"Stop":[{"hooks":[{"type":"command","command":"rota hook stop # rota-hook"}]}]}}\n' > "$SP"
OUT="$(dd_run || true)"
[ "$(echo "$OUT" | dd_field statusline status)" = "fail" ] && [ "$(echo "$OUT" | dd_field statusline hint)" = "rota hook install --wrap-statusline" ] || fail "D1: a partial install without the dump should fail: $OUT"
[ "$(echo "$OUT" | dd_field stop-hook status)" = "fail" ] && [ "$(echo "$OUT" | dd_field stop-hook hint)" = "rota hook install" ] || fail "D1: a partial install without SessionStart should fail: $OUT"
printf '{"statusLine":{"type":"command","command":"~/line.sh"}}\n' > "$SP"
oh 0 -C "$P" hook install --wrap-statusline >/dev/null
OUT="$(dd_run || true)"
[ "$(echo "$OUT" | dd_field statusline status)" = "pass" ] && [ "$(echo "$OUT" | dd_field stop-hook status)" = "pass" ] || fail "D1: a wrapped install should pass both: $OUT"
rm "$DB/rota"
OUT="$(dd_run || true)"
[ "$(echo "$OUT" | dd_field stop-hook status)" = "fail" ] || fail "D1: hooks whose rota does not resolve should fail: $OUT"
oh 0 -C "$P" hook uninstall >/dev/null
pass "D1: doctor skips until opted in, fails a partial install and passes a wrapped one"

kill "$ORCH" "$WORKER" 2>/dev/null || true
wait "$ORCH" "$WORKER" 2>/dev/null || true
trap 'rm -rf "$TMP"' EXIT
