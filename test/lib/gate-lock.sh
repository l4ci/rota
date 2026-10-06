# Machine-wide gate lock, sourced by test/gate.sh and smoke section 107.
# The lock is a symlink whose target is the owner pid: ln -s is atomic and
# creates the owner record in the same step, so a gate killed mid-acquire can
# never leave a lock with no pid (macOS has no flock; mkdir + pid-write had that gap).

gate_lock_owner() { readlink "$1" 2>/dev/null || true; }

# Serialize stale removers with a kernel lock (Python is already a gate
# dependency; fcntl.flock works on Linux and macOS). Keep the guard inode:
# unlinking it could let waiters lock different files under the same name.
# The kernel releases this guard even if a recovery process is killed.
gate_lock_reap() {
  python3 - "$1" <<'PY'
import fcntl
import os
import sys

lock = sys.argv[1]
with open(lock + ".guard", "a") as guard:
    fcntl.flock(guard, fcntl.LOCK_EX)
    try:
        owner = int(os.readlink(lock))
    except FileNotFoundError:
        sys.exit(0)
    except ValueError:
        sys.exit(1)
    if owner <= 0:
        sys.exit(1)
    try:
        os.kill(owner, 0)
    except PermissionError:
        sys.exit(1)  # An inaccessible process is still alive.
    except ProcessLookupError:
        # No other reaper can remove this dead owner's symlink while we hold
        # the guard. A new owner cannot acquire until this unlink completes.
        os.unlink(lock)
    else:
        sys.exit(1)
PY
}

# gate_lock_acquire <lock> [poll-seconds]: block until this shell ($$) owns it.
gate_lock_acquire() {
  local lock="$1" poll="${2:-2}" waited=0 owner
  until ln -s "$$" "$lock" 2>/dev/null; do
    owner="$(gate_lock_owner "$lock")"
    if [ -n "$owner" ] && ! kill -0 "$owner" 2>/dev/null; then
      # The observation above may be stale itself. Re-read and check the
      # current owner under the recovery guard; never move a live lock aside.
      if gate_lock_reap "$lock"; then continue; fi
    fi
    [ "$waited" -gt 0 ] || echo "gate: another gate holds $lock (pid ${owner:-?}); waiting" >&2
    waited=1; sleep "$poll"
  done
}

# gate_lock_release <lock>: remove it only if this shell owns it.
gate_lock_release() {
  [ "$(gate_lock_owner "$1")" = "$$" ] && rm -f "$1"
  return 0
}
