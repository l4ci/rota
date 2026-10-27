echo "round bundle: assign hands one slot a bundle; done and the gate check every member (#1592)"
# Same fakes-only issue-mode fixture as section 163: gh and tmux fakes, a local bare origin.

TMP_BN="$(mktemp -d)"
FKN="$TMP_BN/fake"
mkdir -p "$FKN/tmux"
BNPROJ="$TMP_BN/proj"
git init -q --bare "$TMP_BN/origin.git"
mkdir -p "$BNPROJ/.rota"
(
  cd "$BNPROJ" && git init -q -b main . && git config user.email t@t && git config user.name t \
    && echo seed > seed.txt && git add seed.txt && git commit -q -m seed \
    && git remote add origin "$TMP_BN/origin.git" && git push -q origin main
) || fail "bundle fixture repo setup failed"
printf 'stub worker contract\n' > "$TMP_BN/contract.md"
printf '{"issues":{"source":"github"},"round":{"dispatch":"tmux","brief":"%s","bundleMax":2},"test":{"fast":["echo fast"]}}\n' "$TMP_BN/contract.md" > "$BNPROJ/.rota/config.json"
printf 'Welcome to Claude Code\n' > "$FKN/tmux/pane"
: > "$FKN/tmux/log"

BNHOLD=$$
BNFAKES="$TESTDIR/fakes:$ROTA_POISON_BIN:$PATH"
bn() { ( cd "$BNPROJ" && PATH="$BNFAKES" ROTA_ROUND_HOLDER_PID="$BNHOLD" FAKE_TMUX="$FKN/tmux" FAKE_TRACKER_DB="$TMP_BN/db.json" ROTA_HOST_KILL_WAIT=1 "$ROTA_BIN" --json "$@" 2>/dev/null ); }

for t in Lead Alpha Beta Gamma; do
  printf '## Acceptance\n- [ ] %s works\n' "$t" > "$TMP_BN/body.md"
  bn item create --kind tasks --title "$t" --body-file "$TMP_BN/body.md" >/dev/null || fail "could not create issue $t"
done
bn round start --holder-pid "$BNHOLD" --scope slate --items 1,2,3,4 --slots 2 >/dev/null || fail "round start failed"

# ── a bundle over round.bundleMax is refused ────────────────────────────────
RC=0; OUT=$(bn round assign 1 --bundle 2,3,4) || RC=$?
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.blockedBy)" = "bundle" ] && [ "$(echo "$OUT" | jget data.changed)" = "false" ] \
  || fail "3 members past bundleMax 2 must exit 4 blockedBy bundle: rc=$RC $OUT"
grep -q "round.bundleMax" <<<"$OUT" || fail "the refusal names round.bundleMax: $OUT"
pass "round assign refuses a bundle over round.bundleMax"

# ── assign hands the slot lead and members ──────────────────────────────────
RC=0; OUT=$(bn round assign 1 --bundle 2,3) || RC=$?
[ "$RC" = "0" ] || fail "assign with a bundle failed: rc=$RC $OUT"
SLOTB=$(echo "$OUT" | jget data.agent)
echo "$OUT" > "$TMP_BN/assign.json"
python3 - "$BNPROJ/.rota/workers.json" "$SLOTB" <<'PYEOF' || fail "workers.json does not hold the bundle"
import json, sys
slot = [s for s in json.load(open(sys.argv[1]))["slots"] if s["name"] == sys.argv[2]][0]
t = str(slot["task"]).lstrip("#")
assert t == "1", slot
assert [str(x).lstrip("#") for x in slot["bundle"]] == ["2", "3"], slot
PYEOF
pass "the slot records the lead as task and the members as bundle"

for n in 2 3; do
  OUT=$(bn item show "$n") || vfail
  grep -q 'in-progress\|in progress' <<<"$OUT" || fail "member $n should be in progress: $OUT"
done
pass "every member is marked in progress"

# The dispatch payload is the brief the worker reads: it names every issue's acceptance.
for want in "Lead works" "Alpha works" "Beta works"; do
  grep -q "$want" "$FKN/tmux/payload" || fail "the brief should quote the acceptance '$want'"
done
grep -q "bundle of #1, #2, #3" "$FKN/tmux/payload" || fail "the brief should name the bundle"
pass "the brief lists every issue's acceptance"

# ── worker done wants a PASS row for the lead and each member ───────────────
BNWT="$BNPROJ/.worktrees/$SLOTB"
( cd "$BNWT" && echo w > w.txt && git add w.txt && git commit -q -m work && git push -q origin "ben/1-lead" ) || fail "could not push the bundle branch"
BNHEAD=$(git -C "$BNWT" rev-parse --short HEAD)
# proof add pins the sha: the slot branch HEAD, whichever checkout the verb runs in.
bnp() { bn proof add "$1" --check "echo fast" --result PASS --evidence "smoke" --sha "$BNHEAD"; }

bnp 1 >/dev/null || fail "proof record for the lead failed"
RC=0; OUT=$(bn worker done "$SLOTB") || RC=$?
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.changed)" = "false" ] || fail "done with members lacking proof must exit 4: rc=$RC $OUT"
grep -q "2 has no PASS proof row" <<<"$OUT" || fail "the refusal should name member 2: $OUT"
grep -q '"state": *"done"' "$BNPROJ/.rota/workers.json" && fail "a refused done must leave the slot busy"
pass "worker done refuses while a member lacks its PASS row"

bnp 2 >/dev/null || fail "proof record for member 2 failed"
RC=0; OUT=$(bn worker done "$SLOTB") || RC=$?
[ "$RC" = "4" ] && grep -q "3 has no PASS proof row" <<<"$OUT" || fail "one member still lacks proof, done must exit 4 naming it: rc=$RC $OUT"
bnp 3 >/dev/null || fail "proof record for member 3 failed"
RC=0; OUT=$(bn worker done "$SLOTB") || RC=$?
[ "$RC" = "4" ] && grep -q "unmet acceptance" <<<"$OUT" || fail "proof alone must not mark acceptance: rc=$RC $OUT"
for n in 1 2 3; do
  bn plan pass "#$n" AC-1 --proof "$BNHEAD:echo fast" >/dev/null || fail "could not mark acceptance for $n"
done
RC=0; OUT=$(bn worker done "$SLOTB") || RC=$?
[ "$RC" = "0" ] && [ "$(echo "$OUT" | jget data.changed)" = "true" ] || fail "every issue proven and marked: done should pass: rc=$RC $OUT"
grep -q '"state": *"done"' "$BNPROJ/.rota/workers.json" || fail "the slot should be recorded done"
pass "worker done passes once the lead and every member have proof and acceptance marks with a local origin"
rm -rf "$TMP_BN"

# ── the gate wants the PR to close every issue of the bundle ────────────────
# Same fake gh as section 131, run with --check-only so nothing merges. The slot holds
# #5 with members #6 and #7; the fake answers pr view and issue view for any number.
TMP_BG="$(mktemp -d)"
BGPROJ="$TMP_BG/proj"; BGBIN="$TMP_BG/bin"; mkdir -p "$BGPROJ/.rota" "$BGBIN"
cat > "$BGBIN/gh" <<'PYEOF'
#!/usr/bin/env python3
import json, os, sys
st = json.load(open(os.environ["BG_STATE"]))
a = sys.argv[1:]
if a[:2] == ["pr", "view"]:
    print(json.dumps({"headRefName": "ben/5-thing", "headRefOid": st["sha"], "baseRefName": "main",
                      "state": "OPEN", "mergeCommit": None, "body": st["body"] + "\n## Assumptions\nNone\n\n## Rulings\nNone\n"}))
elif a[:2] == ["issue", "view"]:
    print(json.dumps({"number": int(a[2]), "title": "t", "body": "", "state": "OPEN", "url": "",
                      "labels": [], "assignees": [], "milestone": None}))
else:
    print("fake gh: unsupported: %s" % a, file=sys.stderr); sys.exit(2)
PYEOF
chmod +x "$BGBIN/gh"
git init -q --bare -b main "$TMP_BG/origin.git"
(
  cd "$BGPROJ" && git init -q -b main . && git config user.email t@t && git config user.name t \
    && echo seed > seed.txt && git add seed.txt && git commit -q -m seed \
    && git remote add origin "$TMP_BG/origin.git" && git push -q origin main \
    && git checkout -q -b ben/5-thing && echo w > w.txt && git add w.txt && git commit -q -m work \
    && git push -q origin ben/5-thing && git checkout -q main
) || fail "bundle gate fixture repo setup failed"
BGSHA="$(git -C "$BGPROJ" rev-parse ben/5-thing)"
printf '{"issues":{"source":"github"},"ship":{"review":"none"},"test":{"full":["true"]}}\n' > "$BGPROJ/.rota/config.json"
printf '{"slots":[{"name":"ben","branch":"ben/5-thing","task":"#5","bundle":["6","7"],"pr":"https://github.com/o/r/pull/7"}]}\n' > "$BGPROJ/.rota/workers.json"
bg_state() { printf '{"sha":"%s","body":"%s"}\n' "$BGSHA" "$1" > "$TMP_BG/state.json"; }
bg() { ( cd "$BGPROJ" && BG_STATE="$TMP_BG/state.json" ROTA_GATE_SHA_WAIT=0 PATH="$BGBIN:$ROTA_POISON_BIN:$PATH" "$ROTA_BIN" --json "$@" 2>/dev/null ); }

bg_state "Closes #5"
RC=0; OUT=$(bg worker gate ben --base main --check-only) || RC=$?
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.blockedBy)" = "closes" ] && [ "$(echo "$OUT" | jget data.changed)" = "false" ] \
  || fail "a body closing only the lead should be refused with 4 blockedBy closes: rc=$RC $OUT"
grep -q '#6' <<<"$OUT" && grep -q '#7' <<<"$OUT" || fail "the refusal should name the members the body leaves open: $OUT"
pass "gate refuses a PR that closes only the lead of a bundle"

bg_state "Closes #5\\nCloses #6\\nCloses #7"
RC=0; OUT=$(bg worker gate ben --base main --check-only) || RC=$?
[ "$RC" = "0" ] || fail "a body closing the lead and every member should pass the check: rc=$RC $OUT"
pass "gate passes a PR that closes the lead and every member"
rm -rf "$TMP_BG"
