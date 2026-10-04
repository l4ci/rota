# Machine-wide gate lock, sourced by test/gate.sh and smoke section 107.
# The lock is a symlink whose target is the owner pid: ln -s is atomic and
# creates the owner record in the same step, so a gate killed mid-acquire can
# never leave a lock with no pid (macOS has no flock; mkdir + pid-write had that gap).

gate_lock_owner() { readlink "$1" 2>/dev/null || true; }

# gate_lock_acquire <lock> [poll-seconds]: block until this shell ($$) owns it.
gate_lock_acquire() {
  local lock="$1" poll="${2:-2}" waited=0 owner moved
  until ln -s "$$" "$lock" 2>/dev/null; do
    owner="$(gate_lock_owner "$lock")"
    if [ -n "$owner" ] && ! kill -0 "$owner" 2>/dev/null; then
      # Take the stale lock out by rename, which is atomic: of two waiters that
      # saw the same dead owner only one wins; the other mv fails and retries.
      if mv "$lock" "$lock.stale.$$" 2>/dev/null; then
        moved="$(gate_lock_owner "$lock.stale.$$")"
        if [ "$moved" = "$owner" ]; then
          rm -f "$lock.stale.$$"
        else
          # Raced: we moved a fresh lock another waiter just took. Put it back.
          mv -n "$lock.stale.$$" "$lock" 2>/dev/null || rm -f "$lock.stale.$$"
        fi
      fi
      continue
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
