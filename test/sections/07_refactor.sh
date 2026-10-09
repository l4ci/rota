echo "refactor age / item complete counter / refactor reset"
# Own state (#46): a private repo with a minimal BACKLOG, so the section passes
# alone (SECTION_LIST) as well as after 06 in the ordered run.
TMP_RF="$(mktemp -d)"
trap 'rm -rf "$TMP_RF"' EXIT
(
  cd "$TMP_RF"
  git init -q -b main . && git config user.email t@t && git config user.name t
  mkdir -p .rota && echo '{}' > .rota/counters.json
  printf '## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n' > .rota/BACKLOG.md
  git add -A && git commit -q -m seed
  git checkout -q main
  # Reset to isolate this section from prior item complete calls in the suite.
  hvj refactor reset >/dev/null
  # Seed three active entries, then complete each against a real commit.
  cat >> .rota/BACKLOG.md <<'EOF'
- **[F40] Feature done.**
- **[B40] Bug fixed.**
- **[F41] Refactor-driven feature.**
EOF
  echo "f1" > f1.txt && git add f1.txt && git commit -q -m "feat: add f1"
  hvj item complete F40 --no-proof >/dev/null
  echo "b1" > b1.txt && git add b1.txt && git commit -q -m "fix: resolve b1"
  hvj item complete B40 --no-proof >/dev/null
  echo "r1" > r1.txt && git add r1.txt && git commit -q -m "refactor: clean up"
  hvj item complete F41 --no-proof >/dev/null
  OUT=$(hvj refactor age) || vfail
  [ "$(echo "$OUT" | jget data.features)" = "1" ] || fail "expected 1 non-refactor feature, got: $OUT"
  [ "$(echo "$OUT" | jget data.bugs)" = "1" ] || fail "expected 1 non-refactor bug, got: $OUT"
  pass "refactor age counts non-refactor completions only"

  # Re-completing an already-completed item must not re-bump.
  OUT=$(hvj item complete F40 --no-proof) || vfail
  [ "$(echo "$OUT" | jget data.changed)" = "false" ] || fail "re-completion should report changed false: $OUT"
  OUT=$(hvj refactor age) || vfail
  [ "$(echo "$OUT" | jget data.features)" = "1" ] || fail "idempotent re-completion bumped counter, got: $OUT"
  pass "item complete is idempotent (no double-bump)"

  # refactor reset zeros the counters.
  OUT=$(hvj refactor reset) || vfail
  [ "$(echo "$OUT" | jget data.changed)" = "true" ] || fail "reset of non-zero counters should report changed true: $OUT"
  OUT=$(hvj refactor age) || vfail
  [ "$(echo "$OUT" | jget data.features)" = "0" ] || fail "reset failed, features != 0: $OUT"
  [ "$(echo "$OUT" | jget data.bugs)" = "0" ] || fail "reset failed, bugs != 0: $OUT"
  OUT=$(hvj refactor reset) || vfail
  [ "$(echo "$OUT" | jget data.changed)" = "false" ] || fail "reset of zeroed counters should report changed false: $OUT"
  pass "refactor reset zeros the counters"

  # Scoped refactor subjects (refactor(scope):) also count as refactor commits.
  cat >> .rota/BACKLOG.md <<'EOF'
- **[F42] Scoped refactor feature.**
EOF
  echo "r2" > r2.txt && git add r2.txt && git commit -q -m "refactor(hosts): consolidate"
  hvj item complete F42 --no-proof >/dev/null
  OUT=$(hvj refactor age) || vfail
  [ "$(echo "$OUT" | jget data.features)" = "0" ] || fail "scoped refactor(scope): subject bumped counter, got: $OUT"
  pass "item complete recognises scoped refactor(scope): subjects"

  echo "ship merge"
  # An empty merge message is refused (usage), and the branch is left alone.
  git branch rota/empty-msg main
  rc=0; echo "" | "$ROTA_BIN" ship merge rota/empty-msg --body-file - >/dev/null 2>&1 || rc=$?
  [ "$rc" = 2 ] || fail "ship merge should reject an empty message with exit 2, got $rc"
  git rev-parse --verify -q rota/empty-msg >/dev/null || fail "ship merge removed the branch it refused to merge"
  git branch -q -D rota/empty-msg
  pass "ship merge rejects empty message"
  # Don't actually run ship pr — no remote
)
trap 'rm -rf "$TMP"' EXIT
