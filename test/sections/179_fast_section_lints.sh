echo "fast.sh queues the section-hygiene lints for a section change (#3771)"

fast_plan() { FAST_DRY_RUN=1 bash "$TESTDIR/fast.sh" "$@"; }
HEAD_LINT="plan: section test/sections/164_no_pipe_head_in_sections.sh"

OUT=$(fast_plan test/sections/120_fast_mapping.sh)
grep -qx "$HEAD_LINT" <<<"$OUT" || fail "a changed section should queue section 164 (printf | head lint) as well as 71: $OUT"

OUT=$(fast_plan test/sections/164_no_pipe_head_in_sections.sh)
[ "$(grep -c "164_" <<<"$OUT")" = 1 ] || fail "section 164 changed itself must be listed once: $OUT"

OUT=$(fast_plan test/lib.sh)
if grep -q "164_" <<<"$OUT"; then fail "164 scans only test/sections, so lib.sh must not queue it: $OUT"; fi

pass "a changed section queues section 164 once; lib.sh and runner.sh do not"
