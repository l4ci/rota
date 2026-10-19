echo "attempt fence: a late done or report from a reclaimed attempt is refused (#958)"

# A solo, file-mode round with ONE slot and a local bare origin. ben takes F01
# (attempt A), is reclaimed, and F01 is assigned to ben again (attempt B): the
# old worker, still alive, reports with A. Nothing reaches a host or a forge.
SA="$(mktemp -d "$TMP/attempt.XXXXXX")"
git init -q --bare "$SA/origin.git"
(
  cd "$SA" && mkdir proj && cd proj && git init -q -b main . && git config user.email a@b && git config user.name n \
    && git commit -q --allow-empty -m init && git remote add origin "$SA/origin.git" && git push -q origin main \
    && mkdir -p .rota && printf '# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n' > .rota/BACKLOG.md
) || fail "attempt fence fixture setup failed"
SP="$SA/proj"
SAENV="env -u HERDR_ENV -u HERDR_PANE_ID -u TMUX -u TMUX_PANE"
sa() { ( cd "$SP" && $SAENV "$ROTA_BIN" --json "$@" 2>/dev/null ); }
sareg() { python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); print(eval(sys.argv[2]))' "$SP/.rota/workers.json" "$1"; }
sa item create --kind features --title "Fenced item" --body-file - <<<$'## Acceptance\n- [ ] works\nTouches internal/a.go' >/dev/null || fail "item create failed"
printf 'stub worker contract\n' > "$SA/contract.md"
sa config set round.brief "$SA/contract.md" >/dev/null
sa config set ship.review none >/dev/null
HOLD=$$

OUT=$(sa round start --holder-pid "$HOLD" --slots 1 --scope open) || fail "round start failed: $OUT"
OUT=$(sa round assign F01 --holder-pid "$HOLD") || fail "assign F01 failed: $OUT"
A="$(sareg '[s for s in d["slots"] if s["name"]=="ben"][0]["attempt"]')"
[ "${#A}" = "8" ] || fail "assign should mint an 8-char attempt id, got '$A'"
case "$OUT" in *"--attempt $A"*) ;; *) fail "the brief should hand the worker its attempt id: $OUT" ;; esac
pass "assign mints an attempt id into the slot and the brief"

# Reclaim ben (solo has no pane to kill, --force skips the health wait) and
# assign the same issue again: a new holder, a new attempt.
OUT=$(sa round reclaim ben --force --holder-pid "$HOLD") || fail "reclaim failed: $OUT"
OUT=$(sa round assign F01 --holder-pid "$HOLD") || fail "re-assign after reclaim failed: $OUT"
B="$(sareg '[s for s in d["slots"] if s["name"]=="ben"][0]["attempt"]')"
[ -n "$B" ] && [ "$B" != "$A" ] || fail "re-assign after reclaim should mint a new attempt: old $A new '$B'"
pass "a re-assign after reclaim mints a new attempt"

# The old worker's late completion carries A.
rc=0; OUT=$(sa worker done ben --attempt "$A") || rc=$?
[ "$rc" = "4" ] || fail "late worker done from attempt A should exit 4, got $rc: $OUT"
rc=0; OUT=$(sa round report ben --state done --pr https://github.com/o/r/pull/7 --attempt "$A") || rc=$?
[ "$rc" = "4" ] || fail "late round report from attempt A should exit 4, got $rc: $OUT"
case "$OUT" in *"$B"*) fail "the refusal must not leak the current attempt id: $OUT" ;; esac
[ "$(sareg '[s for s in d["slots"] if s["name"]=="ben"][0]["state"]')" = "busy" ] \
  || fail "a fenced completion must leave the slot busy: $(cat "$SP/.rota/workers.json")"
[ "$(sareg '[s for s in d["slots"] if s["name"]=="ben"][0].get("pr")')" = "None" ] || fail "a fenced report must record no PR"
pass "a late done and report from the old attempt exit 4 and write nothing"

# The current holder is not fenced.
OUT=$(sa round report ben --state done --pr https://github.com/o/r/pull/7 --attempt "$B") || fail "report with the current attempt failed: $OUT"
[ "$(sareg '[s for s in d["slots"] if s["name"]=="ben"][0]["state"]')" = "done" ] || fail "the current attempt should mark ben done"
pass "the current attempt reports normally"

rm -rf "${SA:?}"
