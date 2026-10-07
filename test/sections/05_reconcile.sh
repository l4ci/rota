# Helper for the drift fixtures: space-joined IDs in data.drift
drift_ids() { python3 -c 'import json,sys; print(" ".join(d["id"] for d in json.load(sys.stdin)["data"]["drift"]))'; }

echo "backlog drift"
TD_TMP="$(mktemp -d)"
(
  cd "$TD_TMP"
  mkdir -p .rota
  cat > .rota/BACKLOG.md <<'EOF'
# TODO

## Bugs
- **[B07] [P1] Pretend bug.** Desc.

## Features

## Tasks

## Completed
EOF
  echo '{"repos": []}' > .rota/repos.json
  git init -q
  git config user.email t@t && git config user.name t
  git checkout -q -b main 2>/dev/null || git branch -m main
  git commit -q --allow-empty -m "fix: do thing [B07]"
  OUT=$(hvj backlog drift)
  [ "$(echo "$OUT" | drift_ids)" = "B07" ] || fail "drift missing B07: $OUT"
  [ "$(echo "$OUT" | jget 'data.drift[0].type')" = "B" ] || fail "drift entry lost its type: $OUT"
  pass "backlog drift detects shipped-but-open ID"

  # Completed (strikethrough) IDs are NOT drift — even if they appear in commits.
  cat > .rota/BACKLOG.md <<'EOF'
# TODO

## Bugs

## Features

## Tasks

## Completed
- ~~**[B07] [P1] Pretend bug.**~~ Done 2026-05-07 [`abc1234`]
EOF
  OUT2=$(hvj backlog drift)
  [ -z "$(echo "$OUT2" | drift_ids)" ] || fail "drift should not flag completed B07: $OUT2"
  pass "backlog drift ignores completed IDs"
)
rm -rf "$TD_TMP"

echo "backlog drift Since-anchor"
SA_TMP="$(mktemp -d)"
(
  cd "$SA_TMP"
  mkdir -p .rota
  echo '{"repos": []}' > .rota/repos.json
  git init -q
  git config user.email t@t && git config user.name t
  git checkout -q -b main 2>/dev/null || git branch -m main
  git commit -q --allow-empty -m "init"

  # Scenario A: ID-reuse simulation. Old commit ships [B07], then a new
  # [B07] is captured AFTER. The new bullet's Since anchor must hide the
  # old commit from drift detection.
  git commit -q --allow-empty -m "feat: old shipment [B07]"
  git commit -q --allow-empty -m "chore: anchor pin"
  ANCHOR="$(git rev-parse --short HEAD)"
  cat > .rota/BACKLOG.md <<EOF
# TODO

## Bugs
- **[B07] [P1] Newcomer at this ID.** Body. Since: $ANCHOR

## Features

## Tasks

## Completed
EOF
  OUT=$(hvj backlog drift)
  [ -z "$(echo "$OUT" | drift_ids)" ] || fail "drift should NOT flag pre-anchor commit for new [B07]: $OUT"
  pass "backlog drift skips commits older than Since: anchor"

  # Scenario B: a NEW commit referencing the same ID, AFTER the anchor,
  # MUST still trigger drift (the anchor is a floor, not a mute).
  git commit -q --allow-empty -m "feat: real shipment [B07]"
  OUT_B=$(hvj backlog drift)
  [ "$(echo "$OUT_B" | drift_ids)" = "B07" ] || fail "drift missed post-anchor commit: $OUT_B"
  pass "backlog drift detects commits newer than Since: anchor"

  # Scenario C: legacy entry (no Since:) preserves full-log behavior.
  cat > .rota/BACKLOG.md <<'EOF'
# TODO

## Bugs
- **[B07] [P1] Legacy entry.** No Since field.

## Features

## Tasks

## Completed
EOF
  OUT_C=$(hvj backlog drift)
  [ "$(echo "$OUT_C" | drift_ids)" = "B07" ] || fail "legacy (no Since) should still drift: $OUT_C"
  pass "backlog drift legacy entries keep full-log behavior"

  # Scenario D: item create auto-stamps Since on fresh captures.
  cat > .rota/BACKLOG.md <<'EOF'
# TODO

## Bugs

## Features

## Tasks

## Completed
EOF
  HEAD_AT_CAP="$(git rev-parse --short HEAD)"
  echo '{"bugs":7,"features":0,"tasks":0,"milestones":0}' > .rota/counters.json
  OUT_D=$(hvj item create --kind bugs --title "Auto-stamp." --desc "Body." --tag P2)
  [ "$(echo "$OUT_D" | jget data.id)" = "B08" ] || fail "item create minted the wrong ID: $OUT_D"
  grep -q "Since: $HEAD_AT_CAP" .rota/BACKLOG.md || { cat .rota/BACKLOG.md; fail "item create did not auto-stamp Since"; }
  pass "item create auto-stamps Since: <HEAD> on fresh bullets"

  # Scenario E: backlog backfill stamps open bullets lacking Since;
  # second invocation is idempotent (no output, no double-stamp).
  cat > .rota/BACKLOG.md <<'EOF'
# TODO

## Bugs
- **[B09] [P1] Needs backfill.** Body.

## Features

## Tasks

## Completed
- ~~**[B07] [P1] Completed item.**~~ Done 2026-05-07 [`abc1234`]
EOF
  OUT_E=$(hvj backlog backfill)
  [ "$(echo "$OUT_E" | jget data.stamped)" = "1" ] || fail "backfill should report 1 stamped, got: $OUT_E"
  [ "$(echo "$OUT_E" | jget data.changed)" = "true" ] || fail "backfill should report changed: $OUT_E"
  HEAD_BF="$(git rev-parse --short HEAD)"
  grep -q "Since: $HEAD_BF" .rota/BACKLOG.md || { cat .rota/BACKLOG.md; fail "backfill did not write Since"; }
  # Completed items must NOT be touched
  grep -q "Since:.*Completed item" .rota/BACKLOG.md && fail "backfill touched ## Completed entry"
  pass "backlog backfill stamps open bullets lacking Since:"
  OUT_E2=$(hvj backlog backfill)
  [ "$(echo "$OUT_E2" | jget data.stamped)" = "0" ] || fail "backfill not idempotent — re-ran with: $OUT_E2"
  [ "$(echo "$OUT_E2" | jget data.changed)" = "false" ] || fail "idempotent backfill should report changed false: $OUT_E2"
  pass "backlog backfill is idempotent"
)
rm -rf "$SA_TMP"

echo "backlog drift symbol-drift"
SY_TMP="$(mktemp -d)"
(
  cd "$SY_TMP"
  mkdir -p .rota
  echo '{"repos": []}' > .rota/repos.json
  git init -q
  git config user.email t@t && git config user.name t
  git checkout -q -b main 2>/dev/null || git branch -m main

  # Initial commit: contains a pre-existing symbol (negative case) but NOT
  # the to-be-shipped symbol. This is the capture point.
  echo "def already_here_helper(): pass" > existing.py
  git add -A && git commit -q -m "init"
  CAP="$(git rev-parse --short HEAD)"

  # Capture two open items anchored at CAP:
  #  - B20 names widget_transcribe_pipeline — NOT in tree at CAP (positive).
  #  - B21 names already_here_helper — already in tree at CAP (negative).
  cat > .rota/BACKLOG.md <<EOF
# TODO

## Bugs
- **[B20] [P1] Wire up widget_transcribe_pipeline.** Add the new symbol. Since: $CAP
- **[B21] [P1] Touch already_here_helper.** Pre-existing symbol. Since: $CAP

## Features

## Tasks

## Completed
EOF

  # LATER commit ships widget_transcribe_pipeline WITHOUT mentioning [B20]
  # in the subject — the silent-ship scenario.
  echo "def widget_transcribe_pipeline(): pass" > pipeline.py
  git add -A && git commit -q -m "refactor: rework pipeline internals"

  OUT=$(hvj backlog drift)

  # Commit-subject drift must be empty — no [B20]/[B21] in any subject.
  DRIFT_IDS=$(echo "$OUT" | drift_ids)
  [ -z "$DRIFT_IDS" ] || fail "commit-drift should be empty, got: $DRIFT_IDS"

  # Symbol drift must flag B20 with the symbol + file, but NOT B21.
  echo "$OUT" | python3 -c "
import json, sys
sd = json.load(sys.stdin)['data']['symbolDrift']
by = {e['id']: e for e in sd}
assert 'B20' in by, f'B20 missing from symbolDrift: {sd}'
assert by['B20']['type'] == 'B', by['B20']
assert 'widget_transcribe_pipeline' in by['B20']['symbols'], by['B20']
assert 'pipeline.py' in by['B20']['files'], by['B20']
assert 'B21' not in by, f'B21 (pre-existing symbol) should NOT be flagged: {sd}'
" || fail "symbolDrift assertions failed: $OUT"
  pass "backlog drift flags symbol shipped after capture, skips pre-existing symbol"
)
rm -rf "$SY_TMP"

echo "knowledge query"
cat > .rota/KNOWLEDGE.md <<'EOF'
# Knowledge

## Architecture
- arch bullet one
- arch bullet two

## Testing
- testing bullet

## Networking
- net bullet
EOF
OUT=$(hvj knowledge query "Testing" "Networking" | jget data.text)
grep -q "testing bullet" <<<"$OUT" || fail "testing topic missing from query"
grep -q "net bullet" <<<"$OUT" || fail "networking topic missing from query"
grep -q "arch bullet" <<<"$OUT" && fail "architecture topic leaked into query"
pass "knowledge query returns only requested topics"

echo "knowledge stats"
KS_TMP="$(mktemp -d)"
trap 'rm -rf "$KS_TMP"' EXIT
(
  cd "$KS_TMP"
  mkdir -p .rota
  cat > .rota/KNOWLEDGE.md <<'EOF'
# Knowledge

## Tiny

- one bullet

## Big

EOF
  # Append 30 bullets to ## Big so it crosses the threshold.
  for i in $(seq 1 30); do echo "- bullet $i" >> .rota/KNOWLEDGE.md; done
  OUT=$(hvj knowledge stats)
  [ "$(echo "$OUT" | python3 -c "import json,sys; print(' '.join(t['name'] for t in json.load(sys.stdin)['data']['topics']))")" = "Tiny Big" ] \
    || fail "stats should list Tiny and Big: $OUT"
  BIG_BULLETS=$(echo "$OUT" | python3 -c "import json,sys; d=json.load(sys.stdin)['data']; print(next(t['bullets'] for t in d['topics'] if t['name']=='Big'))")
  [ "$BIG_BULLETS" = "30" ] || fail "Big bullet count != 30: $BIG_BULLETS"
  pass "knowledge stats counts bullets per topic"
  TINY_BULLETS=$(echo "$OUT" | python3 -c "import json,sys; d=json.load(sys.stdin)['data']; print(next(t['bullets'] for t in d['topics'] if t['name']=='Tiny'))")
  [ "$TINY_BULLETS" = "1" ] || fail "Tiny bullet count != 1: $TINY_BULLETS"
  pass "knowledge stats handles tiny topics"
)
trap 'rm -rf "$TMP"' EXIT

echo "knowledge stats no KNOWLEDGE.md"
KS2_TMP="$(mktemp -d)"
trap 'rm -rf "$KS2_TMP"' EXIT
(
  cd "$KS2_TMP"
  mkdir -p .rota
  OUT=$(hvj knowledge stats)
  [ "$(echo "$OUT" | jget data.topics)" = "[]" ] || fail "missing-file should yield empty: $OUT"
  pass "knowledge stats silent-empty on missing KNOWLEDGE.md"
)
trap 'rm -rf "$TMP"' EXIT

echo "decisions query"
cat > .rota/DECISIONS.md <<'EOF'
# Decisions

## Architecture

### No background queues
Jobs run in-process.
*Why.* Simplicity.
**Forbids.** External queues.
**Permits.** Goroutines.

## Testing

### No mocked DB
Integration tests hit real DB.
*Why.* Mock divergence.
**Forbids.** Mock DB.
**Permits.** Other mocks.

## Networking
### Strict TLS
Only TLS 1.3+.
*Why.* Compliance.
**Forbids.** TLS 1.2 fallback.
**Permits.** Cert pinning.
EOF
OUT_D=$(hvj decisions query "Testing" "Networking" | jget data.text)
grep -q "No mocked DB" <<<"$OUT_D" || fail "Testing decision missing from query"
grep -q "Strict TLS" <<<"$OUT_D" || fail "Networking decision missing from query"
grep -q "No background queues" <<<"$OUT_D" && fail "Architecture decision leaked into query"
pass "decisions query returns only requested topics"

# decisions stats lists the topics with bullet and byte counts, plain and --json
[ "$(hvj decisions stats | python3 -c "import json,sys; print(' '.join(t['name'] for t in json.load(sys.stdin)['data']['topics']))")" = "Architecture Testing Networking" ] \
  || fail "decisions stats should list the three topics"
TXT_DS=$("$ROTA_BIN" decisions stats)
case "$TXT_DS" in "Architecture: "*" bullets, "*" bytes"*) ;; *) fail "decisions stats text shape: $TXT_DS" ;; esac
[ "$(echo "$TXT_DS" | wc -l)" -eq 3 ] || fail "decisions stats text should be one line per topic: $TXT_DS"
rc=0; "$ROTA_BIN" decisions stats --repo web >/dev/null 2>&1 || rc=$?
[ "$rc" -eq 2 ] || fail "decisions stats accepted --repo (rc=$rc)"
pass "decisions stats lists topics with counts, text and --json"

# Forbids/permits content must come through verbatim
grep -q "Forbids.*Mock DB" <<<"$OUT_D" || fail "Forbids line missing for Testing decision"
grep -q "Permits.*Cert pinning" <<<"$OUT_D" || fail "Permits line missing for Networking decision"
pass "decisions query preserves forbids/permits structure"

# Empty/missing file is silent (exit 0, no output)
rm -f .rota/DECISIONS.md
[ "$(hvj decisions stats | jget data.topics)" = "[]" ] || fail "decisions stats should be empty when DECISIONS.md missing"
OUT_EMPTY=$(hvj decisions query "Anything")
[ "$(echo "$OUT_EMPTY" | jget data.text)" = "" ] || fail "decisions query should be silent when DECISIONS.md missing: $OUT_EMPTY"
pass "decisions query silent when file missing"

# Restore .rota/DECISIONS.md so subsequent tests have a known state
cat > .rota/DECISIONS.md <<'EOF'
# Decisions
EOF

echo "init (DECISIONS.md seed)"
# Fresh tmpdir so we test init on a truly clean slate
BOOT_TMP="$(mktemp -d)"
trap 'rm -rf "$BOOT_TMP"' EXIT
cd "$BOOT_TMP"
git init -q
git config user.email t@t && git config user.name t
"$ROTA_BIN" init >/dev/null
[ -f .rota/DECISIONS.md ] || fail "init did not create .rota/DECISIONS.md"
grep -q "^# Decisions" .rota/DECISIONS.md || fail "DECISIONS.md missing # Decisions header"
grep -q "Hard boundaries" .rota/DECISIONS.md || fail "DECISIONS.md missing framing sentence"
pass "init creates .rota/DECISIONS.md with header preamble"

# Re-running init must NOT overwrite existing DECISIONS.md
echo "user content marker" >> .rota/DECISIONS.md
"$ROTA_BIN" init >/dev/null
grep -q "user content marker" .rota/DECISIONS.md || fail "init overwrote existing DECISIONS.md"
pass "init idempotent — preserves existing DECISIONS.md content"

cd "$TMP"
trap 'rm -rf "$TMP"' EXIT

