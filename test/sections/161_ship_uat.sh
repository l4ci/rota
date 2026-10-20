echo "ship.uat: opt-in, off by default, never read by worker gate (#924)"

UP="$(mktemp -d "$TMP/ship-uat.XXXXXX")"
(
  cd "$UP" && git init -q -b main . && git config user.email t@t && git config user.name t \
    && echo seed > seed.txt && git add seed.txt && git commit -q -m seed \
    && git checkout -q -b u1 main && echo u1 > u1.txt && git add u1.txt && git commit -q -m "add u1" && git checkout -q main
) || fail "ship.uat fixture repo setup failed"
mkdir -p "$UP/.rota"
printf '{"slots":[{"name":"u1","branch":"u1"}]}\n' > "$UP/.rota/workers.json"
uat() { ( cd "$UP" && PATH="$TESTDIR/fakes:$ROTA_POISON_BIN:$PATH" "$ROTA_BIN" --json "$@" 2>/dev/null ); }

# (a) default is false, and the key is a bool
printf '{"ship":{"review":"none"},"test":{"full":[]}}\n' > "$UP/.rota/config.json"
[ "$(uat config show ship.uat | jget data.entries[0].value)" = "false" ] \
  || fail "ship.uat should default to false: $(uat config show ship.uat)"
pass "ship.uat defaults to false"

# (b) worker gate output is identical with the key off and on
RC_OFF=0; OFF=$(uat worker gate u1 --base main) || RC_OFF=$?
printf '{"ship":{"review":"none","uat":true},"test":{"full":[]}}\n' > "$UP/.rota/config.json"
RC_ON=0; ON=$(uat worker gate u1 --base main) || RC_ON=$?
[ "$RC_OFF" = "4" ] || fail "fixture gate should refuse with 4 (empty test.full): rc=$RC_OFF $OFF"
[ "$RC_OFF" = "$RC_ON" ] && [ "$OFF" = "$ON" ] \
  || fail "worker gate output must not change with ship.uat on: off=[$RC_OFF $OFF] on=[$RC_ON $ON]"
[ ! -f "$UP/u1.txt" ] || fail "a refused gate must land nothing"
pass "worker gate output is identical with ship.uat on"

# (c) the skills wire it: uat mode exists, /rota-ship gates on it, workers skip it
ROOT_UAT="$REPO"
grep -q '(human)' "$ROOT_UAT/skills/rota-qa/uat.md" || fail "uat.md should name the (human) token"
grep -q 'ship.uat' "$ROOT_UAT/skills/rota-ship/opt-in-gates.md" || fail "opt-in-gates.md should read ship.uat"
grep -q 'Step 3.8' "$ROOT_UAT/skills/rota-ship/round-worker-and-issue-mode.md" || fail "round workers should skip Step 3.8"
pass "skills wire uat mode, the ship gate and the worker skip"
