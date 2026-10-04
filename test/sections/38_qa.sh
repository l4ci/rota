echo "qa query + qa index"

# Build a .rota/qa/ tree with one well-formed target.
mkdir -p .rota/qa
cat > .rota/qa/web.md <<'EOF'
---
target: web
surface: web-ui
summary: Public marketing site QA — perf budgets + a11y + smoke
created: 2026-05-15
touched: 2026-05-15
watch-globs:
  - "src/**/*.tsx"
  - "src/**/*.css"
---

## Surface

Web UI (Next.js, deployed at staging.example.com).

## Watch globs

- `src/**/*.tsx`
- `src/**/*.css`

## Executable checks

- **lighthouse-perf** · `lighthouse https://staging.example.com --budget-path=.budget.json` · all budgets met
- **pa11y-a11y** · `pa11y https://staging.example.com` · 0 errors
- **smoke** · `bash test/smoke.sh` · exit 0

## Audit checks

- Empty states (rubric in references/usability-rubric.md)
- First-run flow
- Error recovery copy

## Infra requirements

- Staging URL reachable: `curl -fsS https://staging.example.com >/dev/null`
- `command -v lighthouse pa11y`

## Out of scope

- Load testing
- Real-payment flows
EOF

# qa query prints body of named target.
QA_OUT=$(hvj qa query web | jget data.text)
grep -q "^## Executable checks" <<<"$QA_OUT" || fail "qa query body missing Executable checks heading"
grep -q "^## Audit checks" <<<"$QA_OUT" || fail "qa query body missing Audit checks heading"
grep -q "^---" <<<"$QA_OUT" && fail "qa query leaked frontmatter into the body"
pass "qa query prints body, strips frontmatter"

# Missing target is silent (exit 0, empty body).
rc=0; QA_MISS=$(hvj qa query nonexistent | jget data.text) || rc=$?
[ "$rc" = 0 ] || fail "qa query must exit 0 for a missing target, got $rc"
[ -z "$QA_MISS" ] || fail "qa query emitted output for missing target"
pass "qa query silent on missing target"

# No target is a usage error.
rc=0; hvj qa query >/dev/null 2>&1 || rc=$?
[ "$rc" = 2 ] || fail "qa query without a target must exit 2, got $rc"
pass "qa query without a target is a usage error"

# Multi-target ordering: requested order is preserved.
cat > .rota/qa/api.md <<'EOF'
---
target: api
surface: http-api
summary: REST API contract + auth probes
created: 2026-05-15
touched: 2026-05-15
---

## Surface

HTTP API.
EOF
QA_MULTI=$(hvj qa query api web | jget data.text)
FIRST_HEAD=$(grep -m1 "^## " <<<"$QA_MULTI")
[ "$FIRST_HEAD" = "## Surface" ] || fail "qa query order not preserved"
pass "qa query preserves argument order"

# qa index regenerates the managed block.
QA_IDX=$(hvj qa index)
[ "$(jget data.key <<<"$QA_IDX")" = "qa" ] || fail "qa index data.key wrong: $QA_IDX"
[ "$(jget data.changed <<<"$QA_IDX")" = "true" ] || fail "first qa index must report changed: $QA_IDX"
grep -q "<!-- rota-qa-start -->" CLAUDE.md || fail "rota-qa managed block not in CLAUDE.md"
grep -q "^## Project QA" CLAUDE.md || fail "Project QA heading missing"
grep -q "\*\*web\*\*" CLAUDE.md || fail "web target bullet missing from index"
grep -q "\*\*api\*\*" CLAUDE.md || fail "api target bullet missing from index"
pass "qa index seeds Project QA block"

# Re-running is idempotent (no duplicate markers).
QA_IDX=$(hvj qa index)
[ "$(jget data.status <<<"$QA_IDX")" = "unchanged" ] || fail "repeat qa index must be unchanged: $QA_IDX"
[ "$(jget data.changed <<<"$QA_IDX")" = "false" ] || fail "repeat qa index must report changed=false: $QA_IDX"
COUNT_START=$(grep -c "rota-qa-start" CLAUDE.md)
[ "$COUNT_START" = "1" ] || fail "rota-qa managed block duplicated on re-run"
pass "qa index updates in place"

# Empty .rota/qa/ renders the no-strategy hint.
rm -f .rota/qa/web.md .rota/qa/api.md
hvj qa index >/dev/null
grep -q "no QA strategy yet" CLAUDE.md || fail "empty-state hint missing"
pass "qa index renders empty-state hint"

# Cleanup so later sections start fresh.
rm -rf .rota/qa
hvj qa index >/dev/null
