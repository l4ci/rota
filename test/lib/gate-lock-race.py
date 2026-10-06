"""Force two stale observers and a third contender through the gate lock."""

import os
from pathlib import Path
import subprocess
import sys
import time


WORKER = r'''
set -eu
. "$1"
cd "$2"
role="$3" dead="$4"
await_file() { while [ ! -f "$1" ]; do command sleep 0.01; done; }
# Delay B after it has decided the original owner is dead.
kill() {
  local rc=0
  builtin kill "$@" 2>/dev/null || rc=$?
  if [ "$role" = b ] && [ "$*" = "-0 $dead" ] && [ ! -f b.observed ]; then
    touch b.observed
    await_file b.resume
  fi
  return "$rc"
}
# The broken implementation moves A's live lock. Hold that gap open for C.
mv() {
  command mv "$@" || return $?
  if [ "$role" = b ] && [ "$1" = gate.lock ]; then
    touch b.checked
    await_file b.continue
  fi
}
sleep() {
  if [ "$role" = b ]; then
    touch b.checked
    await_file b.continue
  elif [ "$role" = c ]; then
    touch c.waiting
  fi
  command sleep "$@"
}
gate_lock_acquire gate.lock 0.01
held=0
if mkdir critical 2>/dev/null; then held=1; else touch overlap; fi
touch "$role.entered"
case "$role" in a|c) await_file "$role.release" ;; esac
command sleep 0.02
[ "$held" = 0 ] || rmdir critical
gate_lock_release gate.lock
'''


def run(library, directory, dead):
    root = Path(directory)
    root.mkdir()
    os.symlink(dead, root / "gate.lock")
    workers = []

    def start(role):
        process = subprocess.Popen(
            ["bash", "-c", WORKER, "_", library, str(root), role, dead],
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
        )
        workers.append(process)
        return process

    def wait_for(*names):
        deadline = time.monotonic() + 10
        while not any((root / name).exists() for name in names):
            if time.monotonic() > deadline:
                raise AssertionError(f"timed out waiting for {names}")
            time.sleep(0.01)

    def signal(name):
        (root / name).touch()

    try:
        start("b")
        wait_for("b.observed")
        a = start("a")
        wait_for("a.entered")
        signal("b.resume")
        wait_for("b.checked")
        start("c")
        wait_for("c.entered", "c.waiting")
        assert not (root / "overlap").exists(), "gate critical sections overlapped"
        assert os.readlink(root / "gate.lock") == str(a.pid), "live owner's lock displaced"
    finally:
        for name in ("b.resume", "b.continue", "a.release", "c.release"):
            signal(name)
        for process in workers:
            try:
                out, err = process.communicate(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                out, err = process.communicate()
            if process.returncode:
                print(f"worker {process.pid}: {process.returncode}\n{out}{err}", file=sys.stderr)
    assert all(p.returncode == 0 for p in workers), "contender failed"
    assert all((root / f"{r}.entered").exists() for r in "abc"), "contender never acquired"
    assert not (root / "overlap").exists(), "gate critical sections overlapped"
    assert not (root / "gate.lock").is_symlink(), "owner lock left behind"
    assert not list(root.glob("gate.lock.stale.*")), "takeover files left behind"


if __name__ == "__main__":
    run(*sys.argv[1:])
