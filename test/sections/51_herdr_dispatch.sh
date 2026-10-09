echo "worker verbs — herdr dispatch backend (work.dispatch: herdr)"
# Covers the herdr host against a FAKE `herdr` on PATH: canned JSON replies in
# the shapes `herdr api schema --json` documents (protocol 22), errors as JSON
# on stderr with exit 1. A live herdr server stays a manual gate — tabs and
# agents on a real server belong to whoever is running it.
#
#   (a) pool init under herdr leaves handle null; `window` migrates to handle
#   (b) dispatch: tab create (cwd, label, account env) -> agent start with the
#       worker args -> prompt confirmed by working/blocked, handle/task/state
#       recorded; re-dispatch closes the old tab; relay reuses the session
#   (c) startup dialogs: the accepting option is chosen by reading the pane
#   (d) prompt errors map to exit 5 (dialog up) and exit 4 (never picked up)
#   (e) poll: native state mapping, slot.state + slot.pr writes, notify once
#   (f) session check/ensure key on HERDR_ENV

TMP_HD="$(mktemp -d)"
trap 'rm -rf "$TMP_HD"' EXIT

FAKE="$TMP_HD/fake"
mkdir -p "$FAKE/bin" "$TMP_HD/repo/.rota"
cat > "$FAKE/bin/herdr" <<'SH'
#!/usr/bin/env bash
# Fake herdr: logs argv, answers from files in $FAKE_HERDR.
F="$FAKE_HERDR"
printf '%s\n' "$*" >>"$F/log"
err() { printf '{"error":{"code":"%s","message":"fake"},"id":"cli"}\n' "$1" >&2; exit 1; }
agent_json() {
  printf '{"id":"cli","result":{"type":"%s","agent":{"agent":"claude","agent_status":"%s","pane_id":"w9:p11","tab_id":"w9:t7","focused":false}}}\n' "$1" "$2"
}
# herdr 0.9.3 accepts only these agent names; tab ids come from --workspace.
case "$1 $2" in
  "agent start"|"agent get"|"agent read"|"agent send-keys"|"agent wait"|"agent prompt")
    [[ "$3" =~ ^[a-z][a-z0-9_-]{0,31}$ ]] || err invalid_agent_name ;;
esac
case "$1 $2" in
  "tab create")
    ws=w9; prev=""; for a in "$@"; do [ "$prev" = "--workspace" ] && ws="$a"; prev="$a"; done
    n=$(( $(cat "$F/tabs" 2>/dev/null || echo 6) + 1 )); echo "$n" >"$F/tabs"
    printf '{"id":"cli","result":{"type":"tab_created","tab":{"tab_id":"%s:t%s","workspace_id":"%s","label":"x","number":%s,"focused":false,"pane_count":1,"agent_status":"unknown"},"root_pane":{"pane_id":"%s:p1%s","tab_id":"%s:t%s","workspace_id":"%s"}}}\n' "$ws" "$n" "$ws" "$n" "$ws" "$n" "$ws" "$n" "$ws" ;;
  "tab close") echo '{"id":"cli","result":{"type":"ok"}}' ;;
  "agent start")
    [ -f "$F/start_not_ready" ] && err agent_not_ready
    agent_json agent_started idle ;;
  "agent get")
    [ -f "$F/gone" ] && err agent_not_found
    agent_json agent_info "$(cat "$F/status" 2>/dev/null || echo idle)" ;;
  "agent wait") agent_json agent_info "$(cat "$F/wait_status" 2>/dev/null || echo idle)" ;;
  "agent read")
    [ -f "$F/gone" ] && err agent_not_found
    cat "$F/pane.txt" 2>/dev/null ;;  # herdr 0.9.3 prints pane text as is, no JSON envelope
  "agent send-keys") echo '{"id":"cli","result":{"type":"ok"}}' ;;
  "agent prompt")
    printf '%s' "$4" >"$F/last_prompt"
    [ -f "$F/prompt_error" ] && [ "$4" != "/exit" ] && err "$(cat "$F/prompt_error")"
    agent_json agent_prompted working ;;
  "notification show") echo '{"id":"cli","result":{"type":"notification_show","shown":true,"reason":"shown"}}' ;;
  *) err unknown_method ;;
esac
SH
chmod +x "$FAKE/bin/herdr"
: >"$FAKE/pane.txt"

(
  cd "$TMP_HD/repo"
  git init -q -b main .
  git config user.email t@t
  git config user.name t
  echo seed > seed.txt
  git add seed.txt
  git commit -q -m seed
  printf '{"work":{"dispatch":"herdr"}}\n' > .rota/config.json
) || fail "herdr fixture repo setup failed"

# Every helper call below runs inside a fake herdr pane on the fake server.
hd() {
  ( cd "$TMP_HD/repo" && PATH="$FAKE/bin:$PATH" FAKE_HERDR="$FAKE" HERDR_ENV=1 \
      HERDR_WORKSPACE_ID=w9 "$@" )
}
slot_field() {
  python3 -c 'import json,sys; s=[s for s in json.load(open(sys.argv[1]))["slots"] if s["name"]==sys.argv[2]][0]; print(s.get(sys.argv[3]))' \
    "$TMP_HD/repo/.rota/workers.json" "$1" "$2"
}

# ── (a) pool ────────────────────────────────────────────────────────────────
hd "$ROTA_BIN" worker pool init --slots 2 --base main >/dev/null || fail "worker pool init failed under herdr"
[ "$(slot_field w1 handle)" = "None" ] \
  || fail "herdr pool slot should start with a null handle (tab ids exist only after dispatch), got $(slot_field w1 handle)"
python3 - "$TMP_HD/repo/.rota/workers.json" <<'PY'
import json, sys
p = sys.argv[1]; d = json.load(open(p))
s = d["slots"][1]; s.pop("handle", None); s["window"] = "rota:w2"
json.dump(d, open(p, "w"))
PY
hd "$ROTA_BIN" worker pool init --slots 2 --base main >/dev/null || fail "worker pool re-init failed"
[ "$(slot_field w2 handle)" = "rota:w2" ] || fail "init did not migrate window -> handle, got $(slot_field w2 handle)"
[ "$(slot_field w2 window)" = "None" ] || fail "init left the legacy window field behind"
pass "worker pool: herdr slots start without a handle; init migrates window -> handle"

# ── (b) dispatch ────────────────────────────────────────────────────────────
WT1="$(slot_field w1 worktree)"
python3 - "$TMP_HD/repo/.rota/workers.json" <<'PY'
import json, sys
p = sys.argv[1]; d = json.load(open(p))
d["slots"][0]["configDir"] = "/acct/one"
json.dump(d, open(p, "w"))
PY
echo "do the task" > "$TMP_HD/brief.md"
: >"$FAKE/log"
OUT="$(hd "$ROTA_BIN" worker dispatch w1 --body-file "$TMP_HD/brief.md" --task T1)" \
  || fail "herdr dispatch failed: $OUT"
grep -q "^tab create --workspace w9 --cwd $WT1 --label w1 --no-focus --env CLAUDE_CONFIG_DIR=/acct/one --env ROTA_SLOT=w1 --env ROTA_PORT_BASE=[0-9]* --env ROTA_PORT_BLOCK=[0-9]* --env ROTA_DB_SUFFIX=_w1\$" "$FAKE/log" \
  || fail "tab create must adopt the slot worktree with the account env; log: $(cat "$FAKE/log")"
grep -q '^agent start rota-w1-w9-t7 --kind claude --pane w9:p17 --timeout 60000 -- --model sonnet --dangerously-skip-permissions$' "$FAKE/log" \
  || fail "agent start must run claude in the new pane with the worker args; log: $(cat "$FAKE/log")"
grep -q '^do the task --wait --until working --until blocked --timeout 60000$' "$FAKE/log" \
  || fail "the brief must be confirmed by working/blocked, not by waiting for the whole task; log: $(cat "$FAKE/log")"
[ "$(slot_field w1 handle)" = "w9:t7" ] || fail "dispatch did not record the tab id as handle"
[ "$(slot_field w1 task)" = "T1" ] || fail "dispatch did not record slot.task"
[ "$(slot_field w1 state)" = "busy" ] || fail "dispatch did not set slot.state=busy"
pass "worker dispatch: tab create -> agent start -> confirmed prompt; handle, task, state recorded"

: >"$FAKE/log"
hd "$ROTA_BIN" worker dispatch w1 --body-file "$TMP_HD/brief.md" --task T2 >/dev/null \
  || fail "herdr re-dispatch failed"
grep -q '^agent prompt rota-w1-w9-t7 /exit$' "$FAKE/log" || fail "re-dispatch did not /exit the old session"
grep -q '^tab close w9:t7$' "$FAKE/log" || fail "re-dispatch did not close the old tab"
[ "$(slot_field w1 handle)" = "w9:t8" ] || fail "re-dispatch did not record the new tab"
pass "worker dispatch: a task re-dispatch closes the old tab and starts fresh"

: >"$FAKE/log"
hd "$ROTA_BIN" worker dispatch w1 --body-file "$TMP_HD/brief.md" --relay >/dev/null \
  || fail "herdr relay failed"
if grep -q '^tab \|^agent start' "$FAKE/log"; then
  fail "a relay must go into the running session, not a fresh one; log: $(cat "$FAKE/log")"
fi
grep -q 'ORCHESTRATOR RELAY' "$FAKE/last_prompt" || fail "relay text lost its ORCHESTRATOR RELAY marker"
[ "$(head -n 1 "$FAKE/last_prompt")" = "--- ORCHESTRATOR (round 1) ---" ] \
  || fail "a relay must open with the signature line, got: $(head -n 1 "$FAKE/last_prompt")"
[ "$(slot_field w1 task)" = "T2" ] || fail "a relay must not change slot.task"
python3 - "$TMP_HD/repo/.rota/workers.json" <<'PY'
import json, sys
p = sys.argv[1]; d = json.load(open(p))
d["slots"][1]["handle"] = None
json.dump(d, open(p, "w"))
PY
RC=0
hd "$ROTA_BIN" worker dispatch w2 --body-file "$TMP_HD/brief.md" --relay >/dev/null 2>&1 || RC=$?
[ "$RC" = "3" ] || fail "relay into a never-dispatched herdr slot should exit 3, got $RC"
pass "worker dispatch --relay reuses the live session and keeps the relay marker"

# ── (b1) provenance: signature + relay log ──────────────────────────────────
relays_json() { slot_field "$1" relays; }
python3 - "$TMP_HD/repo/.rota/workers.json" <<'PY' || fail "relay was not logged as {round, ts, summary}"
import json, sys
r = json.load(open(sys.argv[1]))["slots"][0]["relays"]
assert len(r) == 1 and r[0]["round"] == 1 and r[0]["summary"] == "do the task" and r[0]["ts"].endswith("Z"), r
PY
printf 'use the per-user cache\n' > "$TMP_HD/answer.md"
hd "$ROTA_BIN" worker dispatch w1 --body-file "$TMP_HD/answer.md" --relay --round 3 >/dev/null \
  || fail "relay with --round failed"
[ "$(head -n 1 "$FAKE/last_prompt")" = "--- ORCHESTRATOR (round 3) ---" ] || fail "--round must set the signature"
python3 - "$TMP_HD/repo/.rota/workers.json" <<'PY' || fail "relays[] must hold both relays, newest round 3 with its summary"
import json, sys
d = json.load(open(sys.argv[1]))
r = d["slots"][0]["relays"]
assert d["round"] == 3 and [x["round"] for x in r] == [1, 3] and r[1]["summary"] == "use the per-user cache", d
PY
# A task brief is signed too, and a new task starts a clean relay log.
hd "$ROTA_BIN" worker dispatch w1 --body-file "$TMP_HD/brief.md" --task T3 >/dev/null \
  || fail "T3 dispatch failed"
[ "$(head -n 1 "$FAKE/last_prompt")" = "--- ORCHESTRATOR (round 3) ---" ] \
  || fail "a task brief must open with the signature, got: $(head -n 1 "$FAKE/last_prompt")"
[ "$(relays_json w1)" = "[]" ] || fail "a new task dispatch must reset relays[], got $(relays_json w1)"
# A refused relay (dialog up, nothing sent) is not logged.
echo agent_blocked > "$FAKE/prompt_error"
hd "$ROTA_BIN" worker dispatch w1 --body-file "$TMP_HD/answer.md" --relay >/dev/null 2>&1 || true
rm -f "$FAKE/prompt_error"
[ "$(relays_json w1)" = "[]" ] || fail "a relay refused by a dialog must not be logged"
pass "worker dispatch signs every payload with the round and logs relays in relays[]"

RC=0
( cd "$TMP_HD/repo" && PATH="$FAKE/bin:$PATH" FAKE_HERDR="$FAKE" env -u HERDR_ENV \
    "$ROTA_BIN" worker dispatch w1 --body-file "$TMP_HD/brief.md" ) >/dev/null 2>&1 || RC=$?
[ "$RC" = "5" ] || fail "herdr dispatch from outside a herdr pane should exit 5 (host not usable), got $RC"

cp "$TMP_HD/repo/.rota/config.json" "$TMP_HD/config.bak"
printf '{"work":{"dispatch":"herdr","workerCommand":"FOO=1 claude --model haiku --settings s.json"}}\n' \
  > "$TMP_HD/repo/.rota/config.json"
: >"$FAKE/log"
hd "$ROTA_BIN" worker dispatch w1 --body-file "$TMP_HD/brief.md" >/dev/null \
  || fail "dispatch with a custom workerCommand failed"
grep -q -- '--env FOO=1 --env CLAUDE_CONFIG_DIR=/acct/one --env ROTA_SLOT=w1 --env ROTA_PORT_BASE=[0-9]* --env ROTA_PORT_BLOCK=[0-9]* --env ROTA_DB_SUFFIX=_w1$' "$FAKE/log" \
  || fail "leading env assignments in workerCommand must become tab --env; log: $(cat "$FAKE/log")"
grep -q -- '-- --model haiku --settings s.json$' "$FAKE/log" \
  || fail "workerCommand args must pass through to agent start; log: $(cat "$FAKE/log")"
printf '{"work":{"dispatch":"herdr","workerCommand":"my-wrapper --x"}}\n' > "$TMP_HD/repo/.rota/config.json"
RC=0
hd "$ROTA_BIN" worker dispatch w1 --body-file "$TMP_HD/brief.md" >/dev/null 2>&1 || RC=$?
[ "$RC" = "5" ] || fail "a workerCommand that does not run claude should exit 5 under herdr, got $RC"
cp "$TMP_HD/config.bak" "$TMP_HD/repo/.rota/config.json"
pass "worker dispatch: outside herdr refused; workerCommand env + args map onto tab/agent start"

# ── (c) startup dialogs ─────────────────────────────────────────────────────
# Bypass Permissions puts "No, exit" first; folder trust puts "Yes" first. A
# fixed `down enter` picks "No, exit" on the trust prompt and kills the worker.
printf 'WARNING: Claude Code running in Bypass Permissions mode\n ❯ 1. No, exit\n   2. Yes, I accept\n' > "$TMP_HD/bypass.txt"
printf 'Pick a colour\n ❯ 1. Red\n   2. Blue\n' > "$TMP_HD/other.txt"

touch "$FAKE/start_not_ready"
cp "$TMP_HD/bypass.txt" "$FAKE/pane.txt"
: >"$FAKE/log"
hd "$ROTA_BIN" worker dispatch w1 --body-file "$TMP_HD/brief.md" >/dev/null \
  || fail "dispatch through a startup dialog failed"
grep -q '^agent send-keys rota-w1-w9-t1[0-9] down enter$' "$FAKE/log" \
  || fail "dispatch did not answer the bypass dialog from the pane; log: $(cat "$FAKE/log")"
# Claude Code v2.1.288's real folder-trust dialog: unnumbered, cursor on "No, exit" (#209).
cp "$REPO/internal/host/testdata/trust-dialog-2.1.288.txt" "$FAKE/pane.txt"
: > "$FAKE/log"
hd "$ROTA_BIN" worker dispatch w1 --body-file "$TMP_HD/brief.md" >/dev/null \
  || fail "dispatch through the unnumbered trust dialog should succeed: $(cat "$FAKE/log")"
grep -q "agent send-keys rota-w1-w9-t[0-9]* down enter" "$FAKE/log" \
  || fail "the unnumbered trust dialog is answered with down + enter: $(grep send-keys "$FAKE/log")"
cp "$TMP_HD/other.txt" "$FAKE/pane.txt"
RC=0
hd "$ROTA_BIN" worker dispatch w1 --body-file "$TMP_HD/brief.md" >/dev/null 2>&1 || RC=$?
[ "$RC" = "5" ] || fail "dispatch stuck on an unknown startup dialog should exit 5, got $RC"
rm -f "$FAKE/start_not_ready"
: >"$FAKE/pane.txt"
pass "startup dialogs are answered by reading the pane, unknown ones refused"

# ── (d) prompt errors ───────────────────────────────────────────────────────
# The refused dialog above left the slot without a session (its handle is
# cleared), so start one for the relays below to land in.
hd "$ROTA_BIN" worker dispatch w1 --body-file "$TMP_HD/brief.md" >/dev/null \
  || fail "re-dispatch after a failed spawn did not start a session"
echo agent_blocked > "$FAKE/prompt_error"
RC=0
hd "$ROTA_BIN" worker dispatch w1 --body-file "$TMP_HD/brief.md" --relay >/dev/null 2>&1 || RC=$?
[ "$RC" = "5" ] || fail "agent_blocked should exit 5 (dialog up, nothing sent), got $RC"
echo agent_prompt_stalled > "$FAKE/prompt_error"
RC=0
hd "$ROTA_BIN" worker dispatch w1 --body-file "$TMP_HD/brief.md" --relay >/dev/null 2>&1 || RC=$?
[ "$RC" = "6" ] || fail "agent_prompt_stalled should exit 6 (never picked up, safe to resend), got $RC"
rm -f "$FAKE/prompt_error"
pass "worker dispatch maps agent_blocked to exit 5 and agent_prompt_stalled to exit 6"

# ── (e) poll ────────────────────────────────────────────────────────────────
FX="$TMP_HD/fx"
mkdir -p "$FX"
: > "$FX/plain.txt"
printf 'ROTA-BLOCKED w1: Should the cache be per user?\n' > "$FX/blocked.txt"
printf 'ROTA-DONE w1 https://github.com/o/r/pull/9\n' > "$FX/done.txt"
printf 'API Error: 529 Overloaded\n' > "$FX/dead.txt"
printf "You've reached your usage limit\n" > "$FX/limited.txt"
check_map() {
  local fx="$1" status="$2" want="$3" got
  got="$( cd "$TMP_HD/repo" && ROTA_TEST_POLL_FIXTURE="$FX/$fx" ROTA_TEST_POLL_STATUS="$status" \
          "$ROTA_BIN" --json worker poll w1 | jget 'data.slots[0].state' )"
  [ "$got" = "$want" ] || fail "herdr $status + $fx: expected $want, got $got"
}
check_map plain.txt   working busy
check_map plain.txt   blocked needs-permission
check_map blocked.txt blocked blocked
check_map done.txt    idle    done
check_map done.txt    working done
check_map plain.txt   idle    idle
check_map plain.txt   done    idle
check_map plain.txt   unknown unknown
check_map dead.txt    unknown dead
check_map plain.txt   gone    dead
check_map limited.txt working limited
pass "worker poll maps herdr agent states (sentinels win; unknown is never done)"

echo blocked > "$FAKE/status"
: >"$FAKE/log"
hd "$ROTA_BIN" --json worker poll w1 --settle 0 >/dev/null || fail "live herdr poll failed"
[ "$(slot_field w1 state)" = "needs-permission" ] || fail "poll did not write slot.state, got $(slot_field w1 state)"
[ "$(grep -c '^notification show' "$FAKE/log")" = "1" ] || fail "a newly blocked slot should notify once"
: >"$FAKE/log"
hd "$ROTA_BIN" --json worker poll w1 --settle 0 >/dev/null || fail "second live herdr poll failed"
if grep -q '^notification show' "$FAKE/log"; then
  fail "a slot that stays blocked must not re-notify on every poll"
fi
echo idle > "$FAKE/status"
printf 'ROTA-DONE w1 rota-worker/w1\n' > "$FAKE/pane.txt"
hd "$ROTA_BIN" --json worker poll w1 --settle 0 >/dev/null
[ "$(slot_field w1 pr)" = "None" ] || fail "a branch name in ROTA-DONE must not become slot.pr"
printf 'ROTA-DONE w1 https://github.com/o/r/pull/9\n' > "$FAKE/pane.txt"
hd "$ROTA_BIN" --json worker poll w1 --settle 0 >/dev/null
[ "$(slot_field w1 state)" = "done" ] || fail "poll did not write state=done"
[ "$(slot_field w1 pr)" = "https://github.com/o/r/pull/9" ] || fail "poll did not record the PR URL in slot.pr"
# Claude Code v2.1.288 starts a reply with "● ": a sentinel after it is still seen (#210).
printf '● ROTA-DONE w1 https://github.com/o/r/pull/10\n' > "$FAKE/pane.txt"
hd "$ROTA_BIN" --json worker poll w1 --settle 0 >/dev/null
[ "$(slot_field w1 pr)" = "https://github.com/o/r/pull/10" ] || fail "ROTA-DONE after the reply bullet should be seen, pr is $(slot_field w1 pr)"
# Codex 0.159.x starts a reply with "• " (#68).
printf '\342\200\242 ROTA-DONE w1 https://github.com/o/r/pull/11\n\n  Worked for 21s \342\200\242 5:40 AM\n' > "$FAKE/pane.txt"
hd "$ROTA_BIN" --json worker poll w1 --settle 0 >/dev/null
[ "$(slot_field w1 pr)" = "https://github.com/o/r/pull/11" ] || fail "ROTA-DONE after the codex bullet should be seen, pr is $(slot_field w1 pr)"
touch "$FAKE/gone"
STATE="$( hd "$ROTA_BIN" --json worker poll w1 --settle 0 | jget 'data.slots[0].state' )"
[ "$STATE" = "dead" ] || fail "a slot whose agent is gone should poll dead, got $STATE"
rm -f "$FAKE/gone" "$FAKE/status"
: >"$FAKE/pane.txt"
pass "worker poll writes slot.state and slot.pr, notifies once per transition"

# ── (f) session ─────────────────────────────────────────────────────────────
OUT="$(hd "$ROTA_BIN" --json worker session check)" || fail "session check inside herdr should exit 0"
[ "$(jget data.inside <<<"$OUT")" = "true" ] || fail "session check inside herdr should report inside, got '$OUT'"
[ "$(jget data.where <<<"$OUT")" = "herdr workspace w9" ] || fail "session check inside herdr reported '$OUT'"
RC=0
( cd "$TMP_HD/repo" && env -u HERDR_ENV TMUX=/tmp/fake,1,0 "$ROTA_BIN" --json worker session check ) >/dev/null 2>&1 || RC=$?
[ "$RC" = "1" ] || fail "under herdr, being in tmux is not being in herdr (expected exit 1, got $RC)"
RC=0
( cd "$TMP_HD/repo" && PATH="$FAKE/bin:$PATH" env -u HERDR_ENV "$ROTA_BIN" worker session ensure ) >/dev/null 2>&1 || RC=$?
[ "$RC" = "4" ] || fail "ensure outside herdr should refuse with exit 4, got $RC"
hd "$ROTA_BIN" worker session ensure >/dev/null || fail "ensure inside herdr should be a no-op exit 0"
pass "worker session keys on HERDR_ENV; ensure refuses outside herdr"

# ── (g) a workspace id with an uppercase letter (#204) ─────────────────────
# Real ids are mixed case (w1W); herdr takes only [a-z][a-z0-9_-]{0,31}, and so
# does the fake, so a name built from the raw id fails at agent start.
: > "$FAKE/log"
( cd "$TMP_HD/repo" && PATH="$FAKE/bin:$PATH" FAKE_HERDR="$FAKE" HERDR_ENV=1 HERDR_WORKSPACE_ID=w1W \
    "$ROTA_BIN" worker dispatch w2 --body-file "$TMP_HD/brief.md" --task T9 >/dev/null 2>&1 ) \
  || fail "dispatch in an uppercase workspace should start the agent: $(cat "$FAKE/log")"
case "$(slot_field w2 handle)" in w1W:t*) ;; *) fail "the handle keeps the real tab id, got $(slot_field w2 handle)" ;; esac
NAME="$(awk '$1=="agent" && $2=="start" {print $3}' "$FAKE/log")"
[[ "$NAME" =~ ^[a-z][a-z0-9_-]{0,31}$ ]] || fail "the agent name herdr gets must be valid, got '$NAME'"
pass "worker dispatch in an uppercase workspace starts an agent with a valid name"

trap 'rm -rf "$TMP"' EXIT
pass "worker verbs herdr dispatch contract"
