echo "D3: rota limit watch|status, usage limits: sleep until the reset or switch accounts (#67)"

# Everything lives under $TMP_LM (a fresh mktemp -d), driven by FAKES only: gh is
# test/fakes/gh (state in a JSON store), tmux is test/fakes/tmux (logs its argv,
# one shared pane text), herdr is a stub binary plus a fake unix-socket server
# that speaks events.subscribe and can emit pane.output_matched, account meters
# are ROTA_ACCOUNT_USAGE_DIR fixtures, the clock is ROTA_TEST_NOW. Nothing starts a
# real claude, herdr or tmux, and nothing touches the network.
TMP_LM="$(mktemp -d)"
LMSRV=""; LMSUP=""; LMSLEEP=""
lm_cleanup() { # only the processes this section started
  for p in "$LMSRV" "$LMSUP" "$LMSLEEP"; do [ -n "$p" ] && kill "$p" 2>/dev/null || true; done
  rm -rf "${TMP_LM:?}"
}
trap lm_cleanup EXIT
FK="$TMP_LM/fake"
mkdir -p "$FK/bin" "$FK/tmux" "$FK/herdr" "$FK/usage"
printf '#!/usr/bin/env bash\nexec bash "%s/fakes/gh" "$@"\n' "$TESTDIR" > "$FK/bin/gh"
cp "$TESTDIR/fakes/tmux" "$FK/bin/tmux"
chmod +x "$FK/bin/gh" "$FK/bin/tmux"
FAKES="$FK/bin:$ROTA_POISON_BIN:$PATH"
NOW="2026-10-03T12:00:00Z"
LIMIT_MSG="Claude usage limit reached. Your limit will reset at 5pm."
PLAIN_MSG="You've hit your usage limit."
PROMPT="The usage limit has reset. Continue where you left off."
HOLDER=$$

ORIGIN="$TMP_LM/origin.git"; PROJ="$TMP_LM/proj"; REG="$PROJ/.rota/workers.json"
git init -q --bare "$ORIGIN"
mkdir -p "$PROJ/.rota"
(
  cd "$PROJ" && git init -q -b main . && git config user.email t@t && git config user.name t \
    && echo seed > seed.txt && git add seed.txt && git commit -q -m seed \
    && git remote add origin "$ORIGIN" && git push -q origin main
) || fail "D3 fixture repo setup failed"
printf 'stub worker contract\n' > "$TMP_LM/contract.md"
lm_cfg() { # lm_cfg [<extra top-level json members, with a leading comma>]
  printf '{"backlog":{"backend":"issues"},"issues":{"provider":"github","retryWaitSeconds":0},"work":{"dispatch":"tmux","accounts":[{"name":"a","configDir":"%s/cfg-a"},{"name":"b","configDir":"%s/cfg-b"}]},"round":{"brief":"%s"}%s}\n' \
    "$TMP_LM" "$TMP_LM" "$TMP_LM/contract.md" "${1:-}" > "$PROJ/.rota/config.json"
}
lm_cfg
: > "$FK/tmux/log"
pane() { printf '%s\n' "$1" > "$FK/tmux/pane"; }
pane "Welcome to Claude Code"

# lm <rota args...>: rota from the project at $LMNOW (default $NOW), the holder stands in for the orchestrator.
lm() { ( cd "$PROJ" && env PATH="$FAKES" FAKE_TMUX="$FK/tmux" FAKE_TRACKER_DB="$TMP_LM/db.json" ROTA_ACCOUNT_USAGE_DIR="$FK/usage" \
  ROTA_TEST_NOW="${LMNOW:-$NOW}" ROTA_TEST_HOLDER_PID="$HOLDER" ROTA_HOST_KILL_WAIT=1 TZ=UTC "$@" ); }
hvlm() { lm "$ROTA_BIN" --json "$@" 2>/dev/null; }
watch_() { hvlm limit watch --timeout 1 --settle 0.1 "$@"; } # one short watch; exit code is the caller's to check
usage_fix() { # usage_fix <account> <utilization> <resets_at|null>
  local r="$3"; [ "$r" = null ] || r="\"$r\""
  printf '{"five_hour":{"utilization":%s,"resets_at":%s},"seven_day":{"utilization":10,"resets_at":null}}\n' "$2" "$r" > "$FK/usage/$1.json"
}
lim() { python3 -c '
import json, sys
d = json.load(open(sys.argv[1]))
l = [e for e in d.get("limits", []) if e["id"] == sys.argv[2]]
print((l[0].get(sys.argv[3], "") if l else "NOENTRY"))' "${LMREG:-$REG}" "$1" "$2"; }
nlim() { python3 -c 'import json,sys; print(len(json.load(open(sys.argv[1])).get("limits", [])))' "${LMREG:-$REG}"; }
reset_log() { python3 -c 'import json,sys; p=sys.argv[1]; d=json.load(open(p)); d.pop("limits",None); json.dump(d,open(p,"w"),indent=2)' "${LMREG:-$REG}"; }
reg() { python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); s=[x for x in d["slots"] if x["name"]==sys.argv[2]][0]; v=s.get(sys.argv[3]); print("" if v is None else v)' "$REG" "$1" "$2"; }
set_slot() { python3 -c 'import json,sys; p=sys.argv[1]; d=json.load(open(p)); [s.__setitem__(sys.argv[3], json.loads(sys.argv[4])) for s in d["slots"] if s["name"]==sys.argv[2]]; json.dump(d,open(p,"w"),indent=2)' "$REG" "$1" "$2" "$3"; }
dbq() { python3 -c '
import json, sys
n = int(sys.argv[1])
issue = next(i for i in json.load(open(sys.argv[2]))["issues"] if i["number"] == n)
print("\n=====\n".join(c["body"] for c in issue["comments"]))' "$1" "$TMP_LM/db.json"; }
sent() { grep -c -F -- "-l -- $PROMPT" "$FK/tmux/log" || true; } # resume prompts typed into any pane

# Safety: gh and tmux must be the fakes, claude is never named.
[ "$(lm sh -c 'command -v gh')" = "$FK/bin/gh" ] || fail "D3: gh must resolve to the fake"
[ "$(lm sh -c 'command -v tmux')" = "$FK/bin/tmux" ] || fail "D3: tmux must resolve to the fake"

# --- usage, and refusals before any wait ---------------------------------------------
for A in "x" "--timeout -1" "--settle 0"; do
  RC=0; lm "$ROTA_BIN" limit watch $A >/dev/null 2>&1 || RC=$?
  [ "$RC" = "2" ] || fail "D3: 'limit watch $A' must exit 2, got $RC"
done
RC=0; (cd "$TMP_LM" && "$ROTA_BIN" limit watch >/dev/null 2>&1) || RC=$?
[ "$RC" = "3" ] || fail "D3: watch outside a project must exit 3, got $RC"
RC=0; (cd "$TMP_LM" && "$ROTA_BIN" limit status >/dev/null 2>&1) || RC=$?
[ "$RC" = "3" ] || fail "D3: status outside a project must exit 3, got $RC"
RC=0; OUT="$(hvlm limit watch)" || RC=$?
[ "$RC" = "4" ] && [ "$(jget data.blockedBy <<<"$OUT")" = "no round" ] && [ "$(jget data.changed <<<"$OUT")" = "false" ] || fail "D3: watch without the lease must exit 4 'no round', rc=$RC: $OUT"
OUT="$(hvlm limit status)"
[ "$(jget data.watching <<<"$OUT")" = "false" ] && [ "$(jget data.limits <<<"$OUT")" = "[]" ] || fail "D3: status before any limit: $OUT"
pass "D3: usage errors exit 2 or 3, watch without the lease exits 4 'no round' before any wait, status starts empty"

# --- a started round: ben holds issue 1 on account a, dana is idle on account b --------
printf '## Acceptance\n- [ ] works\n' > "$TMP_LM/body.md"
for T in "Wire the parser" "Second thing" "Escalation thread"; do
  lm "$ROTA_BIN" --json item create --kind tasks --title "$T" --body-file "$TMP_LM/body.md" >/dev/null 2>&1 || fail "D3: could not create issue $T"
done
usage_fix a 5 null; usage_fix b 30 null
OUT="$(hvlm round start --holder-pid "$HOLDER" --scope slate --items 1,2,3 --slots 2)" || fail "D3: round start failed: $OUT"
OUT="$(hvlm round assign 1 --agent ben --holder-pid "$HOLDER")" || fail "D3: assign 1 to ben failed: $OUT"
hvlm worker account assign dana --account b >/dev/null || fail "D3: could not put dana on account b"
[ "$(reg ben account)" = "a" ] && [ "$(reg dana account)" = "b" ] && [ "$(reg ben task)" = "1" ] && [ -z "$(reg dana task)" ] || fail "D3: fixture: $(cat "$REG")"
CD="$(git -C "$PROJ" rev-parse --path-format=absolute --git-common-dir)"

# --- sleep mode: a slot's limit waits for the meter's reset, then resumes --------------
lm_cfg ',"limits":{"mode":"sleep"}'
usage_fix a 100 "2026-10-03T14:00:00+00:00"; usage_fix b 10 null
pane "$LIMIT_MSG"
RC=0; OUT="$(watch_)" || RC=$?
[ "$RC" = "0" ] && [ "$(jget data.waiting <<<"$OUT")" = "1" ] && [ "$(jget data.changed <<<"$OUT")" = "true" ] || fail "D3: sleep mode: rc=$RC $OUT"
[ "$(lim l1 session)" = "ben" ] && [ "$(lim l1 source)" = "data" ] && [ "$(lim l1 window)" = "five_hour" ] && [ "$(lim l1 resetsAt)" = "2026-10-03T14:00:00Z" ] || fail "D3: the entry must take the meter's reset: $(cat "$REG")"
[ "$(lim l1 status)" = "waiting" ] && [ "$(lim l1 action)" = "sleep" ] && [ "$(lim l1 account)" = "a" ] || fail "D3: sleep entry: $(cat "$REG")"
case "$(lim l1 note)" in *"limits.mode is sleep"*) ;; *) fail "D3: the entry must say why it sleeps: $(lim l1 note)" ;; esac
[ "$(sent)" = "0" ] && [ "$(reg ben task)" = "1" ] || fail "D3: nothing must be typed or moved before the reset"
OUT="$(hvlm round status)"
[ "$(jget 'data.limits[0].id' <<<"$OUT")" = "l1" ] && [ "$(jget 'data.limits[0].status' <<<"$OUT")" = "waiting" ] || fail "D3: round status must list the waiting limit: $OUT"
OUT="$(hvlm round reconcile)"
[ "$(jget 'data.limits[0].id' <<<"$OUT")" = "l1" ] || fail "D3: round reconcile must list the waiting limit: $OUT"
OUT="$(hvlm limit status)"
[ "$(jget 'data.limits[0].id' <<<"$OUT")" = "l1" ] && [ "$(jget data.watching <<<"$OUT")" = "false" ] || fail "D3: status reads the log back: $OUT"
LMNOW="2026-10-03T14:00:30Z" watch_ >/dev/null || true # past the reset, inside the margin
[ "$(sent)" = "0" ] && [ "$(lim l1 status)" = "waiting" ] || fail "D3: no prompt before the reset plus the margin"
LMNOW="2026-10-03T14:01:30Z"
RC=0; OUT="$(watch_)" || RC=$?
[ "$RC" = "0" ] && [ "$(jget data.resumed <<<"$OUT")" = "1" ] && [ "$(jget data.waiting <<<"$OUT")" = "0" ] || fail "D3: resume: rc=$RC $OUT"
[ "$(sent)" = "1" ] && grep -q -F -- "send-keys -t rota:ben -l -- $PROMPT" "$FK/tmux/log" || fail "D3: the prompt must be typed once into ben's pane: $(grep send-keys "$FK/tmux/log")"
[ "$(lim l1 status)" = "resumed" ] && [ "$(lim l1 cycles)" = "1" ] && [ "$(lim l1 resolvedAt)" = "2026-10-03T14:01:30Z" ] || fail "D3: resumed entry: $(cat "$REG")"
pass "D3: sleep mode logs a waiting entry with the meter's reset, types nothing early, then types the prompt once and marks it resumed"

# --- switch mode: the issue moves to an idle slot on the other account -----------------
reset_log; lm_cfg
usage_fix a 100 "2026-10-03T14:00:00+00:00"; usage_fix b 10 null
unset LMNOW
: > "$FK/tmux/log"; pane "Welcome to Claude Code
$LIMIT_MSG" # booted enough for the receiver's session to come up, and limited
RC=0; OUT="$(watch_)" || RC=$?
[ "$RC" = "0" ] && [ "$(jget data.switched <<<"$OUT")" = "1" ] && [ "$(jget data.waiting <<<"$OUT")" = "0" ] || fail "D3: switch: rc=$RC $OUT"
[ "$(lim l1 status)" = "switched" ] && [ "$(lim l1 action)" = "switch" ] && [ "$(lim l1 account)" = "a" ] && [ "$(lim l1 to)" = "dana" ] && [ "$(lim l1 session)" = "ben" ] || fail "D3: switched entry: $(cat "$REG")"
[ -n "$(lim l1 resolvedAt)" ] && [ "$(nlim)" = "1" ] || fail "D3: one resolved entry: $(cat "$REG")"
[ "$(reg dana task)" = "1" ] && [ -z "$(reg ben task)" ] || fail "D3: the issue must move to dana: $(cat "$REG")"
[ "$(dbq 1 | grep -c -F '**rota handoff** (transfer, from ben)')" = "1" ] || fail "D3: transfer must run once, with its handoff comment: $(dbq 1)"
RC=0; OUT="$(watch_)" || RC=$?
[ "$RC" = "0" ] && [ "$(nlim)" = "1" ] || fail "D3: ben is idle now, so its pane's old message starts nothing: $(cat "$REG")"
[ "$(dbq 1 | grep -c -F '**rota handoff** (transfer, from ben)')" = "1" ] || fail "D3: a second pass must not transfer again"
pass "D3: a cooling account with a free second one and an idle slot on it moves the issue once, entry switched with account and to"

# --- switch impossible: no idle slot on the other account, or no usable account --------
reset_log
pane "Welcome to Claude Code"
usage_fix a 5 null; usage_fix b 100 "2026-10-03T14:00:00+00:00"
OUT="$(hvlm round assign 2 --agent ben --holder-pid "$HOLDER")" || fail "D3: assign 2 to ben failed: $OUT"
[ "$(reg ben account)" = "a" ] || fail "D3: ben must keep account a: $(cat "$REG")"
pane "$LIMIT_MSG"
RC=0; OUT="$(watch_)" || RC=$?
[ "$RC" = "0" ] && [ "$(nlim)" = "1" ] || fail "D3: only dana is limited (ben's meter is free): rc=$RC $(cat "$REG")"
[ "$(lim l1 session)" = "dana" ] && [ "$(lim l1 status)" = "waiting" ] && [ "$(lim l1 action)" = "sleep" ] && [ "$(lim l1 account)" = "b" ] || fail "D3: no idle slot on a means sleep: $(cat "$REG")"
case "$(lim l1 note)" in *"No idle slot on account a"*) ;; *) fail "D3: the entry must say why: $(lim l1 note)" ;; esac
[ "$(reg dana task)" = "1" ] || fail "D3: nothing must move"
reset_log
usage_fix a 100 "2026-10-03T14:00:00+00:00"
watch_ >/dev/null || true
[ "$(nlim)" = "2" ] || fail "D3: both slots are limited now: $(cat "$REG")"
[ "$(lim l1 action)" = "sleep" ] && [ "$(lim l2 action)" = "sleep" ] || fail "D3: no usable account means sleep: $(cat "$REG")"
case "$(lim l1 note)" in *"No usable account"*) ;; *) fail "D3: the entry must say why: $(lim l1 note)" ;; esac
[ "$(reg dana task)" = "1" ] && [ "$(reg ben task)" = "2" ] || fail "D3: nothing must move"
pass "D3: with no idle slot on the second account, or no usable account, the same limit sleeps and the entry says why"

# --- text with no data: a parsed reset, a fallback, and maxResumes ---------------------
rm -f "$FK/usage/a.json" "$FK/usage/b.json" # no meter at all
reset_log
set_slot dana handle null # one session only
lm_cfg ',"limits":{"mode":"sleep","maxResumes":2},"orchestrator":{"escalateIssue":3}'
pane "$PLAIN_MSG"
watch_ >/dev/null || true
[ "$(lim l1 session)" = "ben" ] && [ "$(lim l1 source)" = "text" ] && [ "$(lim l1 window)" = "unknown" ] && [ "$(lim l1 resetsAt)" = "2026-10-03T12:30:00Z" ] || fail "D3: text without a time falls back to limits.fallbackSleepSeconds: $(cat "$REG")"
: > "$FK/tmux/log"
LMNOW="2026-10-03T12:31:30Z" watch_ >/dev/null || true
[ "$(sent)" = "1" ] && [ "$(lim l1 cycles)" = "1" ] && [ "$(lim l1 status)" = "waiting" ] || fail "D3: the static pane is still limited after the prompt, so a new cycle starts: $(cat "$REG")"
[ "$(lim l1 resetsAt)" = "2026-10-03T13:01:30Z" ] || fail "D3: the next cycle sleeps the fallback again: $(cat "$REG")"
LMNOW="2026-10-03T13:03:00Z"
RC=0; OUT="$(watch_)" || RC=$?
[ "$RC" = "1" ] && [ "$(jget data.failed <<<"$OUT")" = "1" ] || fail "D3: past maxResumes the run must exit 1 with a failed entry, rc=$RC: $OUT"
[ "$(sent)" = "2" ] && [ "$(lim l1 status)" = "failed" ] && [ "$(lim l1 cycles)" = "2" ] || fail "D3: failed entry after two prompts: $(cat "$REG")"
case "$(lim l1 note)" in *"still limited after 2 resume"*"Escalation e1"*) ;; *) fail "D3: the note must name the failure and the escalation: $(lim l1 note)" ;; esac
case "$(dbq 3)" in *"Usage limit not resolved: ben"*"rota:escalation e1"*) ;; *) fail "D3: the escalation comment is missing on issue 3: $(dbq 3)" ;; esac
RC=0; OUT="$(LMNOW="2026-10-03T13:04:00Z" watch_)" || RC=$?
[ "$RC" = "0" ] && [ "$(nlim)" = "1" ] || fail "D3: a failed session is left alone, rc=$RC $(cat "$REG")"
unset LMNOW
# the same with no thread to post on: a host notification only, and a warning
reset_log; lm_cfg ',"limits":{"mode":"sleep","maxResumes":1}'
pane "$PLAIN_MSG"
watch_ >/dev/null || true
RC=0; OUT="$(LMNOW="2026-10-03T12:31:30Z" watch_)" || RC=$?
[ "$RC" = "1" ] && [ "$(lim l1 status)" = "failed" ] || fail "D3: maxResumes 1 fails after one prompt: rc=$RC $(cat "$REG")"
case "$OUT" in *"escalateIssue is unset"*) ;; *) fail "D3: the unset thread must raise a warning: $OUT" ;; esac
[ "$(dbq 3 | grep -c -F 'rota:escalation')" = "1" ] || fail "D3: no second escalation comment without escalateIssue: $(dbq 3)"
pass "D3: a text-only limit sleeps the fallback, maxResumes fails the entry and escalates, or warns when escalateIssue is unset"

# --- the orchestrator: data first, never switches, then text -----------------------------
reset_log; lm_cfg
set_slot ben handle null
RESETS_EPOCH="$(python3 -c 'import calendar,time; print(calendar.timegm(time.strptime("2026-10-03T12:01:30Z","%Y-%m-%dT%H:%M:%SZ")))')"
printf '{"session_id":"orch1","cwd":"%s","rate_limits":{"five_hour":{"used_percentage":100,"resets_at":%s},"seven_day":{"used_percentage":20,"resets_at":%s}}}' "$PROJ" "$RESETS_EPOCH" "$((RESETS_EPOCH + 86400))" \
  | env ROTA_TEST_NOW="$NOW" "$ROTA_BIN" statusline dump || fail "D3: statusline dump failed"
pane "Welcome to Claude Code"; : > "$FK/tmux/log"
OUT="$(lm env TMUX_PANE=%9 "$ROTA_BIN" --json limit watch --timeout 1 --settle 0.1 2>/dev/null)" || fail "D3: orchestrator data watch failed: $OUT"
[ "$(jget data.waiting <<<"$OUT")" = "1" ] && [ "$(lim l1 session)" = "orchestrator" ] && [ "$(lim l1 source)" = "data" ] && [ "$(lim l1 window)" = "five_hour" ] || fail "D3: the orchestrator's data entry: $(cat "$REG")"
[ "$(lim l1 resetsAt)" = "2026-10-03T12:01:30Z" ] && [ "$(lim l1 action)" = "sleep" ] || fail "D3: resetsAt must be the window's resets_at: $(cat "$REG")"
case "$(lim l1 note)" in *"never switches"*) ;; *) fail "D3: the orchestrator never switches: $(lim l1 note)" ;; esac
[ "$(sent)" = "0" ] || fail "D3: no prompt before the reset"
LMNOW="2026-10-03T12:02:31Z"
OUT="$(lm env TMUX_PANE=%9 "$ROTA_BIN" --json limit watch --timeout 1 --settle 0.1 2>/dev/null)" || fail "D3: orchestrator resume failed: $OUT"
[ "$(sent)" = "1" ] && grep -q -F -- "send-keys -t %9 -l -- $PROMPT" "$FK/tmux/log" || fail "D3: one prompt into the orchestrator's pane after reset plus margin: $(grep send-keys "$FK/tmux/log")"
[ "$(lim l1 status)" = "resumed" ] || fail "D3: orchestrator entry resumed: $(cat "$REG")"
unset LMNOW
# no rateLimits and no meter: only the text in the pane
reset_log
python3 -c 'import os,sys; os.remove(sys.argv[1])' "$CD/rota/session/orch1.json"
pane "$LIMIT_MSG"; : > "$FK/tmux/log"
OUT="$(lm env TMUX_PANE=%9 "$ROTA_BIN" --json limit watch --timeout 1 --settle 0.1 2>/dev/null)" || fail "D3: orchestrator text watch failed: $OUT"
[ "$(lim l1 source)" = "text" ] && [ "$(lim l1 resetsAt)" = "2026-10-03T17:00:00Z" ] && [ "$(lim l1 session)" = "orchestrator" ] || fail "D3: the reset must be parsed from 'reset at 5pm': $(cat "$REG")"
pane "Welcome to Claude Code" # the session is answering again by the time the reset comes
LMNOW="2026-10-03T17:01:30Z"
OUT="$(lm env TMUX_PANE=%9 "$ROTA_BIN" --json limit watch --timeout 1 --settle 0.1 2>/dev/null)" || fail "D3: orchestrator text resume failed: $OUT"
[ "$(sent)" = "1" ] && [ "$(lim l1 status)" = "resumed" ] || fail "D3: the text entry resumes the same way: $(cat "$REG") $(grep send-keys "$FK/tmux/log")"
unset LMNOW
pass "D3: the orchestrator sleeps on its session file's rate limits, never switches, and a pane-only message gets a parsed reset and the same resume"

# --- herdr: pane.output_matched over the socket, the poll fallback, the version pin -----
cat > "$FK/bin/herdr" <<'SH'
#!/usr/bin/env bash
F="$FAKE_HERDR"
printf '%s\n' "$*" >>"$F/log"
case "$1 $2" in
  "--version "*|"--version") echo "herdr $(cat "$F/version" 2>/dev/null || echo 0.9.3)" ;;
  "notification show") echo '{"id":"cli","result":{"type":"notification_show","shown":true,"reason":"shown"}}' ;;
  *) echo '{"error":{"code":"unknown_method","message":"fake"}}' >&2; exit 1 ;;
esac
SH
chmod +x "$FK/bin/herdr"
cat > "$FK/server.py" <<'PY'
import json, os, socket, sys, threading, time
sock, fk = sys.argv[1], sys.argv[2]
srv = socket.socket(socket.AF_UNIX); srv.bind(sock); srv.listen(8)
MSG = "work\nClaude usage limit reached. Your limit will reset at 5pm.\n"
def flag(n): return os.path.exists(os.path.join(fk, n))
def read(pane, text):
    return {"pane_id": pane, "workspace_id": "w9", "tab_id": "w9:t1", "source": "recent", "format": "text",
            "text": text, "revision": 3, "truncated": False}
def serve(c):
    f = c.makefile()
    while True:
        line = f.readline()
        if not line: return
        req = json.loads(line)
        m = req["method"]
        open(os.path.join(fk, m.replace(".", "_") + ".json"), "a").write(json.dumps(req) + "\n")
        out = lambda o: c.sendall((json.dumps(o) + "\n").encode())
        if m == "events.subscribe":
            if flag("reject"):
                out({"id": req["id"], "error": {"code": "invalid_request", "message": "unsupported regex"}}); return
            pane = req["params"]["subscriptions"][0]["pane_id"]
            if flag("already"):
                out({"id": req["id"], "result": {"type": "output_matched", "pane_id": pane, "revision": 3,
                     "matched_line": "Claude usage limit reached.", "read": read(pane, MSG)}})
            else:
                out({"id": req["id"], "result": {"type": "subscription_started"}})
            if flag("emit"):
                time.sleep(0.5)
                out({"event": "pane.output_matched", "data": {"pane_id": pane, "matched_line": "Claude usage limit reached.", "read": read(pane, MSG)}})
        elif m == "pane.send_input":
            out({"id": req["id"], "result": {"type": "ok"}})
        elif m == "pane.read":
            txt = open(os.path.join(fk, "pane.txt")).read() if flag("pane.txt") else ""
            out({"id": req["id"], "result": {"type": "pane_read", "read": read(req["params"]["pane_id"], txt)}})
        else:
            out({"id": req["id"], "error": {"code": "unknown_method", "message": m}}); return
while True:
    c, _ = srv.accept()
    threading.Thread(target=serve, args=(c,), daemon=True).start()
PY
python3 "$FK/server.py" "$FK/herdr.sock" "$FK/herdr" >/dev/null 2>&1 &
LMSRV=$!
for _ in $(seq 50); do [ -S "$FK/herdr.sock" ] && break; sleep 0.1; done
[ -S "$FK/herdr.sock" ] || fail "D3: fake herdr socket never came up"

HP="$TMP_LM/hproj"; HREG="$HP/.rota/workers.json"
mkdir -p "$HP/.rota"
( cd "$HP" && git init -q -b main . && git -c user.email=a@b -c user.name=n commit -q --allow-empty -m init \
    && printf '{"git":{"baseBranch":"main"},"work":{"dispatch":"herdr"}}\n' > .rota/config.json ) || fail "D3: herdr fixture setup failed"
HCD="$(git -C "$HP" rev-parse --path-format=absolute --git-common-dir)"
sleep 300 >/dev/null 2>&1 & LMSLEEP=$!
mkdir -p "$HCD/rota"
python3 - "$HCD/rota/round-lease.json" "$LMSLEEP" "$HP" <<'PY' || fail "D3: could not write the lease"
import json, socket, sys
pid = int(sys.argv[2])
start = int(open("/proc/%d/stat" % pid).read().rsplit(")", 1)[1].split()[19])
json.dump({"pid": pid, "start": start, "host": socket.gethostname(), "root": sys.argv[3], "round": 1,
           "startedAt": "2026-10-03T00:00:00Z"}, open(sys.argv[1], "w"))
PY
hlm() { ( cd "$HP" && env PATH="$FAKES" FAKE_HERDR="$FK/herdr" HERDR_ENV=1 HERDR_WORKSPACE_ID=w9 HERDR_PANE_ID=w9:p1 HERDR_SOCKET_PATH="$FK/herdr.sock" \
  ROTA_TEST_NOW="${LMNOW:-$NOW}" ROTA_TEST_HOLDER_PID="$LMSLEEP" TZ=UTC "$ROTA_BIN" --json "$@" 2>/dev/null ); }
LMREG="$HREG"
: > "$FK/herdr/emit"
RC=0; OUT="$(hlm limit watch --timeout 3 --settle 0.2)" || RC=$?
[ "$RC" = "0" ] && [ "$(jget data.waiting <<<"$OUT")" = "1" ] || fail "D3: herdr event: rc=$RC $OUT $(cat "$HREG" 2>/dev/null)"
[ "$(lim l1 source)" = "text" ] && [ "$(lim l1 session)" = "orchestrator" ] && [ "$(lim l1 resetsAt)" = "2026-10-03T17:00:00Z" ] || fail "D3: a pane.output_matched event makes a text entry with the parsed reset: $(cat "$HREG")"
REQ="$FK/herdr/events_subscribe.json"
[ "$(head -1 "$REQ" | jget method)" = "events.subscribe" ] && [ "$(head -1 "$REQ" | jget 'params.subscriptions[0].type')" = "pane.output_matched" ] \
  && [ "$(head -1 "$REQ" | jget 'params.subscriptions[0].pane_id')" = "w9:p1" ] && [ "$(head -1 "$REQ" | jget 'params.subscriptions[0].source')" = "recent" ] \
  && [ "$(head -1 "$REQ" | jget 'params.subscriptions[0].match.type')" = "regex" ] || fail "D3: the subscription: $(head -1 "$REQ")"
case "$(head -1 "$REQ" | jget 'params.subscriptions[0].match.value')" in "(?i)(?:"*"reached your usage limit"*"usage limit reached"*) ;; *) fail "D3: the regex must be the alternation of worker poll's phrases: $(head -1 "$REQ")" ;; esac
rm -f "$FK/herdr/emit"
LMNOW="2026-10-03T17:01:30Z"
OUT="$(hlm limit watch --timeout 1 --settle 0.2)" || fail "D3: herdr resume failed: $OUT"
[ "$(lim l1 status)" = "resumed" ] || fail "D3: herdr resume: $(cat "$HREG")"
SEND="$FK/herdr/pane_send_input.json"
[ "$(wc -l < "$SEND" | tr -d ' ')" = "1" ] && [ "$(jget params.text < "$SEND")" = "$PROMPT" ] && [ "$(jget params.pane_id < "$SEND")" = "w9:p1" ] && [ "$(jget 'params.keys[0]' < "$SEND")" = "enter" ] || fail "D3: the prompt goes through pane.send_input with enter: $(cat "$SEND")"
unset LMNOW
# the subscribe reply can itself be a match
reset_log; : > "$FK/herdr/already"; rm -f "$FK/herdr/events_subscribe.json"
OUT="$(hlm limit watch --timeout 2 --settle 0.2)" || fail "D3: herdr already-matching reply failed: $OUT"
[ "$(lim l1 source)" = "text" ] && [ "$(lim l1 status)" = "waiting" ] || fail "D3: an output_matched subscribe reply must count as a match: $(cat "$HREG")"
rm -f "$FK/herdr/already"
# a regex herdr refuses: warn and capture the pane instead
reset_log; : > "$FK/herdr/reject"; printf '%s\n' "$LIMIT_MSG" > "$FK/herdr/pane.txt"
OUT="$(hlm limit watch --timeout 2 --settle 0.2)" || fail "D3: the poll fallback failed: $OUT"
[ "$(lim l1 source)" = "text" ] && [ "$(lim l1 status)" = "waiting" ] || fail "D3: the fallback must find the message by capturing the pane: $(cat "$HREG")"
case "$OUT" in *"capturing the panes"*) ;; *) fail "D3: the fallback must be named in a warning: $OUT" ;; esac
rm -f "$FK/herdr/reject" "$FK/herdr/pane.txt"
# any other herdr is refused
echo 0.10.1 > "$FK/herdr/version"
RC=0; OUT="$(hlm limit watch --timeout 1 --settle 0.2)" || RC=$?
[ "$RC" = "5" ] || fail "D3: a herdr outside 0.9.x must exit 5, got $RC: $OUT"
echo 0.9.3 > "$FK/herdr/version"
kill "$LMSRV" 2>/dev/null || true; LMSRV=""
LMREG=""
pass "D3: on herdr a pane.output_matched event (or a matching subscribe reply) starts the handling, the prompt goes through pane.send_input, a refused regex falls back to capturing, and another herdr exits 5"

# --- the supervisor runs the loop in-process; watch is refused beside it ---------------
SP="$TMP_LM/sproj"; SREG="$SP/.rota/workers.json"
mkdir -p "$SP/.rota"
( cd "$SP" && git init -q -b main . && git -c user.email=a@b -c user.name=n commit -q --allow-empty -m init \
    && printf '{"git":{"baseBranch":"main"}}\n' > .rota/config.json \
    && printf '{"slots":[],"limits":[{"id":"l1","session":"orchestrator","window":"five_hour","source":"data","detectedAt":"2020-01-01T00:00:00Z","resetsAt":"2020-01-01T05:00:00Z","action":"sleep","status":"waiting","cycles":0}]}\n' > .rota/workers.json ) || fail "D3: supervisor fixture setup failed"
SCD="$(git -C "$SP" rev-parse --path-format=absolute --git-common-dir)"
: > "$FK/tmux/log"
slm() { ( cd "$SP" && exec env -u HERDR_ENV PATH="$FAKES" FAKE_TMUX="$FK/tmux" TMUX_PANE=%7 TZ=UTC "$@" ); }
( cd "$SP" && exec env -u HERDR_ENV PATH="$FAKES" FAKE_TMUX="$FK/tmux" TMUX_PANE=%7 TZ=UTC "$ROTA_BIN" keepalive run -- sleep 60 ) >"$TMP_LM/sup.out" 2>&1 &
LMSUP=$!
for _ in $(seq 100); do [ "$(sent)" != "0" ] && break; sleep 0.1; done
[ "$(sent)" = "1" ] && grep -q -F -- "send-keys -t %7 -l -- $PROMPT" "$FK/tmux/log" || fail "D3: the supervisor's own loop must resume the due entry in its pane: $(cat "$FK/tmux/log") $(cat "$TMP_LM/sup.out")"
LMREG="$SREG"
[ "$(lim l1 status)" = "resumed" ] || fail "D3: the supervisor's loop must record the resume: $(cat "$SREG")"
[ "$(jget mode < "$SCD/rota/limit-watch.json")" = "supervisor" ] && [ "$(jget pid < "$SCD/rota/limit-watch.json")" = "$LMSUP" ] || fail "D3: the loop must record itself: $(cat "$SCD/rota/limit-watch.json")"
OUT="$(slm "$ROTA_BIN" --json limit status 2>/dev/null)"
[ "$(jget data.watching <<<"$OUT")" = "true" ] || fail "D3: status must say watching under a supervisor: $OUT"
RC=0; OUT="$(slm "$ROTA_BIN" --json limit watch --timeout 1 2>/dev/null)" || RC=$?
[ "$RC" = "4" ] && [ "$(jget data.blockedBy <<<"$OUT")" = "supervised" ] || fail "D3: watch must be refused under a live supervisor, rc=$RC: $OUT"
kill -INT "$LMSUP"
RC=0; wait "$LMSUP" || RC=$?
LMSUP=""
[ "$RC" = "0" ] || fail "D3: the interrupted supervisor must exit 0, got $RC: $(cat "$TMP_LM/sup.out")"
[ ! -e "$SCD/rota/limit-watch.json" ] || fail "D3: the loop's record must go when the supervisor stops"
OUT="$(slm "$ROTA_BIN" --json limit status 2>/dev/null)"
[ "$(jget data.watching <<<"$OUT")" = "false" ] && [ "$(jget 'data.limits[0].status' <<<"$OUT")" = "resumed" ] || fail "D3: status after the supervisor: $OUT"
# --no-limits: no loop, no record, the due entry stays waiting
printf '{"slots":[],"limits":[{"id":"l1","session":"orchestrator","window":"five_hour","source":"data","detectedAt":"2020-01-01T00:00:00Z","resetsAt":"2020-01-01T05:00:00Z","action":"sleep","status":"waiting","cycles":0}]}\n' > "$SREG"
: > "$FK/tmux/log"
( cd "$SP" && exec env -u HERDR_ENV PATH="$FAKES" FAKE_TMUX="$FK/tmux" TMUX_PANE=%7 TZ=UTC "$ROTA_BIN" keepalive run --no-limits -- sleep 60 ) >"$TMP_LM/sup2.out" 2>&1 &
LMSUP=$!
for _ in $(seq 100); do [ -e "$SCD/rota/keepalive.json" ] && grep -q '"running"' "$SCD/rota/keepalive.json" && break; sleep 0.1; done
sleep 1
[ ! -e "$SCD/rota/limit-watch.json" ] && [ "$(sent)" = "0" ] && [ "$(lim l1 status)" = "waiting" ] || fail "D3: --no-limits must run no loop: $(cat "$FK/tmux/log")"
kill -INT "$LMSUP"; wait "$LMSUP" || true; LMSUP=""
LMREG=""
pass "D3: the supervisor runs the loop in-process (resumes, records itself, ends with it), watch is refused beside it, --no-limits runs none"

trap 'rm -rf "$TMP"' EXIT
lm_cleanup
