echo "architecture review: counter, both triggers, area items assigned to idle slots (#53)"

# A solo, file-mode round with ONE slot and a local bare origin. Features are
# completed against real commits so counters.json since_refactor counts them.
AR="$(mktemp -d "$TMP/arch.XXXXXX")"
git init -q --bare "$AR/origin.git"
(
  cd "$AR" && mkdir proj && cd proj && git init -q -b main . && git config user.email a@b && git config user.name n \
    && git commit -q --allow-empty -m init && git remote add origin "$AR/origin.git" && git push -q origin main \
    && mkdir -p .rota && printf '# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n' > .rota/BACKLOG.md
) || fail "architecture review fixture setup failed"
AP="$AR/proj"
ARENV="env -u HERDR_ENV -u HERDR_PANE_ID -u TMUX -u TMUX_PANE"
ar() { ( cd "$AP" && $ARENV "$ROTA_BIN" --json "$@" 2>/dev/null ); }
printf 'stub worker contract\n' > "$AR/contract.md"
ar config set round.brief "$AR/contract.md" >/dev/null
ar config set round.scope open >/dev/null
ar config set round.architectureEvery 2 >/dev/null
ar config set round.architectureAreas '["cli","worker"]' >/dev/null
HOLD=$$
done_feature() { # <id>: create, commit, complete: one counted feature
  ar item create --kind features --title "Feature $1" --body-file - <<<$'## Acceptance\n- [ ] works' >/dev/null
  ( cd "$AP" && echo "$1" > "$1.txt" && git add "$1.txt" && git -c user.email=a@b -c user.name=n commit -q -m "feat: add $1" )
  ar item complete "$2" --no-proof >/dev/null || fail "complete $2 failed"
}

OUT=$(ar round start --holder-pid "$HOLD" --slots 1) || fail "round start failed: $OUT"
[ "$(echo "$OUT" | jget data.architecture.count)" = "0" ] || fail "start should report the counter: $OUT"
done_feature one F01
# Queue-empty: a slot is idle, nothing is assignable and something closed since the last review.
OUT=$(ar round architecture --check) || fail "architecture --check failed: $OUT"
[ "$(echo "$OUT" | jget data.due)" = "true" ] && [ "$(echo "$OUT" | jget data.trigger)" = "queue-empty" ] \
  || fail "an idle slot with an empty queue should make a review due: $OUT"
pass "the queue-empty trigger fires with an idle slot and nothing to assign"

# A ready open item keeps the queue non-empty, so only the threshold can fire now.
ar item create --kind features --title "Standing item" --body-file - <<<$'## Acceptance\n- [ ] works' >/dev/null
OUT=$(ar round architecture --check)
[ "$(echo "$OUT" | jget data.count)" = "1" ] && [ "$(echo "$OUT" | jget data.until)" = "1" ] \
  && [ "$(echo "$OUT" | jget data.due)" = "false" ] \
  || fail "one closed feature of two, with work queued, should not be due: $OUT"
[ "$(ar round status | jget data.architecture.until)" = "1" ] && [ "$(ar round candidates | jget data.architecture.until)" = "1" ] \
  || fail "round status and candidates should show the counter"
case "$(cd "$AP" && $ARENV "$ROTA_BIN" round status 2>/dev/null)" in *"architecture review in 1 issues"*) ;; *) fail "text status should say: architecture review in 1 issues" ;; esac
pass "the counter counts closed non-refactor items and shows in round status"

# Threshold: the second counted feature makes a review due; a refactor-form commit does not count.
( cd "$AP" && echo r > r.txt && git add r.txt && git -c user.email=a@b -c user.name=n commit -q -m "refactor: tidy" )
ar item create --kind features --title "Refactor-driven" --body-file - <<<$'## Acceptance\n- [ ] works' >/dev/null
ar item complete F03 --no-proof >/dev/null
[ "$(ar round architecture --check | jget data.count)" = "1" ] || fail "a refactor-form completion must not count"
done_feature three F04
OUT=$(ar round architecture --check)
[ "$(echo "$OUT" | jget data.due)" = "true" ] && [ "$(echo "$OUT" | jget data.trigger)" = "threshold" ] \
  || fail "two counted features should make a threshold review due: $OUT"
pass "the threshold trigger fires; refactor-form completions are excluded"

# Acting: one item per area, as many assigned as there are idle slots (one), the count restarts.
OUT=$(ar round architecture --holder-pid "$HOLD") || fail "architecture failed: $OUT"
MINTED_OUT=$OUT
[ "$(echo "$OUT" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)["data"]["minted"]))')" = "2" ] || fail "should mint one item per area: $OUT"
[ "$(echo "$OUT" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)["data"]["assigned"]))')" = "1" ] || fail "one idle slot takes one item: $OUT"
[ "$(echo "$OUT" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)["data"]["unassigned"]))')" = "1" ] || fail "the second item stays a candidate: $OUT"
case "$(cat "$AP/.rota/BACKLOG.md")" in *"arch(cli): architecture review"*"arch(worker): architecture review"*) ;; *) fail "review items should be in the backlog" ;; esac
[ "$(ar refactor age | jget data.features)" = "0" ] || fail "the review should restart the counter"
OUT=$(ar round architecture --check)
[ "$(echo "$OUT" | jget data.due)" = "false" ] || fail "a review in flight should not retrigger: $OUT"
case "$(ar round candidates)" in *"arch(worker)"*) ;; *) fail "the unassigned review item should be a candidate" ;; esac
pass "a due review mints one item per area, assigns idle slots and restarts the count"

# A review item has no PR: its worker reports the issues it filed, and assigning the
# next item to the slot closes the review item and frees the slot (#149).
SLOT=$(echo "$MINTED_OUT" | python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["assigned"][0]["agent"])')
FIRST=$(echo "$MINTED_OUT" | python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["assigned"][0]["id"])')
SECOND=$(echo "$MINTED_OUT" | python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["unassigned"][0])')
ar round assign "$SECOND" --agent "$SLOT" --holder-pid "$HOLD" >/dev/null && fail "a busy review slot should refuse a new item"
ar round report "$SLOT" --state done --issues "#139,#140" >/dev/null || fail "report --issues failed"
ar round report "$SLOT" --state done --pr 5 --issues "#1" >/dev/null && fail "--pr and --issues are exclusive"
ar round assign "$SECOND" --agent "$SLOT" --holder-pid "$HOLD" >/dev/null || fail "assign onto a done review slot failed"
case "$(cat "$AP/.rota/BACKLOG.md")" in *"Completed"*"[T01] arch(cli)"*) ;; *) fail "the finished review item should be completed: $(cat "$AP/.rota/BACKLOG.md")" ;; esac
pass "a review item reports done with its issues, frees the slot without a PR and closes"

# Off: round.architectureEvery 0 never fires.
ar config set round.architectureEvery 0 >/dev/null
done_feature four F05
done_feature five F06
OUT=$(ar round architecture) || fail "architecture (off) failed: $OUT"
[ "$(echo "$OUT" | jget data.due)" = "false" ] && [ "$(echo "$OUT" | jget data.changed)" = "false" ] \
  || fail "every 0 must turn the review off: $OUT"
pass "round.architectureEvery 0 turns the review off"

rm -rf "${AR:?}"
