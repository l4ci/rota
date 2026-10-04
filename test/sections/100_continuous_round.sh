echo "continuous round: open scope, wait returns a change once, a PR frees its slot (#29)"

# A solo, file-mode round with ONE slot and a local bare origin: the slot must
# take three issues in a row while the earlier PRs wait in review. Nothing
# reaches a host or a forge.
CR="$(mktemp -d "$TMP/cont.XXXXXX")"
git init -q --bare "$CR/origin.git"
(
  cd "$CR" && mkdir proj && cd proj && git init -q -b main . && git config user.email a@b && git config user.name n \
    && git commit -q --allow-empty -m init && git remote add origin "$CR/origin.git" && git push -q origin main \
    && mkdir -p .rota && printf '# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n' > .rota/BACKLOG.md
) || fail "continuous round fixture setup failed"
CP="$CR/proj"
CRENV="env -u HERDR_ENV -u HERDR_PANE_ID -u TMUX -u TMUX_PANE"
cr() { ( cd "$CP" && $CRENV "$ROTA_BIN" --json "$@" 2>/dev/null ); }
creg() { python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); print(eval(sys.argv[2]))' "$CP/.rota/workers.json" "$1"; }
for n in a b c; do
  cr item create --kind features --title "Item $n" --body-file - <<<$'## Acceptance\n- [ ] works\nTouches internal/'"$n"'.go' >/dev/null
done
printf 'stub worker contract\n' > "$CR/contract.md"
cr config set round.brief "$CR/contract.md" >/dev/null
HOLD=$$

# Scope open: no milestone exists, and every open item is a candidate.
OUT=$(cr round start --holder-pid "$HOLD" --slots 1 --scope open) || fail "start --scope open failed: $OUT"
[ "$(echo "$OUT" | jget data.scope)" = "open" ] || fail "start should record scope open: $OUT"
case "$OUT" in *F01*F02*F03*) ;; *) fail "scope open should offer every open item: $OUT" ;; esac
OUT=$(cr round start --holder-pid "$HOLD" --slots 1) || fail "start re-run failed: $OUT"
[ "$(echo "$OUT" | jget data.scope)" = "open" ] && [ "$(creg 'd["scope"]')" = "open" ] \
  || fail "a start re-run without --scope should keep scope open: $OUT"
pass "scope open takes every open item, and a start re-run keeps it"

# ben works F01 and reports done with a PR (the worker's commit is simulated).
cr round assign F01 --holder-pid "$HOLD" >/dev/null || fail "assign F01 failed"
( cd "$CP/.worktrees/ben" && echo a > a.txt && git add a.txt && git -c user.email=a@b -c user.name=n commit -q -m a ) \
  || fail "worker commit failed"
cr round report ben --state done --pr https://github.com/o/r/pull/7 >/dev/null || fail "report ben failed"
OUT=$(cr round wait) || fail "wait should return the done slot: $OUT"
[ "$(echo "$OUT" | jget data.slot)" = "ben" ] || fail "wait should return ben: $OUT"
RC=0; OUT=$(cr round wait) || RC=$?
[ "$RC" = "1" ] && [ "$(echo "$OUT" | jget data.timedOut)" = "true" ] || fail "a second wait should not return ben again: rc=$RC $OUT"
pass "wait returns a done slot once"

# The only slot is done with a PR, so it is free: F02 takes it and F01's PR waits in review.
OUT=$(cr round assign F02 --holder-pid "$HOLD") || fail "assign F02 onto the done slot failed: $OUT"
[ "$(echo "$OUT" | jget data.agent)" = "ben" ] || fail "F02 should go to ben: $OUT"
[ "$(creg '[p["issue"] for p in d["prs"]]')" = "['F01']" ] || fail "F01's PR should wait in review: $(cat "$CP/.rota/workers.json")"
git --git-dir="$CR/origin.git" rev-parse -q --verify "refs/heads/$(creg 'd["prs"][0]["branch"]')" >/dev/null \
  || fail "the reviewed branch should be pushed to origin"
[ "$(creg '[s for s in d["slots"] if s["name"]=="ben"][0]["task"]')" = "F02" ] || fail "ben should hold F02"
OUT=$(cr round status)
[ "$(echo "$OUT" | python3 -c 'import json,sys; print([r["issue"] for r in json.load(sys.stdin)["data"]["review"]])')" = "['F01']" ] || fail "round status should list F01 in review: $OUT"
case "$(cr round candidates)" in *'"F01"'*) fail "an issue in review is not a candidate" ;; esac
RC=0; cr worker gate ben --base main >/dev/null || RC=$?
[ "$RC" = "2" ] || fail "gate <slot> whose PR moved to review should refuse with 2, got $RC"
pass "a done slot with a PR takes the next issue; the PR waits in review"

# A busy slot is never taken.
RC=0; OUT=$(cr round assign F03 --holder-pid "$HOLD") || RC=$?
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.blockedBy)" = "no free slot" ] || fail "a busy slot must not be taken: rc=$RC $OUT"
pass "a busy slot is not taken"

rm -rf "${CR:?}"
