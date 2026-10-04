# F03 — Knowledge promotion lifecycle (tier sidecar + auto-promotion + contradictions)
echo "F03: knowledge-tier sidecar"

# Clean any stale state from prior sections
rm -f .rota/knowledge-tier.json .rota/knowledge-contradictions.json

# Seed KNOWLEDGE.md with three titled bullets and verify migration stamps them
cat > .rota/KNOWLEDGE.md <<'EOF'
# Knowledge

## Architecture

- **Foo rule** — body text for foo. <!-- 2026-05-15 -->
- **Bar rule** — body text for bar. <!-- 2026-05-15 -->

## Build and Tooling

- **Baz rule** — body text for baz. <!-- 2026-05-15 -->
EOF

# --- knowledge tier subcommands ---
OUT=$(hvj knowledge tier get --topic "Architecture" --title "Nonexistent")
[ "$(jget data.found <<<"$OUT")" = "false" ] || fail "knowledge tier get on missing should be found:false: $OUT"
pass "knowledge tier get on missing entry reports found:false"

# A tier read registers every titled bullet as provisional/0 (lazy init, #174),
# so the get above has already tracked "Foo rule".
OUT=$(hvj knowledge tier get --topic "Architecture" --title "Foo rule")
[ "$(jget data.tier <<<"$OUT")/$(jget data.hits <<<"$OUT")" = "provisional/0" ] || fail "tier get should backfill a bullet as provisional/0: $OUT"
pass "knowledge tier get backfills untracked bullets as provisional/0"

# Setting an entry with no bullet and no sidecar row creates it with hits 0.
OUT=$(hvj knowledge tier set --topic "Architecture" --title "Ghost rule" --tier provisional)
[ "$(jget data.changed <<<"$OUT")" = "true" ] || fail "tier set on untracked entry should change: $OUT"
if jget data.previousTier <<<"$OUT" >/dev/null 2>&1; then fail "untracked entry should have no previousTier: $OUT"; fi
OUT=$(hvj knowledge tier get --topic "Architecture" --title "Ghost rule")
[ "$(jget data.tier <<<"$OUT")/$(jget data.hits <<<"$OUT")" = "provisional/0" ] || fail "tier set on untracked entry wrong shape: $OUT"
pass "knowledge tier set creates an untracked entry as provisional/0"

hvj knowledge hit --topic "Architecture" --title "Foo rule" >/dev/null 2>&1
OUT=$(hvj knowledge hit --topic "Architecture" --title "Foo rule" 2>/dev/null)
[ "$(jget data.hits <<<"$OUT")" = "2" ] || fail "knowledge hit twice; expected hits=2: $OUT"
HITS=$(hvj knowledge tier get --topic "Architecture" --title "Foo rule" | jget data.hits)
[ "$HITS" = "2" ] || fail "knowledge hit twice; expected stored hits=2, got $HITS"
pass "knowledge hit increments hits"

OUT=$(hvj knowledge tier set --topic "Architecture" --title "Foo rule" --tier confirmed)
[ "$(jget data.previousTier <<<"$OUT")" = "provisional" ] || fail "tier set should report previousTier: $OUT"
TIER=$(hvj knowledge tier get --topic "Architecture" --title "Foo rule" | jget data.tier)
[ "$TIER" = "confirmed" ] || fail "knowledge tier set didn't update tier; got $TIER"
pass "knowledge tier set updates tier"

RC=0; hvj knowledge tier set --topic "Architecture" --title "Foo rule" --tier garbage >/dev/null 2>&1 || RC=$?
[ "$RC" = "2" ] || fail "knowledge tier set should reject invalid tier with exit 2; got $RC"
pass "knowledge tier set rejects invalid tier"

# Start the hit checks from an empty sidecar: a first hit registers the bullet.
rm -f .rota/knowledge-tier.json

# --- knowledge hit + auto-promote ---
echo '{"learn":{"verify":true,"promoteThreshold":3}}' > .rota/config.json

hvj knowledge hit --topic "Architecture" --title "Bar rule" >/dev/null 2>&1
OUT=$(hvj knowledge hit --topic "Architecture" --title "Bar rule" 2>/dev/null)
[ "$(jget data.promoted <<<"$OUT")" = "false" ] || fail "second hit should not promote: $OUT"
TIER=$(hvj knowledge tier get --topic "Architecture" --title "Bar rule" | jget data.tier)
[ "$TIER" = "provisional" ] || fail "Bar rule should stay provisional at 2 hits; got $TIER"
pass "knowledge hit doesn't promote below threshold"

OUT=$(hvj knowledge hit --topic "Architecture" --title "Bar rule" 2>/dev/null)
[ "$(jget data.promoted <<<"$OUT")" = "true" ] || fail "third hit should report promoted: $OUT"
[ "$(jget data.tier <<<"$OUT")" = "confirmed" ] || fail "third hit should report tier confirmed: $OUT"
TIER=$(hvj knowledge tier get --topic "Architecture" --title "Bar rule" | jget data.tier)
[ "$TIER" = "confirmed" ] || fail "third hit should auto-promote to confirmed; got $TIER"
pass "knowledge hit auto-promotes at threshold"

# --- knowledge contradiction ---
OUT=$(hvj knowledge contradiction add --topic "Build and Tooling" --title "Baz rule" --text "user said baz is wrong")
[ "$(jget data.pending <<<"$OUT")" = "1" ] || fail "contradiction add should leave 1 pending: $OUT"
LIST=$(hvj knowledge contradiction list | jget data.items | python3 -c 'import json,sys;print(len(json.load(sys.stdin)))')
[ "$LIST" = "1" ] || fail "expected 1 contradiction, got $LIST"
pass "knowledge contradiction add records a candidate"

OUT=$(hvj knowledge contradiction has --topic "Build and Tooling" --title "Baz rule") \
  || fail "contradiction has should exit 0 for existing entry"
[ "$(jget data.has <<<"$OUT")" = "true" ] || fail "contradiction has should report has:true: $OUT"
pass "knowledge contradiction has exits 0 for known entry"

RC=0; OUT=$(hvj knowledge contradiction has --topic "Nope" --title "Nope" 2>/dev/null) || RC=$?
[ "$RC" = "1" ] || fail "contradiction has should exit 1 for missing entry; got $RC"
[ "$(jget data.has <<<"$OUT")" = "false" ] || fail "contradiction has miss should report has:false: $OUT"
pass "knowledge contradiction has exits 1 for missing entry"

# --- auto-promote skips when contradiction pending ---
# Hit Baz rule three times; should NOT auto-promote because contradiction is pending.
for i in 1 2 3; do
  OUT=$(hvj knowledge hit --topic "Build and Tooling" --title "Baz rule" 2>/dev/null)
done
[ "$(jget data.promotionBlocked <<<"$OUT")" = "true" ] || fail "third hit on Baz rule should report promotionBlocked: $OUT"
TIER=$(hvj knowledge tier get --topic "Build and Tooling" --title "Baz rule" | jget data.tier)
[ "$TIER" = "provisional" ] || fail "Baz rule should stay provisional under contradiction; got $TIER"
pass "knowledge hit skips auto-promote when contradiction pending"

# --- knowledge contradiction clear ---
OUT=$(hvj knowledge contradiction clear)
[ "$(jget data.cleared <<<"$OUT")" = "1" ] || fail "contradiction clear should report 1 cleared: $OUT"
LIST=$(hvj knowledge contradiction list | jget data.items | python3 -c 'import json,sys;print(len(json.load(sys.stdin)))')
[ "$LIST" = "0" ] || fail "queue should be empty after clear; got $LIST"
pass "knowledge contradiction clear wipes the queue"

# --- knowledge query tier-aware output ---
# Build a fresh state: Foo confirmed, Bar deprecated, Baz provisional.
hvj knowledge tier set --topic "Architecture" --title "Foo rule" --tier confirmed >/dev/null
hvj knowledge tier set --topic "Architecture" --title "Bar rule" --tier deprecated >/dev/null
hvj knowledge tier set --topic "Build and Tooling" --title "Baz rule" --tier provisional >/dev/null

OUT=$(hvj knowledge query Architecture | jget data.text)
grep -q "Foo rule" <<<"$OUT" || fail "confirmed Foo rule should appear in default query"
if grep -q "Bar rule" <<<"$OUT"; then
  fail "deprecated Bar rule should be hidden in default query: $OUT"
fi
pass "knowledge query hides deprecated bullets by default"

OUT=$(hvj knowledge query --include-deprecated Architecture | jget data.text)
grep -q "Bar rule" <<<"$OUT" || fail "--include-deprecated should surface Bar rule"
pass "knowledge query --include-deprecated re-surfaces hidden bullets"

OUT=$(hvj knowledge query "Build and Tooling" | jget data.text)
grep -q "(provisional)" <<<"$OUT" || fail "provisional Baz rule should carry suffix: $OUT"
pass "knowledge query suffixes (provisional) on probation bullets"

OUT=$(hvj knowledge query --tier confirmed Architecture | jget data.text)
grep -q "Foo rule" <<<"$OUT" || fail "--tier confirmed should keep Foo rule"
if grep -q "Bar rule" <<<"$OUT"; then
  fail "--tier confirmed should drop deprecated Bar rule: $OUT"
fi
pass "knowledge query --tier filter works"

OUT=$(hvj knowledge tier list --tier deprecated | jget data.entries)
grep -q "Bar rule" <<<"$OUT" || fail "tier list --tier deprecated should include Bar rule: $OUT"
if grep -q "Foo rule" <<<"$OUT"; then fail "tier list --tier deprecated should drop Foo rule: $OUT"; fi
pass "knowledge tier list --tier filters entries"

# --- knowledge add initializes sidecar on new bullet ---
hvj knowledge add --topic "Architecture" --title "Qux rule" --body-file - <<<"Body of qux rule" >/dev/null
TIER=$(hvj knowledge tier get --topic "Architecture" --title "Qux rule" | jget data.tier)
[ "$TIER" = "provisional" ] || fail "new bullet from add should be provisional in sidecar; got $TIER"
pass "knowledge add initializes new bullet as provisional"

# Cleanup
rm -f .rota/knowledge-tier.json .rota/knowledge-contradictions.json
