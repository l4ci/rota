echo "worker gate: a PR no slot or review record owns is gated by number (#463)"
# A fake gh answers `pr view` and `issue view` from a small state file; the gate
# runs --check-only, which is also what a train member goes through, so nothing merges.

TMP_EX="$(mktemp -d)"
EXPROJ="$TMP_EX/proj"; EXBIN="$TMP_EX/bin"; mkdir -p "$EXPROJ/.rota" "$EXBIN"
cat > "$EXBIN/gh" <<'PYEOF'
#!/usr/bin/env python3
import json, os, sys
st = json.load(open(os.environ["EX_STATE"]))
a = sys.argv[1:]
if a[:2] == ["pr", "view"]:
    print(json.dumps({"headRefName": "ben/5-thing", "headRefOid": st["sha"], "baseRefName": "main",
                      "state": "OPEN", "mergeCommit": None, "body": st["body"]}))
elif a[:2] == ["issue", "view"]:
    print(json.dumps({"number": 5, "title": "t", "body": "", "state": "OPEN", "url": "",
                      "labels": [{"name": n} for n in st["labels"]], "assignees": [], "milestone": None}))
else:
    print("fake gh: unsupported: %s" % a, file=sys.stderr); sys.exit(2)
PYEOF
chmod +x "$EXBIN/gh"
EXORIGIN="$TMP_EX/origin.git"
git init -q --bare -b main "$EXORIGIN"
(
  cd "$EXPROJ" && git init -q -b main . && git config user.email t@t && git config user.name t \
    && echo seed > seed.txt && git add seed.txt && git commit -q -m seed \
    && git remote add origin "$EXORIGIN" && git push -q origin main \
    && git checkout -q -b ben/5-thing && echo w > w.txt && git add w.txt && git commit -q -m work \
    && git push -q origin ben/5-thing && git checkout -q main
) || fail "external-PR fixture repo setup failed"
EXSHA="$(git -C "$EXPROJ" rev-parse ben/5-thing)"
printf '{"test":{"full":["true"]}}\n' > "$EXPROJ/.rota/config.json"
printf '{"slots":[{"name":"ben","branch":"park/ben"}]}\n' > "$EXPROJ/.rota/workers.json"
ex_state() { printf '{"sha":"%s","body":"%s","labels":[%s]}\n' "$EXSHA" "$1" "$2" > "$TMP_EX/state.json"; }
ex() { ( cd "$EXPROJ" && EX_STATE="$TMP_EX/state.json" ROTA_GATE_SHA_WAIT=0 PATH="$EXBIN:$ROTA_POISON_BIN:$PATH" "$ROTA_BIN" --json "$@" 2>/dev/null ); }


ex_state "Closes #5" ""
RC=0; OUT=$(ex worker gate '#7' --base main --check-only) || RC=$?
[ "$RC" = "0" ] && [ "$(echo "$OUT" | jget data.verdict)" = "fresh" ] && [ "$(echo "$OUT" | jget data.branch)" = "ben/5-thing" ] \
  || fail "an unrecorded PR should be gated by number: rc=$RC $OUT"
pass "gate takes a PR no slot or review record owns"

ex_state "Adds the thing." ""
RC=0; OUT=$(ex worker gate 7 --base main --check-only) || RC=$?
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.blockedBy)" = "closes" ] \
  || fail "the closes check should read the issue from the branch name: rc=$RC $OUT"
pass "an unrecorded PR still has to close the issue its branch names"

ex_state "Closes #5" ""
RC=0; OUT=$(ex worker train '#7' --base main) || RC=$?
[ "$RC" != "0" ] && [ ! -f "$EXPROJ/w.txt" ] \
  || fail "train should keep refusing a PR nothing records: rc=$RC $OUT"
pass "train still refuses an unrecorded PR"

RC=0; OUT=$(ex worker gate nobody --base main --check-only) || RC=$?
[ "$RC" = "3" ] || fail "an unknown slot name should still exit 3: rc=$RC $OUT"
pass "an unknown slot name is still refused"
# an adopted slot (kind external) gates by its slot name with no relays logged;
# its Approvals line is "None", which passes
ex_state "Closes #5\\n\\n## Approvals\\nNone" ""
printf '{"slots":[{"name":"ext-1","kind":"external","branch":"ben/5-thing","base":"main","task":"5","pr":"https://github.com/o/r/pull/7"}]}\n' > "$EXPROJ/.rota/workers.json"
RC=0; OUT=$(ex worker gate ext-1 --base main --check-only) || RC=$?
[ "$RC" = "0" ] && [ "$(echo "$OUT" | jget data.verdict)" = "fresh" ] \
  || fail "an adopted slot should pass the provenance step with no relays: rc=$RC $OUT"
grep -q '"ext-1"' "$EXPROJ/.rota/workers.json" \
  || fail "a check-only gate must not release the adopted slot"
pass "worker gate: an adopted slot gates with an Approvals line of None and stays registered under --check-only"

# the fences and the adopt refusals (#617): a destructive verb on the adopted
# slot exits 4 blockedBy external, dispatch has no host to reach, and adopt
# refuses a bad call before it reads the backlog
RC=0; OUT=$(ex worker reset ext-1) || RC=$?
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.blockedBy)" = "external" ] \
  || fail "reset should refuse an adopted slot: rc=$RC $OUT"
RC=0; OUT=$(ex round reclaim ext-1) || RC=$?
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.blockedBy)" = "external" ] \
  || fail "reclaim should refuse an adopted slot: rc=$RC $OUT"
echo brief > "$TMP_EX/body.txt"
RC=0; OUT=$(ex worker dispatch ext-1 --task 5 --body-file "$TMP_EX/body.txt") || RC=$?
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.blockedBy)" = "host" ] \
  || fail "dispatch should find no host for an adopted slot: rc=$RC $OUT"
RC=0; OUT=$(ex worker adopt ben/5-thing) || RC=$?
[ "$RC" = "2" ] || fail "adopt without --issue should be a usage error: rc=$RC $OUT"
RC=0; OUT=$(ex worker adopt ben/5-thing other --issue 5) || RC=$?
[ "$RC" = "2" ] || fail "adopt with two refs should be a usage error: rc=$RC $OUT"
# --pr is read from the forge: the fake gh heads every PR by ben/5-thing, so
# adopting another branch under it is refused (exit 3) and registers nothing
git -C "$EXPROJ" branch ben/6-other main
RC=0; OUT=$(ex worker adopt ben/6-other --issue 6 --pr https://github.com/o/r/pull/7) || RC=$?
[ "$RC" = "3" ] || fail "adopt --pr headed by another branch should exit 3: rc=$RC $OUT"
grep -q '"ben/6-other"' "$EXPROJ/.rota/workers.json" \
  && fail "a refused adopt must not register the branch"
grep -q '"ext-1"' "$EXPROJ/.rota/workers.json" \
  || fail "a refused verb must leave the adopted slot registered"
pass "worker reset, round reclaim and worker dispatch refuse an adopted slot; worker adopt refuses a bad call and a PR headed by another branch"

rm -rf "$TMP_EX"
