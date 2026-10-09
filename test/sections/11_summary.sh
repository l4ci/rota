echo "summary"
# Reset to a known state and check the summary lines
rm -f .rota/ARCHIVE.md
cat > .rota/BACKLOG.md <<'EOF'
# TODO

## Bugs
- **[B60] [P1] Active bug.** Desc.

## Features
- **[F60] [Minor] Pending feature.** Desc.
- **[F61] [Cosmetic] Another feature.** Desc.

## Tasks

## Completed
- ~~**[B01] Resolved bug.**~~ Done 2026-04-18 [`abc1234`]
EOF
cat > .rota/KNOWLEDGE.md <<'EOF'
# Knowledge

## Architecture
- a

## Testing
- t
EOF
OUT=$(hvj summary) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
[ "$(echo "$OUT" | jget data.backlog.bugs)" = "1" ] || fail "bug count wrong: $OUT"
[ "$(echo "$OUT" | jget data.backlog.features)" = "2" ] || fail "feature count wrong: $OUT"
[ "$(echo "$OUT" | jget data.backlog.tasks)" = "0" ] || fail "task count wrong: $OUT"
[ "$(echo "$OUT" | jget 'data.recent[0].id')" = "B01" ] || fail "recent completion missing: $OUT"
[ "$(echo "$OUT" | jget 'data.recent[0].type')" = "B" ] || fail "recent completion lost its type: $OUT"
[ "$(echo "$OUT" | jget data.knowledge.count)" = "2" ] || fail "knowledge topic count wrong: $OUT"
pass "summary reports backlog/recent/knowledge correctly"

