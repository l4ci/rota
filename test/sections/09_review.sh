echo "review scaffolding — flags scaffolding patterns in diff"
SCAF_TMP="$(mktemp -d)"
trap 'rm -rf "$SCAF_TMP"' EXIT
(
  cd "$SCAF_TMP"
  git init -q
  git config user.email t@t && git config user.name t
  git checkout -q -b main 2>/dev/null || git branch -m main
  mkdir -p .rota
  # Seed main with a benign file
  echo "ok" > a.txt
  git add a.txt && git commit -q -m "init"
  git checkout -q -b feat
  # Add a file with scaffolding text and a clean line
  cat > b.sh <<'EOF'
#!/usr/bin/env bash
# Umbrella behavior is added in Task 7 — for now --repo is parsed but ignored
set -e
echo "real work"
EOF
  git add b.sh && git commit -q -m "feat: add b.sh"

  OUT=$(hvj review scaffolding feat --base main)
  [ "$(echo "$OUT" | jget data.findings[0].file)" = "b.sh" ] || fail "scaffolding scan missed b.sh: $OUT"
  echo "$OUT" | jget data.findings[0].text | grep "Task 7" >/dev/null || fail "scaffolding scan missed 'Task 7' in b.sh: $OUT"
  [ "$(echo "$OUT" | jget data.findings[0].line)" = "2" ] || fail "scaffolding finding should sit on line 2: $OUT"
  [ "$(echo "$OUT" | jget data.findings | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')" = "1" ] \
    || fail "scaffolding scan should flag only the Task 7 line: $OUT"

  # Add a clean-only commit; the verb should still surface the existing match
  echo "more" >> a.txt && git add a.txt && git commit -q -m "feat: tweak a"
  OUT2=$(hvj review scaffolding feat --base main)
  echo "$OUT2" | jget data.findings[0].text | grep "Task 7" >/dev/null || fail "scaffolding scan lost match after benign commit: $OUT2"

  # Empty diff (branch == base) -> no findings, exit 0
  git checkout -q main
  rc=0; OUT3=$(hvj review scaffolding main --base main) || rc=$?
  [ $rc -eq 0 ] || fail "empty-diff scan should exit 0, got $rc"
  [ "$(echo "$OUT3" | jget data.findings)" = "[]" ] || fail "empty-diff scan should have no findings, got: $OUT3"

  # an unknown branch is a resolution failure
  rc=0; hvj review scaffolding no-such-branch --base main >/dev/null 2>&1 || rc=$?
  [ $rc -eq 3 ] || fail "scaffolding on an unknown branch should exit 3, got $rc"

  pass "review scaffolding flags scaffolding + handles empty diff"
)
trap 'rm -rf "$TMP"' EXIT
