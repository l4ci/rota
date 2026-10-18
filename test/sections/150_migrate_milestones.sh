echo "migrate milestones: Acceptance criteria becomes Acceptance with checkboxes; preview first, idempotent (#853)"

TMP_MM="$(mktemp -d)"
trap 'rm -rf "$TMP_MM"' EXIT
mkdir -p "$TMP_MM/.rota/milestones"
printf '{"issues":{"source":"file"}}\n' > "$TMP_MM/.rota/config.json"
printf '# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n' > "$TMP_MM/.rota/BACKLOG.md"
printf '# Milestones\n\n## Milestones\n' > "$TMP_MM/.rota/MILESTONES.md"
cat > "$TMP_MM/.rota/milestones/M01.md" <<'MD'
---
id: M01
title: Old shape
status: planned
depends: []
created: 2026-01-01
---

# M01 — Old shape

## Goal

Old goal.

## Acceptance criteria

- first criterion
- second criterion

## Rationale

- stays a bare bullet
MD
mm() { ( cd "$TMP_MM" && "$ROTA_BIN" "$@" 2>/dev/null ); }
F="$TMP_MM/.rota/milestones/M01.md"
before=$(cat "$F")

rc=0
OUT=$(mm migrate milestones) || rc=$?
[ "$rc" -eq 0 ] || fail "preview exited $rc: $OUT"
[ "$OUT" = "M01: would migrate" ] || fail "preview output: $OUT"
[ "$(cat "$F")" = "$before" ] || fail "preview changed the milestone file"

rc=0
OUT=$(mm migrate milestones --apply) || rc=$?
[ "$rc" -eq 0 ] || fail "apply exited $rc: $OUT"
[ "$OUT" = "M01: migrated" ] || fail "apply output: $OUT"
grep -q '^## Acceptance$' "$F" || fail "heading not renamed"
! grep -q 'Acceptance criteria' "$F" || fail "old heading still present"
[ "$(grep -c '^- \[ \] ' "$F")" -eq 2 ] || fail "want two checkbox bullets: $(cat "$F")"
grep -q '^- stays a bare bullet$' "$F" || fail "bullet outside the section was touched"

rc=0
OUT=$(mm migrate milestones --apply) || rc=$?
[ "$rc" -eq 0 ] || fail "second apply exited $rc: $OUT"
[ "$OUT" = "nothing to migrate" ] || fail "second apply output: $OUT"
rm -rf "$TMP_MM"
trap 'rm -rf "$TMP"' EXIT
pass "migrate milestones previews, applies and is idempotent"
