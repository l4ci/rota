echo "worker gate: merges a clean stale branch itself, sends a conflict back uncounted (#31, #2072)"
# An issue-mode project driven by FAKES only (gh and tmux fakes, a local bare
# origin), as section 87. The slot has no PR, so the gate merges locally; the
# forge is never touched: a stale refusal parks nothing.

TMP_GS="$(mktemp -d)"
FKG="$TMP_GS/fake"
mkdir -p "$FKG/tmux"
GPROJ="$TMP_GS/proj"
git init -q --bare "$TMP_GS/origin.git"
mkdir -p "$GPROJ/.rota"
(
  cd "$GPROJ" && git init -q -b main . && git config user.email t@t && git config user.name t \
    && echo seed > seed.txt && git add seed.txt && git commit -q -m seed \
    && git remote add origin "$TMP_GS/origin.git" && git push -q origin main
) || fail "gate stale-merge fixture repo setup failed"
printf 'stub worker contract\n' > "$TMP_GS/contract.md"
printf '{"ship":{"review":"none"},"forge":{"provider":"github"},"issues":{"source":"github"},"round":{"dispatch":"tmux"},"test":{"full":["true"]},"round":{"brief":"%s"}}\n' "$TMP_GS/contract.md" > "$GPROJ/.rota/config.json"
printf 'Welcome to Claude Code\n' > "$FKG/tmux/pane"
: > "$FKG/tmux/log"

GHOLD=$$
GFAKES="$TESTDIR/fakes:$ROTA_POISON_BIN:$PATH"
gs() { ( cd "$GPROJ" && PATH="$GFAKES" ROTA_ROUND_HOLDER_PID="$GHOLD" FAKE_TMUX="$FKG/tmux" FAKE_TRACKER_DB="$TMP_GS/db.json" ROTA_HOST_KILL_WAIT=1 "$ROTA_BIN" --json "$@" 2>/dev/null ); }
gslabels() { python3 -c '
import json, sys
issue = next(i for i in json.load(open(sys.argv[1]))["issues"] if i["number"] == 1)
print(",".join(sorted(issue["labels"])))' "$TMP_GS/db.json"; }
gsslot() { python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); s=[x for x in d["slots"] if x["name"]=="ben"][0]; v=s.get(sys.argv[2]); print("" if v is None else v)' "$GPROJ/.rota/workers.json" "$1"; }
# gsmain <file>: land a commit on main in the gate checkout.
gsmain() { ( cd "$GPROJ" && echo main > "$1" && git add "$1" && git -c user.email=t@t -c user.name=t commit -q -m "main adds $1" ); }
# gswork <file>: a commit on the slot's branch.
gswork() { ( cd "$GPROJ/.worktrees/ben" && echo work > "$1" && git add "$1" && git -c user.email=t@t -c user.name=t commit -q -m "work adds $1" ); }

printf '## Acceptance\n- [ ] works\n' > "$TMP_GS/body.md"
gs item create --kind tasks --title "Gate me" --body-file "$TMP_GS/body.md" >/dev/null || fail "could not create the issue"
OUT=$(gs round start --holder-pid "$GHOLD" --scope slate --items 1 --slots 1) || fail "round start failed: $OUT"
OUT=$(gs round assign 1 --holder-pid "$GHOLD") || fail "assign failed: $OUT"

# A branch behind main whose files main did not touch merges as is.
gswork own.txt
gsmain other.txt
RC=0; OUT=$(gs worker gate ben --base main) || RC=$?
[ "$RC" = "0" ] && [ "$(echo "$OUT" | jget data.verdict)" = "pass" ] || fail "a clean stale branch should merge: rc=$RC $OUT"
[ -f "$GPROJ/own.txt" ] && [ -f "$GPROJ/other.txt" ] || fail "main should hold both the slot's and main's own work"
pass "a stale branch with a clean merge and no shared file is merged by the gate"

# A conflict goes back to the worker uncounted (#2072): it is bookkeeping the
# worker repairs, so it never counts toward round.maxBounces (default 3) and
# never parks the issue, however often the gate sees it.
LABELS0=$(gslabels)
gswork clash.txt
gsmain clash.txt
for N in 1 2 3 4; do
  [ "$N" != "1" ] && gswork "again$N.txt"   # a new head each time
  RC=0; OUT=$(gs worker gate ben --base main) || RC=$?
  [ "$RC" = "1" ] && [ "$(echo "$OUT" | jget data.verdict)" = "stale" ] \
    || fail "refusal $N should be a stale refusal: rc=$RC $OUT"
  [ -z "$(echo "$OUT" | jget data.bounces 2>/dev/null || true)" ] && [ -z "$(echo "$OUT" | jget data.parked 2>/dev/null || true)" ] \
    || fail "a stale merge is no bounce, refusal $N: $OUT"
done
[ "$(gsslot task)" = "1" ] || fail "the slot still holds the issue past the old cap"
[ "$(gslabels)" = "$LABELS0" ] || fail "a stale merge must not park the issue: $(gslabels)"
pass "a conflicting branch is refused stale without a bounce: nothing is counted and the issue is never parked"

rm -rf "${TMP_GS:?}"
