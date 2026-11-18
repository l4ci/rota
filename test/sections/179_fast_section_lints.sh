echo "section lints are tagged, and the fast tier queues each tagged lint for its trigger paths (#3771)"

fast_plan() { FAST_DRY_RUN=1 bash "$TESTDIR/fast.sh" "$@"; }

# Every lint section declares where it runs: `# lint-trigger: <glob>...`. fast.sh reads
# that tag, so a new lint needs its tag and nothing else. Drift check: each tagged
# section must be queued for a path its own globs match.
TAGGED="$(grep -l '^# lint-trigger: ' "$TESTDIR"/sections/*.sh || true)"
[ -n "$TAGGED" ] || fail "no section carries a '# lint-trigger:' tag"
for lint in 71_no_pipe_grep_q.sh 164_no_pipe_head_in_sections.sh; do
  grep -q '^# lint-trigger: ' "$TESTDIR/sections/$lint" || fail "$lint must carry a '# lint-trigger:' tag"
done

for lf in $TAGGED; do
  name="$(basename "$lf")"
  read -r -a globs <<<"$(sed -n 's/^# lint-trigger: //p' "$lf")"
  [ "${#globs[@]}" -gt 0 ] || fail "$name: empty lint-trigger tag"
  for g in "${globs[@]}"; do
    sample="${g//\*/probe}"
    OUT=$(fast_plan "$sample")
    grep -qx "plan: section test/sections/$name" <<<"$OUT" \
      || fail "$name is tagged for '$g' but fast.sh does not queue it for '$sample': $OUT"
  done
done

OUT=$(fast_plan test/sections/120_fast_mapping.sh)
[ "$(grep -c "164_" <<<"$OUT")" = 1 ] || fail "section 164 must be queued once for a section change: $OUT"
[ "$(grep -c "71_" <<<"$OUT")" = 1 ] || fail "section 71 must be queued once for a section change: $OUT"

OUT=$(fast_plan test/sections/164_no_pipe_head_in_sections.sh)
[ "$(grep -c "164_" <<<"$OUT")" = 1 ] || fail "section 164 changed itself must be listed once: $OUT"

OUT=$(fast_plan test/lib.sh)
if grep -q "164_" <<<"$OUT"; then fail "164 scans only test/sections, so lib.sh must not queue it: $OUT"; fi
grep -q "71_" <<<"$OUT" || fail "lib.sh must still queue section 71: $OUT"

OUT=$(fast_plan docs/foo.md)
if grep -q "plan: section" <<<"$OUT"; then fail "a docs-only change must queue no lint: $OUT"; fi

pass "every tagged section lint is queued for its trigger paths; dedupe and docs-only cost hold"
