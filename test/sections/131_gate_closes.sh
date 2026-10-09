echo "worker gate/train: a PR whose body does not close the slot's issue is refused with exit 4 (#466)"
# A fake gh answers `pr view` and `issue view` from a small state file; the gate
# runs --check-only, which is also what a train member goes through, so nothing merges.

TMP_CL="$(mktemp -d)"
CLPROJ="$TMP_CL/proj"; CLBIN="$TMP_CL/bin"; mkdir -p "$CLPROJ/.rota" "$CLBIN"
cat > "$CLBIN/gh" <<'PYEOF'
#!/usr/bin/env python3
import json, os, sys
st = json.load(open(os.environ["CL_STATE"]))
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
chmod +x "$CLBIN/gh"
CLORIGIN="$TMP_CL/origin.git"
git init -q --bare -b main "$CLORIGIN"
(
  cd "$CLPROJ" && git init -q -b main . && git config user.email t@t && git config user.name t \
    && echo seed > seed.txt && git add seed.txt && git commit -q -m seed \
    && git remote add origin "$CLORIGIN" && git push -q origin main \
    && git checkout -q -b ben/5-thing && echo w > w.txt && git add w.txt && git commit -q -m work \
    && git push -q origin ben/5-thing && git checkout -q main
) || fail "closes fixture repo setup failed"
CLSHA="$(git -C "$CLPROJ" rev-parse ben/5-thing)"
printf '{"ship":{"review":"none"},"test":{"full":["true"]}}\n' > "$CLPROJ/.rota/config.json"
printf '{"slots":[{"name":"ben","branch":"ben/5-thing","task":"#5","pr":"https://github.com/o/r/pull/7"}]}\n' > "$CLPROJ/.rota/workers.json"
cl_state() { printf '{"sha":"%s","body":"%s","labels":[%s]}\n' "$CLSHA" "$1" "$2" > "$TMP_CL/state.json"; }
cl() { ( cd "$CLPROJ" && CL_STATE="$TMP_CL/state.json" ROTA_GATE_SHA_WAIT=0 PATH="$CLBIN:$ROTA_POISON_BIN:$PATH" "$ROTA_BIN" --json "$@" 2>/dev/null ); }

cl_state "Adds the thing." ""
RC=0; OUT=$(cl worker gate ben --base main --check-only) || RC=$?
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.blockedBy)" = "closes" ] && [ "$(echo "$OUT" | jget data.changed)" = "false" ] \
  || fail "a body without a closing keyword should be refused with 4: rc=$RC $OUT"
case "$OUT" in *"Closes #5"*) ;; *) fail "the hint should show the line to add: $OUT" ;; esac
pass "gate refuses a body that does not close the slot's issue, with the line to add"

RC=0; OUT=$(cl worker train ben --base main) || RC=$?
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.blockedBy)" = "closes" ] && [ ! -f "$CLPROJ/w.txt" ] \
  || fail "train should refuse the same member with 4: rc=$RC $OUT"
pass "train refuses a member whose body does not close its issue"

cl_state "Fixes #5" ""
RC=0; OUT=$(cl worker gate ben --base main --check-only) || RC=$?
[ "$RC" = "0" ] || fail "Fixes #5 should pass the check: rc=$RC $OUT"
cl_state "Refs #5" '"partial-slice"'
RC=0; OUT=$(cl worker gate ben --base main --check-only) || RC=$?
[ "$RC" = "0" ] || fail "a partial-slice issue should pass without a closing keyword: rc=$RC $OUT"
pass "a closing keyword, or the partial-slice label, passes"
rm -rf "$TMP_CL"
