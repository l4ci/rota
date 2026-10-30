echo "worker gate: a docs-only branch verifies with test.docs, code with test.full (#2348)"
# A local-forge project (no PR, a local merge): test.full is a command that
# fails, so only a gate that ran test.docs instead can pass. `|| RC=$?`
# captures the non-zero exits.

TMP_GT="$(mktemp -d)"
GTPROJ="$TMP_GT/proj"; mkdir -p "$GTPROJ/.rota"
(
  cd "$GTPROJ" && git init -q -b main . && git config user.email t@t && git config user.name t \
    && echo seed > seed.txt && git add seed.txt && git commit -q -m seed \
    && git checkout -q -b ben/5-docs && echo moved > README.md && git add README.md && git commit -q -m docs \
    && git checkout -q -b ben/6-code main && echo 'package x' > x.go && git add x.go && git commit -q -m code \
    && git checkout -q main
) || fail "gate-tier fixture repo setup failed"
printf '{"issues":{"source":"file"},"forge":{"provider":"local"},"test":{"full":["exit 1"],"docs":["true"]}}\n' > "$GTPROJ/.rota/config.json"
gt_slot() { printf '{"slots":[{"name":"ben","branch":"%s"}]}\n' "$1" > "$GTPROJ/.rota/workers.json"; }
gt_gate() { ( cd "$GTPROJ" && PATH="$ROTA_POISON_BIN:$PATH" "$ROTA_BIN" --json worker gate ben --base main 2>/dev/null ); }

RC=0; OUT=$( cd "$GTPROJ" && "$ROTA_BIN" --json change classify ben/5-docs ) || RC=$?
[ "$RC" = "0" ] && [ "$(echo "$OUT" | jget data.tier)" = "docs" ] || fail "change classify should call a README-only branch docs: rc=$RC $OUT"
pass "rota change classify puts a README-only branch in the docs tier"

gt_slot ben/5-docs
RC=0; OUT=$(gt_gate) || RC=$?
[ "$RC" = "0" ] && [ "$(echo "$OUT" | jget data.verdict)" = "pass" ] && [ "$(echo "$OUT" | jget data.tier)" = "docs" ] \
  && [ "$(echo "$OUT" | jget 'data.skipped[0]')" = "test.full" ] \
  || fail "a docs-only branch should pass on test.docs with test.full red: rc=$RC $OUT"
[ -f "$GTPROJ/README.md" ] || fail "the docs-only branch did not land"
pass "the gate merges a docs-only branch on test.docs, with no review under the default ship.review, and reports test.full skipped"

gt_slot ben/6-code
# a code branch needs the full review; turn review off so test.full decides
printf '{"issues":{"source":"file"},"forge":{"provider":"local"},"ship":{"review":"none"},"test":{"full":["exit 1"],"docs":["true"]}}\n' > "$GTPROJ/.rota/config.json"
RC=0; OUT=$(gt_gate) || RC=$?
[ "$RC" != "0" ] && [ "$(echo "$OUT" | jget data.verdict)" = "verify-failed" ] && [ "$(echo "$OUT" | jget data.tier)" = "full" ] \
  || fail "a code branch should run the red test.full: rc=$RC $OUT"
[ ! -f "$GTPROJ/x.go" ] || fail "the code branch landed despite a red test.full"
pass "the gate verifies a code branch with test.full"
rm -rf "$TMP_GT"
