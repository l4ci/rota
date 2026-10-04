echo "worker dispatch — a brief left unsent on the prompt line is submitted, never retyped"
# Covers #89 under herdr: `agent prompt` typed the brief but its Enter was lost
# (the pane shows the text, the agent never started). Dispatch presses Enter
# with bounded retries; a relay resend after a stall submits what is on the
# prompt line instead of typing a second copy. The host is test/fakes/herdr:
# prompt_error makes `agent prompt` stall, pane.txt is the prompt line, and
# wait_status is what the agent does after an Enter.

TMP_SU="$(mktemp -d)"
trap 'rm -rf "$TMP_SU"' EXIT
SF="$TMP_SU/fake"
mkdir -p "$SF/bin" "$TMP_SU/repo/.rota"
cp "$TESTDIR/fakes/herdr" "$SF/bin/herdr"
chmod +x "$SF/bin/herdr"
(
  cd "$TMP_SU/repo"
  git init -q -b main .
  git config user.email t@t
  git config user.name t
  echo seed > seed.txt
  git add seed.txt
  git commit -q -m seed
) || fail "submit fixture repo setup failed"
printf '{"work":{"dispatch":"herdr"}}\n' > "$TMP_SU/repo/.rota/config.json"

su()  { ( cd "$TMP_SU/repo" && PATH="$SF/bin:$PATH" FAKE_HERDR="$SF" HERDR_ENV=1 HERDR_WORKSPACE_ID=w9 "$@" ); }
slot_field() {
  python3 -c 'import json,sys; s=[s for s in json.load(open(sys.argv[1]))["slots"] if s["name"]==sys.argv[2]][0]; print(s.get(sys.argv[3]))' \
    "$TMP_SU/repo/.rota/workers.json" "$1" "$2"
}

su hvj worker pool init --slots 1 --base main >/dev/null 2>&1 || fail "pool init failed"
printf 'first line\nthe last line of the brief\n' > "$TMP_SU/brief.md"
: >"$SF/log"
su hvj worker dispatch w1 --body-file "$TMP_SU/brief.md" --task T1 >/dev/null 2>&1 || fail "first dispatch failed"
[ "$(slot_field w1 unsent)" = "None" ] || fail "a clean dispatch must not mark the slot unsent"

# (a) first Enter lost: the prompt stalls, the brief is visible, the Enter lands.
echo agent_prompt_stalled > "$SF/prompt_error"
echo working > "$SF/wait_status"
printf '❯ first line\nthe last line of the brief\n' > "$SF/pane.txt"
: >"$SF/log"
RC=0; su hvj worker dispatch w1 --body-file "$TMP_SU/brief.md" --relay >/dev/null 2>&1 || RC=$?
[ "$RC" = "0" ] || fail "a dropped Enter must be retried to success, got exit $RC"
[ "$(grep -c '^agent send-keys rota-w1-w9-t7 enter$' "$SF/log")" = "1" ] \
  || fail "expected exactly one Enter press; log: $(cat "$SF/log")"
[ "$(grep -c '^agent prompt ' "$SF/log")" = "1" ] || fail "the brief must be typed once; log: $(cat "$SF/log")"
pass "dispatch presses Enter on a visible-but-unsent brief"

# (b) every Enter lost: bounded retries, exit 6, slot marked unsent.
echo idle > "$SF/wait_status"
: >"$SF/log"
RC=0; su hvj worker dispatch w1 --body-file "$TMP_SU/brief.md" --relay >/dev/null 2>&1 || RC=$?
[ "$RC" = "6" ] || fail "Enters that never land must exit 6, got $RC"
[ "$(grep -c '^agent send-keys ' "$SF/log")" = "3" ] || fail "Enter retries must stop at 3; log: $(cat "$SF/log")"
[ "$(slot_field w1 unsent)" = "True" ] || fail "a stalled dispatch must mark the slot unsent, got $(slot_field w1 unsent)"
pass "Enter retries are bounded; a stall marks the slot unsent"

# (c) the resend submits what is on the prompt line and types nothing.
echo working > "$SF/wait_status"
: >"$SF/log"
RC=0; su hvj worker dispatch w1 --body-file "$TMP_SU/brief.md" --relay >/dev/null 2>&1 || RC=$?
[ "$RC" = "0" ] || fail "the resend must submit the pending brief, got exit $RC"
grep -q '^agent prompt ' "$SF/log" && fail "a resend must not type the brief again; log: $(cat "$SF/log")"
[ "$(slot_field w1 unsent)" = "None" ] || fail "a submitted brief must clear the unsent mark"
pass "a resend submits the pending brief without retyping it"

trap 'rm -rf "$TMP"' EXIT
