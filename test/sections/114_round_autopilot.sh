echo "round autopilot: tick assigns ready items, escalates the rest, never merges without a gate (#93)"
# An issue-mode project driven by FAKES only (gh and tmux fakes, a local bare
# origin), as section 104. The gate has a pushed, reviewed PR but fails its
# verification command: the autopilot must escalate and remember that head.

TMP_AP="$(mktemp -d)"
FKA="$TMP_AP/fake"
mkdir -p "$FKA/tmux"
APROJ="$TMP_AP/proj"
git init -q --bare "$TMP_AP/origin.git"
mkdir -p "$APROJ/.rota"
(
  cd "$APROJ" && git init -q -b main . && git config user.email t@t && git config user.name t \
    && echo seed > seed.txt && git add seed.txt && git commit -q -m seed \
    && git remote add origin "$TMP_AP/origin.git" && git push -q origin main
) || fail "autopilot fixture repo setup failed"
printf 'stub worker contract\n' > "$TMP_AP/contract.md"
printf '{"issues":{"source":"github"},"round":{"dispatch":"tmux","brief":"%s"}}\n' "$TMP_AP/contract.md" > "$APROJ/.rota/config.json"
printf 'Welcome to Claude Code\n' > "$FKA/tmux/pane"
: > "$FKA/tmux/log"

AHOLD=$$
AFAKES="$TESTDIR/fakes:$ROTA_POISON_BIN:$PATH"
CDA="$(git -C "$APROJ" rev-parse --path-format=absolute --git-common-dir)"
ap() { ( cd "$APROJ" && PATH="$AFAKES" ROTA_ROUND_HOLDER_PID="$AHOLD" FAKE_TMUX="$FKA/tmux" FAKE_TRACKER_DB="$TMP_AP/db.json" ROTA_HOST_KILL_WAIT=1 "$ROTA_BIN" --json "$@" 2>/dev/null ); }
# apslot <name> <state> [pr]: put a slot in a state the way a worker's report would.
apslot() { python3 - "$APROJ/.rota/workers.json" "$1" "$2" "${3:-}" <<'PY'
import json, sys
p, name, state, pr = sys.argv[1:5]
d = json.load(open(p))
for s in d["slots"]:
    if s["name"] == name:
        s["state"] = state
        s["pr"] = pr or None
        s["handle"] = None  # no live tab: the dead-tab repair must not rewrite the state
json.dump(d, open(p, "w"))
PY
}
# apdid lists what a tick did except the reconcile repairs: the fake host has no
# live agents, so a slot that was just assigned reads as a dead tab.
apdid() { python3 -c 'import json,sys; d=json.load(sys.stdin)["data"]; print(",".join(a["action"]+":"+a["target"] for a in d["did"] if a["action"] != "reconcile"))'; }
apneeds() { python3 -c 'import json,sys; d=json.load(sys.stdin)["data"]; print(",".join(i["kind"]+":"+i["target"] for i in d["needsYou"]))'; }

printf '## Acceptance\n- [ ] works\n' > "$TMP_AP/body.md"
ap item create --kind tasks --title "One" --body-file "$TMP_AP/body.md" >/dev/null || fail "could not create issue 1"
ap item create --kind tasks --title "Two" --body-file "$TMP_AP/body.md" >/dev/null || fail "could not create issue 2"
ap round start --holder-pid "$AHOLD" --scope slate --items 1,2 --slots 2 >/dev/null || fail "round start failed"

# ── off by default ──────────────────────────────────────────────────────────
for level in off roundtrip; do
  ap config set autonomy.level "$level" >/dev/null
  RC=0; OUT=$(ap round tick) || RC=$?
  [ "$RC" = "4" ] || fail "tick must refuse under autonomy.level $level, got $RC: $OUT"
  RC=0; OUT=$(ap round watch --autopilot --heartbeat 5) || RC=$?
  [ "$RC" = "4" ] || fail "watch --autopilot must refuse under autonomy.level $level, got $RC: $OUT"
done
pass "the autopilot runs only under autonomy.level auto: tick and watch --autopilot refuse under off and roundtrip"

# ── assign: ready items to idle slots, bounded by the cap, audited ─────────
ap config set autonomy.level auto >/dev/null
ap config set round.autopilotCap 1 >/dev/null
RC=0; OUT=$(ROTA_TEST_NOW=2026-10-03T12:00:00Z ap round tick) || RC=$?
[ "$RC" = "0" ] && [ "$(apdid <<<"$OUT")" = "assign:1" ] || fail "first tick should assign exactly one item (cap 1): rc=$RC $OUT"
FIRST_ASSIGN=$(grep -m1 '"verb": "round tick assign"' "$APROJ/.rota/gate-audit.jsonl")
grep -q '"ts": "2026-10-03T12:00:00Z"' <<<"$FIRST_ASSIGN" \
  || fail "ROTA_TEST_NOW should pin the autopilot audit line: $(cat "$APROJ/.rota/gate-audit.jsonl")"
OUT=$(ap round tick)
[ "$(apdid <<<"$OUT")" = "assign:2" ] || fail "second tick should assign the next item: $OUT"
OUT=$(ap round tick)
[ -z "$(apdid <<<"$OUT")" ] || fail "with no idle slot a tick assigns nothing: $OUT"
[ "$(grep -c '"verb": "round tick assign"' "$APROJ/.rota/gate-audit.jsonl")" = "2" ] || fail "one audit line per assign: $(cat "$APROJ/.rota/gate-audit.jsonl")"
grep -q '"gate": "autopilot"' "$APROJ/.rota/gate-audit.jsonl" && grep -q '"verb": "round tick reconcile"' "$APROJ/.rota/gate-audit.jsonl" || fail "autopilot lines should carry the gate and cover the repairs too"
pass "tick assigns one ready item per tick up to the cap, audits each, and idles when no slot is free"

# ── attention states are escalated, never answered ─────────────────────────
apslot dana working
apslot ben blocked
OUT=$(ap round tick)
[ "$(apneeds <<<"$OUT")" = "blocked:ben" ] && [ -z "$(apdid <<<"$OUT")" ] || fail "a blocked slot is escalated and nothing is done: $OUT"
[ "$(echo "$OUT" | jget 'data.new[0].target')" = "ben" ] || fail "the first tick reports it as new: $OUT"
OUT=$(ap round tick)
[ "$(apneeds <<<"$OUT")" = "blocked:ben" ] && [ "$(echo "$OUT" | jget 'data.new')" = "[]" ] || fail "the same blocked slot is listed but not new on the next tick: $OUT"
pass "a blocked slot lands in needsYou once as new, and the autopilot does nothing about it"

# ── a failed gate is escalated and held; a policy asking a human merges nothing
ap config set forge.provider github >/dev/null || fail "could not set the fixture forge"
ap config set ship.mergeApproval none >/dev/null || fail "could not allow the fixture gate"
ap config set test.full '["false"]' >/dev/null || fail "could not set the failing verification command"
AP_BRANCH=ben/1-one
git -C "$APROJ" push -q origin "$AP_BRANCH" || fail "could not push the reviewed fixture head"
AP_HEAD=$(git -C "$APROJ" rev-parse "$AP_BRANCH") || fail "could not read the fixture head"
python3 - "$TMP_AP/db.json" "$AP_BRANCH" "$AP_HEAD" <<'PY'
import json, sys
p, branch, head = sys.argv[1:]
d = json.load(open(p))
d["prs"].append({"number": 7, "title": "One", "body": "Closes #1\n\n## Approvals\n\nNone\n\n## Rulings\n\nNone\n\n## Assumptions\n\nNone\n",
                 "head": branch, "base": "main", "sha": head, "state": "open"})
d["next_issue"] = max(d.get("next_issue", 1), 8)
json.dump(d, open(p, "w"))
PY
for kind in review-spec review-quality; do
  ap verdict add "$AP_BRANCH" --kind "$kind" --verdict PASS --sha "$AP_HEAD" >/dev/null \
    || fail "could not record the fixture's $kind verdict at head"
done
AP_PROOF=$(ap proof record 1 -- true) || fail "could not record the fixture's acceptance proof"
AP_PROOF_SHA=$(echo "$AP_PROOF" | jget data.sha)
AP_PROOF_CHECK=$(echo "$AP_PROOF" | jget data.check)
AP_PASS=$(ap plan pass '#1' AC-1 --proof "$AP_PROOF_SHA:$AP_PROOF_CHECK") \
  || fail "could not mark the fixture's acceptance criterion: $AP_PASS"
apslot ben done https://github.com/o/r/pull/7
OUT=$(ap round tick)
[ "$(apneeds <<<"$OUT")" = "gate-failed:ben" ] || fail "a gate that cannot pass must be escalated: $OUT"
[[ "$(echo "$OUT" | jget 'data.needsYou[0].why')" == verify-failed:* ]] || fail "the reviewed PR must reach the deliberately failing verifier: $OUT"
python3 - "$CDA/rota/round-autopilot.json" "$AP_HEAD" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
held = d.get("failedHeads", {}).get("ben", {})
assert held.get("head") == "7:" + sys.argv[2], d
assert held.get("why", "").startswith("verify-failed:"), d
PY
OUT=$(ap round tick)
[ "$(apneeds <<<"$OUT")" = "gate-failed:ben" ] && [ "$(echo "$OUT" | jget 'data.new')" = "[]" ] || fail "a held failure is listed again but not re-run or re-reported: $OUT"
ap config set ship.mergeApproval all >/dev/null
OUT=$(ap round tick)
[ "$(apneeds <<<"$OUT")" = "merge:ben" ] || fail "ship.mergeApproval all must leave the merge to a person: $OUT"
ap config set ship.mergeApproval none >/dev/null
pass "a failed gate is escalated and held; ship.mergeApproval other than none stops the autopilot merging"

# ── a done slot with no PR is not merged ───────────────────────────────────
# Remove the preceding case's PR so reconcile cannot adopt it onto this slot.
python3 - "$TMP_AP/db.json" <<'PY'
import json, sys
p = sys.argv[1]
d = json.load(open(p))
d["prs"] = [pr for pr in d["prs"] if pr["number"] != 7]
json.dump(d, open(p, "w"))
PY
apslot ben done
OUT=$(ap round tick)
[ -z "$(apneeds <<<"$OUT")" ] && [ -z "$(apdid <<<"$OUT")" ] || fail "a done slot with no PR is not the autopilot's to merge: $OUT"
pass "a done slot without a recorded PR is left alone"

# ── the watch runs the tick and wakes only for something new ───────────────
apslot ben blocked
OUT=$(ap round watch --autopilot --heartbeat 5 --poll 1 --settle 1 --forge-poll 0) || fail "watch --autopilot failed: $OUT"
[ "$(echo "$OUT" | jget data.reason)" = "heartbeat" ] || [ "$(echo "$OUT" | jget data.reason)" = "change" ] || fail "unexpected watch reason: $OUT"
[ "$(echo "$OUT" | jget 'data.autopilot.needsYou[0].target')" = "ben" ] || fail "the watch result should carry the tick: $OUT"
pass "round watch --autopilot returns the tick's needsYou"

# ── wind-down and a lost lease stop it cleanly ─────────────────────────────
RC=0; OUT=$(cd "$APROJ" && PATH="$AFAKES" ROTA_ROUND_HOLDER_PID=1 "$ROTA_BIN" --json round tick 2>/dev/null) || RC=$?
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.stopped)" = "true" ] || fail "a process without the lease must stop: rc=$RC $OUT"
OUT=$(cd "$APROJ" && PATH="$AFAKES" ROTA_ROUND_HOLDER_PID=1 "$ROTA_BIN" --json round watch --autopilot --heartbeat 5 2>/dev/null) || fail "watch should stop with exit 0 when the lease is not held: $OUT"
[ "$(echo "$OUT" | jget data.reason)" = "stopped" ] || fail "watch --autopilot must end with reason stopped: $OUT"
python3 - "$CDA/rota/round-autopilot.json" <<'PY'
import json, sys
p = sys.argv[1]
d = json.load(open(p)); d["stopped"] = True
json.dump(d, open(p, "w"))
PY
RC=0; OUT=$(ap round tick) || RC=$?
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.stopped)" = "true" ] || fail "a round winding down (stopped marker) must stop the tick: rc=$RC $OUT"
pass "a lost lease or a wind-down marker stops tick and watch --autopilot without an error"

rm -rf "${TMP_AP:?}"
