echo "worker gate: merges a clean stale branch itself, counts bounces, parks at the cap (#31)"
# An issue-mode project driven by FAKES only (gh and tmux fakes, a local bare
# origin), as section 87. The slot has no PR, so the gate merges locally; the
# forge is only touched when the issue is parked.

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
printf '{"ship":{"review":"none"},"backlog":{"backend":"issues"},"issues":{"provider":"github","retryWaitSeconds":0},"work":{"dispatch":"tmux"},"test":{"full":["true"]},"round":{"brief":"%s"}}\n' "$TMP_GS/contract.md" > "$GPROJ/.rota/config.json"
printf 'Welcome to Claude Code\n' > "$FKG/tmux/pane"
: > "$FKG/tmux/log"

GHOLD=$$
GFAKES="$TESTDIR/fakes:$ROTA_POISON_BIN:$PATH"
gs() { ( cd "$GPROJ" && PATH="$GFAKES" ROTA_ROUND_HOLDER_PID="$GHOLD" FAKE_TMUX="$FKG/tmux" FAKE_TRACKER_DB="$TMP_GS/db.json" ROTA_HOST_KILL_WAIT=1 "$ROTA_BIN" --json "$@" 2>/dev/null ); }
gslabels() { python3 -c '
import json, sys
issue = next(i for i in json.load(open(sys.argv[1]))["issues"] if i["number"] == 1)
print(",".join(sorted(issue["labels"])))' "$TMP_GS/db.json"; }
gscomments() { python3 -c '
import json, sys
issue = next(i for i in json.load(open(sys.argv[1]))["issues"] if i["number"] == 1)
print("\n=====\n".join(c["body"] for c in issue["comments"]))' "$TMP_GS/db.json"; }
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

# A conflict goes back to the worker; each bounce is counted and, at the cap
# (round.maxBounces, default 3), the issue is parked needs-human.
gswork clash.txt
gsmain clash.txt
for N in 1 2; do
  [ "$N" = "2" ] && gswork "again$N.txt"   # a new head between bounces: it counts
  RC=0; OUT=$(gs worker gate ben --base main) || RC=$?
  [ "$RC" = "1" ] && [ "$(echo "$OUT" | jget data.verdict)" = "stale" ] && [ "$(echo "$OUT" | jget data.bounces)" = "$N" ] \
    || fail "bounce $N should be a counted stale refusal: rc=$RC $OUT"
  [ "$(echo "$OUT" | jget data.parked)" = "false" ] || fail "bounce $N is under the cap: $OUT"
done
# Re-gating an unchanged head is not a new bounce.
RC=0; OUT=$(gs worker gate ben --base main) || RC=$?
[ "$RC" = "1" ] && [ "$(echo "$OUT" | jget data.verdict)" = "stale" ] && [ "$(echo "$OUT" | jget data.bounces)" = "2" ] \
  || fail "re-gating an unchanged head must not count: rc=$RC $OUT"
gswork again3.txt
[ "$(gsslot task)" = "1" ] || fail "the slot still holds the issue under the cap"
RC=0; OUT=$(gs worker gate ben --base main --check-only) || RC=$?
[ "$RC" = "1" ] && [ -z "$(echo "$OUT" | jget data.bounces 2>/dev/null || true)" ] || fail "--check-only must not count a bounce: rc=$RC $OUT"
RC=0; OUT=$(gs worker gate ben --base main) || RC=$?
[ "$RC" = "1" ] && [ "$(echo "$OUT" | jget data.bounces)" = "3" ] && [ "$(echo "$OUT" | jget data.parked)" = "true" ] \
  || fail "the third bounce should park the issue: rc=$RC $OUT"
[ "$(gslabels)" = "needs-human,type:task" ] || fail "a parked issue is labelled needs-human: $(gslabels)"
case "$(gscomments)" in *"sent branch"*"back 3 time(s)"*) ;; *) fail "the park should leave a comment: $(gscomments)" ;; esac
[ -z "$(gsslot task)" ] && [ "$(git -C "$GPROJ/.worktrees/ben" symbolic-ref --short HEAD)" = "park/ben" ] || fail "the slot should be freed and parked"
pass "bounces are counted per item; the cap parks the issue needs-human with a comment, and --check-only never counts"

rm -rf "${TMP_GS:?}"
