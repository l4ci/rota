echo "F36: item rm"
# Behaviour guard for `rota item rm` across all modes.
# See [F36] — backlog removal (rota-capture --remove).

# ── fixture builder ──────────────────────────────────────────────────────────
# Creates (or re-creates) the standard F36 fixture inside RM_TMP.
build_rm_fixture() {
  local root="$1"
  rm -rf "$root/.rota"
  mkdir -p "$root/.rota/features" "$root/.rota/tasks" "$root/.rota/bugs" "$root/.rota/plans"

  cat > "$root/.rota/BACKLOG.md" <<'FIXEOF'
# TODO

## Bugs
- **[B01] [P2] Crash on startup.** Repro: always. Related: [F01]

## Features
- **[F01] [Minor] Add dark mode.** Would be nice.
- **[F02] [Minor] Export to CSV.** Active feature.

## Tasks
- **[T01] [S] Write release notes.** Due soon.

## Completed
FIXEOF

  printf '# F01\n\nDark mode detail.\n' > "$root/.rota/features/F01.md"
  printf '# F02\n\nExport detail.\n'    > "$root/.rota/features/F02.md"
  printf '# Plan for F01\n\nApproach.\n' > "$root/.rota/plans/M01-F01.md"

  # status.json: F02 is active on branch rota/feature-f02.
  printf '{"active":[{"branch":"rota/feature-f02","items":["F02"]}]}\n' \
    > "$root/.rota/status.json"
}

RM_TMP="$(mktemp -d)"
build_rm_fixture "$RM_TMP"

# ── (a) preview exits 0, reports the plan, warns, BACKLOG.md unchanged ───────
(
  cd "$RM_TMP"
  ORIG_TODO=$(cat .rota/BACKLOG.md)
  rc=0; OUT=$(hvj item rm F01 2>/dev/null) || rc=$?
  [ "$rc" = "0" ] || { echo "FAIL F36(a): preview should exit 0, got $rc"; exit 1; }
  [ "$(echo "$OUT" | jget data.applied)" = "false" ] || { echo "FAIL F36(a): preview must report applied=false: $OUT"; exit 1; }
  [ "$(echo "$OUT" | jget data.changed)" = "false" ] || { echo "FAIL F36(a): preview must report changed=false: $OUT"; exit 1; }
  [ "$(echo "$OUT" | jget 'data.items[0].id')" = "F01" ] || { echo "FAIL F36(a): plan missing F01: $OUT"; exit 1; }
  [ "$(echo "$OUT" | jget 'data.items[0].type')" = "F" ] || { echo "FAIL F36(a): plan type: $OUT"; exit 1; }
  [ "$(echo "$OUT" | jget 'data.items[0].todoEntry')" = "true" ] || { echo "FAIL F36(a): plan todoEntry: $OUT"; exit 1; }
  [ "$(echo "$OUT" | jget 'data.items[0].crossRefs')" = "1" ] || { echo "FAIL F36(a): plan crossRefs: $OUT"; exit 1; }
  [ "$(echo "$OUT" | jget 'data.items[0].detailFile')" = ".rota/features/F01.md" ] || { echo "FAIL F36(a): plan detailFile: $OUT"; exit 1; }
  [ "$(echo "$OUT" | jget 'data.items[0].planFiles')" = '[".rota/plans/M01-F01.md"]' ] || { echo "FAIL F36(a): plan planFiles: $OUT"; exit 1; }
  [ "$(echo "$OUT" | jget 'warnings[0]')" = "preview only; pass --apply" ] || { echo "FAIL F36(a): missing preview warning: $OUT"; exit 1; }
  NEW_TODO=$(cat .rota/BACKLOG.md)
  [ "$ORIG_TODO" = "$NEW_TODO" ] \
    || { echo "FAIL F36(a): preview must not modify BACKLOG.md"; exit 1; }
  [ -f .rota/features/F01.md ] && [ -f .rota/plans/M01-F01.md ] \
    || { echo "FAIL F36(a): preview must not delete files"; exit 1; }
)

# ── (b) not-found exits 3, nothing changes ──────────────────────────────────
(
  cd "$RM_TMP"
  ORIG_TODO=$(cat .rota/BACKLOG.md)
  rc=0; "$ROTA_BIN" item rm BX99 --apply >/dev/null 2>&1 || rc=$?
  [ "$rc" = "3" ] || { echo "FAIL F36(b): not-found should exit 3, got $rc"; exit 1; }
  rc=0; "$ROTA_BIN" item rm F01 BX99 --apply >/dev/null 2>&1 || rc=$?
  [ "$rc" = "3" ] || { echo "FAIL F36(b): one unknown ID among known ones should exit 3, got $rc"; exit 1; }
  [ "$ORIG_TODO" = "$(cat .rota/BACKLOG.md)" ] \
    || { echo "FAIL F36(b): a refused removal must not modify BACKLOG.md"; exit 1; }
  [ -f .rota/features/F01.md ] \
    || { echo "FAIL F36(b): a refused removal must not delete F01's detail file"; exit 1; }
)

# ── (c) active item: preview names its branch, --apply is refused ───────────
(
  cd "$RM_TMP"
  ORIG_TODO=$(cat .rota/BACKLOG.md)
  rc=0; OUT=$(hvj item rm F02 2>/dev/null) || rc=$?
  [ "$rc" = "0" ] || { echo "FAIL F36(c): preview of an active item should exit 0, got $rc"; exit 1; }
  [ "$(echo "$OUT" | jget 'data.items[0].id')" = "F02" ] || { echo "FAIL F36(c): preview missing F02: $OUT"; exit 1; }
  [ "$(echo "$OUT" | jget 'data.items[0].type')" = "F" ] || { echo "FAIL F36(c): preview type: $OUT"; exit 1; }
  [ "$(echo "$OUT" | jget 'data.items[0].activeBranch')" = "rota/feature-f02" ] \
    || { echo "FAIL F36(c): preview missing activeBranch: $OUT"; exit 1; }
  rc=0; OUT=$(hvj item rm F02 --apply 2>/dev/null) || rc=$?
  [ "$rc" = "4" ] || { echo "FAIL F36(c): --apply on an active item should exit 4, got $rc"; exit 1; }
  [ "$(echo "$OUT" | jget data.changed)" = "false" ] || { echo "FAIL F36(c): refusal must report changed=false: $OUT"; exit 1; }
  [ "$(echo "$OUT" | jget data.blockedBy)" = active ] && [ "$(echo "$OUT" | jget data.activeBranch)" = rota/feature-f02 ] \
    || { echo "FAIL F36(c): refusal data must be blockedBy=active with the branch: $OUT"; exit 1; }
  [ "$ORIG_TODO" = "$(cat .rota/BACKLOG.md)" ] \
    || { echo "FAIL F36(c): a refused removal must not modify BACKLOG.md"; exit 1; }
  [ -f .rota/features/F02.md ] \
    || { echo "FAIL F36(c): a refused removal must not delete the detail file"; exit 1; }
  grep -q 'F02' .rota/status.json \
    || { echo "FAIL F36(c): a refused removal must leave status.json alone"; exit 1; }
)

# ── (d) --apply F01: entry gone, cross-ref gone, detail+plan deleted ─────────
(
  cd "$RM_TMP"
  rc=0; OUT=$(hvj item rm F01 --apply 2>/dev/null) || rc=$?
  [ "$rc" = "0" ] || { echo "FAIL F36(d): --apply should exit 0, got $rc"; exit 1; }
  [ "$(echo "$OUT" | jget data.applied)" = "true" ] || { echo "FAIL F36(d): applied should be true: $OUT"; exit 1; }
  [ "$(echo "$OUT" | jget data.changed)" = "true" ] || { echo "FAIL F36(d): changed should be true: $OUT"; exit 1; }
  # F01 entry removed from TODO.
  grep -q '\[F01\]' .rota/BACKLOG.md \
    && { echo "FAIL F36(d): [F01] entry still in BACKLOG.md after --apply"; exit 1; }
  # Related: [F01] cross-ref on B01 stripped.
  grep -q 'Related:.*\[F01\]' .rota/BACKLOG.md \
    && { echo "FAIL F36(d): Related: [F01] cross-ref still present in BACKLOG.md"; exit 1; }
  # Detail file deleted.
  [ ! -f .rota/features/F01.md ] \
    || { echo "FAIL F36(d): .rota/features/F01.md still exists after --apply"; exit 1; }
  # Plan file deleted.
  [ ! -f .rota/plans/M01-F01.md ] \
    || { echo "FAIL F36(d): .rota/plans/M01-F01.md still exists after --apply"; exit 1; }
)

# ── (e) active item: --apply is refused until the stream is removed with
#        `status rm`; then --apply succeeds and status.json holds no F02 ─────
build_rm_fixture "$RM_TMP"
(
  cd "$RM_TMP"
  rc=0; "$ROTA_BIN" item rm F02 --apply >/dev/null 2>&1 || rc=$?
  [ "$rc" = "4" ] || { echo "FAIL F36(e): --apply on an active item should exit 4, got $rc"; exit 1; }
  rc=0; "$ROTA_BIN" status rm rota/feature-f02 >/dev/null 2>&1 || rc=$?
  [ "$rc" = "0" ] || { echo "FAIL F36(e): status rm should exit 0, got $rc"; exit 1; }
  rc=0; OUT=$(hvj item rm F02 --apply 2>/dev/null) || rc=$?
  [ "$rc" = "0" ] || { echo "FAIL F36(e): --apply after status rm should exit 0, got $rc"; exit 1; }
  [ "$(echo "$OUT" | jget 'data.items[0].activeBranch')" = "" ] \
    || { echo "FAIL F36(e): F02 is no longer active: $OUT"; exit 1; }
  grep -q '\[F02\]' .rota/BACKLOG.md \
    && { echo "FAIL F36(e): [F02] entry still in BACKLOG.md after --apply"; exit 1; }
  # The active entry whose only item was F02 must be gone from status.json.
  python3 -c "
import json, sys
d = json.load(open('.rota/status.json'))
for e in d.get('active', []):
    items = e.get('items', [])
    if isinstance(items, list):
        ids = [i.strip() for i in items]
    else:
        ids = [i.strip() for i in str(items).split(',')]
    if 'F02' in ids:
        print('FAIL F36(e): F02 still present in status.json active entry')
        sys.exit(1)
" || exit 1
)

# ── (f) --apply B01 T01 (variadic batch): both removed, F01 untouched ────────
build_rm_fixture "$RM_TMP"
(
  cd "$RM_TMP"
  rc=0; OUT=$(hvj item rm B01 T01 --apply 2>/dev/null) || rc=$?
  [ "$rc" = "0" ] || { echo "FAIL F36(f): batch should exit 0, got $rc"; exit 1; }
  [ "$(echo "$OUT" | jget 'data.items[1].id')" = "T01" ] || { echo "FAIL F36(f): batch data lists both IDs: $OUT"; exit 1; }
  grep -q '\[B01\]' .rota/BACKLOG.md \
    && { echo "FAIL F36(f): [B01] still in BACKLOG.md after batch removal"; exit 1; }
  grep -q '\[T01\]' .rota/BACKLOG.md \
    && { echo "FAIL F36(f): [T01] still in BACKLOG.md after batch removal"; exit 1; }
  # F01 must still be present.
  grep -q '\[F01\]' .rota/BACKLOG.md \
    || { echo "FAIL F36(f): [F01] was unexpectedly removed during B01 T01 batch"; exit 1; }
)

# ── (g) --scrub-archive: archive entry removed ───────────────────────────────
build_rm_fixture "$RM_TMP"
(
  cd "$RM_TMP"
  # Add F09 to ARCHIVE.md so it can be scrubbed.
  cat > .rota/ARCHIVE.md <<'ARCHEOF'
# Archive

## Features
- ~~**[F09] [Minor] Old archived feature.** Done.~~ Done 2026-01-01 [`abc1234`]
ARCHEOF

  rc=0; OUT=$(hvj item rm F09 --scrub-archive 2>/dev/null) || rc=$?
  [ "$rc" = "0" ] || { echo "FAIL F36(g): --scrub-archive preview should exit 0, got $rc"; exit 1; }
  [ "$(echo "$OUT" | jget 'data.items[0].archive')" = "true" ] || { echo "FAIL F36(g): preview should flag the archive entry: $OUT"; exit 1; }
  grep -q '\[F09\]' .rota/ARCHIVE.md \
    || { echo "FAIL F36(g): preview must leave ARCHIVE.md alone"; exit 1; }
  rc=0; "$ROTA_BIN" item rm F09 --scrub-archive --apply >/dev/null 2>&1 || rc=$?
  [ "$rc" = "0" ] || { echo "FAIL F36(g): --scrub-archive should exit 0, got $rc"; exit 1; }
  grep -q '\[F09\]' .rota/ARCHIVE.md \
    && { echo "FAIL F36(g): [F09] entry still in ARCHIVE.md after --scrub-archive"; exit 1; } \
    || true
)

# ── (h) --apply B01 B02 (multi-ID batch): BOTH cross-refs stripped from the
#        surviving F01 line, with no dangling `Related:` remnant ──────────────
build_rm_fixture "$RM_TMP"
(
  cd "$RM_TMP"
  cat > .rota/BACKLOG.md <<'FIXEOF'
# TODO

## Bugs
- **[B01] [P2] First bug.** Repro: always.
- **[B02] [P2] Second bug.** Repro: sometimes.

## Features
- **[F01] [Minor] Depends on both bugs.** Body text. Related: [B01], [B02]

## Tasks

## Completed
FIXEOF
  rc=0; "$ROTA_BIN" item rm B01 B02 --apply >/dev/null 2>&1 || rc=$?
  [ "$rc" = "0" ] || { echo "FAIL F36(h): multi-ID batch should exit 0, got $rc"; exit 1; }
  F01_LINE=$(grep '\[F01\]' .rota/BACKLOG.md) \
    || { echo "FAIL F36(h): surviving [F01] bullet missing from BACKLOG.md"; exit 1; }
  grep -q '\[B01\]' <<<"$F01_LINE" \
    && { echo "FAIL F36(h): [B01] cross-ref still on F01 line: $F01_LINE"; exit 1; }
  grep -q '\[B02\]' <<<"$F01_LINE" \
    && { echo "FAIL F36(h): [B02] cross-ref still on F01 line: $F01_LINE"; exit 1; }
  grep -q 'Related:' <<<"$F01_LINE" \
    && { echo "FAIL F36(h): dangling 'Related:' remnant on F01 line: $F01_LINE"; exit 1; }
  true
)

rm -rf "$RM_TMP"
pass "F36 item rm smoke"

# ── F32: loop-mode auto-planning helpers + wiring ────────────────────────────
