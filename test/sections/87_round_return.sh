echo "round return/transfer/reclaim: park, handoff comment, claim moves (C10, #76)"
# A started round in an issue-mode project, driven end to end by FAKES only:
# gh is test/fakes/gh (state in a JSON store), tmux is test/fakes/tmux (logs its
# argv; rrsnap feeds its host snapshot), origin is a local bare repo. Nothing
# reaches a real forge, host or remote.

TMP_RR="$(mktemp -d)"
trap 'rm -rf "${TMP_RR:?}"' EXIT
FK="$TMP_RR/fake"
mkdir -p "$FK/tmux"
ORIGIN="$TMP_RR/origin.git"
PROJ="$TMP_RR/proj"
git init -q --bare "$ORIGIN"
mkdir -p "$PROJ/.rota"
(
  cd "$PROJ" && git init -q -b main . && git config user.email t@t && git config user.name t \
    && echo seed > seed.txt && git add seed.txt && git commit -q -m seed \
    && git remote add origin "$ORIGIN" && git push -q origin main
) || fail "round return fixture repo setup failed"
printf 'stub worker contract\n' > "$TMP_RR/contract.md"
printf '{"backlog":{"backend":"issues"},"issues":{"provider":"github","retryWaitSeconds":0},"work":{"dispatch":"tmux"},"round":{"brief":"%s"}}\n' "$TMP_RR/contract.md" > "$PROJ/.rota/config.json"
printf 'Welcome to Claude Code\n' > "$FK/tmux/pane"
: > "$FK/tmux/log"

# The host snapshot: `session:window<TAB>path` lines the tmux fake lists for rrsnap.
: > "$FK/snapshot"

HOLDER=$$
FAKES="$TESTDIR/fakes:$ROTA_POISON_BIN:$PATH"
# rrin <dir> <rota args...>: one rota call from <dir>, tmux and gh are the fakes.
rrin() { local d="$1"; shift; ( cd "$d" && PATH="$FAKES" FAKE_TMUX="$FK/tmux" FAKE_TRACKER_DB="$TMP_RR/db.json" ROTA_HOST_KILL_WAIT=1 "$ROTA_BIN" --json "$@" 2>/dev/null ); }
rr() { rrin "$PROJ" "$@"; }
# rrsnap: the same with the host snapshot on, so liveness and stalls are known.
rrsnap() { ( cd "$PROJ" && PATH="$FAKES" FAKE_TMUX_SNAPSHOT="$FK/snapshot" FAKE_TMUX="$FK/tmux" FAKE_TRACKER_DB="$TMP_RR/db.json" ROTA_HOST_KILL_WAIT=1 "$ROTA_BIN" --json "$@" 2>/dev/null ); }
rc_of() { local rc=0; "$@" >/dev/null 2>&1 || rc=$?; echo "$rc"; }
gh_() { ( cd "$PROJ" && PATH="$FAKES" FAKE_TRACKER_DB="$TMP_RR/db.json" gh "$@" ); }
# The tracker's own state: labels and comment bodies of one issue.
dbq() { python3 -c '
import json, sys
mode, n = sys.argv[1], int(sys.argv[2])
issue = next(i for i in json.load(open(sys.argv[3]))["issues"] if i["number"] == n)
if mode == "labels":
    print(",".join(sorted(issue["labels"])))
else:
    print("\n=====\n".join(c["body"] for c in issue["comments"]))' "$1" "$2" "$TMP_RR/db.json"; }
reg() { python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); s=[x for x in d["slots"] if x["name"]==sys.argv[2]][0]; v=s.get(sys.argv[3]); print("" if v is None else v)' "$PROJ/.rota/workers.json" "$1" "$2"; }
branch_of() { git -C "$PROJ/.worktrees/$1" symbolic-ref --short HEAD; }
# drifts: "<kind>:<slot>,..|<repaired kind>,.." of a reconcile envelope on stdin.
drifts() { python3 -c 'import json,sys; d=json.load(sys.stdin)["data"]; print(",".join(x["kind"]+":"+x.get("slot","") for x in d["drift"]) + "|" + ",".join(x["kind"] for x in d["repaired"]))'; }
cands() { rr round candidates | python3 -c 'import json,sys; print(",".join(c["id"] for c in json.load(sys.stdin)["data"]["candidates"]))'; }

# The issues. 1 is the one that moves around; 2 and 3 are for reclaim.
printf '## Acceptance\n- [ ] works\n' > "$TMP_RR/body.md"
for T in "Wire the parser" "Second thing" "Third thing"; do
  rr item create --kind tasks --title "$T" --body-file "$TMP_RR/body.md" >/dev/null || fail "could not create issue $T"
done
OUT=$(rr round start --holder-pid "$HOLDER" --scope slate --items 1,2,3 --slots 2) || fail "round start failed: $OUT"
[ "$(echo "$OUT" | jget data.round)" = "1" ] || fail "first start should be round 1: $OUT"

# ── assign 1 to ben, give it work ───────────────────────────────────────────
OUT=$(rr round assign 1 --agent ben --holder-pid "$HOLDER") || fail "assign 1 to ben failed: $OUT"
[ "$(echo "$OUT" | jget data.dispatched)" = "true" ] || fail "assign should dispatch: $OUT"
B1="ben/1-wire-the-parser"
[ "$(branch_of ben)" = "$B1" ] || fail "ben should be on $B1, on $(branch_of ben)"
echo work > "$PROJ/.worktrees/ben/work.txt"
git -C "$PROJ/.worktrees/ben" add work.txt && git -C "$PROJ/.worktrees/ben" commit -q -m "ben's first step" \
  || fail "could not commit in ben's worktree"
echo half > "$PROJ/.worktrees/ben/half.txt"   # uncommitted: Park must salvage it
printf 'tried the recursive parser; the table-driven one is next\n' > "$TMP_RR/note.md"

# ── return: refusals first, then the worker's own call ──────────────────────
RC=0; OUT=$(rr round return ben --reason "x" --holder-pid 1) || RC=$?
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.blockedBy)" = "not your slot" ] || fail "a caller that is neither the slot nor the lease holder is refused: $RC $OUT"
RC=0; OUT=$(rrin "$PROJ/.worktrees/ben" round return ben) || RC=$?
[ "$RC" = "2" ] || fail "return without --reason is a usage error, got $RC: $OUT"
RC=0; OUT=$(rrin "$PROJ/.worktrees/ben" round return dana --reason x) || RC=$?
[ "$RC" = "3" ] || fail "return of a slot holding no issue is exit 3, got $RC: $OUT"
[ "$(dbq labels 1)" = "in-progress,type:task" ] || fail "refusals must not touch the tracker: $(dbq labels 1)"

OUT=$(rrin "$PROJ/.worktrees/ben" round return ben --reason "wrong premise: the ticket asks for the old format" --note-file "$TMP_RR/note.md") \
  || fail "return from inside the worktree failed: $OUT"
[ "$(echo "$OUT" | jget data.issue)" = "1" ] && [ "$(echo "$OUT" | jget data.branch)" = "$B1" ] || fail "return data should name the issue and branch: $OUT"
[ "$(echo "$OUT" | jget data.salvaged)" = "true" ] && [ "$(echo "$OUT" | jget data.released)" = "true" ] || fail "return should salvage and release: $OUT"
[ -n "$(echo "$OUT" | jget data.commentId)" ] || fail "return should report the handoff comment: $OUT"
[ -n "$(git -C "$ORIGIN" rev-parse --verify -q "refs/heads/$B1")" ] || fail "the work branch must be pushed to origin"
[ "$(git -C "$ORIGIN" log -1 --format=%s "$B1")" = "wip: parked from ben (rota round return)" ] || fail "salvage commit subject: $(git -C "$ORIGIN" log -1 --format=%s "$B1")"
[ "$(branch_of ben)" = "park/ben" ] || fail "ben must be parked, on $(branch_of ben)"
[ -z "$(git -C "$PROJ/.worktrees/ben" status --porcelain)" ] || fail "a parked worktree is clean"
[ "$(dbq labels 1)" = "type:task" ] || fail "in-progress must be gone: $(dbq labels 1)"
HANDOFF="$(dbq comments 1)"
for W in '**rota handoff** (return, from ben)' "Branch: \`$B1\`" 'State: committed, salvage commit' 'Reason: wrong premise: the ticket asks for the old format' 'table-driven one is next' '<!-- rota:handoff ben@1 -->'; do
  case "$HANDOFF" in *"$W"*) ;; *) fail "handoff comment lacks '$W': $HANDOFF" ;; esac
done
case "$HANDOFF" in *"<!-- rota:release ben@1 -->"*) ;; *) fail "the claim must be released: $HANDOFF" ;; esac
[ -z "$(reg ben task)" ] && [ -z "$(reg ben claimId)" ] && [ "$(reg ben state)" = "idle" ] || fail "ben's slot must be free: $(cat "$PROJ/.rota/workers.json")"
pass "return pushes the branch, salvages dirty work, posts the handoff, releases the claim and parks the worker's worktree"

# ── return then assign: the issue is a candidate again ──────────────────────
case ",$(cands)," in *,1,*) ;; *) fail "a returned issue must be a candidate again: $(cands)" ;; esac
OUT=$(rr round assign 1 --agent dana --holder-pid "$HOLDER") || fail "assign after return failed: $OUT"
[ "$(echo "$OUT" | jget data.agent)" = "dana" ] || fail "assign after return: $OUT"
PAYLOAD=$(cat "$FK/tmux/payload")
case "$PAYLOAD" in *"rota:handoff"*"$B1"*) ;; *) fail "the brief must name the handoff and the pushed branch: $PAYLOAD" ;; esac
pass "return then assign takes the item up again, and the brief names the handoff"

# ── transfer to a slot: the existing branch is checked out in the receiver ──
B2="dana/1-wire-the-parser"
[ "$(branch_of dana)" = "$B2" ] || fail "dana should be on $B2, on $(branch_of dana)"
echo dana-step > "$PROJ/.worktrees/dana/step.txt"
git -C "$PROJ/.worktrees/dana" add step.txt && git -C "$PROJ/.worktrees/dana" commit -q -m "dana's step" || fail "could not commit in dana's worktree"
RC=0; OUT=$(rr round transfer 1 --to dana --holder-pid "$HOLDER") || RC=$?
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.blockedBy)" = "same slot" ] || fail "transfer to the slot that holds it is refused: $RC $OUT"
RC=0; OUT=$(rr round transfer 1 --to ben --holder-pid 1) || RC=$?
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.blockedBy)" = "no round" ] || fail "a process without the lease is refused: $RC $OUT"
RC=0; OUT=$(rr round transfer 1 --to nobody --holder-pid "$HOLDER") || RC=$?
[ "$RC" = "2" ] || fail "--to outside the roster is a usage error: $RC $OUT"
OUT=$(rr round transfer 1 --to ben --holder-pid "$HOLDER" --note-file "$TMP_RR/note.md") || fail "transfer to ben failed: $OUT"
[ "$(echo "$OUT" | jget data.from)" = "dana" ] && [ "$(echo "$OUT" | jget data.to)" = "ben" ] || fail "transfer data: $OUT"
[ "$(echo "$OUT" | jget data.branch)" = "$B2" ] && [ "$(echo "$OUT" | jget data.claimId)" = "ben@1" ] || fail "transfer should move the branch and claim: $OUT"
[ "$(echo "$OUT" | jget data.dispatched)" = "true" ] || fail "transfer to a slot dispatches: $OUT"
[ "$(branch_of ben)" = "$B2" ] || fail "ben must have the existing branch checked out, on $(branch_of ben)"
[ -f "$PROJ/.worktrees/ben/step.txt" ] || fail "the receiver starts from the pushed work"
[ "$(branch_of dana)" = "park/dana" ] || fail "the sender is parked, on $(branch_of dana)"
[ "$(reg ben task)" = "1" ] && [ "$(reg ben claimId)" = "ben@1" ] && [ -z "$(reg dana task)" ] || fail "registry after the transfer: $(cat "$PROJ/.rota/workers.json")"
[ "$(dbq labels 1)" = "in-progress,type:task" ] || fail "in-progress stays on across a transfer: $(dbq labels 1)"
case "$(dbq comments 1)" in *"(transfer, from dana)"*"<!-- rota:release dana@1 -->"*"<!-- rota:claim ben@1 -->"*) ;; *) fail "transfer comment and claim order: $(dbq comments 1)" ;; esac
case "$(cat "$FK/tmux/payload")" in *"handed to you by dana"*) ;; *) fail "the receiver's brief names the handoff: $(cat "$FK/tmux/payload")" ;; esac
pass "transfer to a slot parks the sender, moves the claim and checks the existing branch out in the receiver"

# ── transfer to the human: needs-human, no claim, nothing dispatched ────────
: > "$FK/tmux/log"
OUT=$(rr round transfer 1 --to human --holder-pid "$HOLDER" --note-file "$TMP_RR/note.md") || fail "transfer to human failed: $OUT"
[ "$(echo "$OUT" | jget data.to)" = "human" ] && [ "$(echo "$OUT" | jget data.dispatched)" = "false" ] || fail "transfer to human data: $OUT"
[ -z "$(echo "$OUT" | jget data.claimId 2>/dev/null || true)" ] || fail "no claim is taken for the human: $OUT"
[ "$(dbq labels 1)" = "needs-human,type:task" ] || fail "the issue gets needs-human and loses in-progress: $(dbq labels 1)"
if grep -q 'new-window\|load-buffer' "$FK/tmux/log"; then fail "nothing may be dispatched for the human: $(cat "$FK/tmux/log")"; fi
[ "$(branch_of ben)" = "park/ben" ] && [ -z "$(reg ben task)" ] || fail "the sender is parked and free"
case ",$(cands)," in *,1,*) fail "candidates must skip a needs-human issue: $(cands)" ;; esac
gh_ issue edit 1 --remove-label needs-human >/dev/null || fail "could not clear the label"
case ",$(cands)," in *,1,*) ;; *) fail "clearing the label puts the issue back: $(cands)" ;; esac
pass "transfer to the human labels needs-human, claims nothing, dispatches nothing; candidates skip it until the label is cleared"

# ── reclaim ─────────────────────────────────────────────────────────────────
OUT=$(rr round assign 2 --agent dana --holder-pid "$HOLDER") || fail "assign 2 to dana failed: $OUT"
: > "$FK/tmux/log"
echo wip > "$PROJ/.worktrees/dana/wip.txt"
DWT="$PROJ/.worktrees/dana"
printf 'hvfake:dana\t%s\n' "$DWT" > "$FK/snapshot"
RC=0; OUT=$(rrsnap round reclaim dana --holder-pid "$HOLDER") || RC=$?
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.blockedBy)" = "healthy" ] || fail "a live, recently active slot is refused without --force: $RC $OUT"
RC=0; OUT=$(rr round reclaim dana --holder-pid 1) || RC=$?
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.blockedBy)" = "no round" ] || fail "reclaim needs the lease: $RC $OUT"
# A host that cannot be asked: the agent cannot be proved gone, so even --force
# may not move the worktree under it.
mkdir -p "$FK/down"
printf '#!/bin/sh\necho "no server running" >&2\nexit 1\n' > "$FK/down/tmux"; chmod +x "$FK/down/tmux"
RC=0; OUT=$( cd "$PROJ" && PATH="$FK/down:$FAKES" FAKE_TMUX="$FK/tmux" FAKE_TRACKER_DB="$TMP_RR/db.json" "$ROTA_BIN" --json round reclaim dana --force --holder-pid "$HOLDER" 2>/dev/null ) || RC=$?
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.blockedBy)" = "live agent" ] || fail "no host: a live agent cannot be proved gone: $RC $OUT"
[ "$(branch_of dana)" = "dana/2-second-thing" ] && [ "$(dbq labels 2)" = "in-progress,type:task" ] || fail "refusals change nothing"
if grep -q 'kill-window' "$FK/tmux/log"; then fail "a refused reclaim must not kill anything"; fi

# dana's window is gone from the host (the tmux fake lists none): it is dead,
# which needs no kill.
OUT=$(rr round reclaim dana --holder-pid "$HOLDER" --note-file "$TMP_RR/note.md") || fail "reclaim of a dead slot failed: $OUT"
[ "$(echo "$OUT" | jget data.health)" = "dead" ] && [ "$(echo "$OUT" | jget data.salvaged)" = "true" ] && [ "$(echo "$OUT" | jget data.released)" = "true" ] && [ "$(echo "$OUT" | jget data.parked)" = "true" ] || fail "reclaim data: $OUT"
[ -n "$(git -C "$ORIGIN" rev-parse --verify -q "refs/heads/dana/2-second-thing")" ] || fail "reclaim must push the branch"
[ "$(branch_of dana)" = "park/dana" ] && [ -z "$(reg dana task)" ] && [ -z "$(reg dana handle)" ] && [ "$(reg dana state)" = "idle" ] || fail "dana's slot must be parked and free: $(cat "$PROJ/.rota/workers.json")"
[ "$(dbq labels 2)" = "type:task" ] || fail "in-progress must be gone: $(dbq labels 2)"
case "$(dbq comments 2)" in *"Reason: reclaimed, dead"*"rota:handoff dana@1"*) ;; *) fail "reclaim handoff: $(dbq comments 2)" ;; esac
OUT=$(rr round reclaim dana --holder-pid "$HOLDER") || fail "reclaim of an idle slot failed: $OUT"
[ "$(echo "$OUT" | jget data.health)" = "idle" ] && [ "$(echo "$OUT" | jget data.changed)" = "false" ] || fail "an idle slot is a no-op: $OUT"
pass "reclaim of a dead slot parks it, posts the handoff, releases the claim and clears the handle; an idle slot is a no-op"

# A live but stalled worker: reconcile reports it, reclaim kills the pane first.
OUT=$(rr round assign 3 --agent ben --holder-pid "$HOLDER") || fail "assign 3 to ben failed: $OUT"
BWT="$PROJ/.worktrees/ben"
printf 'hvfake:ben\t%s\n' "$BWT" > "$FK/snapshot"
OUT=$(rrsnap round reconcile --apply)
case "$(echo "$OUT" | drifts)" in *stalled*) fail "a worker that just started is not stalled: $OUT" ;; esac
RC=0; OUT=$(rrsnap round reclaim ben --holder-pid "$HOLDER") || RC=$?
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.blockedBy)" = "healthy" ] || fail "a live, recently active slot is healthy: $RC $OUT"
python3 - "$PROJ/.rota/workers.json" <<'PY' || fail "could not age ben's activity"
import json, sys
p = sys.argv[1]
d = json.load(open(p))
for s in d["slots"]:
    if s["name"] == "ben":
        s["activeAt"] = "2020-01-01T00:00:00Z"
json.dump(d, open(p, "w"), indent=2)
PY
BEFORE=$(cat "$PROJ/.rota/workers.json")
OUT=$(rrsnap round reconcile --apply)
case "$(echo "$OUT" | drifts)" in *stalled:ben*) ;; *) fail "reconcile should report ben as stalled: $OUT" ;; esac
[ "$(cat "$PROJ/.rota/workers.json")" = "$BEFORE" ] || fail "stalled is never repaired: --apply changed the registry"

# claim-mismatch: a human released the claim on the tracker. Only the registry
# side is repaired.
gh_ api -X POST repos/o/r/issues/3/comments -f body='<!-- rota:release ben@1 -->' >/dev/null || fail "could not release the claim by hand"
OUT=$(rrsnap round reconcile)
case "$(echo "$OUT" | drifts)" in *claim-mismatch:ben*) ;; *) fail "reconcile should report the claim mismatch: $OUT" ;; esac
OUT=$(rrsnap round reconcile --apply)
case "$(echo "$OUT" | drifts)" in *"|claim-mismatch"*) ;; *) fail "apply should repair the registry side: $OUT" ;; esac
[ -z "$(reg ben claimId)" ] && [ "$(reg ben task)" = "3" ] || fail "claimId cleared, task kept: $(cat "$PROJ/.rota/workers.json")"
case "$(dbq comments 3)" in *"rota:claim ben@1"*"rota:release ben@1"*) ;; *) fail "the tracker is the source of truth and is not edited: $(dbq comments 3)" ;; esac

: > "$FK/tmux/log"
OUT=$(rrsnap round reclaim ben --holder-pid "$HOLDER") || fail "reclaim of a stalled slot failed: $OUT"
[ "$(echo "$OUT" | jget data.health)" = "stalled" ] && [ "$(echo "$OUT" | jget data.changed)" = "true" ] || fail "stalled reclaim data: $OUT"
[ "$(echo "$OUT" | jget data.released)" = "false" ] || fail "a claim already gone is not an error and releases nothing: $OUT"
grep -q 'kill-window' "$FK/tmux/log" || fail "a live pane must be killed before its worktree is parked: $(cat "$FK/tmux/log")"
[ "$(branch_of ben)" = "park/ben" ] && [ -z "$(reg ben task)" ] || fail "ben must be parked and free"
case "$(dbq comments 3)" in *"Reason: reclaimed, stalled"*) ;; *) fail "reclaim reason: $(dbq comments 3)" ;; esac
rm -rf "${TMP_RR:?}"
trap 'rm -rf "$TMP"' EXIT
pass "reconcile reports stalled and a claim mismatch (repairing only the registry); reclaim of a stalled slot kills the pane first"
