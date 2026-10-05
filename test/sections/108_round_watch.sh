echo "round watch: background watch, Stop hook and prompt digest for a lease-holding orchestrator (#81)"

# A fixture project on the tmux fake, one busy worker and a live round lease held
# by a sleeper standing in for the orchestrator (ROTA_TEST_HOLDER_PID, as in
# section 90). --forge-poll 0 keeps the watch off the forge.
TMP_RWT="$(mktemp -d)"
RWP="$TMP_RWT/proj"; FK="$TMP_RWT/fake"; CC="$TMP_RWT/claude"
mkdir -p "$RWP/.rota" "$FK/bin" "$FK/tmux" "$CC"
cp "$TESTDIR/fakes/tmux" "$FK/bin/tmux"
sleep 300 >/dev/null 2>&1 & ORCH_RWT=$!
WATCH_PID=""
trap 'kill "$ORCH_RWT" "$WATCH_PID" 2>/dev/null || true; rm -rf "${TMP_RWT:?}"' EXIT
(
  cd "$RWP" && git init -q -b main . && git -c user.email=a@b -c user.name=n commit -q --allow-empty -m init \
    && printf '{"git":{"baseBranch":"main"},"work":{"dispatch":"tmux"}}\n' > .rota/config.json
) || fail "round-watch fixture setup failed"
CDW="$(git -C "$RWP" rev-parse --path-format=absolute --git-common-dir)"
mkdir -p "$CDW/rota"
python3 - "$CDW/rota/round-lease.json" "$ORCH_RWT" "$RWP" <<'PY' || fail "could not write the lease"
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
rwslots() { # rwslots <state>: w1 holds issue 81, w2 is parked
  printf '{"session":"rota","round":1,"slots":[{"name":"w1","branch":"b1","worktree":"%s/w1","base":"main","handle":"rota:w1","state":"%s","issue":"81","task":"T1","relays":[]},{"name":"w2","branch":"b2","worktree":"%s/w2","base":"main","handle":"rota:w2","state":"idle","task":null,"relays":[]}]}\n' \
    "$TMP_RWT" "$1" "$TMP_RWT" > "$RWP/.rota/workers.json"
}
rwr() { ( cd "$RWP" && PATH="$FK/bin:$PATH" FAKE_TMUX="$FK/tmux" CLAUDE_CONFIG_DIR="$CC" ROTA_TEST_HOLDER_PID="$ORCH_RWT" "$@" ); }
rw_stop() { printf '{"session_id":"s1","cwd":"%s","stop_hook_active":%s}' "$RWP" "$1" | rwr "$ROTA_BIN" hook stop; }
rw_prompt() { printf '{"session_id":"s1","cwd":"%s","prompt":"hi"}' "$RWP" | rwr "$ROTA_BIN" hook prompt; }
# A tmux pane that never changes reads as idle, so a busy worker is a pane whose
# text moves. The fake's tick file makes every capture differ; a background
# writer here was starved under the sharded gate and the pane read as idle (#135).
churn() { printf 'working\n' > "$FK/tmux/pane"; : > "$FK/tmux/tick"; }
unchurn() { rm -f "$FK/tmux/tick"; }
trap 'unchurn; kill "$ORCH_RWT" "$WATCH_PID" 2>/dev/null || true; rm -rf "${TMP_RWT:?}"' EXIT
churn
rwslots busy

# ── hooks without a watch ───────────────────────────────────────────────────
OUT="$(rw_stop false)"
[ "$(jget decision <<<"$OUT")" = "block" ] || fail "Stop must block an orchestrator with a busy worker and no watch: $OUT"
case "$(jget reason <<<"$OUT")" in *"rota round watch"*) ;; *) fail "the block reason must name the verb: $OUT" ;; esac
OUT="$(rw_stop true)"
[ -z "$OUT" ] || fail "a second stop in the same turn must pass, not loop: $OUT"
OUT="$(rw_prompt)"
[ "$(jget hookSpecificOutput.hookEventName <<<"$OUT")" = "UserPromptSubmit" ] || fail "the prompt hook should inject context: $OUT"
case "$(jget hookSpecificOutput.additionalContext <<<"$OUT")" in *"w1 busy #81"*"NO WATCH ARMED"*) ;; *) fail "digest should list w1 and say no watch is armed: $OUT" ;; esac
RC=0; OUT="$(printf '{"session_id":"s1","cwd":"%s"}' "$RWP" | rwr env ROTA_TEST_HOLDER_PID=1 "$ROTA_BIN" hook stop)" || RC=$?
[ "$RC" -eq 0 ] && [ -z "$OUT" ] || fail "a session that does not hold the lease must pass: rc=$RC out=$OUT"
rwslots idle
OUT="$(rw_stop false)"; [ -z "$OUT" ] || fail "an all-idle round has nothing to watch, Stop must pass: $OUT"
OUT="$(rw_prompt)"; [ -z "$OUT" ] || fail "the prompt hook must stay silent when nothing is active: $OUT"
rwslots busy
pass "Stop refuses an idle lease holder with a busy worker and no watch; the prompt hook shows the digest"

# ── a heartbeat, and the hooks with a watch armed ──────────────────────────
rwr "$ROTA_BIN" --json round watch --heartbeat 6 --poll 1 --settle 1 --forge-poll 0 > "$TMP_RWT/watch.out" 2>"$TMP_RWT/watch.err" &
WATCH_PID=$!
for _ in $(seq 50); do [ -f "$CDW/rota/round-watch.json" ] && break; sleep 0.1; done
[ -f "$CDW/rota/round-watch.json" ] || fail "the watch never wrote its marker: $(cat "$TMP_RWT/watch.out" "$TMP_RWT/watch.err" 2>&1)"
OUT="$(rw_stop false)"; [ -z "$OUT" ] || fail "Stop must pass while a watch is armed: $OUT"
case "$(jget hookSpecificOutput.additionalContext <<<"$(rw_prompt)")" in *"Watch armed."*) ;; *) fail "the digest should say the watch is armed" ;; esac
RC=0; OUT="$(rwr "$ROTA_BIN" --json round watch --heartbeat 6 2>/dev/null)" || RC=$?
[ "$RC" = "4" ] || fail "a second watch must be refused (exit 4), got $RC: $OUT"
wait "$WATCH_PID" || fail "the watch should exit 0 at its heartbeat"
WATCH_PID=""
OUT="$(cat "$TMP_RWT/watch.out")"
[ "$(jget data.reason <<<"$OUT")" = "heartbeat" ] || fail "a quiet watch ends with reason heartbeat: $OUT"
[ ! -f "$CDW/rota/round-watch.json" ] || fail "the watch must remove its marker on exit"
OUT="$(rw_stop false)"; [ "$(jget decision <<<"$OUT")" = "block" ] || fail "with the watch gone Stop blocks again: $OUT"
pass "round watch arms one marker, refuses a second watch, returns a heartbeat and cleans up"

# ── a slot that finishes wakes the watch ───────────────────────────────────
rwr "$ROTA_BIN" --json round watch --heartbeat 120 --poll 1 --settle 1 --forge-poll 0 > "$TMP_RWT/watch.out" 2>/dev/null &
WATCH_PID=$!
for _ in $(seq 50); do [ -f "$CDW/rota/round-watch.json" ] && break; sleep 0.1; done
sleep 1
unchurn
printf 'ROTA-DONE w1 https://github.com/o/r/pull/7\n' > "$FK/tmux/pane"
for _ in $(seq 100); do kill -0 "$WATCH_PID" 2>/dev/null || break; sleep 0.1; done
kill -0 "$WATCH_PID" 2>/dev/null && fail "the watch did not return when w1 finished"
wait "$WATCH_PID" || fail "the watch should exit 0 on a slot change"
WATCH_PID=""
OUT="$(cat "$TMP_RWT/watch.out")"
[ "$(jget data.reason <<<"$OUT")" = "slot" ] && [ "$(jget data.slot <<<"$OUT")" = "w1" ] && [ "$(jget data.state <<<"$OUT")" = "done" ] || fail "expected reason slot, w1 done: $OUT"
pass "round watch returns the slot that finished, with its state"

# ── a registry change (PR recorded) wakes it too, usage errors, solo ───────
rwslots busy
churn
rwr "$ROTA_BIN" --json round watch --heartbeat 120 --poll 1 --settle 1 --forge-poll 0 > "$TMP_RWT/watch.out" 2>/dev/null &
WATCH_PID=$!
for _ in $(seq 50); do [ -f "$CDW/rota/round-watch.json" ] && break; sleep 0.1; done
sleep 1
python3 - "$RWP/.rota/workers.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1])); d["slots"][0]["pr"] = "https://github.com/o/r/pull/9"
json.dump(d, open(sys.argv[1], "w"))
PY
for _ in $(seq 100); do kill -0 "$WATCH_PID" 2>/dev/null || break; sleep 0.1; done
kill -0 "$WATCH_PID" 2>/dev/null && fail "the watch did not return when a PR was recorded"
wait "$WATCH_PID" || true
WATCH_PID=""
OUT="$(cat "$TMP_RWT/watch.out")"
[ "$(jget data.reason <<<"$OUT")" = "change" ] && [ "$(jget 'data.changes[0].key' <<<"$OUT")" = "pr/w1" ] || fail "expected reason change on pr/w1: $OUT"
RC=0; rwr "$ROTA_BIN" round watch --heartbeat 0 >/dev/null 2>&1 || RC=$?
[ "$RC" = "2" ] || fail "--heartbeat 0 must exit 2, got $RC"
RC=0; rwr "$ROTA_BIN" round watch extra >/dev/null 2>&1 || RC=$?
[ "$RC" = "2" ] || fail "a positional argument must exit 2, got $RC"
printf '{"host":"solo","slots":[]}\n' > "$RWP/.rota/workers.json"
RC=0; rwr "$ROTA_BIN" round watch --heartbeat 5 >/dev/null 2>&1 || RC=$?
[ "$RC" = "5" ] || fail "a solo round has nothing to watch (exit 5), got $RC"
pass "round watch returns on a recorded PR; bad flags exit 2; solo exits 5"
# a solo registry with a busy-looking slot still has nothing to watch: no digest, no Stop block
rwslots busy
python3 - "$RWP/.rota/workers.json" <<'PY'
import json,sys
d=json.load(open(sys.argv[1])); d["host"]="solo"; json.dump(d,open(sys.argv[1],"w"))
PY
OUT="$(rw_prompt)"; [ -z "$OUT" ] || fail "a solo round must not get the NO WATCH ARMED digest: $OUT"
OUT="$(rw_stop false)"; [ -z "$OUT" ] || fail "a solo round must not be blocked by Stop: $OUT"
pass "prompt and Stop hooks stay silent under a solo registry with slots"

# ── install registers the prompt hook ──────────────────────────────────────
OUT="$(rwr "$ROTA_BIN" --json hook install --scope project-local)" || fail "hook install failed: $OUT"
case "$(jget data.hooks <<<"$OUT")" in *UserPromptSubmit*) ;; *) fail "hook install must register UserPromptSubmit: $OUT" ;; esac
grep -q 'rota hook prompt' "$RWP/.claude/settings.local.json" || fail "settings should name rota hook prompt"
pass "hook install registers the UserPromptSubmit digest hook"

unchurn
kill "$ORCH_RWT" 2>/dev/null || true
trap 'rm -rf "$TMP"' EXIT
rm -rf "${TMP_RWT:?}"
