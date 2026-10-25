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
  - nested detail

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
if grep -q 'Acceptance criteria' "$F"; then fail "old heading still present"; fi
[ "$(grep -c '^- \[ \] ' "$F")" -eq 2 ] || fail "want two checkbox bullets: $(cat "$F")"
grep -q '^  - nested detail$' "$F" || fail "nested bullet was changed: $(cat "$F")"
grep -q '^- stays a bare bullet$' "$F" || fail "bullet outside the section was touched"

rc=0
OUT=$(mm migrate milestones --apply) || rc=$?
[ "$rc" -eq 0 ] || fail "second apply exited $rc: $OUT"
[ "$OUT" = "nothing to migrate" ] || fail "second apply output: $OUT"
rm -rf "$TMP_MM"
trap 'rm -rf "$TMP"' EXIT
pass "migrate milestones previews, applies and is idempotent"

# Exercise the label migration through the CLI and the fake tracker boundary.
(
  ML="$(mktemp -d)"
  trap 'rm -rf "$ML"' EXIT
  mkdir -p "$ML/.rota"
  printf '{"forge":{"provider":"github"},"issues":{"source":"github","labels":{"support":"remote"}},"round":{"support":"mac"}}\n' > "$ML/.rota/config.json"
  cd "$ML"
  git init -q
  git remote add origin https://github.com/fake/repo.git
  export PATH="$TESTDIR/fakes:$PATH" FAKE_TRACKER_DB="$ML/db.json"
  gh label create remote
  gh issue create --title 'Open legacy issue' --label remote >/dev/null
  gh issue create --title 'Closed legacy issue' --label remote >/dev/null
  gh issue close 2 >/dev/null
  cp "$ML/db.json" "$ML/before.json"
  OUT=$(hvj migrate labels) || vfail
  [ "$(echo "$OUT" | jget 'data.changed')" = "false" ] || fail "label preview changed state: $OUT"
  [ "$(echo "$OUT" | jget 'data.issues[0].id')" = "1" ] || fail "preview must list the open legacy issue: $OUT"
  cmp "$ML/db.json" "$ML/before.json" || fail "label preview wrote to the tracker"
  OUT=$(hvj migrate labels --apply) || vfail
  [ "$(echo "$OUT" | jget 'data.changed')" = "true" ] || fail "label apply did not report a change: $OUT"
  python3 - "$ML" <<'PY' || fail "label migration did not preserve closed issues and retire the key"
import json, sys
from pathlib import Path
root = Path(sys.argv[1])
db = json.loads((root / 'db.json').read_text())
issues = {i['number']: i for i in db['issues']}
assert issues[1]['labels'] == ['machine:mac'], issues[1]
assert issues[2]['labels'] == ['remote'], issues[2]
cfg = json.loads((root / '.rota/config.json').read_text())
assert 'support' not in cfg.get('issues', {}).get('labels', {}), cfg
PY
  OUT=$(hvj migrate labels --apply) || vfail
  [ "$(echo "$OUT" | jget 'data.changed')" = "false" ] || fail "second label apply must be a no-op: $OUT"
)
pass "migrate labels previews, applies the configured label to open issues only, and is idempotent"
