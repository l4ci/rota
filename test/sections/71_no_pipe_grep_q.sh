echo "no piped grep -q in the smoke harness"
# Sections run under `set -euo pipefail`. A producer piped into grep -q lets grep exit
# on the first match, the producer then dies of SIGPIPE (141), and pipefail turns
# a MATCH into a failed pipeline. Read from a here-string or file instead:
#   grep -q PAT <<<"$OUT"      grep -q PAT file      cmd | grep PAT >/dev/null  (no -q: grep drains the pipe)
# The regex below is written so this file does not match itself.
PIPE_GREP_Q='\|[[:space:]]*grep[[:space:]]+-[a-zA-Z]*q'
PIPE_GREP_HITS="$(grep -nE "$PIPE_GREP_Q" "$TESTDIR"/sections/*.sh "$TESTDIR/lib.sh" "$TESTDIR/runner.sh" || true)"
if [ -n "$PIPE_GREP_HITS" ]; then
  fail "piped grep -q is SIGPIPE-flaky under pipefail (use a here-string or file):
$PIPE_GREP_HITS"
fi
pass "no piped grep -q in test/sections, test/lib.sh or test/runner.sh"
