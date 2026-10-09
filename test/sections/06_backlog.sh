echo "backlog list"
# Seed a mix of items in BACKLOG.md
cat > .rota/BACKLOG.md <<'EOF'
# TODO

## Bugs
- **[B10] [P2] Minor glitch.** Desc.
- **[B11] [P0] Crash on launch.** Desc. Related: [F20]

## Features
- **[F20] [Minor] Quick-switch.** Desc. Related: [B11]
- **[F21] [Cosmetic] Tweak spacing.** Desc.

## Tasks
- **[T30] Update toolchain.** Desc.

## Completed
EOF
OUT=$(hvj backlog list) || vfail
[ "$(echo "$OUT" | jget 'data.bugs[0].id')" = "B11" ] || fail "P0 not sorted before P2: $OUT"
[ "$(echo "$OUT" | jget 'data.bugs[0].priority')" = "P0" ] || fail "B11 priority should be P0: $OUT"
[ "$(echo "$OUT" | jget 'data.bugs[1].id')" = "B10" ] || fail "B10 should follow B11: $OUT"
[ "$(echo "$OUT" | jget 'data.features[0].id')" = "F21" ] || fail "Cosmetic not sorted before Minor: $OUT"
[ "$(echo "$OUT" | jget 'data.features[1].id')" = "F20" ] || fail "F20 should follow F21: $OUT"
pass "backlog sorts bugs by priority and features by size"

# Clusters: B11 <-> F20 (mutual Related). Isolated items must not appear.
[ "$(echo "$OUT" | jget data.clusters)" = '[["B11","F20"]]' ] \
  || fail "expected one B11/F20 cluster and no isolated items: $(echo "$OUT" | jget data.clusters)"
pass "backlog emits clusters for related items"

# Triple cluster + isolated item: F22↔F23↔T30 form one component, comma-separated.
cat > .rota/BACKLOG.md <<'EOF'
# TODO

## Bugs
- **[B90] [P1] Solo bug.** Desc.

## Features
- **[F90] [Minor] Hub.** Desc. Related: [F91], [T90]
- **[F91] [Minor] Spoke.** Desc. Related: [F90]

## Tasks
- **[T90] Toolchain.** Desc. Related: [F90]

## Completed
EOF
OUT=$(hvj backlog list) || vfail
[ "$(echo "$OUT" | jget data.clusters)" = '[["F90","F91","T90"]]' ] \
  || fail "triple cluster not reported as one component: $(echo "$OUT" | jget data.clusters)"
pass "backlog reports 3+ member clusters as one component"

# No-cluster fixture: must omit the section entirely.
cat > .rota/BACKLOG.md <<'EOF'
# TODO

## Bugs
- **[B95] [P2] Lone bug.** Desc.

## Features

## Tasks

## Completed
EOF
OUT=$(hvj backlog list) || vfail
[ "$(echo "$OUT" | jget data.clusters)" = '[]' ] \
  || fail "clusters reported with no related items: $OUT"
pass "backlog reports no clusters when nothing is related"

# Restore the original fixture for the In-Progress assertions below.
cat > .rota/BACKLOG.md <<'EOF'
# TODO

## Bugs
- **[B10] [P2] Minor glitch.** Desc.
- **[B11] [P0] Crash on launch.** Desc. Related: [F20]

## Features
- **[F20] [Minor] Quick-switch.** Desc. Related: [B11]
- **[F21] [Cosmetic] Tweak spacing.** Desc.

## Tasks
- **[T30] Update toolchain.** Desc.

## Completed
EOF

# Active items should move to In Progress
"$ROTA_BIN" status add rota/real-branch --items F20 >/dev/null
OUT=$(hvj backlog list) || vfail
[ "$(echo "$OUT" | jget 'data.inProgress[0].id')" = "F20" ] || fail "In Progress should list F20: $OUT"
# F20 should no longer appear in the features list
if echo "$OUT" | jget data.features | grep "F20" >/dev/null; then fail "active F20 leaked into features"; fi
pass "active items excluded from features"
"$ROTA_BIN" status rm rota/real-branch >/dev/null

echo "backlog list --grep matches"
cat > .rota/BACKLOG.md <<'EOF'
# TODO

## Bugs
- **[B70] [P1] Database connection drops after timeout.** Network glitch handler.

## Features
- **[F70] [Cosmetic] Add loading spinner to dashboard.** Cosmetic UX polish.
- **[F71] [Minor] Implement export to CSV.** Need a button on the dashboard view.

## Tasks

## Completed
EOF
echo '{"active":[]}' > .rota/status.json

OUT=$(hvj backlog list --grep dashboard) || vfail
[ "$(echo "$OUT" | jget 'data.features[0].id')" = "F70" ] || fail "F70 (matches 'dashboard') missing: $OUT"
[ "$(echo "$OUT" | jget 'data.features[1].id')" = "F71" ] || fail "F71 (matches 'dashboard') missing: $OUT"
[ "$(echo "$OUT" | jget data.bugs)" = "[]" ] || fail "B70 (no match) leaked: $OUT"
pass "backlog list --grep filters by title/description substring"

echo "backlog list --grep case-insensitive"
OUT=$(hvj backlog list --grep DASHBOARD) || vfail
[ "$(echo "$OUT" | jget 'data.features[0].id')" = "F70" ] || fail "case-insensitive 'DASHBOARD' should match: $OUT"
pass "backlog list --grep is case-insensitive"

echo "backlog list --grep matches ID"
OUT=$(hvj backlog list --grep B70) || vfail
[ "$(echo "$OUT" | jget 'data.bugs[0].id')" = "B70" ] || fail "ID match B70 missing: $OUT"
[ "$(echo "$OUT" | jget data.features)" = "[]" ] || fail "F70 leaked when grepping B70: $OUT"
pass "backlog list --grep matches by ID"

echo "backlog list --grep no matches"
OUT=$(hvj backlog list --grep nonexistent_xyz) || vfail
for k in inProgress bugs features tasks clusters; do
  [ "$(echo "$OUT" | jget "data.$k")" = "[]" ] || fail "data.$k should be empty on no-match: $OUT"
done
pass "backlog list --grep with no matches returns empty lists"

echo "backlog list --grep cluster"
cat > .rota/BACKLOG.md <<'EOF'
# TODO

## Bugs

## Features
- **[F80] [Minor] Auth refactor.** Wire OAuth. Related: [F81]
- **[F81] [Minor] Token rotation.** Refresh JWT. Related: [F80]
- **[F82] [Cosmetic] Unrelated thing.** Standalone.

## Tasks

## Completed
EOF
OUT=$(hvj backlog list --grep "Auth refactor") || vfail
[ "$(echo "$OUT" | jget 'data.features[0].id')" = "F80" ] || fail "F80 missing in filtered output: $OUT"
# The cluster keeps both members, even though only F80 matched
[ "$(echo "$OUT" | jget data.clusters)" = '[["F80","F81"]]' ] || fail "cluster should preserve both members: $OUT"
if echo "$OUT" | jget data.features | grep "F82" >/dev/null; then fail "F82 (no match) should not appear: $OUT"; fi
pass "backlog list --grep filters clusters but preserves all members"

echo "backlog list no-flag regression"
OUT_FILTERED=$(hvj backlog list --grep "") || vfail
OUT_PLAIN=$(hvj backlog list) || vfail
[ "$OUT_FILTERED" = "$OUT_PLAIN" ] || fail "empty --grep should equal no-flag output"
pass "backlog list --grep '' equals unfiltered output"

