echo "milestone check: feature issues hold against criteria and capabilities; threshold and --strict (#853)"

TMP_MC="$(mktemp -d)"
trap 'rm -rf "$TMP_MC"' EXIT
mkdir -p "$TMP_MC/.rota/milestones"
printf '{"issues":{"source":"file"}}\n' > "$TMP_MC/.rota/config.json"
printf '# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n' > "$TMP_MC/.rota/BACKLOG.md"
printf '# Milestones\n\n## Milestones\n' > "$TMP_MC/.rota/MILESTONES.md"
for m in M01 M02; do
  cat > "$TMP_MC/.rota/milestones/$m.md" <<MD
---
id: $m
title: Check $m
status: planned
depends: []
created: 2026-01-01
---

# $m — Check $m

## Goal

Goal.

## Capabilities

- \`rota round assign\` (modified)
- round.maxBounces (new)

## Acceptance

- [ ] AC-1: first criterion
- [ ] AC-2: second criterion

## Rationale

Why.
MD
done
mc() { ( cd "$TMP_MC" && "$ROTA_BIN" "$@" 2>&1 ); }

printf '## Change\n\nADDED rota round assign\nServes: M01 AC-1\n\n## Touches\n\n- `Rota Round Assign`\n' > "$TMP_MC/a.md"
printf '## Change\n\nREMOVED round.maxBounces\nServes: AC-1\nReason: unused\n' > "$TMP_MC/b.md"
printf '## Change\n\nMODIFIED rota round assign\nServes: AC-1\n\n## Touches\n\n- POST /items\n' > "$TMP_MC/c.md"
printf 'A bug with no change section.\n' > "$TMP_MC/bug.md"

rc=0; OUT=$(mc item create --kind features --milestone M01 --title "Add the assign verb" --body-file "$TMP_MC/a.md") || rc=$?
[ "$rc" -eq 0 ] || fail "file feature a: $rc: $OUT"
rc=0; OUT=$(mc item create --kind features --milestone M01 --title "Drop the bounce cap" --body-file "$TMP_MC/b.md") || rc=$?
[ "$rc" -eq 0 ] || fail "file feature b: $rc: $OUT"
rc=0; OUT=$(mc item create --kind features --milestone M01 --title "Change the assign flow" --body-file "$TMP_MC/c.md") || rc=$?
[ "$rc" -eq 0 ] || fail "file feature c: $rc: $OUT"
rc=0; OUT=$(mc item create --kind bugs --tag P2 --milestone M01 --title "Assign crashes on empty" --body-file "$TMP_MC/bug.md") || rc=$?
[ "$rc" -eq 0 ] || fail "file bug: $rc: $OUT"

rc=0; OUT=$(mc milestone check M01) || rc=$?
[ "$rc" -eq 1 ] || fail "check: want exit 1, got $rc: $OUT"
case "$OUT" in *"REMOVED without Migration"*) ;; *) fail "missing REMOVED line: $OUT" ;; esac
case "$OUT" in *"Touches 'post /items' is not a capability"*) ;; *) fail "missing Touches line: $OUT" ;; esac
case "$OUT" in *"AC-2: no feature issue serves it (warning)"*) ;; *) fail "missing uncovered warning: $OUT" ;; esac
case "$OUT" in *"no ## Change"*) fail "the bug was not ignored: $OUT" ;; esac

rc=0; OUT=$(mc milestone check M01 --strict) || rc=$?
[ "$rc" -eq 1 ] || fail "strict: want exit 1, got $rc: $OUT"
case "$OUT" in *"AC-2: no feature issue serves it"*) ;; *) fail "strict: missing uncovered line: $OUT" ;; esac
case "$OUT" in *"(warning)"*) fail "strict: still a warning: $OUT" ;; esac

rc=0; OUT=$(mc --json milestone check M01) || rc=$?
[ "$rc" -eq 1 ] || fail "json: want exit 1, got $rc"
case "$OUT" in *'"ok": false'*) ;; *) fail "json: ok not false: $OUT" ;; esac
case "$OUT" in *'"features": 3'*) ;; *) fail "json: features not 3: $OUT" ;; esac

printf '## Change\n\nADDED round.maxBounces\nServes: AC-1\n' > "$TMP_MC/d.md"
rc=0; OUT=$(mc item create --kind features --milestone M02 --title "Add the first thing" --body-file "$TMP_MC/d.md") || rc=$?
[ "$rc" -eq 0 ] || fail "file M02 feature 1: $rc: $OUT"
rc=0; OUT=$(mc item create --kind features --milestone M02 --title "Add the second thing" --body-file "$TMP_MC/d.md") || rc=$?
[ "$rc" -eq 0 ] || fail "file M02 feature 2: $rc: $OUT"
rc=0; OUT=$(mc milestone check M02 --strict) || rc=$?
[ "$rc" -eq 0 ] || fail "M02 below threshold: want exit 0, got $rc: $OUT"
case "$OUT" in *"below threshold: 2 feature items"*) ;; *) fail "M02: missing threshold line: $OUT" ;; esac

# Siblings that copy a capability into ## Touches clash with no extra declaration:
# F01 (Touches `Rota Round Assign`) is in flight, so a new sibling touching the
# same capability is reported against its slot, and F03 (POST /items) is not.
printf '{"slots":[{"name":"ben","branch":"ben/f01-add","task":"F01","state":"busy"}]}\n' > "$TMP_MC/.rota/workers.json"
printf '## Change\n\nMODIFIED rota round assign\nServes: AC-2\n\n## Touches\n\n- `rota round assign`\n' > "$TMP_MC/e.md"
rc=0; OUT=$(mc item create --kind features --milestone M01 --title "Tweak assign again" --body-file "$TMP_MC/e.md") || rc=$?
[ "$rc" -eq 0 ] || fail "file feature e: $rc: $OUT"
rc=0; OUT=$(mc round candidates --scope open) || rc=$?
[ "$rc" -eq 0 ] || fail "candidates: $rc: $OUT"
case "$OUT" in *"(ben): scopes rota round assign"*) ;; *) fail "candidates: sibling should clash on rota round assign: $OUT" ;; esac
if printf '%s\n' "$OUT" | grep -E 'Change the assign flow|F03' | grep -q 'scopes rota round assign'; then fail "candidates: F03 (POST /items) must not clash: $OUT"; fi

rm -rf "$TMP_MC"
trap 'rm -rf "$TMP"' EXIT
pass "milestone check flags missing Change, bad Touches and REMOVED gaps; threshold and --strict behave"
