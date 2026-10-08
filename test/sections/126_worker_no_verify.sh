echo "worker gate/train: an empty test.full is refused with exit 4 unless --no-verify (#461)"
# Local slots only, as section 111: the refusal comes before anything merges.

TMP_NV="$(mktemp -d)"
NPROJ="$TMP_NV/proj"
mkdir -p "$NPROJ/.rota"
(
  cd "$NPROJ" && git init -q -b main . && git config user.email t@t && git config user.name t \
    && echo seed > seed.txt && git add seed.txt && git commit -q -m seed
  for b in n1 n2; do
    git checkout -q -b "$b" main && echo "$b" > "$b.txt" && git add "$b.txt" && git commit -q -m "add $b" && git checkout -q main
  done
) || fail "no-verify fixture repo setup failed"
printf '{"slots":[{"name":"n1","branch":"n1"},{"name":"n2","branch":"n2"}]}\n' > "$NPROJ/.rota/workers.json"
printf '{"ship":{"review":"none"},"test":{"full":[]}}\n' > "$NPROJ/.rota/config.json"
nv() { ( cd "$NPROJ" && PATH="$TESTDIR/fakes:$ROTA_POISON_BIN:$PATH" "$ROTA_BIN" --json "$@" 2>/dev/null ); }

RC=0; OUT=$(nv worker gate n1 --base main) || RC=$?
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.blockedBy)" = "no-verify" ] && [ "$(echo "$OUT" | jget data.changed)" = "false" ] \
  || fail "gate with an empty test.full should be refused with 4: rc=$RC $OUT"
case "$OUT" in *"rota config set test.full"*"--no-verify"*) ;; *) fail "the hint should name the config verb and --no-verify: $OUT" ;; esac
[ ! -f "$NPROJ/n1.txt" ] || fail "a refused gate must land nothing"
pass "gate refuses an empty test.full with exit 4, blockedBy no-verify"

RC=0; OUT=$(nv worker train n1 n2 --base main) || RC=$?
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.blockedBy)" = "no-verify" ] && [ ! -f "$NPROJ/n1.txt" ] \
  || fail "train with an empty test.full should be refused with 4: rc=$RC $OUT"
pass "train refuses an empty test.full with exit 4, blockedBy no-verify"

RC=0; OUT=$(nv worker gate n1 --base main --no-verify) || RC=$?
[ "$RC" = "0" ] && [ "$(echo "$OUT" | jget data.verifySkipped)" = "true" ] && [ -f "$NPROJ/n1.txt" ] \
  || fail "gate --no-verify should merge: rc=$RC $OUT"
RC=0; OUT=$(nv worker train n2 --base main --no-verify) || RC=$?
[ "$RC" = "0" ] && [ -f "$NPROJ/n2.txt" ] || fail "train --no-verify should land: rc=$RC $OUT"
pass "--no-verify merges on an empty test.full"
