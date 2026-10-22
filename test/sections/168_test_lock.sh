echo "test lock: a second heavy run waits for the first (#1815)"

TMP_TL="$(mktemp -d "$TMP/testlock.XXXXXX")"
TLP="$TMP_TL/proj"
mkdir -p "$TLP/.rota"
(
  cd "$TLP" && git init -q -b main . && git config user.email t@t && git config user.name t \
    && echo seed > seed.txt && git add seed.txt && git commit -q -m seed
) || fail "test-lock fixture repo setup failed"
# The gate runs this suite inside its own lock slot and marks it held; the fixture is a
# different repo, but strip the mark so these runs queue the way a developer's do.
tlrun() { ( cd "$TLP" && env -u ROTA_TEST_LOCK_HELD "$ROTA_BIN" "$@" ); }

printf '%s\n' '{"test":{"fast":["sleep 2"]}}' > "$TLP/.rota/config.json"
tlrun test run fast >/dev/null 2>"$TMP_TL/first.err" &
FIRST=$!
# Wait until the first run records itself as the holder.
for _ in $(seq 1 100); do
  [ -s "$TLP/.git/rota/test-0.lock" ] && break
  sleep 0.1
done
[ -s "$TLP/.git/rota/test-0.lock" ] || fail "the first run never took the test lock"
RC=0; tlrun test run fast >/dev/null 2>"$TMP_TL/second.err" || RC=$?
wait "$FIRST" || fail "the first run should pass"
[ "$RC" = "0" ] || fail "the waiting run should pass once it gets the slot, got $RC"
grep -q 'waiting for the test lock (held by [0-9]* test run)' "$TMP_TL/second.err" \
  || fail "the second run should say it waits for the test lock: $(cat "$TMP_TL/second.err")"
[ ! -s "$TMP_TL/first.err" ] || fail "the first run held a free lock and should print nothing: $(cat "$TMP_TL/first.err")"
pass "a second rota test run waits and says so on stderr"

# test.slots 2: two runs hold the lock at once, neither waits.
printf '%s\n' '{"test":{"slots":2,"fast":["sleep 2"]}}' > "$TLP/.rota/config.json"
tlrun test run fast >/dev/null 2>"$TMP_TL/a.err" &
A=$!
tlrun test run fast >/dev/null 2>"$TMP_TL/b.err" &
B=$!
wait "$A" && wait "$B" || fail "both runs should pass under test.slots 2"
[ ! -s "$TMP_TL/a.err" ] && [ ! -s "$TMP_TL/b.err" ] || fail "test.slots 2 should not make either run wait: $(cat "$TMP_TL/a.err" "$TMP_TL/b.err")"
pass "test.slots 2 lets two runs hold the lock together"

# The verb the gate uses: the command runs holding a slot and its exit status comes back.
RC=0; tlrun test lock -- sh -c 'exit 7' >/dev/null 2>&1 || RC=$?
[ "$RC" = "7" ] || fail "test lock should return the command's exit status, got $RC"
pass "rota test lock returns the command's exit status"
