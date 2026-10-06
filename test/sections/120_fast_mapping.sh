echo "fast.sh: maps changed files to checks (#381)"

fast_plan() { FAST_DRY_RUN=1 bash "$TESTDIR/fast.sh" "$@"; }
GOLD="internal/cli/testdata/golden/TestGlossaryWriteMatchGolden__alias_collision_refuses.json"

OUT=$(fast_plan internal/knowledge/instructions.go)
[ "$OUT" = "plan: go ./internal/knowledge" ] || fail "a .go file should select its package: $OUT"

OUT=$(fast_plan "$GOLD")
[ "$OUT" = "plan: go ./internal/cli" ] || fail "testdata should select its owning package: $OUT"

OUT=$(fast_plan internal/knowledge/skills_block.md)
grep -qx "plan: go ./internal/knowledge" <<<"$OUT" || fail "a go:embed asset should select its package: $OUT"

OUT=$(fast_plan test/lib/isolate.sh test/fakes/fake_forge.py test/runner.sh)
[ "$OUT" = "plan: infra" ] || fail "test infra should select the infra check: $OUT"

OUT=$(fast_plan notes/unmapped.txt)
[ -z "$OUT" ] || fail "an unmapped file should select nothing: $OUT"
pass "fast.sh selects package, testdata, embed and infra checks; unmapped files select none"
