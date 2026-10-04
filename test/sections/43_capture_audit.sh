echo "F27 item shipped — surfaces ship evidence for stale milestone-spec captures"

# Builds a fixture repo with a few commits whose subjects mention specific
# helper names + paths, then asserts `item shipped`:
#   - exits 0 with found=true and hits for titles whose distinctive tokens
#     appear in commit subjects
#   - exits 1 with found=false and no hits for titles with no overlap
#   - exits 2 on missing args
#
# The audit is heuristic — keyword grep against `git log` + path existence.
# False-positive tolerance is acceptable; false-negatives (missing a shipped
# title) defeat the purpose, so the assertions favor wide token shapes.

TMP_AUDIT="$(mktemp -d)"
trap 'rm -rf "$TMP_AUDIT"' EXIT
cd "$TMP_AUDIT"
git init -q
git config user.email t@t
git config user.name t

mkdir -p bin runner
cat > runner/postgres.go <<'EOF'
package runner
EOF
cat > bin/flagship <<'EOF'
#!/bin/sh
EOF
chmod +x bin/flagship
git add -A
git commit -q -m "seed runner/postgres.go and bin/flagship"
git commit --allow-empty -q -m "feat: implement Driver for postgres backend [F76]"
git commit --allow-empty -q -m "refactor: rename bin/flagship to bin/flagship-v2"

# The verb needs a project root (no `.rota/` walk-up past the fixture), but audits
# the git repo it runs in.
mkdir -p .rota

# Exit 0 (evidence found): a title whose tokens hit a real commit subject.
rc=0; OUT="$(hvj item shipped "Implement Driver for postgres backend" 2>/dev/null)" || rc=$?
[ "$rc" = "0" ] || fail "item shipped exit code: expected 0 on matched title, got $rc"
[ "$(echo "$OUT" | jget data.found)" = "true" ] || fail "item shipped found should be true: $OUT"
[ "$(echo "$OUT" | jget 'data.titles[0].hits[0].level')" = "strong" ] || fail "item shipped did not report a strong hit for matched title (output: $OUT)"
grep -q "F76" <<<"$(jget 'data.titles[0].hits[0].subject' <<<"$OUT")" || fail "item shipped hit missing the F76 commit reference (output: $OUT)"
pass "F27 shipped — exit 0 + strong hit on shipped title"

# Path match: title contains `runner/postgres.go`, which exists.
rc=0; OUT_PATH="$(hvj item shipped "Add tests for \`runner/postgres.go\`" 2>/dev/null)" || rc=$?
[ "$rc" = "0" ] || fail "item shipped exit code: expected 0 on path-match, got $rc"
[ "$(echo "$OUT_PATH" | jget 'data.titles[0].hits[0].level')" = "path" ] || fail "item shipped did not report a path hit for an existing file (output: $OUT_PATH)"
[ "$(echo "$OUT_PATH" | jget 'data.titles[0].hits[0].path')" = "runner/postgres.go" ] || fail "item shipped path hit names the wrong file (output: $OUT_PATH)"
pass "F27 shipped — exit 0 + path hit when title names an existing file"

# Exit 1: a title with no overlap. Use distinctive made-up tokens so common
# words like "session" don't accidentally hit prior commits.
rc=0; OUT_CLEAN="$(hvj item shipped "Add zorblax-foofoo zonkmind handler" 2>/dev/null)" || rc=$?
[ "$rc" = "1" ] || fail "item shipped exit code: expected 1 on no-overlap title, got $rc (output: $OUT_CLEAN)"
[ "$(echo "$OUT_CLEAN" | jget data.found)" = "false" ] || fail "item shipped found should be false on a clean title: $OUT_CLEAN"
[ "$(echo "$OUT_CLEAN" | jget 'data.titles[0].hits')" = "[]" ] || fail "item shipped reported hits on a clean title: $OUT_CLEAN"
pass "F27 shipped — exit 1, found=false and no hits on titles with no ship evidence"

# Exit 2: usage error on no args.
rc=0; "$ROTA_BIN" item shipped >/dev/null 2>&1 || rc=$?
[ "$rc" = "2" ] || fail "item shipped exit code: expected 2 on missing args, got $rc"
pass "F27 shipped — exit 2 on missing args"

# Multi-arg: clean + flagged in one call is found (any hit wins); the clean title has no hits.
rc=0; OUT_MIX="$(hvj item shipped "Add zorblax-foofoo zonkmind handler" "Implement Driver for postgres backend" 2>/dev/null)" || rc=$?
[ "$rc" = "0" ] || fail "item shipped exit code: expected 0 when any input has evidence, got $rc"
[ "$(echo "$OUT_MIX" | jget 'data.titles[0].hits')" = "[]" ] || fail "multi-arg call reported hits on the clean title: $OUT_MIX"
[ "$(echo "$OUT_MIX" | jget 'data.titles[1].hits[0].level')" = "strong" ] || fail "multi-arg call did not report the flagged title: $OUT_MIX"
pass "F27 shipped — multi-arg call reports hits only on the flagged titles"

cd "$TMP"
trap 'rm -rf "$TMP"' EXIT
