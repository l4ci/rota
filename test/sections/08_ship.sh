echo "ship merge / ship pr"
# Check syntactic integrity — they should error cleanly without a body
rc=0; echo "" | hvj ship merge rota/real-branch --body-file - >/dev/null 2>&1 || rc=$?
[ "$rc" = 2 ] || fail "ship merge should reject an empty message with exit 2 (got $rc)"
rc=0; hvj ship merge rota/real-branch >/dev/null 2>&1 || rc=$?
[ "$rc" = 2 ] || fail "ship merge without --body-file should exit 2 (got $rc)"
rc=0; echo "msg" | hvj ship merge rota/no-such-branch --body-file - >/dev/null 2>&1 || rc=$?
[ "$rc" = 3 ] || fail "ship merge of an unknown branch should exit 3 (got $rc)"
pass "ship merge rejects empty message and unknown branch"

# ship merge: --no-ff merge into the base branch with the body as message, branch deleted
TMP_MG="$(mktemp -d)"
trap 'rm -rf "$TMP_MG"' EXIT
(
  cd "$TMP_MG"
  git init -q && git config user.email t@t && git config user.name t
  git checkout -q -b main 2>/dev/null || git branch -m main
  mkdir .rota && echo '{}' > .rota/config.json
  echo base > a.txt && git add a.txt && git commit -q -m seed
  git checkout -q -b rota/merge-ok && echo one > b.txt && git add b.txt && git commit -q -m "feat: one"
  git checkout -q -b rota/merge-conflict main && echo two > a.txt && git commit -q -am "feat: two"
  git checkout -q main && echo three > a.txt && git commit -q -am "feat: three"

  OUT=$(printf 'merge: ok branch' | hvj ship merge rota/merge-ok --body-file -) || fail "ship merge failed: $OUT"
  [ "$(echo "$OUT" | jget data.base)" = main ] && [ "$(echo "$OUT" | jget data.changed)" = true ] || fail "ship merge data: $OUT"
  [ "$(echo "$OUT" | jget data.sha)" = "$(git rev-parse --short HEAD)" ] || fail "ship merge sha should be HEAD: $OUT"
  [ "$(git log -1 --format=%s)" = "merge: ok branch" ] || fail "merge subject should be the body"
  [ "$(git rev-list --parents -1 HEAD | wc -w | tr -d ' ')" = 3 ] || fail "ship merge should create a merge commit"
  git rev-parse --verify -q refs/heads/rota/merge-ok >/dev/null && fail "ship merge should delete the branch" || true

  rc=0; OUT=$(printf 'merge: x' | hvj ship merge main --body-file - 2>/dev/null) || rc=$?
  [ "$rc" = 4 ] && [ "$(echo "$OUT" | jget data.blockedBy)" = "base branch" ] || fail "merging the base branch should exit 4: rc=$rc $OUT"
  rc=0; OUT=$(printf 'merge: x' | hvj ship merge rota/merge-conflict --body-file - 2>/dev/null) || rc=$?
  [ "$rc" = 4 ] && [ "$(echo "$OUT" | jget data.blockedBy)" = conflict ] || fail "a conflicting merge should exit 4: rc=$rc $OUT"
  [ -z "$(git status --porcelain --untracked-files=no)" ] && ! git rev-parse -q --verify MERGE_HEAD >/dev/null \
    || fail "a conflicting ship merge must abort and leave the tree clean"
)
trap 'rm -rf "$TMP"' EXIT
pass "ship merge merges --no-ff, deletes the branch, refuses base branch and conflicts"
# Don't actually run ship pr — no remote (63_pr covers it)

echo "regression: backlog list preserves periods in titles"
cat > .rota/BACKLOG.md <<'EOF'
# TODO

## Features
- **[F50] [Minor] Add v1.2 support.** Desc here.

## Bugs

## Tasks

## Completed
EOF
OUT=$(hvj backlog list)
echo "$OUT" | jget data.features[0].title | grep "Add v1.2 support" >/dev/null || fail "title with period was truncated: $OUT"
pass "backlog keeps mid-title periods intact"

echo "regression: backlog archive always reports a count"
cat > .rota/BACKLOG.md <<'EOF'
# TODO

## Bugs

## Features

## Tasks

## Completed
EOF
OUT=$(hvj backlog archive --days 5)
[ "$(echo "$OUT" | jget data.moved)" = "0" ] || fail "expected moved 0 when nothing to archive, got: $OUT"
[ "$(echo "$OUT" | jget data.changed)" = "false" ] || fail "nothing archived should report changed=false: $OUT"
pass "backlog archive reports 0 when no items to move"

echo "regression: backlog archive only archives canonical completed shape"
FAKE_HASH="abc1234"
OLD_DATE="2024-01-01"
cat > .rota/BACKLOG.md <<EOF
# TODO

## Bugs

## Features

## Tasks

## Completed

- ~~**[B01] Fix login crash.**~~ Done ${OLD_DATE} [\`${FAKE_HASH}\`]
- Note: see issue [B05] which was Done 2024-01-01 by accident.
EOF
OUT=$(hvj backlog archive --days 1)
[ "$(echo "$OUT" | jget data.moved)" = "1" ] || fail "expected 1 item archived, got: $OUT"
[ "$(echo "$OUT" | jget data.changed)" = "true" ] || fail "archiving should report changed=true: $OUT"
grep -q "Fix login crash" .rota/ARCHIVE.md || fail "canonical bullet not found in ARCHIVE.md"
grep -q "by accident" .rota/BACKLOG.md || fail "free-form note was wrongly removed from BACKLOG.md"
! grep -q "Fix login crash" .rota/BACKLOG.md || fail "canonical bullet still present in BACKLOG.md"
pass "backlog archive only moves canonical completed bullets"

echo "ship body"
# Fresh branch state for ship-body + review-scope
git checkout -q main 2>/dev/null || true
cat > .rota/BACKLOG.md <<'EOF'
# TODO

## Bugs

## Features

## Tasks

## Completed
- ~~**[B70] [P1] Ship demo bug.** Broken badge.~~ Done 2026-04-18 [`aaa1111`]
- ~~**[F70] [Minor] Ship demo feature.** Overlay.~~ Done 2026-04-18 [`bbb2222`]
EOF
git add -A && git commit -q -m "seed ship demo" || true
git checkout -q -b rota/ship-demo
echo ship1 > ship1.txt && git add ship1.txt && git commit -q -m "fix: badge invalidation [B70]"
echo ship2 > ship2.txt && git add ship2.txt && git commit -q -m "feat: overlay [F70]"
git checkout -q main

BODY=$(hvj ship body rota/ship-demo | jget data.body)
grep -q "^## Summary" <<<"$BODY" || fail "ship body missing Summary section"
grep -q "^## Items resolved" <<<"$BODY" || fail "ship body missing Items resolved section"
grep -q "\[B70\] Ship demo bug" <<<"$BODY" || fail "ship body missing B70 title"
grep -q "\[F70\] Ship demo feature" <<<"$BODY" || fail "ship body missing F70 title"
pass "ship body emits Summary + Items resolved with resolved titles"

rc=0; hvj ship body main >/dev/null 2>&1 || rc=$?
[ "$rc" = 1 ] || fail "ship body should reject main with exit 1 (no commits vs base), got $rc"
rc=0; hvj ship body rota/no-such-branch >/dev/null 2>&1 || rc=$?
[ "$rc" = 3 ] || fail "ship body of an unknown branch should exit 3, got $rc"
pass "ship body errors when base has no commits"

# ship-body emits Closes #N for GH refs in resolved item bullets
git checkout -q main
cat > .rota/BACKLOG.md <<'EOF'
# TODO

## Bugs

## Features

## Tasks

## Completed
- ~~**[B71] [P1] Has GH ref.** Something. GH: #42~~ Done 2026-04-18 [`ccc3333`]
- ~~**[F71] [Minor] No GH ref.** Plain.~~ Done 2026-04-18 [`ddd4444`]
EOF
git add -A && git commit -q -m "seed gh-closes test" || true
git checkout -q -b rota/ship-gh-closes
echo g1 > g1.txt && git add g1.txt && git commit -q -m "fix: thing [B71]"
echo g2 > g2.txt && git add g2.txt && git commit -q -m "feat: thing [F71]"
git checkout -q main
BODY=$(hvj ship body rota/ship-gh-closes | jget data.body)
grep -q "^Closes #42$" <<<"$BODY" || fail "ship body missing Closes #42 line: $BODY"
GH_LINES=$(echo "$BODY" | grep -c "^Closes #" || true)
[ "$GH_LINES" = "1" ] || fail "ship body expected 1 Closes line, got $GH_LINES: $BODY"
pass "ship body emits Closes #N from GH refs in TODO bullets"
git checkout -q main
git branch -D rota/ship-gh-closes >/dev/null 2>&1 || true
rm -f g1.txt g2.txt

# Negative case: no GH refs anywhere → no Closes lines.
git checkout -q main
cat > .rota/BACKLOG.md <<'EOF'
# TODO

## Bugs

## Features

## Tasks

## Completed
- ~~**[B72] [P1] No ref.** Plain.~~ Done 2026-04-18 [`eee5555`]
EOF
git add -A && git commit -q -m "seed gh-closes negative" || true
git checkout -q -b rota/ship-gh-noclose
echo g3 > g3.txt && git add g3.txt && git commit -q -m "fix: thing [B72]"
git checkout -q main
BODY=$(hvj ship body rota/ship-gh-noclose | jget data.body)
if grep -q "^Closes #" <<<"$BODY"; then fail "ship body emitted Closes line with no GH refs: $BODY"; fi
pass "ship body emits no Closes lines when no GH refs present"
git checkout -q main
git branch -D rota/ship-gh-noclose >/dev/null 2>&1 || true
rm -f g3.txt

# Dedup: two commits referencing the same ID emit a single Closes #N line.
git checkout -q main
cat > .rota/BACKLOG.md <<'EOF'
# TODO

## Bugs

## Features

## Tasks

## Completed
- ~~**[B73] [P1] Has GH ref.** Something. GH: #99~~ Done 2026-04-18 [`fff6666`]
EOF
git add -A && git commit -q -m "seed gh-closes dedup" || true
git checkout -q -b rota/ship-gh-dedup
echo g4 > g4.txt && git add g4.txt && git commit -q -m "fix: thing one [B73]"
echo g5 > g5.txt && git add g5.txt && git commit -q -m "fix: thing two [B73]"
git checkout -q main
BODY=$(hvj ship body rota/ship-gh-dedup | jget data.body)
DEDUP_LINES=$(echo "$BODY" | grep -c "^Closes #99$" || true)
[ "$DEDUP_LINES" = "1" ] || fail "ship body expected 1 Closes #99 line (dedup), got $DEDUP_LINES: $BODY"
pass "ship body dedups Closes #N across multiple commits referencing same ID"
git checkout -q main
git branch -D rota/ship-gh-dedup >/dev/null 2>&1 || true
rm -f g4.txt g5.txt

# Restore demo TODO state for downstream review-scope tests
cat > .rota/BACKLOG.md <<'EOF'
# TODO

## Bugs

## Features

## Tasks

## Completed
- ~~**[B70] [P1] Ship demo bug.** Broken badge.~~ Done 2026-04-18 [`aaa1111`]
- ~~**[F70] [Minor] Ship demo feature.** Overlay.~~ Done 2026-04-18 [`bbb2222`]
EOF
git add -A && git commit -q -m "restore ship demo TODO" || true

echo "review scope"
OUT=$(hvj review scope rota/ship-demo)
[ "$(echo "$OUT" | jget data.commitCount)" = "2" ] || fail "review scope commitCount != 2: $OUT"
[ "$(echo "$OUT" | jget data.base)" = "main" ] || fail "review scope base != main: $OUT"
[ "$(echo "$OUT" | jget data.referencedIds)" = '["F70","B70"]' ] || fail "review scope referencedIds: $OUT"
# intents follow the log order (newest commit first)
[ "$(echo "$OUT" | jget data.intents[0].id)" = "F70" ] || fail "review scope missing F70: $OUT"
[ "$(echo "$OUT" | jget data.intents[0].type)" = "F" ] || fail "review scope F70 type: $OUT"
[ "$(echo "$OUT" | jget data.intents[1].id)" = "B70" ] || fail "review scope missing B70: $OUT"
[ "$(echo "$OUT" | jget data.intents[1].type)" = "B" ] || fail "review scope B70 type: $OUT"
[ "$(echo "$OUT" | jget data.intents[1].title)" = "Ship demo bug" ] || fail "review scope missing B70 title: $OUT"
[ "$(echo "$OUT" | jget data.touchedFiles)" = '["ship1.txt","ship2.txt"]' ] || fail "review scope touchedFiles: $OUT"
[ "$(echo "$OUT" | jget data.commits[0].subject)" = "feat: overlay [F70]" ] || fail "review scope commits: $OUT"
pass "review scope emits commits, IDs, titles, and files"

rc=0; hvj review scope main >/dev/null 2>&1 || rc=$?
[ "$rc" = 1 ] || fail "review scope should reject the base branch with exit 1, got $rc"
rc=0; hvj review scope rota/no-such-branch >/dev/null 2>&1 || rc=$?
[ "$rc" = 3 ] || fail "review scope of an unknown branch should exit 3, got $rc"
pass "review scope rejects base branch"

# Regression: review-scope must attribute an ID to its OWN bullet, not to
# another item that mentions the ID in a `Related:` suffix.
git checkout -q main
cat > .rota/BACKLOG.md <<'EOF'
# TODO

## Bugs

## Features
- **[F80] [Minor] Refers to B70.** Something else. Related: [B70]

## Tasks

## Completed
EOF
cat > .rota/ARCHIVE.md <<'EOF'
# Archive

- ~~**[B70] [P1] Ship demo bug.** Broken badge.~~ Done 2026-04-10 [`aaa1111`]
EOF
git add -A && git commit -q -m "seed related-link test" || true
git checkout -q -b rota/scope-regression
echo r > r.txt && git add r.txt && git commit -q -m "fix: badge [B70]"
git checkout -q main
OUT=$(hvj review scope rota/scope-regression)
[ "$(echo "$OUT" | jget data.intents[0].title)" = "Ship demo bug" ] || fail "review scope picked wrong bullet for B70 (Related-link regression): $OUT"
pass "review scope picks origin bullet, ignores Related-link references"
git branch -D rota/scope-regression >/dev/null 2>&1 || true
rm -f r.txt

# Regression: `rota ship body` must attribute an ID to its OWN bullet, not to
# another item that mentions the ID in a `Related:` suffix.
git checkout -q main
cat > .rota/BACKLOG.md <<'EOF'
# TODO

## Bugs

## Features
- **[F80] [Minor] Refers to B70.** Something else. Related: [B70]

## Tasks

## Completed
EOF
cat > .rota/ARCHIVE.md <<'EOF'
# Archive

- ~~**[B70] [P1] Ship demo bug.** Broken badge.~~ Done 2026-04-10 [`aaa1111`]
EOF
git add -A && git commit -q -m "seed ship-body related-link test" || true
git checkout -q -b rota/ship-body-regression
echo r > r2.txt && git add r2.txt && git commit -q -m "fix: badge [B70]"
git checkout -q main
BODY=$(hvj ship body rota/ship-body-regression | jget data.body)
grep -q "\[B70\] Ship demo bug" <<<"$BODY" || fail "ship body picked wrong bullet for B70 (Related-link regression): $BODY"
pass "ship body picks origin bullet, ignores Related-link references"
git checkout -q main
git branch -D rota/ship-body-regression >/dev/null 2>&1 || true
rm -f r2.txt

# Cleanup demo branch before later tests
git branch -D rota/ship-demo >/dev/null 2>&1 || true
rm -f ship1.txt ship2.txt

echo "review brief"
# Re-seed the ship-demo branch + TODO state for the second-opinion brief test.
git checkout -q main
cat > .rota/BACKLOG.md <<'EOF'
# TODO

## Bugs

## Features

## Tasks

## Completed
- ~~**[B70] [P1] Ship demo bug.** Broken badge.~~ Done 2026-04-18 [`aaa1111`]
- ~~**[F70] [Minor] Ship demo feature.** Overlay.~~ Done 2026-04-18 [`bbb2222`]
EOF
git add -A && git commit -q -m "seed second-opinion demo" || true
git checkout -q -b rota/second-opinion-demo
echo so1 > so1.txt && git add so1.txt && git commit -q -m "fix: badge invalidation [B70]"
echo so2 > so2.txt && git add so2.txt && git commit -q -m "feat: overlay [F70]"
git checkout -q main

BRIEF_ENV=$(hvj review brief rota/second-opinion-demo)
[ "$(echo "$BRIEF_ENV" | jget data.commitCount)" = "2" ] && [ "$(echo "$BRIEF_ENV" | jget data.base)" = "main" ] \
  || fail "review brief commitCount/base: $BRIEF_ENV"
BRIEF=$(echo "$BRIEF_ENV" | jget data.brief)
grep -qi "no prior conversation context" <<<"$BRIEF" \
  || fail "review brief missing fresh-context framing"
grep -q "^\*\*Goal" <<<"$BRIEF" \
  || fail "review brief missing Goal section"
grep -q "\[B70\] Ship demo bug" <<<"$BRIEF" \
  || fail "review brief missing B70 in goal"
grep -q "\[F70\] Ship demo feature" <<<"$BRIEF" \
  || fail "review brief missing F70 in goal"
grep -q "^\*\*Commits" <<<"$BRIEF" \
  || fail "review brief missing Commits section"
grep -q "fix: badge invalidation" <<<"$BRIEF" \
  || fail "review brief missing commit subject"
grep -q "^\*\*Diff" <<<"$BRIEF" \
  || fail "review brief missing Diff section"
grep -q "so1.txt" <<<"$BRIEF" \
  || fail "review brief missing per-file diff path"
grep -q '^`verdict` is one of:' <<<"$BRIEF" \
  || fail "review brief missing verdict-instruction"
if grep -qi "KNOWLEDGE\.md\|DECISIONS\.md\|hard boundaries\|known gotchas" <<<"$BRIEF"; then
  fail "review brief leaked KNOWLEDGE/DECISIONS context (must be diff+goal only)"
fi
pass "review brief emits goal+commits+diff with no project-context leak"

rc=0; hvj review brief main >/dev/null 2>&1 || rc=$?
[ "$rc" = 1 ] || fail "review brief should reject the base branch with exit 1, got $rc"
rc=0; hvj review brief rota/no-such-branch >/dev/null 2>&1 || rc=$?
[ "$rc" = 3 ] || fail "review brief of an unknown branch should exit 3, got $rc"
pass "review brief rejects base branch"

# Zero-commit branch — refused with exit 1
git checkout -q -b rota/second-opinion-empty
git checkout -q main
rc=0; hvj review brief rota/second-opinion-empty >/dev/null 2>&1 || rc=$?
[ "$rc" = 1 ] || fail "review brief should reject a zero-commit branch with exit 1, got $rc"
pass "review brief rejects branch with no commits beyond base"
git branch -D rota/second-opinion-empty >/dev/null 2>&1 || true

# Schema parity — brief's commit count matches review scope's commitCount.
# Catches silent drift if the scope schema changes without the brief noticing.
SCOPE_COUNT=$(hvj review scope rota/second-opinion-demo | jget data.commitCount)
BRIEF_COMMITS=$(hvj review brief rota/second-opinion-demo | jget data.brief | grep -cE '^- `[a-f0-9]+` ')
[ "$SCOPE_COUNT" = "$BRIEF_COMMITS" ] || fail "schema drift: review scope reports $SCOPE_COUNT commits but review brief lists $BRIEF_COMMITS"
pass "review brief commit count matches review scope (schema parity)"

# Cleanup
git branch -D rota/second-opinion-demo >/dev/null 2>&1 || true
rm -f so1.txt so2.txt

