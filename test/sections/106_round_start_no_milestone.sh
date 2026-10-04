echo "round start with no milestone: open fallback, empty reason, criteria from the brief (#20)"

# A fresh solo, file-mode repo: open backlog items, no milestone, round.scope
# left at its default. start then assign must be enough, with no bookkeeping PR.
NM="$(mktemp -d "$TMP/nomile.XXXXXX")"
git init -q --bare "$NM/origin.git"
(
  cd "$NM" && mkdir proj && cd proj && git init -q -b main . && git config user.email a@b && git config user.name n \
    && git commit -q --allow-empty -m init && git remote add origin "$NM/origin.git" && git push -q origin main \
    && mkdir -p .rota && printf '# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n' > .rota/BACKLOG.md
) || fail "no-milestone fixture setup failed"
NP="$NM/proj"
NMENV="env -u HERDR_ENV -u HERDR_PANE_ID -u TMUX -u TMUX_PANE"
nm() { ( cd "$NP" && $NMENV "$ROTA_BIN" --json "$@" 2>/dev/null ); }
printf 'stub worker contract\n' > "$NM/contract.md"
nm config set round.brief "$NM/contract.md" >/dev/null
HOLD=$$

# Empty backlog: the list is empty and says why and what to run.
OUT=$(nm round start --holder-pid "$HOLD" --slots 1) || fail "start on an empty backlog failed: $OUT"
case "$(echo "$OUT" | jget data.empty.reason)" in *"no open items"*) ;; *) fail "empty start should say why: $OUT" ;; esac
case "$(echo "$OUT" | jget data.empty.next)" in *rota-capture*) ;; *) fail "empty start should name the next command: $OUT" ;; esac
pass "an empty candidate list carries a reason and the next command"

# One item with no criteria (not ready), one with criteria, no milestone anywhere.
nm item create --kind features --title "Bare" --body-file - <<<'Touches internal/a.go' >/dev/null
nm item create --kind features --title "Ready" --body-file - <<<$'## Acceptance\n- [ ] works\nTouches internal/b.go' >/dev/null
OUT=$(nm round start --holder-pid "$HOLD" --slots 1) || fail "start failed: $OUT"
[ "$(echo "$OUT" | jget data.scope)" = "milestone" ] && [ "$(echo "$OUT" | jget data.fellBack)" = "true" ] \
  || fail "start with no milestone should fall back and say so: $OUT"
case "$OUT" in *F01*F02*) ;; *) fail "the fallback should offer every open item: $OUT" ;; esac
pass "no unfinished milestone: the default scope offers every open item"

# A not-ready item reaches assign when the brief carries its acceptance criteria.
RC=0; OUT=$(nm round assign F01 --holder-pid "$HOLD") || RC=$?
[ "$RC" = "4" ] || fail "a bare item should be refused without criteria: rc=$RC $OUT"
printf '## Acceptance\n- [ ] the bare item works\n' > "$NM/brief.md"
OUT=$(nm round assign F01 --holder-pid "$HOLD" --body-file "$NM/brief.md") || fail "assign with criteria in the brief failed: $OUT"
[ "$(echo "$OUT" | jget data.agent)" = "ben" ] || fail "F01 should go to ben: $OUT"
pass "assign --body-file with acceptance criteria stands in for a plan note"

# Everything held: the reason says so and points at round status.
OUT=$(nm round start --holder-pid "$HOLD" --slots 1 --scope slate --items F01) || fail "start --scope slate failed: $OUT"
case "$(echo "$OUT" | jget data.empty.reason)" in *"held"*) ;; *) fail "all-held candidates should say why: $OUT" ;; esac
case "$(echo "$OUT" | jget data.empty.next)" in *"round status"*) ;; *) fail "all-held candidates should name round status: $OUT" ;; esac
pass "an all-held candidate list says why"
