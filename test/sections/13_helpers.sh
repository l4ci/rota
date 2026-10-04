echo "## git base + git worktree-clear + block (refactor)"

# 1. git base
BB_TMP="$(mktemp -d)"
(
  cd "$BB_TMP"
  mkdir -p .rota
  git init -q
  git config user.email t@t && git config user.name t
  git checkout -q -b main 2>/dev/null || git branch -m main
  echo seed > seed.txt && git add seed.txt && git commit -q -m "seed"
  OUT=$(hvj git base) || { echo "FAIL git base: exit non-zero: $OUT"; exit 1; }
  [ "$(jget data.base <<<"$OUT")" = "main" ] || { echo "FAIL git base: expected 'main', got '$OUT'"; exit 1; }
)
rm -rf "$BB_TMP"
pass "git base resolves 'main' in a fresh git repo"

# 1b. git base respects git.baseBranch from config
BB2_TMP="$(mktemp -d)"
(
  cd "$BB2_TMP"
  git init -q
  git config user.email t@t && git config user.name t
  git checkout -q -b main 2>/dev/null || git branch -m main
  echo seed > seed.txt && git add seed.txt && git commit -q -m "seed"
  git checkout -q -b develop
  echo dev > dev.txt && git add dev.txt && git commit -q -m "dev"
  git checkout -q main
  mkdir -p .rota
  printf '{"git":{"baseBranch":"develop"}}\n' > .rota/config.json
  OUT=$(hvj git base) || { echo "FAIL git base config override: exit non-zero: $OUT"; exit 1; }
  [ "$(jget data.base <<<"$OUT")" = "develop" ] || { echo "FAIL git base config override: expected 'develop', got '$OUT'"; exit 1; }
)
rm -rf "$BB2_TMP"
pass "git base respects git.baseBranch config override"

# 3. block knowledge (flat-list mode)
MB_TMP="$(mktemp -d)"
(
  cd "$MB_TMP"
  mkdir -p .rota
  git init -q && git config user.email t@t && git config user.name t
  OUT=$(hvj block knowledge) || { echo "FAIL: block knowledge exit non-zero: $OUT"; exit 1; }
  [ "$(jget data.status <<<"$OUT")" = "created" ] || { echo "FAIL: expected 'created', got '$OUT'"; exit 1; }
  [ "$(jget data.changed <<<"$OUT")" = "true" ] || { echo "FAIL: expected changed true: $OUT"; exit 1; }
  grep -q "<!-- rota-knowledge-start -->" CLAUDE.md || { echo "FAIL: marker missing"; exit 1; }
  grep -q "no topics yet" CLAUDE.md || { echo "FAIL: empty msg missing"; exit 1; }

  mkdir -p .rota
  printf '# Knowledge\n\n## Build\n- details\n\n## Testing\n- more\n' > .rota/KNOWLEDGE.md
  OUT=$(hvj block knowledge) || { echo "FAIL: block knowledge exit non-zero: $OUT"; exit 1; }
  [ "$(jget data.status <<<"$OUT")" = "updated" ] || { echo "FAIL: expected 'updated', got '$OUT'"; exit 1; }
  grep -q "^- Build" CLAUDE.md || { echo "FAIL: Build topic missing"; exit 1; }
  grep -q "^- Testing" CLAUDE.md || { echo "FAIL: Testing topic missing"; exit 1; }

)
rm -rf "$MB_TMP"
pass "block knowledge: creates, updates, and migrates legacy markers"

# 4. block decisions --body-file -
BS_TMP="$(mktemp -d)"
(
  cd "$BS_TMP"
  mkdir -p .rota
  CUSTOM_BODY="## Project Decisions

Custom intro.

- Topic A"
  OUT=$(printf '%s' "$CUSTOM_BODY" | hvj block decisions --body-file -) || { echo "FAIL: block decisions exit non-zero: $OUT"; exit 1; }
  [ "$(jget data.status <<<"$OUT")" = "created" ] || { echo "FAIL: expected 'created', got '$OUT'"; exit 1; }
  grep -q "<!-- rota-decisions-start -->" CLAUDE.md || { echo "FAIL: start marker missing"; exit 1; }
  grep -q "<!-- rota-decisions-end -->" CLAUDE.md || { echo "FAIL: end marker missing"; exit 1; }
  grep -q "Topic A" CLAUDE.md || { echo "FAIL: body content missing"; exit 1; }
)
rm -rf "$BS_TMP"
pass "block decisions --body-file - writes stdin body wrapped in markers"

echo "milestone index heals archived Status line"
# Seed MILESTONES.md with a stale archived line; frontmatter says planned.
# milestone index must overwrite the archived line with planned.
HEAL_TMP="$(mktemp -d)"
(
  cd "$HEAL_TMP"
  git init -q
  git config user.email t@t && git config user.name t
  git checkout -q -b main 2>/dev/null || git branch -m main
  mkdir -p .rota/milestones
  printf -- '---\nid: M99\ntitle: Foo\nstatus: planned\ndepends: []\n---\nBody.\n' > .rota/milestones/M99.md
  printf '# MILESTONES\n\n## Active milestones\n\n_(none)_\n\n## Milestones\n\n### M99 — Foo\n\n**Status:** archived\n' > .rota/MILESTONES.md
  touch CLAUDE.md
  git add . && git commit -q -m "seed"
  hvj milestone index >/dev/null || { echo "FAIL: milestone index exit non-zero"; exit 1; }
  python3 -c "
import re, sys
ms = open('.rota/MILESTONES.md').read()
m = re.search(r'### M99 — Foo\n\n\*\*Status:\*\* (\w+)', ms)
if not (m and m.group(1) == 'planned'):
    print('heal failed; Status line:', m.group(0) if m else 'not found', file=sys.stderr)
    sys.exit(1)
" || { echo "FAIL: milestone index did not heal archived -> planned"; exit 1; }
)
rm -rf "$HEAL_TMP"
pass "milestone index heals stale 'archived' Status line to match frontmatter"

echo "## backlog milestones"
FM4I_TMP="$(mktemp -d)"
(
  cd "$FM4I_TMP"
  mkdir -p .rota
  cat > .rota/BACKLOG.md <<'EOF'
# TODO

## Bugs
- **[B70] [P1] Single-tag bug.** Desc. Milestone: M01

## Features
- **[F70] [Minor] Multi-tag feature.** Desc. Milestone: M02, M10
- **[F71] [Cosmetic] Untagged feature.** Just a tweak.

## Tasks
- **[T70] Plain task tagged M01.** Body. Milestone: M01

## Completed
- ~~**[F99] [Minor] Should not surface.** Desc. Milestone: M99~~ Done 2026-05-01 [`abc1234`]
EOF

  # 1. Unknown IDs → silent, exit 0
  OUT=$(hvj backlog milestones ZZ99) || fail "exit non-zero on unknown ID"
  [ "$(jget data.milestones <<<"$OUT")" = "[]" ] || fail "unknown ID produced output: '$OUT'"

  # 2. Single tag
  OUT=$(hvj backlog milestones B70) || fail "single-tag lookup failed"
  [ "$(jget data.milestones <<<"$OUT")" = '["M01"]' ] || fail "single-tag wrong: '$OUT'"

  # 3. Multi-tag
  OUT=$(hvj backlog milestones F70) || fail "multi-tag lookup failed"
  [ "$(jget data.milestones <<<"$OUT")" = '["M02","M10"]' ] || fail "multi-tag wrong: '$OUT'"

  # 4. Dedup across input IDs sharing M01
  OUT=$(hvj backlog milestones B70 T70) || fail "dedup lookup failed"
  [ "$(jget data.milestones <<<"$OUT")" = '["M01"]' ] || fail "dedup wrong: '$OUT'"

  # 5. Completed items don't surface
  OUT=$(hvj backlog milestones F99) || fail "completed lookup failed"
  [ "$(jget data.milestones <<<"$OUT")" = "[]" ] || fail "completed item leaked milestone: '$OUT'"

  # 6. Untagged item is silent
  OUT=$(hvj backlog milestones F71) || fail "untagged lookup failed"
  [ "$(jget data.milestones <<<"$OUT")" = "[]" ] || fail "untagged item leaked: '$OUT'"

  # 7. Numeric sort: M01 < M02 < M10
  OUT=$(hvj backlog milestones B70 F70 T70) || fail "sort lookup failed"
  [ "$(jget data.milestones <<<"$OUT")" = '["M01","M02","M10"]' ] || fail "sort wrong: '$OUT'"

  # 8. No args → exit 2 (usage)
  rc=0; hvj backlog milestones >/dev/null 2>&1 || rc=$?
  [ "$rc" = "2" ] || fail "no-args case: expected exit 2, got $rc"
)
rm -rf "$FM4I_TMP"
pass "backlog milestones: lookup semantics, dedup, sort, open-sections only"

echo "## plan rename-check"
PRC_TMP="$(mktemp -d)"
(
  cd "$PRC_TMP"
  mkdir -p .rota
  git init -q
  git config user.email t@t && git config user.name t

  # 1. No args → exit 2 (usage)
  rc=0; hvj plan rename-check >/dev/null 2>&1 || rc=$?
  [ "$rc" = "2" ] || fail "no-args case: expected exit 2, got $rc"

  # 2. Single-file match
  printf 'line one referencing OLDNAME\n' > foo.txt
  git add foo.txt && git commit -q -m seed
  OUT=$(hvj plan rename-check OLDNAME) || fail "single-file lookup failed"
  [ "$(jget data.files <<<"$OUT")" = '["foo.txt"]' ] || fail "single-file match wrong: '$OUT'"

  # 3. Multi-file match
  printf 'also has OLDNAME here\n' > bar.md
  printf 'unrelated content\n' > baz.txt
  git add bar.md baz.txt && git commit -q -m "add more"
  OUT=$(hvj plan rename-check OLDNAME) || fail "multi-file lookup failed"
  FILES=$(jget data.files <<<"$OUT")
  grep -q '"foo.txt"' <<<"$FILES" || fail "multi-file missing foo.txt: '$OUT'"
  grep -q '"bar.md"' <<<"$FILES" || fail "multi-file missing bar.md: '$OUT'"
  if grep -q '"baz.txt"' <<<"$FILES"; then fail "matched unrelated baz.txt: '$OUT'"; fi

  # 4. No matches → empty list, exit 0
  OUT=$(hvj plan rename-check NEVER_REFERENCED) || fail "no-match exit non-zero"
  [ "$(jget data.files <<<"$OUT")" = "[]" ] || fail "no-match produced output: '$OUT'"

  # 5. Scope pathspec after --
  OUT=$(hvj plan rename-check OLDNAME -- '*.md') || fail "scope lookup failed"
  [ "$(jget data.files <<<"$OUT")" = '["bar.md"]' ] || fail "scope filter wrong: '$OUT'"
)
rm -rf "$PRC_TMP"

# 6. Outside any git repo
PRC_NON="$(mktemp -d)"
(
  cd "$PRC_NON"
  OUT=$(hvj plan rename-check ANYTHING) || fail "non-repo exit non-zero"
  [ "$(jget data.files <<<"$OUT")" = "[]" ] || fail "non-repo produced output: '$OUT'"
)
rm -rf "$PRC_NON"
pass "plan rename-check: lookup semantics, multi-file, scope filter, non-repo silence"

echo "## knowledge add"
KM_TMP="$(mktemp -d)"
(
  cd "$KM_TMP"
  mkdir -p .rota
  cat > .rota/KNOWLEDGE.md <<'EOF'
# Knowledge

## Existing Topic

- **Older rule** — body of the older rule. <!-- 2026-04-01 -->
- legacy bullet without a title <!-- 2026-03-15 -->
EOF

  # 1. Missing args → exit 2
  rc=0; hvj knowledge add >/dev/null 2>&1 || rc=$?
  [ "$rc" = "2" ] || fail "no-args: expected exit 2, got $rc"
  rc=0; hvj knowledge add --topic "Existing Topic" --body-file - <<<"x" >/dev/null 2>&1 || rc=$?
  [ "$rc" = "2" ] || fail "missing --title: expected exit 2, got $rc"

  # 2. Missing topic → exit 3
  rc=0; hvj knowledge add --topic "Nonexistent" --title "X" --body-file - <<<"body" >/dev/null 2>&1 || rc=$?
  [ "$rc" = "3" ] || fail "missing topic: expected exit 3, got $rc"

  # 3. Successful insert prepends bullet at top of topic
  OUT=$(hvj knowledge add --topic "Existing Topic" --title "Fresh insight" --date 2026-05-11 --body-file - <<<"Fresh insight body.") || fail "insert failed: $OUT"
  [ "$(jget data.changed <<<"$OUT")" = "true" ] || fail "insert: expected changed true: $OUT"
  grep -q "^- \*\*Fresh insight\*\* — Fresh insight body\. <!-- 2026-05-11 -->" .rota/KNOWLEDGE.md || fail "insert wrong format"
  # The new bullet must come BEFORE 'Older rule'
  python3 -c "
import sys
content = open('.rota/KNOWLEDGE.md').read()
fresh_idx = content.index('**Fresh insight**')
older_idx = content.index('**Older rule**')
assert fresh_idx < older_idx, f'Fresh insight ({fresh_idx}) should come before Older rule ({older_idx})'
" || fail "ordering wrong: Fresh insight should be above Older rule"
  # Legacy bullet preserved
  grep -q "^- legacy bullet without a title <!-- 2026-03-15 -->" .rota/KNOWLEDGE.md || fail "legacy bullet lost"

  # 4. Idempotent: calling with the same title is a no-op
  COUNT_BEFORE=$(grep -c "^\- \*\*Fresh insight\*\*" .rota/KNOWLEDGE.md)
  OUT=$(hvj knowledge add --topic "Existing Topic" --title "fresh insight" --date 2026-05-11 --body-file - <<<"Different body, same title.") || fail "dedup call failed: $OUT"
  [ "$(jget data.changed <<<"$OUT")" = "false" ] || fail "dedup: expected changed false: $OUT"
  COUNT_AFTER=$(grep -c "^\- \*\*Fresh insight\*\*" .rota/KNOWLEDGE.md)
  [ "$COUNT_BEFORE" = "$COUNT_AFTER" ] || fail "dedup failed: title repeated (count went from $COUNT_BEFORE to $COUNT_AFTER)"

  # 5. --body-file <path> alternative (the old inline --body flag is gone)
  printf 'Body passed from a file.\n' > body.txt
  hvj knowledge add --topic "Existing Topic" --title "Body via file" --body-file body.txt --date 2026-05-11 >/dev/null || fail "--body-file path add failed"
  grep -q "^- \*\*Body via file\*\* — Body passed from a file\. <!-- 2026-05-11 -->" .rota/KNOWLEDGE.md || fail "--body-file path wrong"
)
rm -rf "$KM_TMP"
pass "knowledge add: argv, missing topic, insert-at-top, idempotent dedup, --body-file path"

echo "## knowledge amend"
KA_TMP="$(mktemp -d)"
(
  cd "$KA_TMP"
  mkdir -p .rota
  cat > .rota/KNOWLEDGE.md <<'EOF'
# Knowledge

## Topic A

- **First rule** — body with unique fragment ALPHA. <!-- 2026-04-01 -->
- **Second rule** — body with unique fragment BETA. <!-- 2026-04-02 -->

## Topic B

- **Third rule** — body with fragment GAMMA. <!-- 2026-04-03 -->
EOF

  # 1. Missing args → exit 2
  rc=0; hvj knowledge amend >/dev/null 2>&1 || rc=$?
  [ "$rc" = "2" ] || fail "no-args: expected exit 2, got $rc"

  # 2. Missing topic → exit 3
  rc=0; hvj knowledge amend --topic "Nonexistent" --fragment "X" --mode append --body-file - <<<"Y" >/dev/null 2>&1 || rc=$?
  [ "$rc" = "3" ] || fail "missing topic: expected exit 3, got $rc"

  # 3. No matching fragment → exit 3
  rc=0; hvj knowledge amend --topic "Topic A" --fragment "NOTHING" --mode append --body-file - <<<"X" >/dev/null 2>&1 || rc=$?
  [ "$rc" = "3" ] || fail "missing fragment: expected exit 3, got $rc"

  # 4. Successful append after the trailing date comment
  OUT=$(hvj knowledge amend --topic "Topic A" --fragment "ALPHA" --mode append --body-file - <<<"Upstream: rota#42") || fail "append failed: $OUT"
  [ "$(jget data.changed <<<"$OUT")" = "true" ] || fail "append: expected changed true: $OUT"
  grep -q "^- \*\*First rule\*\* — body with unique fragment ALPHA\. <!-- 2026-04-01 --> Upstream: rota#42$" .rota/KNOWLEDGE.md || fail "append wrong (Topic A First rule)"

  # 5. Other bullets and topics untouched
  grep -q "^- \*\*Second rule\*\* — body with unique fragment BETA\. <!-- 2026-04-02 -->$" .rota/KNOWLEDGE.md || fail "Second rule changed unexpectedly"
  grep -q "^- \*\*Third rule\*\* — body with fragment GAMMA\. <!-- 2026-04-03 -->$" .rota/KNOWLEDGE.md || fail "Topic B Third rule changed unexpectedly"

  # 6. Fragment must be within the named topic (Topic B has GAMMA, calling with Topic A should miss)
  rc=0; hvj knowledge amend --topic "Topic A" --fragment "GAMMA" --mode append --body-file - <<<"WRONG" >/dev/null 2>&1 || rc=$?
  [ "$rc" = "3" ] || fail "fragment leaked across topics (Topic A should not match GAMMA from Topic B): exit $rc"
)
rm -rf "$KA_TMP"
pass "knowledge amend: argv, missing topic, missing fragment, scoped append, other-bullet preservation"
