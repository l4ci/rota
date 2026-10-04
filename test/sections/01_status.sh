echo "id next"
ID=$(hvj id next --kind bugs | jget data.id)
[ "$ID" = "B01" ] || fail "expected B01, got $ID"
pass "first bug id = B01"

ID2=$(hvj id next --kind bugs | jget data.id)
[ "$ID2" = "B02" ] || fail "expected B02, got $ID2"
pass "second bug id = B02"

ID3=$(hvj id next --kind features | jget data.id)
[ "$ID3" = "F01" ] || fail "expected F01, got $ID3"
pass "first feature id = F01"

ID4=$(hvj id next --kind milestones | jget data.id)
[ "$ID4" = "M01" ] || fail "expected M01, got $ID4"
pass "first milestone id = M01"

COUNTERS=$(cat .rota/counters.json)
grep -q '"bugs": 2' <<<"$COUNTERS" || fail "counters.bugs != 2: $COUNTERS"
grep -q '"features": 1' <<<"$COUNTERS" || fail "counters.features != 1: $COUNTERS"
grep -q '"milestones": 1' <<<"$COUNTERS" || fail "counters.milestones != 1: $COUNTERS"
pass "counters persisted"

# Self-heal: counter=2, but TODO has [B07] → next mint should be B08, not B03.
cat > .rota/BACKLOG.md <<'EOF'
# TODO

## Bugs
- **[B07] [P1] Imported bug.** Desc.

## Features

## Tasks

## Completed
EOF
ID5=$(hvj id next --kind bugs | jget data.id)
[ "$ID5" = "B08" ] || fail "self-heal: expected B08 (max(2,7)+1), got $ID5"
pass "id next self-heals when TODO has higher IDs than counter"

# Self-heal: ARCHIVE.md is also scanned.
cat > .rota/ARCHIVE.md <<'EOF'
# Archive

- ~~**[B15] [P1] Old bug.** Desc.~~ Done 2026-01-01 [`abc1234`]
EOF
ID6=$(hvj id next --kind bugs | jget data.id)
[ "$ID6" = "B16" ] || fail "self-heal: expected B16 (ARCHIVE max=15), got $ID6"
pass "id next scans ARCHIVE.md for self-heal"

# Self-heal is per-prefix: features counter is unaffected by bugs traffic.
ID7=$(hvj id next --kind features | jget data.id)
[ "$ID7" = "F02" ] || fail "self-heal per-prefix: expected F02 (features counter still=1), got $ID7"
pass "id next self-heal is per-prefix"

# Reset state for downstream tests that expect a clean slate.
rm -f .rota/ARCHIVE.md
cat > .rota/BACKLOG.md <<'EOF'
# TODO

## Bugs

## Features

## Tasks

## Completed
EOF
echo '{"bugs":0,"features":0,"tasks":0,"milestones":0}' > .rota/counters.json

echo "item create --raw-file"
"$ROTA_BIN" item create --kind bugs --raw-file - <<<'- **[B01] [P1] First bug.** Desc.' >/dev/null
grep -q "\[B01\] \[P1\] First bug" .rota/BACKLOG.md || fail "B01 not found in BACKLOG.md"
pass "bug appended to ## Bugs (item create --raw-file)"

"$ROTA_BIN" item create --kind features --raw-file - <<<'- **[F01] [Minor] First feature.** Desc.' >/dev/null
grep -q "\[F01\] \[Minor\] First feature" .rota/BACKLOG.md || fail "F01 not found in BACKLOG.md"
pass "feature appended to ## Features (item create --raw-file)"

echo "item complete"
git add -A && git commit -q -m "add B01"
HASH=$(git log --oneline -1 --format='%h')
"$ROTA_BIN" item complete B01 --commit "$HASH" --no-proof >/dev/null
grep -q "~~.*\[B01\].*~~ Done" .rota/BACKLOG.md || fail "B01 not marked completed"
grep -q "^- \*\*\[B01\]" .rota/BACKLOG.md && fail "B01 still in active section"
pass "B01 moved to Completed with strikethrough"
# Proof gate (details in section 52): a done close without proof exits 4 unless --no-proof.
[ "$(hvj item create --kind bugs --raw-file - <<<'- **[B70] [P2] Unproven bug.** Desc.' | jget data.type)" = "B" ] \
  || fail "item create should report type B for a bug"
rc=0; OUT=$(hvj item complete B70 --commit "$HASH" 2>/dev/null) || rc=$?
[ "$rc" = "4" ] || fail "item complete without proof should exit 4, got $rc"
[ "$(echo "$OUT" | jget data.changed)" = "false" ] || fail "proof refusal should report changed=false: $OUT"
grep -q "^- \*\*\[B70\]" .rota/BACKLOG.md || fail "refused item complete must leave B70 open"
pass "item complete refuses a done close with no proof"

# Idempotent: running item complete again on an already-completed ID is a no-op.
rc=0; OUT=$(hvj item complete B01 --commit "$HASH" 2>/dev/null) || rc=$?
[ "$rc" = "0" ] || fail "second item complete errored on already-completed ID (rc $rc)"
[ "$(echo "$OUT" | jget data.changed)" = "false" ] || fail "second item complete should report changed=false: $OUT"
COMPLETED_COUNT=$(grep -c "~~.*\[B01\].*~~ Done" .rota/BACKLOG.md || true)
[ "$COMPLETED_COUNT" = "1" ] || fail "re-running item complete duplicated B01: found $COMPLETED_COUNT rows"
pass "item complete is idempotent on already-completed ID"

# Typo guard: an ID that is nowhere in BACKLOG.md still errors out.
rc=0; "$ROTA_BIN" item complete B99 --commit "$HASH" 2>/dev/null || rc=$?
[ "$rc" = "3" ] || fail "item complete should exit 3 on an unknown ID, got $rc"
pass "item complete rejects unknown ID"

# Closure reason: default path stays byte-identical; non-done reasons add a suffix.
"$ROTA_BIN" item create --kind bugs --raw-file - <<<'- **[B71] [P2] Reason bug.** Desc.' >/dev/null
"$ROTA_BIN" item create --kind bugs --raw-file - <<<'- **[B72] [P2] Blocked bug.** Desc.' >/dev/null
"$ROTA_BIN" item create --kind bugs --raw-file - <<<'- **[B73] [P2] Dropped bug.** Desc.' >/dev/null
"$ROTA_BIN" item complete B71 --commit "$HASH" --reason done --no-proof >/dev/null
grep -qF "Done $(date +%Y-%m-%d) [\`$HASH\`]" .rota/BACKLOG.md || fail "done reason changed marker"
grep -E "^- ~~.*\[B71\].*~~ Done [0-9-]+ \[\`$HASH\`\]$" .rota/BACKLOG.md >/dev/null || fail "--reason done must render the plain marker"
"$ROTA_BIN" item complete B72 --commit "$HASH" --reason blocked --note "waiting on upstream (see #9)" >/dev/null
grep -qF "[\`$HASH\`] (blocked: waiting on upstream (see #9))" .rota/BACKLOG.md || fail "blocked reason+note not rendered"
"$ROTA_BIN" item complete B73 --commit "$HASH" --reason dropped >/dev/null
grep -E "^- ~~.*\[B73\].*~~ Done [0-9-]+ \[\`$HASH\`\] \(dropped\)$" .rota/BACKLOG.md >/dev/null || fail "dropped reason without note not rendered"
[ "$(hvj item field get B72 --name reason | jget data.value)" = "blocked" ] || fail "item field reason"
[ "$(hvj item field get B72 --name note | jget data.value)" = "waiting on upstream (see #9)" ] || fail "item field note"
[ "$(hvj item field get B71 --name reason | jget data.value)" = "done" ] || fail "item field reason for plain done"
grep -qF "{\"id\":\"B72\",\"type\":\"B\",\"date\":\"$(date +%Y-%m-%d)\",\"reason\":\"blocked\"}" <<<"$(hvj summary | jget data.recent)" || fail "summary missing reason"
rc=0; "$ROTA_BIN" item complete B01 --commit "$HASH" --reason bogus 2>/dev/null || rc=$?
[ "$rc" = "2" ] || fail "invalid --reason should exit 2, got $rc"
pass "item complete --reason/--note renders, reads back via item field get and summary"

echo "item field set"
# F01 is still an open feature bullet at this point (B01 was completed above).
"$ROTA_BIN" item field set F01 --name milestone --value M01 >/dev/null
grep -q "\[F01\].*Milestone: M01" .rota/BACKLOG.md || fail "F01 milestone not appended"
pass "item field set appends absent Milestone"

"$ROTA_BIN" item field set F01 --name milestone --value M07 >/dev/null
grep -q "\[F01\].*Milestone: M07" .rota/BACKLOG.md || fail "F01 milestone not replaced in place"
grep -q "Milestone: M01" .rota/BACKLOG.md && fail "old Milestone M01 value lingered"
pass "item field set replaces present Milestone in place"

"$ROTA_BIN" item field set F01 --name repos --value "web, api" >/dev/null
grep -q "\[F01\].*Repos: web, api" .rota/BACKLOG.md || fail "F01 repos not set"
"$ROTA_BIN" item field set F01 --name repos --value "" >/dev/null
grep -q "\[F01\].*Repos:" .rota/BACKLOG.md && fail "F01 repos segment not cleared"
grep -q "\[F01\].*Milestone: M07" .rota/BACKLOG.md || fail "clearing repos disturbed Milestone"
pass "item field set clears a field on empty value, leaving neighbors intact"

BEFORE_MD5=$(md5sum .rota/BACKLOG.md)
CHANGED=$(hvj item field set F01 --name milestone --value M07 | jget data.changed) || fail "idempotent item field set errored"
[ "$CHANGED" = "false" ] || fail "idempotent item field set reported changed=$CHANGED"
[ "$BEFORE_MD5" = "$(md5sum .rota/BACKLOG.md)" ] || fail "idempotent item field set rewrote the file"
pass "item field set is idempotent on unchanged value"

rc=0; "$ROTA_BIN" item field set F01 --name title --value "X" 2>/dev/null || rc=$?
[ "$rc" = "2" ] || fail "item field set should reject a non-settable field with 2, got $rc"
pass "item field set rejects non-settable field"

rc=0; "$ROTA_BIN" item field set B01 --name milestone --value M01 2>/dev/null || rc=$?
[ "$rc" = "4" ] || fail "item field set should refuse a completed/archived ID (no open bullet) with 4, got $rc"
pass "item field set refuses ID with no open bullet"

echo "git guard clean"
git add -A && git commit -q -m "progress"
"$ROTA_BIN" git guard clean --context test >/dev/null 2>&1 || fail "guard rejected clean tree"
pass "clean tree passes"
echo "dirty" > dirtyfile
rc=0; OUT=$(hvj git guard clean --context test 2>/dev/null) || rc=$?
[ "$rc" = "1" ] || fail "guard should have rejected dirty tree with 1, got $rc"
[ "$(echo "$OUT" | jget data.clean)" = "false" ] || fail "dirty guard data.clean should be false: $OUT"
pass "dirty tree rejected"
rm dirtyfile

echo "git guard clean greenfield"
GF_TMP=$(mktemp -d)
(
  cd "$GF_TMP"
  git init -q
  mkdir .rota
  echo "spec" > briefing.md
  rc=0; out=$(hvj git guard clean --context test 2>/dev/null) || rc=$?
  [ "$rc" = "1" ] || { echo "[FAIL] greenfield guard should exit 1, got $rc"; exit 1; }
  [ "$(echo "$out" | jget data.greenfield)" = "true" ] \
    || { echo "[FAIL] greenfield data.greenfield should be true: $out"; exit 1; }
) || fail "greenfield guard did not report greenfield"
rm -rf "$GF_TMP"
pass "greenfield tree is reported as greenfield"

echo "status add / status rm"
"$ROTA_BIN" status add rota/test-branch --items B02,F01 >/dev/null
grep -q '"branch": "rota/test-branch"' .rota/status.json || fail "status add did not write entry"
grep -q '"items"' .rota/status.json || fail "items field missing"
pass "status add wrote entry"

"$ROTA_BIN" status rm rota/test-branch >/dev/null
grep -q '"branch": "rota/test-branch"' .rota/status.json && fail "status rm did not remove"
pass "status rm cleared entry"

echo "status rm no-op when branch absent"
# Cross-platform mtime read — `stat -c %Y` is GNU-only (Linux); macOS BSD stat
# uses `-f %m`. Python is hermetic across both and already a smoke-suite hard
# dep.
MTIME_BEFORE=$(python3 -c "import os; print(int(os.path.getmtime('.rota/status.json')))")
"$ROTA_BIN" status rm rota/no-such-branch >/dev/null
MTIME_AFTER=$(python3 -c "import os; print(int(os.path.getmtime('.rota/status.json')))")
[ "$MTIME_BEFORE" = "$MTIME_AFTER" ] || fail "status rm rewrote status.json for absent branch"
pass "status rm skipped rewrite for absent branch"

echo "status add upsert (no flag) overwrites startedAt"
"$ROTA_BIN" status add rota/ts-branch --items X01 >/dev/null
TS1=$(python3 -c "import json; d=json.load(open('.rota/status.json')); print(next(e['startedAt'] for e in d['active'] if e['branch']=='rota/ts-branch'))")
sleep 1
"$ROTA_BIN" status add rota/ts-branch --items X01 >/dev/null
TS2=$(python3 -c "import json; d=json.load(open('.rota/status.json')); print(next(e['startedAt'] for e in d['active'] if e['branch']=='rota/ts-branch'))")
[ "$TS1" != "$TS2" ] || fail "second status add should have updated startedAt but it did not"
pass "status add (no flag) overwrites startedAt on second call"
"$ROTA_BIN" status rm rota/ts-branch >/dev/null

echo "status add --if-absent is no-op when entry exists"
"$ROTA_BIN" status add rota/ia-branch --items Y01 >/dev/null
TS1=$(python3 -c "import json; d=json.load(open('.rota/status.json')); print(next(e['startedAt'] for e in d['active'] if e['branch']=='rota/ia-branch'))")
sleep 1
rc=0; OUT=$(hvj status add --if-absent rota/ia-branch --items Y01) || rc=$?
[ "$rc" = "0" ] && [ "$(echo "$OUT" | jget data.changed)" = "false" ] || fail "--if-absent on an existing entry should report changed=false: $OUT"
TS2=$(python3 -c "import json; d=json.load(open('.rota/status.json')); print(next(e['startedAt'] for e in d['active'] if e['branch']=='rota/ia-branch'))")
[ "$TS1" = "$TS2" ] || fail "--if-absent overwrote startedAt but should have been a no-op"
pass "status add --if-absent preserved startedAt when entry already exists"
"$ROTA_BIN" status rm rota/ia-branch >/dev/null

echo "status rm sweeps handoff note (B13)"
mkdir -p .rota/handoff/rota
# Single-repo: handoff at .rota/handoff/<branch>.md is swept on remove
"$ROTA_BIN" status add rota/sw-single --items B01 >/dev/null
echo "stale" > .rota/handoff/rota/sw-single.md
OUT=$(hvj status rm rota/sw-single)
[ ! -f .rota/handoff/rota/sw-single.md ] || fail "status rm did not sweep single-repo handoff"
[ "$(echo "$OUT" | jget data.handoffRemoved)" = "true" ] || fail "status rm should report handoffRemoved=true: $OUT"
pass "status rm sweeps single-repo handoff at .rota/handoff/<branch>.md"

# Idempotent: status rm on a branch with no handoff is a silent no-op
"$ROTA_BIN" status add rota/sw-no-handoff --items B02 >/dev/null
"$ROTA_BIN" status rm rota/sw-no-handoff >/dev/null
pass "status rm is silent when no handoff exists"

# Umbrella keying: --repo sweeps .rota/handoff/<branch>@<repo>.md (and only that)
mkdir -p .rota/handoff/rota
echo "umbrella-web" > .rota/handoff/rota/sw-umb@web.md
echo "single-fallback" > .rota/handoff/rota/sw-umb.md
# A bare-call without --repo only matches legacy (repo:null) entries — and for
# handoff sweep it must mirror that scope, touching only <branch>.md.
"$ROTA_BIN" status add rota/sw-umb --items B03 >/dev/null
"$ROTA_BIN" status rm rota/sw-umb >/dev/null
[ ! -f .rota/handoff/rota/sw-umb.md ] || fail "status rm (no --repo) did not sweep legacy handoff"
[ -f .rota/handoff/rota/sw-umb@web.md ] || fail "status rm (no --repo) wrongly swept umbrella-keyed handoff"
pass "status rm (no --repo) sweeps only the legacy <branch>.md handoff"

# --repo sweeps the umbrella-keyed handoff
echo '{"repos":[{"name":"web","path":"web"}]}' > .rota/repos.json
"$ROTA_BIN" --repo web status add rota/sw-umb --items B03 >/dev/null
"$ROTA_BIN" --repo web status rm rota/sw-umb >/dev/null
rm -f .rota/repos.json
[ ! -f .rota/handoff/rota/sw-umb@web.md ] || fail "status rm --repo did not sweep umbrella-keyed handoff"
pass "status rm --repo sweeps the .rota/handoff/<branch>@<repo>.md handoff"
rm -rf .rota/handoff

echo "backlog archive"
# Inject two completed items: one old, one recent
python3 - <<'PY'
from pathlib import Path
from datetime import date, timedelta
p = Path(".rota/BACKLOG.md")
c = p.read_text()
old = (date.today() - timedelta(days=10)).strftime("%Y-%m-%d")
recent = (date.today() - timedelta(days=1)).strftime("%Y-%m-%d")
c = c.rstrip() + f"\n- ~~**[B99] Old bug.**~~ Done {old} [`aaa`]\n- ~~**[F99] Recent feature.**~~ Done {recent} [`bbb`]\n"
p.write_text(c)
PY
OUT=$(hvj backlog archive --days 5)
[ "$(echo "$OUT" | jget data.moved)" = "1" ] || fail "expected 1 archived: $OUT"
[ "$(echo "$OUT" | jget data.changed)" = "true" ] || fail "archive should report changed=true: $OUT"
grep -q "B99" .rota/ARCHIVE.md || fail "B99 not in ARCHIVE.md"
grep -q "B99" .rota/BACKLOG.md && fail "B99 still in BACKLOG.md"
grep -q "F99" .rota/BACKLOG.md || fail "F99 should still be in BACKLOG.md"
pass "old item archived, recent item kept"


echo "status show"
"$ROTA_BIN" status add rota/sh-a --items B01,B02 --worktree /tmp/sh-a-wt >/dev/null
rc=0; OUT=$(hvj status show rota/sh-a) || rc=$?
[ "$rc" = "0" ] || fail "status show of an active branch: expected exit 0, got $rc: $OUT"
[ "$(jget data.branch <<<"$OUT")" = "rota/sh-a" ] || fail "status show: wrong branch: $OUT"
[ "$(jget data.active <<<"$OUT")" = "true" ] || fail "status show: expected active true: $OUT"
[ "$(jget data.repo <<<"$OUT")" = "null" ] || fail "status show: single-repo entry should have repo null: $OUT"
[ "$(jget data.items <<<"$OUT")" = '["B01","B02"]' ] || fail "status show: wrong items: $OUT"
[ "$(jget data.worktree <<<"$OUT")" = "/tmp/sh-a-wt" ] || fail "status show: wrong worktree: $OUT"
TS_SHOW=$(jget data.startedAt <<<"$OUT")
TS_FILE=$(python3 -c "import json; d=json.load(open('.rota/status.json')); print(next(e['startedAt'] for e in d['active'] if e['branch']=='rota/sh-a'))")
[ "$TS_SHOW" = "$TS_FILE" ] || fail "status show startedAt '$TS_SHOW' != status.json '$TS_FILE'"
pass "status show reports branch, repo, items, worktree and startedAt of an active branch"

"$ROTA_BIN" status rm rota/sh-a >/dev/null
rc=0; OUT=$(hvj status show rota/sh-a) || rc=$?
[ "$rc" = "0" ] || fail "status show of an unknown branch: expected exit 0, got $rc: $OUT"
[ "$(jget data.active <<<"$OUT")" = "false" ] || fail "status show after rm: expected active false: $OUT"
[ "$(jget data.items <<<"$OUT")" = "[]" ] || fail "status show after rm: expected empty items: $OUT"
[ "$(jget data.repo <<<"$OUT")" = "null" ] && [ "$(jget data.worktree <<<"$OUT")" = "null" ] || fail "status show after rm: repo/worktree should be null: $OUT"
pass "status show of a branch with no entry is exit 0 with active false"

rc=0; OUT=$(hvj status show 2>/dev/null) || rc=$?
[ "$rc" = "2" ] && [ "$(jget error.code <<<"$OUT")" = "usage" ] || fail "status show without a branch: expected usage exit 2, got $rc: $OUT"
rc=0; OUT=$(hvj status show rota/a rota/b 2>/dev/null) || rc=$?
[ "$rc" = "2" ] && [ "$(jget error.code <<<"$OUT")" = "usage" ] || fail "status show with two branches: expected usage exit 2, got $rc: $OUT"
rc=0; OUT=$(hvj status show rota/a --repo web 2>/dev/null) || rc=$?
[ "$rc" = "3" ] && [ "$(jget error.code <<<"$OUT")" = "resolution" ] || fail "status show --repo outside umbrella mode: expected resolution exit 3, got $rc: $OUT"
pass "status show exits 2 on bad arguments and 3 on --repo outside umbrella mode"
