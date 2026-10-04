echo "round autopilot: tick assigns ready items, escalates the rest, never merges without a gate (#93)"
# An issue-mode project driven by FAKES only (gh and tmux fakes, a local bare
# origin), as section 104. The gate cannot pass here (the fake forge knows no PR
# 7), which is the point: the autopilot must refuse to merge and say why.

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
printf '{"backlog":{"backend":"issues"},"issues":{"provider":"github","retryWaitSeconds":0},"work":{"dispatch":"tmux"},"round":{"brief":"%s"}}\n' "$TMP_AP/contract.md" > "$APROJ/.rota/config.json"
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
RC=0; OUT=$(ap round tick) || RC=$?
[ "$RC" = "4" ] || fail "tick must refuse while round.autopilot is off, got $RC: $OUT"
RC=0; OUT=$(ap round watch --autopilot --heartbeat 5) || RC=$?
[ "$RC" = "4" ] || fail "watch --autopilot must refuse while round.autopilot is off, got $RC: $OUT"
pass "round.autopilot is off by default: tick and watch --autopilot refuse"

# ── assign: ready items to idle slots, bounded by the cap, audited ─────────
ap config set round.autopilot true >/dev/null
ap config set round.autopilotCap 1 >/dev/null
RC=0; OUT=$(ap round tick) || RC=$?
[ "$RC" = "0" ] && [ "$(apdid <<<"$OUT")" = "assign:1" ] || fail "first tick should assign exactly one item (cap 1): rc=$RC $OUT"
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
apslot ben done https://github.com/o/r/pull/7
OUT=$(ap round tick)
[ "$(apneeds <<<"$OUT")" = "gate-failed:ben" ] || fail "a gate that cannot pass must be escalated: $OUT"
grep -q '"ben"' "$CDA/rota/round-autopilot.json" && grep -q '"held"' "$CDA/rota/round-autopilot.json" || fail "a failed gate should be held: $(cat "$CDA/rota/round-autopilot.json")"
OUT=$(ap round tick)
[ "$(apneeds <<<"$OUT")" = "gate-failed:ben" ] && [ "$(echo "$OUT" | jget 'data.new')" = "[]" ] || fail "a held failure is listed again but not re-run or re-reported: $OUT"
ap config set ship.mergeApproval all >/dev/null
OUT=$(ap round tick)
[ "$(apneeds <<<"$OUT")" = "merge:ben" ] || fail "ship.mergeApproval all must leave the merge to a person: $OUT"
ap config set ship.mergeApproval none >/dev/null
pass "a failed gate is escalated and held; ship.mergeApproval other than none stops the autopilot merging"

# ── a done slot with no PR is not merged ───────────────────────────────────
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
