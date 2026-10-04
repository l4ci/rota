echo "block decisions"
mkdir -p .rota
cat > .rota/DECISIONS.md <<'EOF'
# Decisions

## Architecture

### No background queues
Background jobs run in-process.
*Why.* Operational simplicity.
**Forbids.** Adding Sidekiq, RabbitMQ, etc.
**Permits.** In-process Goroutines, threads.

## Testing

### No mocked DB in integration tests
Integration tests must hit a real database.
*Why.* Past mock/prod divergence.
**Forbids.** Mock DB libraries in tests/integration.
**Permits.** Mocks elsewhere.
EOF
"$ROTA_BIN" block decisions >/dev/null
grep -q "<!-- rota-decisions-start -->" CLAUDE.md || fail "rota-decisions managed block not in CLAUDE.md"
grep -q "## Project Decisions" CLAUDE.md || fail "Project Decisions heading missing"
DEC_BLOCK=$(grep -A 20 "<!-- rota-decisions-start -->" CLAUDE.md)
grep -q "^- Architecture" <<<"$DEC_BLOCK" || fail "Architecture topic missing in decisions block"
grep -q "^- Testing" <<<"$DEC_BLOCK" || fail "Testing topic missing in decisions block"
pass "decisions managed block created with topics"

# Re-running should update in place, not duplicate
rc=0; out=$(hvj block decisions) || rc=$?
[ "$rc" = 0 ] || fail "block decisions re-run exit $rc"
[ "$(jget data.key <<<"$out")" = "decisions" ] || fail "block decisions key wrong: $out"
[ "$(jget data.changed <<<"$out")" = "false" ] || fail "re-run of unchanged block should report changed=false: $out"
COUNT_DEC=$(grep -c "rota-decisions-start" CLAUDE.md)
[ "$COUNT_DEC" = "1" ] || fail "decisions managed block duplicated"
pass "decisions block updated in place"

# Empty .rota/DECISIONS.md (no topics) — block should still appear with placeholder
cat > .rota/DECISIONS.md <<'EOF'
# Decisions

Hard boundaries for this project.
EOF
"$ROTA_BIN" block decisions >/dev/null
EMPTY_BLOCK=$(grep -A 10 "<!-- rota-decisions-start -->" CLAUDE.md)
grep -q "no decisions yet" <<<"$EMPTY_BLOCK" || fail "empty-state placeholder missing"
pass "decisions block handles empty file"

