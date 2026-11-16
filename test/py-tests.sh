#!/usr/bin/env bash
# Run the Python harness tests listed in test/py-tests.txt (#3420), the one list
# the full gate, the fast tier and CI share.
#
# Usage: bash test/py-tests.sh [--check]
#   --check  only verify the list: every test/*_test.py is on it and every listed
#            file exists; run nothing
set -uo pipefail

TESTDIR="$(cd "$(dirname "$0")" && pwd)"
LIST="$TESTDIR/py-tests.txt"

listed() { grep -v -e '^[[:space:]]*#' -e '^[[:space:]]*$' "$LIST"; }

check() {
  local rc=0 f t
  for f in "$TESTDIR"/*_test.py; do
    [ -e "$f" ] || continue
    t="$(basename "$f")"
    listed | grep -qxF "$t" || { echo "test/$t is not listed in test/py-tests.txt" >&2; rc=1; }
  done
  while IFS= read -r t; do
    [ -f "$TESTDIR/$t" ] || { echo "$t is listed in test/py-tests.txt but missing" >&2; rc=1; }
  done < <(listed)
  return "$rc"
}

case "${1:-}" in
  --check) check; exit $? ;;
  '') ;;
  *) echo "py-tests: unknown argument: $1" >&2; exit 2 ;;
esac

check || exit 1
fail=0
while IFS= read -r t; do
  echo "py-tests: $t"
  python3 "$TESTDIR/$t" || { echo "py-tests: FAIL $t" >&2; fail=1; }
done < <(listed)
exit "$fail"
