echo "D2: rota keepalive run|status, the supervisor that restarts the orchestrator (#66)"

# Everything lives under $TMP_KA (a fresh mktemp -d): a git project with a
# file-mode backlog, a fake gh and a stand-in tmux server in front of PATH, and
# a fake orchestrator, a shell script that writes, consumes or withholds a
# handoff. No real claude, gh, herdr or tmux is started.
TMP_KA="$(mktemp -d)"
SUP=""
ka_cleanup() { # only the supervisor this section started; the child is its own
  if [ -n "${SUP:-}" ]; then kill "$SUP" 2>/dev/null || true; fi
  rm -rf "${TMP_KA:?}"
}
trap ka_cleanup EXIT
FK="$TMP_KA/fake"; P="$TMP_KA/proj"
mkdir -p "$FK/bin" "$P/.rota/milestones"
printf '#!/usr/bin/env bash\nexec bash "%s/fakes/gh" "$@"\n' "$TESTDIR" > "$FK/bin/gh"
printf '#!/bin/sh\necho "no server running" >&2\nexit 1\n' > "$FK/bin/tmux"
chmod +x "$FK/bin/gh" "$FK/bin/tmux"
KA_BASE='{"git":{"baseBranch":"main"},"issues":{"provider":"github","retryWaitSeconds":0},"orchestrator":{"keepaliveBackoffSeconds":0%s}}'
ka_cfg() { printf "$KA_BASE\n" "${1:-}" > "$P/.rota/config.json"; } # ka_cfg [',"escalateIssue":1']
(
  cd "$P" && git init -q -b main . && git -c user.email=a@b -c user.name=n commit -q --allow-empty -m init \
    && printf '# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n' > .rota/BACKLOG.md \
    && printf -- '---\nid: M01\ntitle: "m"\nstatus: active\ndepends: []\n---\n' > .rota/milestones/M01.md
) || fail "D2 fixture setup failed"
ka_cfg
CD="$(git -C "$P" rev-parse --path-format=absolute --git-common-dir)"
HANDOFF="$P/.rota/handoff/main.md"
LEASE="$CD/rota/round-lease.json"
STATE="$CD/rota/keepalive.json"
PROMPT="Continue as orchestrator: read the handoff injected at session start, run rota round status, and resume the round."

# The fake orchestrator: $1 mode, $2 scratch dir, $3 the restart prompt (restarts only).
CHILD="$TMP_KA/child.sh"
cat > "$CHILD" <<'CH'
#!/bin/bash
mode="$1"; kd="$2"; H="$KA_HANDOFF"
n=$(cat "$kd/count" 2>/dev/null || echo 0); n=$((n+1)); echo "$n" > "$kd/count"
echo "$$" > "$kd/childpid"
echo "$#:${3-}" >> "$kd/args.log"
echo "${ROTA_ROUND_HOLDER_PID-}" >> "$kd/holder.log"
put() { mkdir -p "$(dirname "$H")"; echo "$1" > "$H"; }
case "$mode" in
  once)     if [ "$n" = 1 ]; then put "handoff one"; else rm -f "$H"; fi ;;
  none)     rm -f "$H" ;;
  stuck)    : ;;
  progress) if [ "$n" -le "${KA_MAX:-3}" ]; then put "handoff $n"; else rm -f "$H"; fi ;;
  renew)
    "$ROTA_BIN" --json round start --slots 1 > "$kd/rs.$n.json" 2> "$kd/rs.$n.err"; echo "$?" > "$kd/rs.$n.rc"
    if [ "$n" = 1 ]; then put "handoff renew"; else rm -f "$H"; fi ;;
  wait)
    put "handoff while waiting"
    trap 'echo got > "$kd/got-signal"; exit 0' INT TERM
    : > "$kd/ready"
    while :; do sleep 0.1; done ;;
esac
exit 0
CH
ka_env() { env -u HERDR_PANE_ID -u TMUX_PANE -u HERDR_ENV PATH="$FK/bin:$PATH" FAKE_TRACKER_DB="$TMP_KA/gh.json" KA_HANDOFF="$HANDOFF" "$@"; }
ka() { ( cd "$P" && ka_env "$@" ); }
newkd() { mktemp -d "$TMP_KA/run.XXXXXX"; }
kastate() { python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); print(eval(sys.argv[2]))' "$STATE" "$1"; }
waitfor() { local i=0; while [ ! -e "$1" ] && [ "$i" -lt 150 ]; do sleep 0.1; i=$((i+1)); done; [ -e "$1" ] || fail "D2: timed out waiting for $1"; }

# Safety: gh and tmux must be the fakes, claude is never named.
[ "$(ka sh -c 'command -v gh')" = "$FK/bin/gh" ] || fail "D2: gh must resolve to the fake"
[ "$(ka sh -c 'command -v tmux')" = "$FK/bin/tmux" ] || fail "D2: tmux must resolve to the stand-in"

# --- usage and status before ---------------------------------------------------
RC=0; ka "$ROTA_BIN" keepalive run claude >/dev/null 2>&1 || RC=$?
[ "$RC" = "2" ] || fail "D2: run without -- must exit 2, got $RC"
RC=0; ka "$ROTA_BIN" keepalive run -- >/dev/null 2>&1 || RC=$?
[ "$RC" = "2" ] || fail "D2: run without a command must exit 2, got $RC"
RC=0; ka "$ROTA_BIN" keepalive run --breaker 0 -- true >/dev/null 2>&1 || RC=$?
[ "$RC" = "2" ] || fail "D2: an invalid number flag must exit 2, got $RC"
RC=0; (cd "$TMP_KA" && "$ROTA_BIN" keepalive run -- true >/dev/null 2>&1) || RC=$?
[ "$RC" = "3" ] || fail "D2: run outside a project must exit 3, got $RC"
OUT="$(ka "$ROTA_BIN" --json keepalive status)"
[ "$(jget data.running <<<"$OUT")" = "false" ] && [ "$(jget data.lease.state <<<"$OUT")" = "none" ] || fail "D2: status before any run: $OUT"
[ "$(python3 -c 'import json,sys; print("keepalive" in json.load(sys.stdin)["data"])' <<<"$OUT")" = "False" ] || fail "D2: status before any run must have no keepalive object: $OUT"
pass "D2: usage errors exit 2 or 3, and status before any run reports nothing"

# --- restart on a fresh handoff, prompt last on restarts only -----------------------
KD="$(newkd)"; rm -f "$HANDOFF"
RC=0; ka "$ROTA_BIN" --json keepalive run --prompt "$PROMPT" -- bash "$CHILD" once "$KD" >"$KD/out.json" 2>"$KD/err.log" || RC=$?
[ "$RC" = "0" ] || fail "D2: restart run must exit 0, got $RC: $(cat "$KD/out.json") $(cat "$KD/err.log")"
OUT="$(cat "$KD/out.json")"
[ "$(jget data.stopReason <<<"$OUT")" = "no-handoff" ] && [ "$(jget data.restarts <<<"$OUT")" = "1" ] && [ "$(jget data.changed <<<"$OUT")" = "true" ] || fail "D2: restart data: $OUT"
[ "$(cat "$KD/count")" = "2" ] || fail "D2: a fresh handoff must restart the child once, starts=$(cat "$KD/count")"
[ "$(sed -n 1p "$KD/args.log")" = "2:" ] || fail "D2: the first start must carry no prompt: $(cat "$KD/args.log")"
[ "$(sed -n 2p "$KD/args.log")" = "3:$PROMPT" ] || fail "D2: the restart must carry the prompt as the last argument: $(cat "$KD/args.log")"
[ "$(sed -n 1p "$KD/holder.log")" = "$(sed -n 2p "$KD/holder.log")" ] && [ -n "$(sed -n 1p "$KD/holder.log")" ] || fail "D2: the child must see ROTA_ROUND_HOLDER_PID: $(cat "$KD/holder.log")"
[ ! -e "$LEASE" ] || fail "D2: the lease must be released on stop"
[ "$(kastate 'd["status"]')" = "stopped" ] && [ "$(kastate 'd["stopReason"]')" = "no-handoff" ] && [ "$(kastate 'd["restarts"]')" = "1" ] || fail "D2: state file after a stop: $(cat "$STATE")"
OUT="$(ka "$ROTA_BIN" --json keepalive status)"
[ "$(jget data.running <<<"$OUT")" = "false" ] && [ "$(jget data.keepalive.status <<<"$OUT")" = "stopped" ] && [ "$(jget data.lease.state <<<"$OUT")" = "none" ] || fail "D2: status after: $OUT"
pass "D2: a fresh handoff restarts the child with the prompt last, and the first start has none"

# --- no handoff, no restart -----------------------------------------------------------
KD="$(newkd)"; rm -f "$HANDOFF"
RC=0; ka "$ROTA_BIN" --json keepalive run -- bash "$CHILD" none "$KD" >"$KD/out.json" 2>/dev/null || RC=$?
OUT="$(cat "$KD/out.json")"
[ "$RC" = "0" ] && [ "$(jget data.stopReason <<<"$OUT")" = "no-handoff" ] && [ "$(jget data.restarts <<<"$OUT")" = "0" ] || fail "D2: no handoff must stop, rc=$RC: $OUT"
[ "$(cat "$KD/count")" = "1" ] || fail "D2: one start only without a handoff, starts=$(cat "$KD/count")"
[ ! -e "$LEASE" ] || fail "D2: the lease must be released"
pass "D2: an exit without a fresh handoff does not restart: no-handoff, exit 0, lease released"

# --- the lock: a second run, a hand-run round start, a child's round start -------------
KD="$(newkd)"; rm -f "$HANDOFF"
( cd "$P" && exec env -u HERDR_PANE_ID -u TMUX_PANE -u HERDR_ENV PATH="$FK/bin:$PATH" FAKE_TRACKER_DB="$TMP_KA/gh.json" KA_HANDOFF="$HANDOFF" \
    "$ROTA_BIN" --json keepalive run -- bash "$CHILD" wait "$KD" ) >"$KD/out.json" 2>"$KD/err.log" &
SUP=$!
waitfor "$KD/ready"
OUT="$(ka "$ROTA_BIN" --json keepalive status)"
[ "$(jget data.running <<<"$OUT")" = "true" ] && [ "$(jget data.lease.state <<<"$OUT")" = "live" ] || fail "D2: status during a run: $OUT"
[ "$(jget data.lease.holderPid <<<"$OUT")" = "$SUP" ] || fail "D2: the supervisor must hold the lease (pid $SUP): $OUT"
[ "$(jget data.lease.round <<<"$OUT")" = "0" ] && [ "$(jget data.keepalive.status <<<"$OUT")" = "running" ] || fail "D2: an unnumbered lease and a running state: $OUT"
RC=0; OUT="$(ka "$ROTA_BIN" --json keepalive run -- bash "$CHILD" none "$(newkd)" 2>/dev/null)" || RC=$?
[ "$RC" = "4" ] && [ "$(jget data.blockedBy <<<"$OUT")" = "lease held" ] && [ "$(jget data.changed <<<"$OUT")" = "false" ] || fail "D2: a second run must exit 4 lease held, rc=$RC: $OUT"
RC=0; OUT="$(ka "$ROTA_BIN" --json round start --slots 1 2>/dev/null)" || RC=$?
[ "$RC" = "4" ] && [ "$(jget data.blockedBy <<<"$OUT")" = "lease held" ] || fail "D2: a hand-run round start must be refused as in C3, rc=$RC: $OUT"
pass "D2: while the supervisor lives a second run and a hand-run round start exit 4"

# --- SIGINT is forwarded, the supervisor does not restart ------------------------------
kill -INT "$SUP"
RC=0; wait "$SUP" || RC=$?
SUP=""
OUT="$(cat "$KD/out.json")"
[ "$RC" = "0" ] && [ "$(jget data.stopReason <<<"$OUT")" = "interrupted" ] && [ "$(jget data.restarts <<<"$OUT")" = "0" ] || fail "D2: SIGINT must stop with interrupted, exit 0, rc=$RC: $OUT"
[ -e "$KD/got-signal" ] || fail "D2: the child never received the forwarded signal"
[ "$(cat "$KD/count")" = "1" ] || fail "D2: an interrupted supervisor must not restart, starts=$(cat "$KD/count")"
[ -e "$HANDOFF" ] || fail "D2: the supervisor must never delete the handoff"
[ ! -e "$LEASE" ] || fail "D2: the lease must be released after an interrupt"
OUT="$(ka "$ROTA_BIN" --json keepalive status)"
[ "$(jget data.running <<<"$OUT")" = "false" ] && [ "$(jget data.keepalive.stopReason <<<"$OUT")" = "interrupted" ] || fail "D2: status after an interrupt: $OUT"
pass "D2: SIGINT is forwarded to the child; no restart, lease released, handoff kept"

# --- a child's round start under ROTA_ROUND_HOLDER_PID renews and numbers the lease ------
KD="$(newkd)"; rm -f "$HANDOFF"
RC=0; ka "$ROTA_BIN" --json keepalive run -- bash "$CHILD" renew "$KD" >"$KD/out.json" 2>"$KD/err.log" || RC=$?
[ "$RC" = "0" ] || fail "D2: renew run exit $RC: $(cat "$KD/out.json") $(cat "$KD/err.log")"
[ "$(cat "$KD/rs.1.rc")" = "0" ] && [ "$(cat "$KD/rs.2.rc")" = "0" ] || fail "D2: round start under the supervisor must succeed: $(cat "$KD/rs.1.err") $(cat "$KD/rs.2.err")"
[ "$(jget data.round <"$KD/rs.1.json")" = "1" ] || fail "D2: the first round start must number the lease 1: $(cat "$KD/rs.1.json")"
[ "$(jget data.round <"$KD/rs.2.json")" = "1" ] && [ "$(jget data.changed <"$KD/rs.2.json")" = "false" ] || fail "D2: a restarted orchestrator's round start must renew and keep the number: $(cat "$KD/rs.2.json")"
pass "D2: under ROTA_ROUND_HOLDER_PID round start numbers the unnumbered lease and renews on a restart"

# --- the breaker, with and without an escalation thread --------------------------------
ka gh issue create --title "Keepalive escalations" --body "thread" >/dev/null
ka_cfg ',"escalateIssue":1'
KD="$(newkd)"; mkdir -p "$(dirname "$HANDOFF")"; echo "stuck handoff" > "$HANDOFF"
RC=0; ka "$ROTA_BIN" --json keepalive run --backoff 0 -- bash "$CHILD" stuck "$KD" >"$KD/out.json" 2>"$KD/err.log" || RC=$?
OUT="$(cat "$KD/out.json")"
[ "$RC" = "1" ] || fail "D2: the breaker must exit 1, got $RC: $OUT $(cat "$KD/err.log")"
[ "$(jget data.stopReason <<<"$OUT")" = "breaker" ] && [ "$(jget data.restarts <<<"$OUT")" = "2" ] && [ "$(jget data.noProgress <<<"$OUT")" = "3" ] || fail "D2: breaker data: $OUT"
[ "$(cat "$KD/count")" = "3" ] || fail "D2: the breaker must trip after 3 no-progress exits, starts=$(cat "$KD/count")"
[ "$(jget data.escalation <<<"$OUT")" = "e1" ] || fail "D2: data.escalation must be e1: $OUT"
[ "$(kastate 'd["escalation"]')" = "e1" ] && [ "$(kastate 'd["stopReason"]')" = "breaker" ] || fail "D2: the state file must record the escalation: $(cat "$STATE")"
COMMENTS="$(ka gh api --paginate repos/o/r/issues/1/comments)"
python3 -c '
import json, sys
c = json.load(sys.stdin)
b = c[-1]["body"]
sys.exit(0 if len(c) == 1 and "Orchestrator keepalive stopped: breaker" in b and "Run rota keepalive run again after fixing the cause; the handoff is kept." in b else 1)' <<<"$COMMENTS" \
  || fail "D2: the escalation comment is missing or incomplete: $COMMENTS"
[ -e "$HANDOFF" ] || fail "D2: the handoff must be kept after a breaker stop"
pass "D2: the breaker trips after 3 restarts without progress, exits 1 and posts the escalation comment"

# A pending escalation on that thread is a warning; the stop stands.
KD="$(newkd)"
RC=0; ka "$ROTA_BIN" --json keepalive run --backoff 0 -- bash "$CHILD" stuck "$KD" >"$KD/out.json" 2>/dev/null || RC=$?
OUT="$(cat "$KD/out.json")"
[ "$RC" = "1" ] && [ "$(jget data.stopReason <<<"$OUT")" = "breaker" ] && grep -q 'pending' <<<"$OUT" || fail "D2: a pending escalation must be a warning, rc=$RC: $OUT"
[ "$(python3 -c 'import json,sys; print("escalation" in json.load(sys.stdin)["data"])' <<<"$OUT")" = "False" ] || fail "D2: no new escalation id expected: $OUT"

# escalateIssue unset: no comment, a warning.
ka_cfg ""
KD="$(newkd)"
RC=0; ka "$ROTA_BIN" --json keepalive run --backoff 0 -- bash "$CHILD" stuck "$KD" >"$KD/out.json" 2>/dev/null || RC=$?
OUT="$(cat "$KD/out.json")"
[ "$RC" = "1" ] && [ "$(jget data.stopReason <<<"$OUT")" = "breaker" ] || fail "D2: unset escalateIssue still trips the breaker, rc=$RC: $OUT"
[ "$(python3 -c 'import json,sys; print("escalation" in json.load(sys.stdin)["data"])' <<<"$OUT")" = "False" ] || fail "D2: no escalation expected without escalateIssue: $OUT"
grep -q 'escalateIssue is unset' <<<"$OUT" || fail "D2: the warning for an unset escalateIssue is missing: $OUT"
[ "$(ka gh api --paginate repos/o/r/issues/1/comments | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')" = "1" ] || fail "D2: no comment may be posted without escalateIssue"
pass "D2: a pending thread or an unset escalateIssue gives a warning and no new comment; the stop stands"

# --- max restarts, and progress resets the breaker ------------------------------------
KD="$(newkd)"; rm -f "$HANDOFF"
RC=0; ka env KA_MAX=100 "$ROTA_BIN" --json keepalive run --backoff 0 --max-restarts 2 -- bash "$CHILD" progress "$KD" >"$KD/out.json" 2>/dev/null || RC=$?
OUT="$(cat "$KD/out.json")"
[ "$RC" = "1" ] && [ "$(jget data.stopReason <<<"$OUT")" = "max-restarts" ] && [ "$(jget data.restarts <<<"$OUT")" = "2" ] && [ "$(jget data.noProgress <<<"$OUT")" = "0" ] || fail "D2: max-restarts data, rc=$RC: $OUT"
[ "$(cat "$KD/count")" = "3" ] || fail "D2: --max-restarts 2 must allow 3 starts, starts=$(cat "$KD/count")"
pass "D2: --max-restarts stops the loop with exit 1"

KD="$(newkd)"; rm -f "$HANDOFF"
RC=0; ka env KA_MAX=7 "$ROTA_BIN" --json keepalive run --backoff 0 --breaker 2 --max-restarts 50 -- bash "$CHILD" progress "$KD" >"$KD/out.json" 2>/dev/null || RC=$?
OUT="$(cat "$KD/out.json")"
[ "$RC" = "0" ] && [ "$(jget data.stopReason <<<"$OUT")" = "no-handoff" ] && [ "$(jget data.restarts <<<"$OUT")" = "7" ] || fail "D2: a child that writes a new handoff each run must never trip the breaker, rc=$RC: $OUT"
[ "$(cat "$KD/count")" = "8" ] || fail "D2: starts=$(cat "$KD/count"), want 8"
pass "D2: progress (a new handoff each run) resets the counter, so --breaker 2 never trips"

# --- SIGKILL leaves a stale lease that a new run reclaims -----------------------------
KD="$(newkd)"; rm -f "$HANDOFF"
( cd "$P" && exec env -u HERDR_PANE_ID -u TMUX_PANE -u HERDR_ENV PATH="$FK/bin:$PATH" FAKE_TRACKER_DB="$TMP_KA/gh.json" KA_HANDOFF="$HANDOFF" \
    "$ROTA_BIN" --json keepalive run -- bash "$CHILD" wait "$KD" ) >"$KD/out.json" 2>"$KD/err.log" &
SUP=$!
waitfor "$KD/ready"
kill -KILL "$SUP"
wait "$SUP" 2>/dev/null || true
SUP=""
kill "$(cat "$KD/childpid")" 2>/dev/null || true # the orphaned fake orchestrator, started by the supervisor above
[ -e "$LEASE" ] || fail "D2: a SIGKILLed supervisor must leave its lease behind"
OUT="$(ka "$ROTA_BIN" --json keepalive status)"
[ "$(jget data.lease.state <<<"$OUT")" = "stale" ] && [ "$(jget data.running <<<"$OUT")" = "false" ] && [ "$(jget data.keepalive.status <<<"$OUT")" = "running" ] || fail "D2: status after SIGKILL must say stale and not running: $OUT"
rm -f "$HANDOFF"; KD="$(newkd)"
RC=0; ka "$ROTA_BIN" --json keepalive run -- bash "$CHILD" none "$KD" >"$KD/out.json" 2>"$KD/err.log" || RC=$?
OUT="$(cat "$KD/out.json")"
[ "$RC" = "0" ] && [ "$(jget data.stopReason <<<"$OUT")" = "no-handoff" ] || fail "D2: a new run must reclaim the stale lease, rc=$RC: $OUT $(cat "$KD/err.log")"
grep -q 'reclaimed stale lease' <<<"$OUT" || fail "D2: the reclaim warning is missing: $OUT"
[ ! -e "$LEASE" ] || fail "D2: the reclaiming run must release the lease"
pass "D2: a SIGKILLed supervisor leaves a stale lease that the next run reclaims"

trap 'rm -rf "$TMP"' EXIT
rm -rf "${TMP_KA:?}"
