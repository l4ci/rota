#!/usr/bin/env bash
# Template for a smoke section (#579). Copy to test/sections/<N>_<name>.sh.
#
# Sections run under `set -euo pipefail`. A bare `OUT=$(rota ...)` aborts the
# whole section the moment the verb exits non-zero, with no message saying
# which call failed (#461 hid a failure this way). So every verb call captures
# its exit code on the same line, and the assertion reads it:
#
#   success expected:   OUT=$(hvj item show B01) || fail "item show failed"
#   failure expected:   rc=0; OUT=$(hvj item show NOPE 2>&1) || rc=$?
#                       [ "$rc" -eq 3 ] || fail "want exit 3, got $rc"
#
# test/lib.sh check_section_conventions rejects a bare capture of $ROTA_BIN,
# hvj or rota on one line. For a state-writing verb also assert the write
# landed: re-read what it wrote (a `show`/`status`) rather than trusting exit 0.
echo "Section N: <what this section proves>"
TMP_EXAMPLE="$(mktemp -d)"
trap 'rm -rf "$TMP_EXAMPLE"' EXIT

rc=0
OUT=$(cd "$TMP_EXAMPLE" && "$ROTA_BIN" --json config show 2>/dev/null) || rc=$?
[ "$rc" -eq 0 ] || fail "config show exited $rc"

trap 'rm -rf "$TMP"' EXIT
pass "template example"
