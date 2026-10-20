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
# the event sees a working agent and one taken after sees a finished one. The
# server fires only after the client's first `agent get` since subscribing has
# finished (the `snapshot` marker: a snapshot reads the pane twice, then asks
# for the status), never after a fixed delay: a delay lets a loaded machine take
# the snapshot after the event.
cat > "$FK/bin/herdr" <<'SH'
#!/usr/bin/env bash
F="$FAKE_HERDR"
printf '%s\n' "$*" >>"$F/log"
case "$1 $2" in
  "--version "*|"--version") echo "herdr $(cat "$F/version" 2>/dev/null || echo 0.9.3)" ;;
  "agent get")
    st=working; [ -f "$F/fired" ] && st=done
    printf '{"id":"cli","result":{"type":"agent_info","agent":{"agent_status":"%s","pane_id":"w9:p11","tab_id":"w9:t7"}}}\n' "$st"
    : >"$F/snapshot" ;;
  "agent read")
    t="working"; [ -f "$F/fired" ] && t="ROTA-DONE w1 https://github.com/o/r/pull/7"
    # A loaded machine stalls the snapshot read; the lag file stands in for that, once.
    if [ -f "$F/lag" ]; then sleep "$(cat "$F/lag")"; rm -f "$F/lag"; fi
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
accepted = handled = 0
def mark(name, n):
    open(os.path.join(fk, name), "w").write(str(n))
def have(name):
    return os.path.exists(os.path.join(fk, name))
def serve(c, req):
    c.sendall(b'{"id":"%s","result":{"type":"subscription_started"}}\n' % req["id"].encode())
    if have("quiet"):
        return
    for _ in range(300):  # a client that never takes a snapshot still gets its event after 30s
        if have("snapshot") or have("quiet"):
            break
        time.sleep(0.1)
    if not have("quiet"):
        open(os.path.join(fk, "fired"), "w").close()
    pane = req["params"]["subscriptions"][0]["pane_id"]
    try:  # the client may have returned from its snapshot and hung up
        c.sendall((json.dumps({"event": "pane.agent_status_changed", "data": {
            "pane_id": pane, "workspace_id": "w9", "agent_status": "done"}}) + "\n").encode())
    except OSError:
        pass
while True:
    c, _ = srv.accept()
    held.append(c)
    req = json.loads(c.makefile().readline())
    open(os.path.join(fk, "request.json"), "w").write(json.dumps(req))
    if have("snapshot"):
        os.remove(os.path.join(fk, "snapshot"))
    accepted += 1; mark("accepted", accepted)
    serve(c, req)
    handled += 1; mark("handled", handled)
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

# ── herdr: wait for the socket event ────────────────────────────────────────
cfg herdr
slots w9:t7 busy
python3 "$FK/server.py" "$FK/herdr.sock" "$FK" >/dev/null 2>&1 &
SRV=$!
for _ in $(seq 50); do [ -S "$FK/herdr.sock" ] && break; sleep 0.1; done
[ -S "$FK/herdr.sock" ] || fail "fake herdr socket never came up"

echo 2 > "$FK/lag"  # the snapshot read outlasts any fixed delay the server could use before its event
RC=0; OUT="$(rw hvj round wait --settle 0 --timeout 30 2>/dev/null)" || RC=$?
[ "$RC" = "0" ] || fail "herdr: wait must exit 0 once the slot finishes, got $RC: $OUT"
[ "$(jget data.slot <<<"$OUT")" = "w1" ] || fail "herdr: expected slot w1: $OUT"
[ "$(jget data.state <<<"$OUT")" = "done" ] || fail "herdr: expected state done: $OUT"
[ "$(jget data.source <<<"$OUT")" = "herdr-event" ] || fail "herdr: the slot must come back through the event, not the first snapshot: $OUT"
case "$(jget data.evidence <<<"$OUT")" in *"/pull/7") ;; *) fail "herdr: evidence must carry the ROTA-DONE argument: $OUT" ;; esac
[ "$(jget data.changed <<<"$OUT" 2>/dev/null || true)" = "" ] || fail "herdr: round wait data must not report changed: $OUT"
# #29: wait records the slot it returns as poll would, and marks it seen.
W1="$(python3 -c 'import json,sys; s=json.load(open(sys.argv[1]))["slots"][0]; print(s["state"], (s.get("seen") or "").split("\t")[0], s.get("pr"))' "$TMP_RW/repo/.rota/workers.json")"
case "$W1" in "done done "*"/pull/7") ;; *) fail "herdr: round wait should record w1 as done, seen, with its PR: $W1" ;; esac
[ "$(jget method < "$FK/request.json")" = "events.subscribe" ] || fail "herdr: expected an events.subscribe request: $(cat "$FK/request.json")"
[ "$(jget 'params.subscriptions[0].type' < "$FK/request.json")" = "pane.agent_status_changed" ] || fail "herdr: wrong subscription: $(cat "$FK/request.json")"
[ "$(jget 'params.subscriptions[0].pane_id' < "$FK/request.json")" = "w9:p11" ] || fail "herdr: must subscribe to the slot's pane: $(cat "$FK/request.json")"
[ "$(python3 -c 'import json,sys; print(len(json.load(open(sys.argv[1]))["params"]["subscriptions"]))' "$FK/request.json")" = "1" ] || fail "herdr: the parked idle slot must not be watched"
pass "round wait returns the slot that finished, through one herdr event subscription, and records it"

# A slot already returned is not returned again while it has not changed (#29).
RC=0; OUT="$(rw hvj round wait --settle 0 --timeout 1 2>/dev/null)" || RC=$?
[ "$RC" = "1" ] && [ "$(jget data.timedOut <<<"$OUT")" = "true" ] || fail "herdr: a done slot already returned must not come back, rc=$RC: $OUT"
# A slot that needs attention and was never returned comes back at once from the snapshot.
slots w9:t7 busy
RC=0; OUT="$(rw hvj round wait --settle 0 --timeout 30 2>/dev/null)" || RC=$?
[ "$RC" = "0" ] && [ "$(jget data.source <<<"$OUT")" = "snapshot" ] || fail "herdr: a finished slot must return from the snapshot, rc=$RC: $OUT"
pass "a slot comes back once per change; one that needs attention before the call returns immediately (snapshot)"

# ── herdr: timeout is an answer, other herdr versions are refused ───────────
# The server may still be firing for the wait above; let it finish before clearing its flag.
for _ in $(seq 100); do [ "$(cat "$FK/accepted")" = "$(cat "$FK/handled" 2>/dev/null)" ] && break; sleep 0.1; done
[ "$(cat "$FK/accepted")" = "$(cat "$FK/handled")" ] || fail "fake herdr server never finished its last subscription"
rm -f "$FK/fired"; : >"$FK/quiet"
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
