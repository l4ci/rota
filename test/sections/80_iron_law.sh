echo "iron law: ship refuses after FAIL, debug refuses a 4th attempt, --auto-loop is loop-only (B3, #56)"

IL="$(mktemp -d "$TMP/ironlaw.XXXXXX")"
(
  cd "$IL" && git init -q -b main && git -c user.email=a@b -c user.name=n commit -q --allow-empty -m init \
    && git switch -q -c feat/f && git -c user.email=a@b -c user.name=n commit -q --allow-empty -m work \
    && git switch -q main
) || fail "iron law fixture repo setup failed"
mkdir -p "$IL/.rota"

# A recorded review FAIL refuses the merge with exit 4 and changes nothing.
( cd "$IL" && hvj verdict add feat/f --kind review-spec --verdict FAIL >/dev/null ) || fail "review FAIL add failed"
RC=0; OUT=$( cd "$IL" && printf 'merge: f\n' | hvj ship merge feat/f --body-file - 2>/dev/null ) || RC=$?
[ "$RC" = "4" ] || fail "ship merge after a review FAIL should exit 4, got $RC"
[ "$(echo "$OUT" | jget data.blockedBy)" = "verdict" ] \
  || fail "the refusal should name the verdict: $OUT"
git -C "$IL" rev-parse -q --verify refs/heads/feat/f >/dev/null || fail "a refused merge deleted the branch"
pass "ship merge refuses after a recorded review FAIL"

# A newer PASS clears it; a second-opinion FAIL blocks again unless the runner is codex.
( cd "$IL" && hvj verdict add feat/f --kind review-spec --verdict PASS >/dev/null ) || fail "spec PASS add failed"
( cd "$IL" && hvj verdict add feat/f --kind second-opinion --verdict FAIL >/dev/null ) || fail "second-opinion add failed"
RC=0; ( cd "$IL" && printf 'merge: f\n' | hvj ship merge feat/f --body-file - >/dev/null 2>&1 ) || RC=$?
[ "$RC" = "4" ] || fail "a second-opinion FAIL should refuse the merge, got $RC"
printf '{"ship": {"secondOpinionRunner": "codex"}}\n' > "$IL/.rota/config.json"
OUT=$( cd "$IL" && printf 'merge: f\n' | hvj ship merge feat/f --body-file - ) || fail "advisory codex FAIL should not refuse: $OUT"
[ "$(echo "$OUT" | jget data.changed)" = "true" ] || fail "the advisory merge should land: $OUT"
pass "a second-opinion FAIL refuses unless the runner is the advisory codex fallback"

# Three failed fixes on an item refuse a fourth attempt, on any branch.
for _ in 1 2 3; do
  ( cd "$IL" && hvj debug verdict B07 --verdict FAIL >/dev/null ) || fail "debug verdict FAIL failed"
done
RC=0; OUT=$( cd "$IL" && hvj debug counter init B07 2>/dev/null ) || RC=$?
[ "$RC" = "4" ] || fail "debug counter init past the Iron Law should exit 4, got $RC"
[ ! -e "$IL/.rota/debug/main.json" ] || fail "a refused init wrote a session file"
( cd "$IL" && hvj debug counter init B08 >/dev/null ) || fail "another item should still start"
pass "the Iron Law refuses a fourth attempt per item"

# debug reset is a manual gate: refused without --confirm, audited with it.
RC=0; OUT=$( cd "$IL" && hvj debug reset B07 --reason "new angle" 2>/dev/null ) || RC=$?
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.gate)" = "debug-reset" ] || fail "an unconfirmed reset should hit the debug-reset gate: rc=$RC $OUT"
OUT=$( cd "$IL" && hvj debug reset B07 --reason "new angle" --confirm --confirm-note "yes, reset" ) || fail "a confirmed reset failed: $OUT"
[ "$(echo "$OUT" | jget data.cleared)" = "3" ] || fail "the reset should clear three failed fixes: $OUT"
grep -q '"gate": "debug-reset"' "$IL/.rota/gate-audit.jsonl" || fail "the reset wrote no audit line"
( cd "$IL" && hvj debug counter init B07 >/dev/null ) || fail "init should work again after a reset"
pass "debug reset is gated and audited, and starts the count again"

# --auto-loop writes auto: true in loop mode and is a usage error outside it.
RC=0; ( cd "$IL" && hvj design add F01 --title "Auto" --auto-loop >/dev/null 2>&1 ) || RC=$?
[ "$RC" = "2" ] || fail "design add --auto-loop outside loop should exit 2, got $RC"
[ ! -e "$IL/.rota/designs/F01.md" ] || fail "a refused --auto-loop wrote the design"
printf '{"autonomy": {"level": "loop"}}\n' > "$IL/.rota/config.json"
( cd "$IL" && hvj design add F01 --title "Auto" --auto-loop >/dev/null ) || fail "design add --auto-loop in loop failed"
grep -qx 'auto: true' "$IL/.rota/designs/F01.md" || fail "the loop design should carry auto: true"
pass "--auto-loop is loop-only and marks the artifact"
