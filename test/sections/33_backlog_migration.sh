echo "init BACKLOG.md migration (F71/T1)"

# Self-contained: each subtest uses its own BOOT_DIR so state doesn't bleed.
BOOT_DIR="$TMP/boot-test-31"

# Local trap pattern (F38/F62 convention): save runner's EXIT trap, restore after.
trap 'rm -rf "$BOOT_DIR"; trap '"'"'rm -rf "$TMP"'"'"' EXIT' EXIT

# ── (a) Fresh init seeds BACKLOG.md, not TODO.md ─────────────────────────────
mkdir -p "$BOOT_DIR"
OUT=$(cd "$BOOT_DIR" && hvj init) || fail "init failed on a fresh directory: $OUT"
[ -f "$BOOT_DIR/.rota/BACKLOG.md" ] || fail "init did not seed BACKLOG.md on fresh init"
! [ -f "$BOOT_DIR/.rota/TODO.md" ] || fail "init seeded legacy TODO.md on fresh init"
[ "$(echo "$OUT" | jget data.changed)" = "true" ] || fail "fresh init should report changed: $OUT"
echo "$OUT" | python3 -c '
import json, sys
created = json.load(sys.stdin)["data"]["created"]
assert ".rota/BACKLOG.md" in created, created
' || fail "fresh init should list .rota/BACKLOG.md as created: $OUT"
pass "init seeds BACKLOG.md on fresh init"

# The seeded project passes the initialized check.
OUT=$(cd "$BOOT_DIR" && hvj init check) || fail "init check failed on a fresh init: $OUT"
[ "$(echo "$OUT" | jget data.initialized)" = "true" ] || fail "init check: expected initialized: $OUT"

# ── (b) Reader contract — the backlog reads BACKLOG.md only ──
# The legacy TODO.md fallback was removed in v4.1 (F71 self-flagged it for
# removal once the rename shipped in v4.0). Reader test now verifies a listing
# reflects what's at BACKLOG.md, not the legacy path.
cat > "$BOOT_DIR/.rota/BACKLOG.md" <<'EOF'
# BACKLOG

## Bugs

- **[B99] [P1] Reader-contract test bug.** Body.
EOF
OUT=$(cd "$BOOT_DIR" && hvj backlog list) || fail "backlog list failed in the migrated project: $OUT"
[ "$(echo "$OUT" | jget 'data.bugs[0].id')" = "B99" ] \
  || fail "backlog list did not read BACKLOG.md content: $OUT"
pass "backlog list reads BACKLOG.md (legacy TODO.md fallback removed in v4.1)"

# ── Not initialized: init check names what is missing ────────────────────────
rm -rf "$BOOT_DIR"
mkdir -p "$BOOT_DIR"
rc=0; OUT=$(cd "$BOOT_DIR" && hvj init check 2>/dev/null) || rc=$?
[ "$rc" = 1 ] || fail "init check on an uninitialized directory should exit 1, got $rc"
[ "$(echo "$OUT" | jget data.initialized)" = "false" ] || fail "init check: expected initialized false: $OUT"
echo "$OUT" | python3 -c '
import json, sys
missing = json.load(sys.stdin)["data"]["missing"]
assert missing == [".rota"], missing
' || fail "init check should name the missing .rota: $OUT"
pass "init check exits 1 and names the missing path when uninitialized"

# ── Cleanup ───────────────────────────────────────────────────────────────────
rm -rf "$BOOT_DIR"
trap 'rm -rf "$TMP"' EXIT
