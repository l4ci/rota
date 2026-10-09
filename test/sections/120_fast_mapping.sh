echo "fast.sh: maps changed files to checks (#381)"

fast_plan() { FAST_DRY_RUN=1 bash "$TESTDIR/fast.sh" "$@"; }
GOLD="internal/cli/testdata/golden/TestGlossaryWriteMatchGolden__alias_collision_refuses.json"

OUT=$(fast_plan internal/knowledge/instructions.go)
[ "$OUT" = "plan: go ./internal/knowledge" ] || fail "a .go file should select its package: $OUT"

OUT=$(fast_plan "$GOLD")
[ "$OUT" = "plan: go ./internal/cli" ] || fail "testdata should select its owning package: $OUT"

OUT=$(fast_plan internal/knowledge/skills_block.md)
grep -qx "plan: go ./internal/knowledge" <<<"$OUT" || fail "a go:embed asset should select its package: $OUT"

OUT=$(fast_plan test/lib/isolate.sh test/fakes/fake_forge.py)
[ "$OUT" = "plan: infra" ] || fail "test infra should select the infra check: $OUT"

LINT="plan: section test/sections/71_no_pipe_grep_q.sh"
OUT=$(fast_plan test/sections/120_fast_mapping.sh)
grep -qx "$LINT" <<<"$OUT" || fail "a changed section should add the piped-grep lint: $OUT"
OUT=$(fast_plan test/runner.sh)
grep -qx "plan: infra" <<<"$OUT" || fail "runner.sh should still select infra: $OUT"
grep -qx "$LINT" <<<"$OUT" || fail "runner.sh should add the piped-grep lint: $OUT"
OUT=$(fast_plan test/lib.sh)
grep -qx "$LINT" <<<"$OUT" || fail "lib.sh should add the piped-grep lint: $OUT"
OUT=$(fast_plan test/sections/71_no_pipe_grep_q.sh)
[ "$(grep -cx "$LINT" <<<"$OUT")" = 1 ] || fail "section 71 changed itself must be listed once: $OUT"
OUT=$(fast_plan test/lib/isolate.sh docs/foo.md)
if grep -q "section" <<<"$OUT"; then fail "a file outside the lint paths must not add section 71: $OUT"; fi

OUT=$(fast_plan notes/unmapped.txt)
[ -z "$OUT" ] || fail "an unmapped file should select nothing: $OUT"
pass "fast.sh selects package, testdata, embed and infra checks; unmapped files select none"

# #672: the gofmt check CI runs fails the gate and the fast tier on an unformatted file.
GF=$(mktemp -d)/bad.go
printf 'package x\nfunc  f( ) {}\n' > "$GF"
if bash "$TESTDIR/gofmt.sh" "$GF" >/dev/null 2>&1; then fail "gofmt.sh must exit 1 on an unformatted file"; fi
printf 'package x\n\nfunc f() {}\n' > "$GF"
bash "$TESTDIR/gofmt.sh" "$GF" || fail "gofmt.sh must pass a formatted file"
rm -rf "$(dirname "$GF")"
pass "gofmt.sh fails an unformatted Go file and passes a formatted one"
