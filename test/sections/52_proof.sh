echo "proof add / proof show / item complete proof gate"

TMP_PF="$(mktemp -d)"
trap 'rm -rf "$TMP_PF"' EXIT
mkdir -p "$TMP_PF/proj"
(
  cd "$TMP_PF/proj"
  git init -q -b main .
  git config user.email t@t
  git config user.name t
  mkdir -p .rota
  printf '## Bugs\n\n- **[B01] [P1] Proven bug.** Desc.\n- **[B02] [P1] Bare bug.** Desc.\n- **[B03] [P1] Dropped bug.** Desc.\n\n## Features\n\n## Tasks\n\n## Completed\n' > .rota/BACKLOG.md
  echo '{}' > .rota/counters.json
  git add -A && git commit -q -m "seed"
)

(
  cd "$TMP_PF/proj"
  H=$(git log -1 --format=%h)

  [ "$(hvj proof show B01 | jget data.count)" = "0" ] || fail "no proof yet: count should be 0"
  [ "$("$ROTA_BIN" proof show B01 --count)" = "0" ] || fail "no proof yet: --count should print 0"
  OUT=$(hvj proof add B01 --check unit --result PASS --evidence "12 passed · 0 failed" --sha "$H") || fail "proof add B01 failed"
  [ "$(jget data.changed <<<"$OUT")" = "true" ] || fail "first proof row should report changed: $OUT"
  [ "$(jget data.type <<<"$OUT")" = "B" ] || fail "proof add type should be B: $OUT"
  [ -f .rota/bugs/B01.proof ] || fail "proof add should create the proof sidecar"
  [ ! -e .rota/bugs/B01.md ] || fail "proof add must not create the detail file"
  grep -q '^## Proof$' .rota/bugs/B01.proof || fail "missing ## Proof section"
  grep -q "^# B01: Proven bug" .rota/bugs/B01.proof || fail "new sidecar should carry the item title"
  pass "proof add creates the sidecar and Proof section, not the detail file"

  OUT=$(hvj proof add B01 --check unit --result PASS --evidence "12 passed · 0 failed" --sha "$H") || fail "repeat proof add failed"
  [ "$(jget data.changed <<<"$OUT")" = "false" ] || fail "identical row must report changed=false: $OUT"
  [ "$(hvj proof show B01 | jget data.count)" = "1" ] || fail "identical row must be idempotent"
  hvj proof add B01 --check lint --result FAIL --evidence "lint.log" --sha "$H" >/dev/null || fail "distinct proof add failed"
  [ "$(hvj proof show B01 | jget data.count)" = "2" ] || fail "distinct row should append"
  OUT=$(hvj proof show B01) || fail "proof show failed"
  [ "$(jget data.rows[0].check <<<"$OUT")" = "unit" ] || fail "row 0 check: $OUT"
  [ "$(jget data.rows[0].result <<<"$OUT")" = "PASS" ] || fail "row 0 result: $OUT"
  [ "$(jget data.rows[0].sha <<<"$OUT")" = "$H" ] || fail "row 0 sha: $OUT"
  [ "$(jget data.rows[0].evidence <<<"$OUT")" = "12 passed · 0 failed" ] || fail "row 0 evidence: $OUT"
  [ "$(jget data.rows[1].result <<<"$OUT")" = "FAIL" ] || fail "row 1 result: $OUT"
  grep -F "· unit · PASS · $H · 12 passed · 0 failed" .rota/bugs/B01.proof >/dev/null || fail "row format in the sidecar"
  pass "proof add is idempotent on identical rows; proof show reports rows"

  rc=0; hvj proof add B01 --check x --result MAYBE --evidence e >/dev/null 2>&1 || rc=$?
  [ "$rc" = "2" ] || fail "bad --result should exit 2, got $rc"
  rc=0; hvj proof add B99 --check x --result PASS --evidence e >/dev/null 2>&1 || rc=$?
  [ "$rc" = "3" ] || fail "unknown ID should exit 3, got $rc"
  pass "proof add rejects bad result and unknown ID"

  # Existing detail file with content stays untouched; proof goes to the sidecar.
  printf '# B02: Bare bug\n\n## Summary\n\nbody\n\n## Notes\n\nlater\n' > .rota/bugs/B02.md
  rc=0; OUT=$(hvj item complete B02 --commit "$H" 2>/dev/null) || rc=$?
  [ "$rc" = "4" ] || fail "no proof: expected exit 4, got $rc"
  [ "$(jget data.blockedBy <<<"$OUT")" = "proof missing" ] || fail "no proof: blockedBy should be 'proof missing': $OUT"
  grep -q '^- \*\*\[B02\]' .rota/BACKLOG.md || fail "refusal must not modify BACKLOG"
  B02_BEFORE=$(cat .rota/bugs/B02.md)
  hvj proof add B02 --check smoke --result PASS --evidence ok --sha "$H" >/dev/null || fail "proof add B02 failed"
  [ "$(cat .rota/bugs/B02.md)" = "$B02_BEFORE" ] || fail "proof add modified the detail file"
  [ -f .rota/bugs/B02.proof ] || fail "B02 proof sidecar missing"
  pass "item complete refuses without proof (exit 4), BACKLOG untouched"

  hvj item complete B02 --commit "$H" >/dev/null || fail "proven close failed"
  grep -qF "~~**[B02] [P1] Bare bug.** Desc.~~ Done $(date +%Y-%m-%d) [\`$H\`]" .rota/BACKLOG.md || fail "proven close should render the plain marker"
  OUT=$(hvj item complete B02 --commit "$H") || fail "already-completed must stay a no-op"
  [ "$(jget data.changed <<<"$OUT")" = "false" ] || fail "already-completed should report changed=false: $OUT"
  pass "item complete with proof renders the unchanged marker"

  hvj item complete B03 --commit "$H" --reason dropped >/dev/null || fail "dropped close failed"
  grep -q '(dropped)' .rota/BACKLOG.md || fail "dropped close needs no proof"
  pass "non-done reasons skip the proof gate"
)

# Rows an older rota wrote into the detail file are read, and seed the sidecar.
(
  cd "$TMP_PF/proj"
  H=$(git log -1 --format=%h)
  printf '## Features\n\n- **[F01] [Major] Legacy.** Desc.\n\n' >> .rota/BACKLOG.md
  mkdir -p .rota/features
  printf '# F01: Legacy\n\n## Proof\n\n- 2026-01-01 · old · PASS · abc1234 · legacy row\n\n## Notes\n\nkeep\n' > .rota/features/F01.md
  [ "$(hvj proof show F01 | jget data.count)" = "1" ] || fail "legacy detail row should be read"
  hvj proof add F01 --check new --result PASS --evidence ok --sha "$H" >/dev/null || fail "proof add F01 failed"
  [ "$(hvj proof show F01 | jget data.count)" = "2" ] || fail "sidecar should hold legacy plus new row"
  grep -q 'legacy row' .rota/features/F01.proof || fail "legacy row should seed the sidecar"
  ! grep -q '^keep' .rota/features/F01.proof || fail "only proof rows should seed the sidecar"
  grep -q '^keep' .rota/features/F01.md || fail "detail file must stay untouched"
  pass "legacy detail-file rows are read and seed the sidecar"
)

trap 'rm -rf "$TMP"' EXIT
rm -rf "$TMP_PF"
