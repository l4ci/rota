echo "milestone add / status / active / list / index"
# Reset milestones counter so the next mint is M01.
python3 -c "
import json
p='.rota/counters.json'
d=json.load(open(p)); d['milestones']=0; json.dump(d,open(p,'w'))
"
# Re-seed MILESTONES.md (earlier `rota block knowledge` test rewrote CLAUDE.md, but
# MILESTONES.md is untouched).
cat > .rota/MILESTONES.md <<'EOF'
# Milestones

Test project vision.

## Active milestones

_(none active — set with `/rota-vision`)_

## Milestones
EOF
mkdir -p .rota/milestones

OUT=$(hvj milestone add --title "Auth foundation" --summary "OAuth + sessions for end users.") || fail "milestone add failed: $OUT"
ID_M1=$(jget data.id <<<"$OUT")
[ "$ID_M1" = "M01" ] || fail "expected M01 from milestone add, got $ID_M1"
[ "$(jget data.changed <<<"$OUT")" = "true" ] || fail "milestone add should report changed: $OUT"
[ -f .rota/milestones/M01.md ] || fail "M01 detail file not created"
grep -q "^id: M01$" .rota/milestones/M01.md || fail "M01 frontmatter missing id"
grep -q "^status: planned$" .rota/milestones/M01.md || fail "M01 status not planned"
grep -q "### M01 — Auth foundation" .rota/MILESTONES.md || fail "M01 not in MILESTONES.md"
grep -q "Status:\*\* planned" .rota/MILESTONES.md || fail "M01 overview missing status"
pass "milestone add creates detail file + overview entry"

ID_M2=$(hvj milestone add --title "Multi-tenant" --summary "Org isolation for B2B." --depends M01 | jget data.id) || fail "milestone add --depends failed"
[ "$ID_M2" = "M02" ] || fail "expected M02, got $ID_M2"
grep -q "^depends: \[M01\]$" .rota/milestones/M02.md || fail "M02 depends not [M01]"
grep -q "Depends:\*\* M01" .rota/MILESTONES.md || fail "M02 overview missing depends"
pass "milestone add records dependencies"

OUT=$(hvj milestone status M01 --to active) || fail "milestone status failed: $OUT"
[ "$(jget data.changed <<<"$OUT")" = "true" ] || fail "status change should report changed: $OUT"
grep -q "^status: active$" .rota/milestones/M01.md || fail "M01 status not updated to active in detail"
grep -q "### M01 — Auth foundation" .rota/MILESTONES.md || fail "M01 section gone"
# Confirm overview status line for M01 is now active
python3 -c "
import re, sys
ms = open('.rota/MILESTONES.md').read()
m = re.search(r'### M01 — Auth foundation\n\n\*\*Status:\*\* (\w+)', ms)
sys.exit(0 if (m and m.group(1) == 'active') else 1)
" || fail "M01 overview status not updated to active"
OUT=$(hvj milestone status M01 --to active) || fail "repeat milestone status failed: $OUT"
[ "$(jget data.changed <<<"$OUT")" = "false" ] || fail "repeat status should report changed false: $OUT"
pass "milestone status updates frontmatter and overview"

ACTIVE=$(hvj milestone active | jget data.ids) || fail "milestone active failed"
[ "$ACTIVE" = '["M01"]' ] || fail "expected active=[M01], got '$ACTIVE'"
pass "milestone active lists only active IDs"

LIST=$(hvj milestone list) || fail "milestone list failed"
echo "$LIST" | python3 -c "
import json, sys
data = json.load(sys.stdin)['data']['milestones']
ids = {i['id']: i for i in data}
assert 'M01' in ids and 'M02' in ids, f'missing IDs: {ids.keys()}'
assert ids['M02']['depends'] == ['M01'], f'M02 depends: {ids[\"M02\"][\"depends\"]}'
assert ids['M02']['ready'] is False, 'M02 should not be ready (M01 not shipped)'
assert ids['M01']['ready'] is True, 'M01 has no deps; should be ready'
" || fail "milestone list output did not match expectations"
pass "milestone list emits status, depends, ready"

hvj milestone index >/dev/null || fail "milestone index failed"
grep -q "<!-- rota-vision-start -->" CLAUDE.md || fail "vision block not in CLAUDE.md"
grep -q "M01.*Auth foundation" CLAUDE.md || fail "active milestone not in CLAUDE.md vision block"
# Re-running idempotent
OUT=$(hvj milestone index) || fail "second milestone index failed"
[ "$(jget data.changed <<<"$OUT")" = "false" ] || fail "second milestone index should report changed false: $OUT"
COUNT_VISION=$(grep -c "rota-vision-start" CLAUDE.md)
[ "$COUNT_VISION" = "1" ] || fail "vision block duplicated"
pass "milestone index updates CLAUDE.md and active section in MILESTONES.md"

# Active section in MILESTONES.md should now reflect M01
grep -q "^- M01 — Auth foundation" .rota/MILESTONES.md || fail "## Active milestones not updated"
pass "milestone index regenerates ## Active milestones section"

# Marking M01 shipped should mark M02 as ready
hvj milestone status M01 --to shipped >/dev/null || fail "milestone status shipped failed"
LIST=$(hvj milestone list) || fail "milestone list failed"
echo "$LIST" | python3 -c "
import json, sys
data = json.load(sys.stdin)['data']['milestones']
m02 = next(i for i in data if i['id'] == 'M02')
sys.exit(0 if m02['ready'] else 1)
" || fail "M02 should be ready once M01 shipped"
pass "milestone list marks ready when dependencies are shipped"

echo "backlog ids --milestone / Milestone field on entries"
# Reactivate M01 and tag a couple of TODO entries.
hvj milestone status M01 --to active >/dev/null || fail "reactivating M01 failed"
cat > .rota/BACKLOG.md <<'EOF'
# TODO

## Bugs
- **[B60] [P1] Auth flicker.** Sign-in card flashes on render. Related: [F60] Milestone: M01

## Features
- **[F60] [Minor] OAuth rotation.** Refresh tokens before expiry. Milestone: M01, M02
- **[F61] [Cosmetic] Untagged feature.** Just a tweak.

## Tasks

## Completed
EOF
TAGGED=$(hvj backlog ids --milestone M01 | jget data.ids) || fail "backlog ids M01 failed"
grep -q '"B60"' <<<"$TAGGED" || fail "backlog ids missed B60 (M01): '$TAGGED'"
grep -q '"F60"' <<<"$TAGGED" || fail "backlog ids missed F60 (M01): '$TAGGED'"
if grep -q '"F61"' <<<"$TAGGED"; then fail "backlog ids returned untagged F61"; fi
pass "backlog ids --milestone returns only tagged items"
TAGGED2=$(hvj backlog ids --milestone M02 | jget data.ids) || fail "backlog ids M02 failed"
grep -q '"F60"' <<<"$TAGGED2" || fail "backlog ids missed F60 (M02 multi-tag)"
pass "backlog ids --milestone handles multi-milestone tags"

echo "backlog list regression: Milestone field doesn't leak into Related"
OUT=$(hvj backlog list) || fail "backlog list failed"
[ "$(jget 'data.bugs[0].related' <<<"$OUT")" = '["F60"]' ] || fail "B60 related should be exactly [F60] (no Milestone bleed): $OUT"
[ "$(jget 'data.bugs[0].milestone' <<<"$OUT")" = "M01" ] || fail "B60 milestone should be M01: $OUT"
[ "$(jget 'data.features[1].milestone' <<<"$OUT")" = "M01, M02" ] || fail "F60 multi-milestone value missing: $OUT"
pass "backlog list carries milestone without breaking related"

echo "summary surfaces active milestones"
OUT=$(hvj summary) || fail "summary failed"
[ "$(jget 'data.milestones[0].id' <<<"$OUT")" = "M01" ] || fail "summary missing active milestone: $OUT"
pass "summary lists active milestones"

# Reset summary fixtures (no Milestone field) to keep later assertions clean.
hvj milestone status M01 --to shipped >/dev/null || fail "milestone status shipped failed"

echo "init"
# Self-contained: run in a fresh subdir so the existing .rota/ in TMP isn't touched.
BOOT_DIR="$TMP/boot-test"
mkdir -p "$BOOT_DIR"
OUT=$(hvj -C "$BOOT_DIR" init) || fail "init failed: $OUT"
[ "$(jget data.changed <<<"$OUT")" = "true" ] || fail "init in an empty dir should report changed: $OUT"
[ -f "$BOOT_DIR/.rota/BACKLOG.md" ] || fail "init did not seed BACKLOG.md"
[ -f "$BOOT_DIR/.rota/KNOWLEDGE.md" ] || fail "init did not seed KNOWLEDGE.md"
[ -f "$BOOT_DIR/.rota/MILESTONES.md" ] || fail "init did not seed MILESTONES.md"
[ -f "$BOOT_DIR/.rota/counters.json" ] || fail "init did not seed counters.json"
[ -f "$BOOT_DIR/.rota/status.json" ] || fail "init did not seed status.json"
grep -q '^\.rota/' "$BOOT_DIR/.gitignore" || fail "init did not add .rota/ to .gitignore"
grep -q '"milestones": *0' "$BOOT_DIR/.rota/counters.json" || fail "init counters.json missing milestones key"
pass "init seeds dirs, data files, and .gitignore"

HEADING=$(head -1 "$BOOT_DIR/.rota/MILESTONES.md")
[ "$HEADING" = "# Milestones" ] || fail "init seeded MILESTONES.md with wrong H1: '$HEADING' (want '# Milestones')"
pass "init seeds MILESTONES.md with '# Milestones' H1"

# Idempotency: re-running must not overwrite existing data.
echo "user content" > "$BOOT_DIR/.rota/BACKLOG.md"
hvj -C "$BOOT_DIR" init >/dev/null || fail "second init failed"
grep -q "^user content$" "$BOOT_DIR/.rota/BACKLOG.md" || fail "init overwrote existing BACKLOG.md"
pass "init is idempotent (preserves existing files)"

rm -rf "$BOOT_DIR"

echo "init check"
# Ensure all core data files exist (smoke setup creates BACKLOG.md/counters.json/status.json;
# earlier sections seed KNOWLEDGE.md and DECISIONS.md, so a SECTION_LIST run seeds them here).
[ -f .rota/config.json ] || echo '{}' > .rota/config.json
[ -f .rota/KNOWLEDGE.md ] || printf '# Knowledge\n' > .rota/KNOWLEDGE.md
[ -f .rota/DECISIONS.md ] || printf '# Decisions\n' > .rota/DECISIONS.md

# 1. Everything present → init check passes.
OUT=$(hvj init check) || fail "init check failed on fully initialized project: $OUT"
[ "$(jget data.initialized <<<"$OUT")" = "true" ] || fail "init check should report initialized: $OUT"
[ "$(jget data.missing <<<"$OUT")" = "[]" ] || fail "init check should report nothing missing: $OUT"
pass "init check passes when fully initialized"

# 2. Missing core data file → exit 1 (uninitialized), named in data.missing.
mv .rota/BACKLOG.md .rota/BACKLOG.md.bak
rc=0
OUT=$(hvj init check 2>/dev/null) || rc=$?
[ "$rc" = "1" ] || fail "expected exit 1 (uninitialized), got $rc"
[ "$(jget data.initialized <<<"$OUT")" = "false" ] || fail "init check should report initialized false: $OUT"
MISSING=$(jget data.missing <<<"$OUT") || fail "init check data.missing absent: $OUT"
grep -q '".rota/BACKLOG.md"' <<<"$MISSING" || fail "init check should name .rota/BACKLOG.md as missing: $OUT"
pass "init check exits 1 when a data file is missing"
mv .rota/BACKLOG.md.bak .rota/BACKLOG.md

echo "plan add / list / show / rm"
KEY1=$(hvj plan add --milestone M01 --slice --title "Auth foundation" | jget data.key) || fail "plan add --slice failed"
[ "$KEY1" = "M01-S01" ] || fail "expected M01-S01, got $KEY1"
[ -f .rota/plans/M01-S01.md ] || fail "M01-S01.md not created"
grep -q "^key: M01-S01$" .rota/plans/M01-S01.md || fail "key field missing"
grep -q "^unitKind: slice$" .rota/plans/M01-S01.md || fail "unitKind not slice"
grep -q "title: Auth foundation" .rota/plans/M01-S01.md || fail "title missing from plan"
pass "first slice plan = M01-S01"

OUT=$(hvj plan add --milestone M01 --slice --title "Auth refresh") || fail "second plan add --slice failed"
KEY2=$(jget data.key <<<"$OUT")
[ "$KEY2" = "M01-S02" ] || fail "expected M01-S02, got $KEY2"
[ "$(jget data.unitKind <<<"$OUT")" = "slice" ] || fail "slice plan unitKind should be slice: $OUT"
pass "second slice plan auto-mints M01-S02"

OUT=$(hvj plan add M01-B07 --title "Sign-in flicker") || fail "item plan add failed"
KEY3=$(jget data.key <<<"$OUT")
[ "$KEY3" = "M01-B07" ] || fail "expected M01-B07, got $KEY3"
[ "$(jget data.unitKind <<<"$OUT")" = "item" ] || fail "item plan unitKind should be item: $OUT"
[ -f .rota/plans/M01-B07.md ] || fail "M01-B07.md not created"
grep -q "^unitKind: item$" .rota/plans/M01-B07.md || fail "unitKind not item"
pass "item plan uses item ID verbatim"

rc=0; OUT=$(hvj plan add M01-B07 --title "Duplicate" 2>/dev/null) || rc=$?
[ "$rc" = 4 ] || fail "plan add should refuse an existing key with exit 4 (got $rc): $OUT"
[ "$(jget data.blockedBy <<<"$OUT")" = "exists" ] || fail "plan add duplicate should be blockedBy exists: $OUT"
pass "plan add rejects existing key"

rc=0; hvj plan add not-a-milestone-S01 --title "x" >/dev/null 2>&1 || rc=$?
[ "$rc" = 2 ] || fail "plan add should reject a malformed milestone with exit 2 (got $rc)"
pass "plan add rejects malformed milestone"

rc=0; hvj plan add M01-bogus --title "x" >/dev/null 2>&1 || rc=$?
[ "$rc" = 2 ] || fail "plan add should reject a malformed unit with exit 2 (got $rc)"
pass "plan add rejects malformed unit"

hvj plan add --milestone M02 --slice --title "Multi-tenant" >/dev/null || fail "plan add M02 slice failed"
LIST=$(hvj plan list) || fail "plan list failed"
echo "$LIST" | python3 -c "
import json, sys
data = json.load(sys.stdin)['data']['plans']
keys = {i['key']: i for i in data}
for k in ('M01-S01', 'M01-S02', 'M01-B07', 'M02-S01'):
    assert k in keys, f'missing {k}'
assert keys['M01-S01']['unitKind'] == 'slice'
assert keys['M01-B07']['unitKind'] == 'item'
assert keys['M01-B07']['milestone'] == 'M01'
" || fail "plan list output did not match"
pass "plan list emits all plans with correct fields"

LIST_M01=$(hvj plan list --milestone M01) || fail "plan list --milestone failed"
echo "$LIST_M01" | python3 -c "
import json, sys
data = json.load(sys.stdin)['data']['plans']
mss = {i['milestone'] for i in data}
assert mss == {'M01'}, f'leak: {mss}'
" || fail "plan list --milestone M01 leaked other milestones"
pass "plan list filters by milestone"

SHOW=$(hvj plan show M01-S01 | jget data.body) || fail "plan show failed"
grep -q "^# M01-S01 — Auth foundation" <<<"$SHOW" || fail "show output missing title"
pass "plan show prints content"

rc=0; hvj plan show M99-S99 >/dev/null 2>&1 || rc=$?
[ "$rc" = 3 ] || fail "plan show should exit 3 on an unknown key (got $rc)"
pass "plan show rejects unknown key"

hvj plan rm M01-B07 >/dev/null || fail "plan rm failed"
[ -f .rota/plans/M01-B07.md ] && fail "M01-B07 not removed"
pass "plan rm deletes plan"

rc=0; hvj plan rm M99-S99 >/dev/null 2>&1 || rc=$?
[ "$rc" = 3 ] || fail "plan rm should exit 3 on an unknown key (got $rc)"
pass "plan rm rejects unknown key"

echo "milestone show"

# milestone show positive — M01 fixture exists from earlier in this section
SHOW=$(hvj milestone show M01 | jget data.body) || fail "milestone show M01 failed"
grep -q "^id: M01$" <<<"$SHOW" || fail "milestone show should print id: M01 frontmatter"
pass "milestone show prints M01 content"

# milestone show miss — exit 3
rc=0; hvj milestone show M99 >/dev/null 2>&1 || rc=$?
[ "$rc" = 3 ] || fail "milestone show M99 (missing) should exit 3 (got $rc)"
pass "milestone show rejects unknown ID"

# milestone show bad-shape — exit 2
rc=0; hvj milestone show foo >/dev/null 2>&1 || rc=$?
[ "$rc" = 2 ] || fail "milestone show 'foo' (bad shape) should exit 2 (got $rc)"
pass "milestone show rejects bad-shape ID"

echo "spike add / list / finish"
git checkout -q main 2>/dev/null || true

OUT=$(hvj spike add sse-feasibility --question "Can SSE work over our nginx without proxy buffering?") || fail "spike add failed: $OUT"
[ "$(jget data.branch <<<"$OUT")" = "spike/sse-feasibility" ] || fail "expected spike/sse-feasibility, got $OUT"
[ -f .rota/spikes/sse-feasibility.md ] || fail "spike file not created"
git rev-parse --verify spike/sse-feasibility >/dev/null 2>&1 || fail "spike branch not created"
grep -q "^name: sse-feasibility$" .rota/spikes/sse-feasibility.md || fail "spike name missing"
grep -q "^status: open$" .rota/spikes/sse-feasibility.md || fail "spike status not open"
grep -q "Can SSE work" .rota/spikes/sse-feasibility.md || fail "question not embedded"
pass "spike add creates branch and file"

rc=0; hvj spike add "Bad Name" --question "?" >/dev/null 2>&1 || rc=$?
[ "$rc" = 2 ] || fail "spike add should reject a bad name with exit 2 (got $rc)"
pass "spike add rejects bad name"

rc=0; hvj spike add sse-feasibility --question "?" >/dev/null 2>&1 || rc=$?
[ "$rc" = 4 ] || fail "spike add should refuse an existing spike with exit 4 (got $rc)"
pass "spike add rejects existing branch"

SLIST=$(hvj spike list) || fail "spike list failed"
echo "$SLIST" | python3 -c "
import json, sys
data = json.load(sys.stdin)['data']['spikes']
sse = next((s for s in data if s['name'] == 'sse-feasibility'), None)
assert sse is not None, 'sse-feasibility missing'
assert sse['branch'] == 'spike/sse-feasibility', f'wrong branch: {sse[\"branch\"]}'
assert sse['status'] == 'open', f'wrong status: {sse[\"status\"]}'
assert sse['branchExists'] is True, 'branchExists should be True'
" || fail "spike list output did not match"
pass "spike list emits spikes with branch state"

OUT=$(hvj spike finish sse-feasibility) || fail "spike finish failed: $OUT"
[ "$(jget data.changed <<<"$OUT")" = "true" ] || fail "spike finish should report changed: $OUT"
grep -q "^status: done$" .rota/spikes/sse-feasibility.md || fail "spike status not done"
grep -q "^finished:" .rota/spikes/sse-feasibility.md || fail "spike finished date missing"
OUT=$(hvj spike finish sse-feasibility) || fail "repeat spike finish failed: $OUT"
[ "$(jget data.changed <<<"$OUT")" = "false" ] || fail "repeat spike finish should report changed false: $OUT"
pass "spike finish flips status to done"

rc=0; hvj spike finish not-a-spike >/dev/null 2>&1 || rc=$?
[ "$rc" = 3 ] || fail "spike finish should exit 3 on an unknown name (got $rc)"
pass "spike finish rejects unknown name"

echo "spike show"

# spike show positive — sse-feasibility fixture exists from earlier in this block
SHOW=$(hvj spike show sse-feasibility | jget data.body) || fail "spike show failed"
grep -q "^name: sse-feasibility$" <<<"$SHOW" || fail "spike show should print name: sse-feasibility frontmatter"
pass "spike show prints sse-feasibility content"

# spike show miss — exit 3
rc=0; hvj spike show not-a-spike >/dev/null 2>&1 || rc=$?
[ "$rc" = 3 ] || fail "spike show not-a-spike (missing) should exit 3 (got $rc)"
pass "spike show rejects unknown name"

# spike show bad-shape — exit 2 (space in name)
rc=0; hvj spike show "Bad Name" >/dev/null 2>&1 || rc=$?
[ "$rc" = 2 ] || fail "spike show 'Bad Name' (bad shape) should exit 2 (got $rc)"
pass "spike show rejects bad-shape name"

git branch -D spike/sse-feasibility >/dev/null 2>&1 || true

echo "items <-> milestones <-> plans triangle"
# Reset BACKLOG.md to a known state, mint a fresh bug ID via id next, append a
# Milestone-tagged entry, and verify the full chain: backlog ids --milestone picks
# it up, plan add mints a plan keyed under the same milestone, plan list
# surfaces it.
cat > .rota/BACKLOG.md <<'EOF'
# TODO

## Bugs

## Features

## Tasks

## Completed
EOF
TRI_BUG=$(hvj id next --kind bugs | jget data.id) || fail "id next failed"
printf '%s\n' "- **[$TRI_BUG] [P1] Triangle bug.** Desc. Milestone: M01" > "$TMP/tri-bug.md"
hvj item create --kind bugs --raw-file "$TMP/tri-bug.md" >/dev/null || fail "item create --raw-file failed"
rm -f "$TMP/tri-bug.md"
TAGGED=$(hvj backlog ids --milestone M01 | jget data.ids) || fail "backlog ids M01 failed"
grep -q "\"$TRI_BUG\"" <<<"$TAGGED" || fail "triangle: $TRI_BUG not found in M01 items: '$TAGGED'"
pass "triangle: tagged bug surfaces in backlog ids M01"

TRI_KEY=$(hvj plan add "M01-$TRI_BUG" --title "Triangle bug fix" | jget data.key) || fail "triangle: plan add failed"
[ "$TRI_KEY" = "M01-$TRI_BUG" ] || fail "triangle: expected plan key M01-$TRI_BUG, got $TRI_KEY"
[ -f ".rota/plans/M01-$TRI_BUG.md" ] || fail "triangle: plan file .rota/plans/M01-$TRI_BUG.md missing"
pass "triangle: plan add minted plan keyed M01-$TRI_BUG"

LIST_M01=$(hvj plan list --milestone M01) || fail "triangle: plan list failed"
echo "$LIST_M01" | TRI_BUG="$TRI_BUG" python3 -c "
import json, sys, os
key = 'M01-' + os.environ['TRI_BUG']
data = json.load(sys.stdin)['data']['plans']
keys = {i['key']: i for i in data}
assert key in keys, f'missing {key} in {list(keys)}'
assert keys[key]['unitKind'] == 'item', f'unitKind: {keys[key][\"unitKind\"]}'
assert keys[key]['milestone'] == 'M01', f'milestone: {keys[key][\"milestone\"]}'
" || fail "triangle: plan list M01 missing $TRI_KEY entry"
pass "triangle: plan list M01 reports new plan with item unitKind"

# Cleanup the triangle plan to avoid polluting later assertions.
hvj plan rm "$TRI_KEY" >/dev/null || fail "triangle: plan rm failed"

echo "backlog ids field-order regression"
# Wave 1 made the regex order-agnostic. Guard against a future regression by
# tagging Milestone: in three different positions: first, middle, last.
cat > .rota/BACKLOG.md <<'EOF'
# TODO

## Bugs
- **[B71] [P1] Milestone first.** Milestone: M01 Detail: `.rota/bugs/B71.md` Related: [F71]
- **[B72] [P1] Milestone middle.** Detail: `.rota/bugs/B72.md` Milestone: M01 Related: [F71]
- **[B73] [P1] Milestone last.** Detail: `.rota/bugs/B73.md` Related: [F71] Milestone: M01

## Features

## Tasks

## Completed
EOF
TAGGED=$(hvj backlog ids --milestone M01 | jget data.ids) || fail "backlog ids M01 failed"
for ID in B71 B72 B73; do
  grep -q "\"$ID\"" <<<"$TAGGED" || fail "field-order: $ID missing from M01 items (regex regressed?): '$TAGGED'"
done
pass "backlog ids is order-agnostic across Detail/Related/Milestone"

echo "backlog list field-order regression"
# Guard against parse_todo_fields regressions: Milestone before Related, and after.
cat > .rota/BACKLOG.md <<'EOF'
# TODO

## Bugs
- **[B74] [P1] MS before Related.** Desc. Milestone: M01 Related: [F02]
- **[B75] [P1] MS after Related.** Desc. Related: [F02] Milestone: M01

## Features

## Tasks

## Completed
EOF
BL_OUT=$(hvj backlog list) || fail "backlog list failed"
for n in 0 1; do
  [ "$(jget "data.bugs[$n].milestone" <<<"$BL_OUT")" = "M01" ] || fail "backlog list field-order: bugs[$n] missing M01: '$BL_OUT'"
  [ "$(jget "data.bugs[$n].related" <<<"$BL_OUT")" = '["F02"]' ] || fail "backlog list field-order: bugs[$n] related should be [F02]: '$BL_OUT'"
done
pass "backlog list milestone and related correct regardless of field order"

echo "archived milestone status"
# Mint a fresh milestone, archive it, and verify exclusion + frontmatter + overview.
ARCH_ID=$(hvj milestone add --title "Throwaway prototype" --summary "Will be abandoned for testing." | jget data.id) || fail "milestone add failed"
ACTIVE_BEFORE=$(hvj milestone active | jget data.ids) || fail "milestone active failed"
if grep -q "\"$ARCH_ID\"" <<<"$ACTIVE_BEFORE"; then fail "archived test: $ARCH_ID was active before archival (unexpected)"; fi

hvj milestone status "$ARCH_ID" --to archived >/dev/null || fail "milestone status archived failed"
ACTIVE_AFTER=$(hvj milestone active | jget data.ids) || fail "milestone active failed"
if grep -q "\"$ARCH_ID\"" <<<"$ACTIVE_AFTER"; then fail "archived: $ARCH_ID still appears in milestone active"; fi
pass "archived milestone excluded from milestone active"

grep -q "^status: archived$" ".rota/milestones/$ARCH_ID.md" || fail "archived: frontmatter status not 'archived'"
pass "archived milestone frontmatter status updated"

ARCH_ID="$ARCH_ID" python3 -c "
import re, sys, os
mid = os.environ['ARCH_ID']
ms = open('.rota/MILESTONES.md').read()
m = re.search(rf'### {mid} — Throwaway prototype\n\n\*\*Status:\*\* (\w+)', ms)
sys.exit(0 if (m and m.group(1) == 'archived') else 1)
" || fail "archived: MILESTONES.md overview not 'archived'"
pass "archived milestone overview line reflects status"

# Reject unknown status values with the four-option usage error.
rc=0; hvj milestone status "$ARCH_ID" --to bogus >/dev/null 2>&1 || rc=$?
[ "$rc" = 2 ] || fail "archived: milestone status accepted a bogus status value (exit $rc, want 2)"
pass "milestone status rejects unknown status values"
