echo "block knowledge"
mkdir -p .rota
cat > .rota/KNOWLEDGE.md <<'EOF'
# Knowledge

## Architecture
- something

## Testing
- another thing
EOF
"$ROTA_BIN" block knowledge >/dev/null
grep -q "<!-- rota-knowledge-start -->" CLAUDE.md || fail "managed block not in CLAUDE.md"
grep -q "^- Architecture" CLAUDE.md || fail "Architecture topic missing"
grep -q "^- Testing" CLAUDE.md || fail "Testing topic missing"
pass "CLAUDE.md managed block created with topics"

# Re-running should update in place, not duplicate
"$ROTA_BIN" block knowledge >/dev/null
COUNT_START=$(grep -c "rota-knowledge-start" CLAUDE.md)
[ "$COUNT_START" = "1" ] || fail "managed block duplicated"
pass "managed block updated in place"

# knowledge query — unmatched topic warns, exits 0, text untouched (T109/#16)
# No EXIT trap here — clean up explicitly so the runner's global `$TMP` trap
# stays intact (F38 local-trap convention).
KQ_TMP="$(mktemp -d)"
mkdir -p "$KQ_TMP/.rota"
cat > "$KQ_TMP/.rota/KNOWLEDGE.md" <<'EOF'
# Knowledge

## Some Topic
- **Rule one** — body text here <!-- 2026-01-01 -->
EOF

# (a) existing topic: bullets in data.text, nothing missing, no warnings, exit 0
RC=0; OUT=$(hvj -C "$KQ_TMP" knowledge query "Some Topic" 2>/dev/null) || RC=$?
[ "$RC" = "0" ] || fail "knowledge query existing topic exit $RC (want 0)"
TEXT=$(jget data.text <<<"$OUT")
grep -q "^## Some Topic" <<<"$TEXT" || fail "existing topic missing '## Some Topic' in text"
grep -q "Rule one" <<<"$TEXT" || fail "existing topic missing bullet in text"
[ "$(jget data.missing <<<"$OUT")" = "[]" ] || fail "existing topic reported missing: $OUT"
if jget warnings <<<"$OUT" >/dev/null 2>&1; then fail "existing topic emitted warnings: $OUT"; fi

# (b) bogus topic: empty text, topic reported missing, a warning, exit 0
RC=0; OUT=$(hvj -C "$KQ_TMP" knowledge query "Bogus" 2>/dev/null) || RC=$?
[ "$RC" = "0" ] || fail "knowledge query bogus topic exit $RC (want 0)"
[ -z "$(jget data.text <<<"$OUT")" ] || fail "bogus topic produced text: $OUT"
[ "$(jget data.missing <<<"$OUT")" = '["Bogus"]' ] || fail "bogus topic not in missing: $OUT"
grep -q "Bogus" <<<"$(jget 'warnings[0]' <<<"$OUT")" || fail "bogus topic warning missing topic text: $OUT"

# (c) mixed real + bogus: real section in text, only the bogus one missing, exit 0
RC=0; OUT=$(hvj -C "$KQ_TMP" knowledge query "Some Topic" "Bogus" 2>/dev/null) || RC=$?
[ "$RC" = "0" ] || fail "knowledge query mixed topics exit $RC (want 0)"
grep -q "^## Some Topic" <<<"$(jget data.text <<<"$OUT")" || fail "mixed query missing real topic in text"
[ "$(jget data.missing <<<"$OUT")" = '["Bogus"]' ] || fail "mixed query should list only Bogus as missing: $OUT"
if grep -q "Some Topic" <<<"$(jget warnings <<<"$OUT")"; then fail "mixed query warned about matched topic"; fi

# no topic given is a usage error (exit 2)
RC=0; hvj -C "$KQ_TMP" knowledge query >/dev/null 2>&1 || RC=$?
[ "$RC" = "2" ] || fail "knowledge query with no topic exit $RC (want 2)"
rm -rf "$KQ_TMP"
pass "knowledge query warns on unmatched topics, silent on matches, exits 0"
