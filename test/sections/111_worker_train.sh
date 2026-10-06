echo "worker train: verifies several PRs merged together once, lands them, bisects a red train (#83)"
# Local slots only (no PRs, no forge): the train merges the branches of the
# slots in order, as section 104's gate does for one.

TMP_TR="$(mktemp -d)"
TPROJ="$TMP_TR/proj"
mkdir -p "$TPROJ/.rota"
(
  cd "$TPROJ" && git init -q -b main . && git config user.email t@t && git config user.name t \
    && echo seed > seed.txt && git add seed.txt && git commit -q -m seed
  for b in t1 t2 t3; do
    git checkout -q -b "$b" main && echo "$b" > "$b.txt" && git add "$b.txt" && git commit -q -m "add $b" && git checkout -q main
  done
) || fail "train fixture repo setup failed"
printf '{"slots":[{"name":"t1","branch":"t1"},{"name":"t2","branch":"t2"},{"name":"t3","branch":"t3"}]}\n' > "$TPROJ/.rota/workers.json"
trcfg() { printf '{"test":{"full":["%s"]}}\n' "$1" > "$TPROJ/.rota/config.json"; }
trn() { ( cd "$TPROJ" && PATH="$TESTDIR/fakes:$ROTA_POISON_BIN:$PATH" "$ROTA_BIN" --json "$@" 2>/dev/null ); }

# A red train names the first member that breaks the tree and lands nothing.
trcfg 'test ! -f t2.txt'
RC=0; OUT=$(trn worker train t1 t2 t3 --base main) || RC=$?
[ "$RC" = "1" ] && [ "$(echo "$OUT" | jget data.verdict)" = "verify-failed" ] && [ "$(echo "$OUT" | jget data.culprit)" = "t2" ] \
  || fail "a red train should name t2: rc=$RC $OUT"
[ ! -f "$TPROJ/t1.txt" ] && [ "$(echo "$OUT" | jget data.changed)" = "false" ] || fail "a red train must land nothing: $OUT"
[ "$(git -C "$TPROJ" worktree list | wc -l)" = "1" ] || fail "the scratch worktree should be gone"
pass "a red train bisects to the culprit and merges nothing"

# --land-green lands the verified prefix before the culprit.
RC=0; OUT=$(trn worker train t1 t2 t3 --base main --land-green) || RC=$?
[ "$RC" = "1" ] && [ "$(echo "$OUT" | jget data.culprit)" = "t2" ] && [ "$(echo "$OUT" | jget data.changed)" = "true" ] \
  && [ -f "$TPROJ/t1.txt" ] && [ ! -f "$TPROJ/t2.txt" ] || fail "--land-green should land t1 only: rc=$RC $OUT"
pass "--land-green lands the verified members before the culprit"

# A green train lands every remaining member after one verification.
trcfg 'test -f t1.txt && test -f t2.txt && test -f t3.txt'
RC=0; OUT=$(trn worker train t2 t3 --base main) || RC=$?
[ "$RC" = "0" ] && [ "$(echo "$OUT" | jget data.verdict)" = "pass" ] && [ -f "$TPROJ/t2.txt" ] && [ -f "$TPROJ/t3.txt" ] \
  || fail "a green train should land t2 and t3: rc=$RC $OUT"
pass "a green train verifies once and lands every member"

rm -rf "${TMP_TR:?}"
