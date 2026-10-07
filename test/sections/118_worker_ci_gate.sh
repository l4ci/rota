echo "worker train/gate: test.fullWhere ci pushes the merge result and waits on CI checks (#376)"
# Local slots and a local bare origin; the fake gh answers the commit checks
# from FAKE_CI. The CI waits end on the first poll.

TMP_CI="$(mktemp -d "$TMP/ci.XXXXXX")"
CPROJ="$TMP_CI/proj"
git init -q --bare "$TMP_CI/origin.git"
mkdir -p "$CPROJ/.rota"
(
  cd "$CPROJ" && git init -q -b main . && git config user.email t@t && git config user.name t \
    && echo seed > seed.txt && git add seed.txt && git commit -q -m seed \
    && git remote add origin "$TMP_CI/origin.git" && git push -q origin main
  for b in c1 c2; do
    git checkout -q -b "$b" main && echo "$b" > "$b.txt" && git add "$b.txt" && git commit -q -m "add $b" && git checkout -q main
  done
) || fail "ci gate fixture repo setup failed"
printf '{"slots":[{"name":"c1","branch":"c1"},{"name":"c2","branch":"c2"}]}\n' > "$CPROJ/.rota/workers.json"
# test.full is "false": a local run would fail, so a pass proves CI decided.
cicfg() { printf '{"issues":{"provider":"github","retryWaitSeconds":0},"test":{"full":["false"],"fullWhere":"%s","ciChecks":%s}}\n' "$1" "${2:-[\"ci/test\"]}" > "$CPROJ/.rota/config.json"; }
ci() { ( cd "$CPROJ" && PATH="$TESTDIR/fakes:$ROTA_POISON_BIN:$PATH" FAKE_TRACKER_DB="$TMP_CI/db.json" \
  ROTA_CI_POLL=0 ROTA_CI_START_WAIT=0 ROTA_CI_TIMEOUT=0 "$ROTA_BIN" --json "$@" 2>/dev/null ); }
ciref() { git -C "$TMP_CI/origin.git" for-each-ref --format='%(refname)' refs/heads/rota/ci/; }

cicfg ci
RC=0; OUT=$(FAKE_CI= ci worker train c1 c2 --base main) || RC=$?
[ "$RC" = "1" ] && [ "$(echo "$OUT" | jget data.verdict)" = "ci-not-run" ] && [ ! -f "$CPROJ/c1.txt" ] \
  || fail "CI that never starts should refuse the train with nothing landed: rc=$RC $OUT"
[ -z "$(ciref)" ] || fail "the rota/ci branch should be deleted: $(ciref)"
pass "no CI check on the pushed train is ci-not-run, nothing lands"

RC=0; OUT=$(FAKE_CI=lint ci worker gate c1 --base main) || RC=$?
[ "$RC" = "1" ] && [ "$(echo "$OUT" | jget data.verdict)" = "ci-not-run" ] && [ ! -f "$CPROJ/c1.txt" ] \
  && echo "$OUT" | grep -q 'ci/test' || fail "an unrelated passing check must not stand in for the listed one: rc=$RC $OUT"
pass "a listed check that never runs is ci-not-run naming it, even with another check green"

RC=0; OUT=$(FAKE_CI=pending ci worker train c1 c2 --base main) || RC=$?
[ "$RC" = "1" ] && [ "$(echo "$OUT" | jget data.verdict)" = "verify-timeout" ] && [ ! -f "$CPROJ/c1.txt" ] \
  || fail "pending CI past the deadline should be verify-timeout: rc=$RC $OUT"
pass "CI still pending at the deadline is verify-timeout"

RC=0; OUT=$(FAKE_CI=fail ci worker gate c1 --base main) || RC=$?
[ "$RC" = "1" ] && [ "$(echo "$OUT" | jget data.verdict)" = "verify-failed" ] && [ ! -f "$CPROJ/c1.txt" ] \
  || fail "a red CI run should refuse the gate before the merge: rc=$RC $OUT"
pass "a red CI run lands nothing through the gate"

RC=0; OUT=$(FAKE_CI=pass ci worker gate c1 --base main) || RC=$?
[ "$RC" = "0" ] && [ "$(echo "$OUT" | jget data.verdict)" = "pass" ] && [ -f "$CPROJ/c1.txt" ] \
  && [ "$(echo "$OUT" | jget data.verified)" = '["ci/test"]' ] || fail "a green CI run should land c1: rc=$RC $OUT"
pass "a green CI run lands the slot"

RC=0; OUT=$(FAKE_CI=pass ci worker train c2 --base main) || RC=$?
[ "$RC" = "0" ] && [ "$(echo "$OUT" | jget data.verdict)" = "pass" ] && [ -f "$CPROJ/c2.txt" ] && [ -z "$(ciref)" ] \
  || fail "a green CI train should land c2: rc=$RC $OUT"
pass "a green CI train lands its members"

cicfg ci '[]'
RC=0; OUT=$(FAKE_CI=pass ci worker train c1 --base main) || RC=$?
[ "$RC" = "70" ] && echo "$OUT" | grep -q 'test.ciChecks' \
  || fail "test.fullWhere ci without test.ciChecks should be refused: rc=$RC $OUT"
pass "test.fullWhere ci with an empty test.ciChecks is refused up front"

cicfg bogus
RC=0; OUT=$(ci worker train c1 --base main) || RC=$?
[ "$RC" = "70" ] || fail "a bad test.fullWhere should be refused: rc=$RC $OUT"
pass "a bad test.fullWhere is refused, never run locally"

rm -rf "${TMP_CI:?}"
