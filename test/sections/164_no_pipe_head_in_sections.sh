echo "no section reads a first line through a pipe into head under pipefail (#1242)"
# `printf '%s\n' "$X" | head -1` exits 141 when head closes the pipe before printf has written
# everything (output over one stdio buffer); the runner's set -eo pipefail then kills the shard
# with no FAIL line. About 1 run in 1500 on an 8 KB payload, so a section that passes alone dies
# in the sharded gate. Take the first line by expansion: ${X%%$'\n'*}.
HITS=$(grep -nE "printf [^|]*\| *head -n? ?-?1\b" "$REPO"/test/sections/*.sh | grep -v "/164_" | grep -v '^[^:]*:[0-9]*:[[:space:]]*#' || true)
[ -z "$HITS" ] || fail "printf piped into head -1 in a section (SIGPIPE under pipefail): $HITS"
pass "no printf | head -1 pipeline in test/sections"
