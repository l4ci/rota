echo "round batch: round.batch caps open assignments for assign and tick (#936)"
# Same fakes-only issue-mode fixture as section 114: gh and tmux fakes, a local bare origin.

TMP_RB="$(mktemp -d)"
FKB="$TMP_RB/fake"
mkdir -p "$FKB/tmux"
RBPROJ="$TMP_RB/proj"
git init -q --bare "$TMP_RB/origin.git"
mkdir -p "$RBPROJ/.rota"
(
  cd "$RBPROJ" && git init -q -b main . && git config user.email t@t && git config user.name t \
    && echo seed > seed.txt && git add seed.txt && git commit -q -m seed \
    && git remote add origin "$TMP_RB/origin.git" && git push -q origin main
) || fail "batch fixture repo setup failed"
printf 'stub worker contract\n' > "$TMP_RB/contract.md"
printf '{"issues":{"source":"github"},"round":{"dispatch":"tmux","brief":"%s"}}\n' "$TMP_RB/contract.md" > "$RBPROJ/.rota/config.json"
printf 'Welcome to Claude Code\n' > "$FKB/tmux/pane"
: > "$FKB/tmux/log"

RBHOLD=$$
RBFAKES="$TESTDIR/fakes:$ROTA_POISON_BIN:$PATH"
rb() { ( cd "$RBPROJ" && PATH="$RBFAKES" ROTA_ROUND_HOLDER_PID="$RBHOLD" FAKE_TMUX="$FKB/tmux" FAKE_TRACKER_DB="$TMP_RB/db.json" ROTA_HOST_KILL_WAIT=1 "$ROTA_BIN" --json "$@" 2>/dev/null ); }

printf '## Acceptance\n- [ ] works\n' > "$TMP_RB/body.md"
for t in One Two Three; do
  rb item create --kind tasks --title "$t" --body-file "$TMP_RB/body.md" >/dev/null || fail "could not create issue $t"
done
rb round start --holder-pid "$RBHOLD" --scope slate --items 1,2,3 --slots 3 >/dev/null || fail "round start failed"

# ── a negative value is refused ─────────────────────────────────────────────
RC=0; rb config set round.batch -1 >/dev/null || RC=$?
[ "$RC" != "0" ] || fail "round.batch -1 should be rejected by config set"
pass "round.batch rejects a negative value"

# ── refusal at the cap ──────────────────────────────────────────────────────
rb config set round.batch 1 >/dev/null || fail "config set round.batch 1 failed"
RC=0; OUT=$(rb round assign 1) || RC=$?
[ "$RC" = "0" ] || fail "the first assignment fits batch 1: rc=$RC $OUT"
RC=0; OUT=$(rb round assign 2) || RC=$?
[ "$RC" = "4" ] || fail "a second assignment past batch 1 must exit 4, got $RC: $OUT"
[ "$(echo "$OUT" | jget data.blockedBy)" = "batch" ] && [ "$(echo "$OUT" | jget data.batch)" = "1" ] && [ "$(echo "$OUT" | jget data.open)" = "1" ] \
  || fail "the refusal carries blockedBy batch, batch and open: $OUT"
grep -q "land or park" <<<"$OUT" || fail "the refusal hints to land or park an item first: $OUT"
pass "round assign refuses at round.batch with batch and open in the data"

# ── the override covers one call ────────────────────────────────────────────
RC=0; OUT=$(rb round assign 2 --accept-batch) || RC=$?
[ "$RC" = "0" ] || fail "--accept-batch should assign past the cap: rc=$RC $OUT"
SLOT2=$(echo "$OUT" | jget data.agent)
RC=0; OUT=$(rb round assign 3) || RC=$?
[ "$RC" = "4" ] || fail "the override is for one call only, got $RC: $OUT"
pass "--accept-batch overrides the refusal for one call"

# ── repeating an assignment the slot holds is a resume, not a refusal ───────
RC=0; OUT=$(rb round assign 2 --agent "$SLOT2") || RC=$?
[ "$RC" = "0" ] || fail "a resumed assignment must pass at the cap: rc=$RC $OUT"
pass "round assign resumes a held item at the cap"

# ── the tick skips assigning and says batch-full ────────────────────────────
rb config set autonomy.level auto >/dev/null
OUT=$(rb round tick)
[ "$(echo "$OUT" | jget data.batchFull)" = "true" ] || fail "a tick at the cap reports batchFull: $OUT"
if grep -q '"action": "assign"' <<<"$OUT"; then fail "a tick at the cap must assign nothing: $OUT"; fi
pass "round tick assigns nothing at the cap and reports batchFull"

# ── unset means no limit ────────────────────────────────────────────────────
rb config set round.batch 0 >/dev/null
RC=0; OUT=$(rb round assign 3) || RC=$?
[ "$RC" = "0" ] || fail "with round.batch 0 the third assignment goes through: rc=$RC $OUT"
pass "round.batch 0 is no limit"
rm -rf "$TMP_RB"
