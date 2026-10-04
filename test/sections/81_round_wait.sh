echo "round wait — blocks on workers, returns the first slot that needs attention (#57)"
# rota round wait wakes on herdr's socket events (one subscription for all slots)
# or, on tmux, re-captures panes; either way the slot is classified by the same
# code as `rota worker poll`. Nothing here touches a live herdr or tmux: herdr is
# a FAKE binary on PATH plus a FAKE unix-socket server that speaks the subscribe
# protocol of herdr 0.9.x, tmux is test/fakes/tmux.

TMP_RW="$(mktemp -d)"
SRV=""
trap 'kill "$SRV" 2>/dev/null || true; rm -rf "${TMP_RW:?}"' EXIT
FK="$TMP_RW/fake"
mkdir -p "$FK/bin" "$FK/tmux"
cp "$TESTDIR/fakes/tmux" "$FK/bin/tmux"

# herdr: --version, `agent get` (pane id + status), `agent read` (pane text).
# The socket server flips `fired` just before it sends the status event, and
# the pane only reads ROTA-DONE once it exists, so a classification taken before
# the event sees a working agent and one taken after sees a finished one.
cat > "$FK/bin/herdr" <<'SH'
#!/usr/bin/env bash
F="$FAKE_HERDR"
printf '%s\n' "$*" >>"$F/log"
case "$1 $2" in
  "--version "*|"--version") echo "herdr $(cat "$F/version" 2>/dev/null || echo 0.9.3)" ;;
  "agent get")
    st=working; [ -f "$F/fired" ] && st=done
    printf '{"id":"cli","result":{"type":"agent_info","agent":{"agent_status":"%s","pane_id":"w9:p11","tab_id":"w9:t7"}}}\n' "$st" ;;
  "agent read")
    t="working"; [ -f "$F/fired" ] && t="ROTA-DONE w1 https://github.com/o/r/pull/7"
    printf '%s\n' "$t" ;;  # herdr 0.9.3 prints pane text as is, no JSON envelope
  *) echo '{"error":{"code":"unknown_method","message":"fake"}}' >&2; exit 1 ;;
esac
SH
chmod +x "$FK/bin/herdr"

cat > "$FK/server.py" <<'PY'
import json, os, socket, sys, time
sock, fk = sys.argv[1], sys.argv[2]
srv = socket.socket(socket.AF_UNIX); srv.bind(sock); srv.listen(4)
held = []  # a quiet subscription stays open, as herdr's does
while True:
    c, _ = srv.accept()
    held.append(c)
    req = json.loads(c.makefile().readline())
    open(os.path.join(fk, "request.json"), "w").write(json.dumps(req))
    c.sendall(b'{"id":"%s","result":{"type":"subscription_started"}}\n' % req["id"].encode())
    if os.path.exists(os.path.join(fk, "quiet")):
        continue
    time.sleep(1)
    if not os.path.exists(os.path.join(fk, "quiet")):
        open(os.path.join(fk, "fired"), "w").close()
    pane = req["params"]["subscriptions"][0]["pane_id"]
    try:  # the client may have returned from its snapshot and hung up
        c.sendall((json.dumps({"event": "pane.agent_status_changed", "data": {
            "pane_id": pane, "workspace_id": "w9", "agent_status": "done"}}) + "\n").encode())
    except OSError:
        pass
PY

mkdir -p "$TMP_RW/repo/.rota"
(
  cd "$TMP_RW/repo"
  git init -q -b main .
  git config user.email t@t
  git config user.name t
  echo seed > seed.txt
  git add seed.txt
  git commit -q -m seed
) || fail "round-wait fixture repo setup failed"

cfg() { printf '{"work":{"dispatch":"%s"}}\n' "$1" > "$TMP_RW/repo/.rota/config.json"; }
slots() { # slots <handle> <state>: a registry with w1 running, w2 parked
  printf '{"session":"rota","round":1,"slots":[{"name":"w1","branch":"b1","worktree":"%s/w1","base":"main","handle":"%s","state":"%s","task":"T1","relays":[]},{"name":"w2","branch":"b2","worktree":"%s/w2","base":"main","handle":"rota:w2","state":"idle","task":null,"relays":[]}]}\n' \
    "$TMP_RW" "$1" "$2" "$TMP_RW" > "$TMP_RW/repo/.rota/workers.json"
}
rw() { ( cd "$TMP_RW/repo" && PATH="$FK/bin:$PATH" FAKE_HERDR="$FK" FAKE_TMUX="$FK/tmux" HERDR_SOCKET_PATH="$FK/herdr.sock" "$@" ); }
snap() { sha256sum "$TMP_RW/repo/.rota/workers.json"; }

# ── herdr: wait for the socket event ────────────────────────────────────────
cfg herdr
slots w9:t7 busy
python3 "$FK/server.py" "$FK/herdr.sock" "$FK" >/dev/null 2>&1 &
SRV=$!
for _ in $(seq 50); do [ -S "$FK/herdr.sock" ] && break; sleep 0.1; done
[ -S "$FK/herdr.sock" ] || fail "fake herdr socket never came up"

BEFORE="$(snap)"
RC=0; OUT="$(rw hvj round wait --settle 0 --timeout 30 2>/dev/null)" || RC=$?
[ "$RC" = "0" ] || fail "herdr: wait must exit 0 once the slot finishes, got $RC: $OUT"
[ "$(jget data.slot <<<"$OUT")" = "w1" ] || fail "herdr: expected slot w1: $OUT"
[ "$(jget data.state <<<"$OUT")" = "done" ] || fail "herdr: expected state done: $OUT"
[ "$(jget data.source <<<"$OUT")" = "herdr-event" ] || fail "herdr: the slot must come back through the event, not the first snapshot: $OUT"
case "$(jget data.evidence <<<"$OUT")" in *"/pull/7") ;; *) fail "herdr: evidence must carry the ROTA-DONE argument: $OUT" ;; esac
[ "$(jget data.changed <<<"$OUT" 2>/dev/null || true)" = "" ] || fail "herdr: round wait is read-only, data must not report changed: $OUT"
[ "$(snap)" = "$BEFORE" ] || fail "herdr: round wait wrote .rota/workers.json"
[ "$(jget method < "$FK/request.json")" = "events.subscribe" ] || fail "herdr: expected an events.subscribe request: $(cat "$FK/request.json")"
[ "$(jget 'params.subscriptions[0].type' < "$FK/request.json")" = "pane.agent_status_changed" ] || fail "herdr: wrong subscription: $(cat "$FK/request.json")"
[ "$(jget 'params.subscriptions[0].pane_id' < "$FK/request.json")" = "w9:p11" ] || fail "herdr: must subscribe to the slot's pane: $(cat "$FK/request.json")"
[ "$(python3 -c 'import json,sys; print(len(json.load(open(sys.argv[1]))["params"]["subscriptions"]))' "$FK/request.json")" = "1" ] || fail "herdr: the parked idle slot must not be watched"
pass "round wait returns the slot that finished, through one herdr event subscription, writing nothing"

# A slot that already needs attention comes back at once from the snapshot.
RC=0; OUT="$(rw hvj round wait --settle 0 --timeout 30 2>/dev/null)" || RC=$?
[ "$RC" = "0" ] && [ "$(jget data.source <<<"$OUT")" = "snapshot" ] || fail "herdr: a finished slot must return from the snapshot, rc=$RC: $OUT"
pass "a slot that needs attention before the call returns immediately (snapshot)"

# ── herdr: timeout is an answer, other herdr versions are refused ───────────
rm "$FK/fired"; : >"$FK/quiet"
RC=0; OUT="$(rw hvj round wait --settle 0 --timeout 1 2>/dev/null)" || RC=$?
[ "$RC" = "1" ] || fail "herdr: a timeout must exit 1, got $RC: $OUT"
[ "$(jget data.timedOut <<<"$OUT")" = "true" ] || fail "herdr: timeout data must say timedOut: $OUT"
[ "$(jget 'data.slots[0].state' <<<"$OUT")" = "busy" ] || fail "herdr: timeout data must list the slot states: $OUT"
echo 0.10.1 > "$FK/version"
RC=0; OUT="$(rw hvj round wait --settle 0 --timeout 5 2>/dev/null)" || RC=$?
[ "$RC" = "5" ] || fail "herdr: an unsupported herdr version must exit 5, got $RC: $OUT"
echo 0.9.3 > "$FK/version"
pass "round wait times out with exit 1 and timedOut data, and refuses a herdr outside 0.9.x (exit 5)"

# ── usage and resolution ────────────────────────────────────────────────────
RC=0; rw hvj round wait --timeout -1 >/dev/null 2>&1 || RC=$?
[ "$RC" = "2" ] || fail "a negative --timeout must exit 2, got $RC"
RC=0; rw hvj round wait nope >/dev/null 2>&1 || RC=$?
[ "$RC" = "3" ] || fail "an unknown slot must exit 3, got $RC"
slots rota:w1 idle
RC=0; rw hvj round wait --settle 0 >/dev/null 2>&1 || RC=$?
[ "$RC" = "3" ] || fail "a pool with only idle slots has nothing to watch (exit 3), got $RC"
pass "round wait: bad flag exits 2, unknown slot and nothing-to-watch exit 3"

# ── tmux: no events, classified from the pane ───────────────────────────────
cfg tmux
slots rota:w1 busy
printf 'ROTA-DONE w1 rota-worker/w1\n' > "$FK/tmux/pane"
RC=0; OUT="$(rw hvj round wait --settle 0 --timeout 30 2>/dev/null)" || RC=$?
[ "$RC" = "0" ] || fail "tmux: wait must exit 0, got $RC: $OUT"
[ "$(jget data.slot <<<"$OUT")" = "w1" ] && [ "$(jget data.state <<<"$OUT")" = "done" ] || fail "tmux: expected w1 done: $OUT"
[ "$(jget data.source <<<"$OUT")" = "snapshot" ] || fail "tmux: expected the snapshot source: $OUT"
pass "round wait on tmux classifies the pane through the same rules as worker poll"

kill "$SRV" 2>/dev/null || true
SRV=""
trap 'rm -rf "$TMP"' EXIT
rm -rf "${TMP_RW:?}"
