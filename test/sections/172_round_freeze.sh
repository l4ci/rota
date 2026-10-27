#!/usr/bin/env bash
echo "Section 172: round freeze repair designation and audited manual recovery (#2170)"
TMP_FREEZE="$(mktemp -d "$TMP/freeze.XXXXXX")"
(
  cd "$TMP_FREEZE"
  git init -q -b main
  git -c user.name=test -c user.email=test@test commit -q --allow-empty -m seed
  mkdir -p .rota .git/rota
  printf '%s\n' ".rota/" > .gitignore
  git add .gitignore
  git -c user.name=test -c user.email=test@test commit -q -m ignore-runtime
  printf '%s\n' '{"forge":{"provider":"local"},"test":{"full":["true"]}}' > .rota/config.json
  printf '%s\n' '{"active":true,"base":"main","sha":"red","prs":["7"],"checks":["false"]}' > .git/rota/freeze.json
  OUT=$("$ROTA_BIN" --json round freeze --repair-issue 99) || vfail
  [ "$(jget data.freeze.repairIssue <<<"$OUT")" = 99 ] || fail "repair designation missing: $OUT"
  OUT=$("$ROTA_BIN" --json round freeze) || vfail
  [ "$(jget data.freeze.active <<<"$OUT")" = true ] || fail "repair designation cleared freeze: $OUT"
  rc=0; OUT=$("$ROTA_BIN" --json round freeze --clear 2>&1) || rc=$?
  [ "$rc" -eq 2 ] || fail "manual clear without reason should exit 2: $OUT"
  OUT=$("$ROTA_BIN" --json round freeze --clear --reason 'obsolete check') || vfail
  OUT=$("$ROTA_BIN" --json round freeze) || vfail
  [ "$(jget data.freeze.active <<<"$OUT")" = false ] || fail "manual clear did not persist: $OUT"
  rg -q 'obsolete check' .rota/gate-audit.jsonl || fail "manual clear missing audit reason"
  printf '%s\n' '{"active":true,"base":"main","sha":"red","prs":["7"],"checks":["false"]}' > .git/rota/freeze.json
  OUT=$("$ROTA_BIN" --json round freeze --verify) || vfail
  OUT=$("$ROTA_BIN" --json round freeze) || vfail
  [ "$(jget data.freeze.active <<<"$OUT")" = false ] || fail "green verification did not clear freeze: $OUT"
  [ "$(jget data.freeze.clearedSHA <<<"$OUT")" = "$(git rev-parse HEAD)" ] || fail "clearance SHA missing: $OUT"
)
rm -rf "$TMP_FREEZE"
pass "freeze repair designation, required reason, audit and verified recovery"
