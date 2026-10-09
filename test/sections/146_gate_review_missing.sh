echo "worker gate: review-missing refusal, labelled issue read, stale verdict (#642)"
# Same fake-gh fixture as section 131; the gate runs --check-only. The policy has a
# labels map, so the gate reads the slot's issue for its labels. `|| RC=$?`
# captures the non-zero exits.

TMP_RM="$(mktemp -d)"
RMPROJ="$TMP_RM/proj"; RMBIN="$TMP_RM/bin"; mkdir -p "$RMPROJ/.rota" "$RMBIN"
cat > "$RMBIN/gh" <<'PYEOF'
#!/usr/bin/env python3
import json, os, sys
st = json.load(open(os.environ["RM_STATE"]))
a = sys.argv[1:]
if a[:2] == ["pr", "view"]:
    print(json.dumps({"headRefName": "ben/5-thing", "headRefOid": st["sha"], "baseRefName": "main",
                      "state": "OPEN", "mergeCommit": None, "body": "Closes #5"}))
elif a[:2] == ["issue", "view"]:
    open(os.environ["RM_LOG"], "a").write("issue view\n")
    print(json.dumps({"number": 5, "title": "t", "body": "", "state": "OPEN", "url": "",
                      "labels": [{"name": n} for n in st["labels"]], "assignees": [], "milestone": None}))
else:
    print("fake gh: unsupported: %s" % a, file=sys.stderr); sys.exit(2)
PYEOF
chmod +x "$RMBIN/gh"
RMORIGIN="$TMP_RM/origin.git"
git init -q --bare -b main "$RMORIGIN"
(
  cd "$RMPROJ" && git init -q -b main . && git config user.email t@t && git config user.name t \
    && echo seed > seed.txt && git add seed.txt && git commit -q -m seed \
    && git remote add origin "$RMORIGIN" && git push -q origin main \
    && git checkout -q -b ben/5-thing && echo w > w.txt && git add w.txt && git commit -q -m work \
    && git push -q origin ben/5-thing && git checkout -q main
) || fail "review-missing fixture repo setup failed"
RMSHA="$(git -C "$RMPROJ" rev-parse ben/5-thing)"
printf '{"ship":{"review":{"default":"light","labels":{"risk:high":"full"}}},"test":{"full":["true"]}}\n' > "$RMPROJ/.rota/config.json"
printf '{"slots":[{"name":"ben","branch":"ben/5-thing","task":"#5","pr":"https://github.com/o/r/pull/7"}]}\n' > "$RMPROJ/.rota/workers.json"
rm_state() { printf '{"sha":"%s","labels":[%s]}\n' "$RMSHA" "$1" > "$TMP_RM/state.json"; }
rm_gate() { ( cd "$RMPROJ" && RM_STATE="$TMP_RM/state.json" RM_LOG="$TMP_RM/log" ROTA_GATE_SHA_WAIT=0 PATH="$RMBIN:$ROTA_POISON_BIN:$PATH" "$ROTA_BIN" --json worker gate ben --base main --check-only 2>/dev/null ); }
rm_verdict() { ( cd "$RMPROJ" && "$ROTA_BIN" verdict add ben/5-thing --kind "$1" --verdict PASS >/dev/null ); }

rm_state ""
: > "$TMP_RM/log"
RC=0; OUT=$(rm_gate) || RC=$?
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.blockedBy)" = "review-missing" ] && [ "$(echo "$OUT" | jget data.changed)" = "false" ] \
  || fail "a light branch with no verdict should be refused with 4: rc=$RC $OUT"
[ -s "$TMP_RM/log" ] || fail "the gate should read the issue for its labels when the policy has a labels map"
pass "gate refuses a branch with no recorded review (exit 4, review-missing) after reading the issue's labels"

rm_state '"risk:high"'
RC=0; OUT=$(rm_gate) || RC=$?
case "$OUT" in *"review-spec and review-quality"*) ;; *) fail "risk:high label should force full and name both verdicts: rc=$RC $OUT" ;; esac
pass "a risk:high label read from the issue raises the required depth to full"

rm_state ""
rm_verdict review-quality || fail "could not record the head verdict"
RC=0; OUT=$(rm_gate) || RC=$?
[ "$RC" = "0" ] || fail "a verdict at head should pass: rc=$RC $OUT"
pass "a verdict recorded at the branch head passes"

# rework lands after the verdict: the record is now on an older commit
( cd "$RMPROJ" && git checkout -q ben/5-thing && echo more >> w.txt && git commit -q -am rework \
    && git push -q origin ben/5-thing && git checkout -q main ) || fail "could not push the rework commit"
RMSHA="$(git -C "$RMPROJ" rev-parse ben/5-thing)"
rm_state ""
RC=0; OUT=$(rm_gate) || RC=$?
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.blockedBy)" = "review-missing" ] \
  || fail "a verdict on an older commit should not satisfy the gate: rc=$RC $OUT"
pass "a verdict recorded before a later push is refused as review-missing"
rm -rf "$TMP_RM"
