echo "gate lock: stale takeover, pid-less window, ownership on release (#82)"
# test/lib/gate-lock.sh is what test/gate.sh uses. Each case runs in its own bash
# so $$ is a distinct, real pid.

GL="$(mktemp -d "$TMP/gatelock.XXXXXX")"
LIB="$TESTDIR/lib/gate-lock.sh"

# A dead owner (a reaped pid) is taken over, not waited on forever.
sleep 0 & DEAD=$!; wait "$DEAD" 2>/dev/null || true
ln -s "$DEAD" "$GL/stale.lock"
OUT=$(timeout 20 bash -c '. "$1"; gate_lock_acquire "$2" 0.1; readlink "$2"; echo "$$"' _ "$LIB" "$GL/stale.lock") \
  || fail "gate lock: a lock held by a dead pid was not taken over"
[ "$(echo "$OUT" | sed -n 1p)" = "$(echo "$OUT" | sed -n 2p)" ] || fail "gate lock: takeover did not record the new owner: $OUT"
if grep -q 'stale.lock.stale' <<<"$(ls "$GL")"; then fail "gate lock: takeover left a .stale.* file behind"; fi
pass "a lock held by a dead pid is taken over and the stale file removed"

# The lock records its owner at creation: there is no pid-less state to be stranded in.
timeout 20 bash -c '. "$1"; gate_lock_acquire "$2" 0.1; [ -n "$(readlink "$2")" ]' _ "$LIB" "$GL/atomic.lock" \
  || fail "gate lock: a fresh lock had no owner recorded"
pass "a fresh lock names its owner from the moment it exists"

# A live owner is respected: acquire keeps waiting (timeout kills it, rc 124).
sleep 30 & LIVE=$!
ln -s "$LIVE" "$GL/live.lock"
rc=0; timeout 2 bash -c '. "$1"; gate_lock_acquire "$2" 0.1' _ "$LIB" "$GL/live.lock" 2>/dev/null || rc=$?
[ "$rc" -eq 124 ] || fail "gate lock: acquire did not wait on a live owner"
[ "$(readlink "$GL/live.lock")" = "$LIVE" ] || fail "gate lock: a live owner's lock was disturbed"
kill "$LIVE" 2>/dev/null || true
pass "a lock held by a live pid is left alone"

# Release removes only the caller's own lock.
ln -s 1 "$GL/foreign.lock"
bash -c '. "$1"; gate_lock_release "$2"' _ "$LIB" "$GL/foreign.lock"
[ -L "$GL/foreign.lock" ] || fail "gate lock: release removed a lock it does not own"
bash -c '. "$1"; gate_lock_acquire "$2" 0.1; gate_lock_release "$2"' _ "$LIB" "$GL/own.lock"
if [ -e "$GL/own.lock" ] || [ -L "$GL/own.lock" ]; then fail "gate lock: release left the caller's own lock behind"; fi
pass "release removes the caller's lock and never another gate's"

# Force B's stale observation to outlive A's acquisition, then introduce C at
# B's removal/wait boundary. Check the critical sections, not only completion.
python3 "$TESTDIR/lib/gate-lock-race.py" "$LIB" "$GL/controlled" "$DEAD" \
  || fail "gate lock: controlled stale takeover violated mutual exclusion"
pass "delayed stale takeover preserves the live lock and critical-section exclusion"

# Uncontrolled contention also checks overlap and takeover-file cleanup.
ln -s "$DEAD" "$GL/race.lock"
for i in 1 2 3 4; do
  ( timeout 20 bash -c '
    . "$1"; gate_lock_acquire "$2" 0.1
    mkdir "$3/critical" || { touch "$3/overlap"; exit 1; }
    sleep 0.1
    rmdir "$3/critical"
    gate_lock_release "$2"; echo ok
  ' _ "$LIB" "$GL/race.lock" "$GL" > "$GL/race$i.out" 2>/dev/null ) &
done
wait
[ "$(cat "$GL"/race?.out | grep -c ok)" = 4 ] || fail "gate lock: racing waiters did not all get a turn"
[ ! -e "$GL/overlap" ] || fail "gate lock: racing critical sections overlapped"
[ -z "$(find "$GL" -name '*.stale.*' -print)" ] || fail "gate lock: racing waiters left takeover files behind"
pass "racing waiters on a dead lock each acquire in turn"
rm -rf "${GL:?}"
