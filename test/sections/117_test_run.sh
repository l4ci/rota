echo "test run: executes a config test tier (#375)"

TMP_TT="$(mktemp -d "$TMP/testrun.XXXXXX")"
TTP="$TMP_TT/proj"
mkdir -p "$TTP/.rota"
(
  cd "$TTP" && git init -q -b main . && git config user.email t@t && git config user.name t \
    && echo seed > seed.txt && git add seed.txt && git commit -q -m seed
) || fail "test-run fixture repo setup failed"
ttcfg() { printf '%s\n' "$1" > "$TTP/.rota/config.json"; }
ttrun() { ( cd "$TTP" && "$ROTA_BIN" --json "$@" 2>/dev/null ); }

ttcfg '{"test":{"fast":["true"]}}'
RC=0; OUT=$(ttrun test run fast) || RC=$?
[ "$RC" = "0" ] && [ "$(echo "$OUT" | jget data.tier)" = "fast" ] || fail "a passing tier should exit 0: rc=$RC $OUT"
pass "a passing tier exits 0"

ttcfg '{"test":{"fast":["true","false","touch never.txt"]}}'
RC=0; OUT=$(ttrun test run fast) || RC=$?
[ "$RC" != "0" ] && [ "$(echo "$OUT" | jget data.failed)" = '["false"]' ] && [ ! -f "$TTP/never.txt" ] \
  || fail "a failing tier should stop at the first failure: rc=$RC $OUT"
rm -f "$(echo "$OUT" | jget data.logPath)"
pass "a failing command fails the tier and stops it"

ttcfg '{"test":{}}'
RC=0; OUT=$(ttrun test run e2e) || RC=$?
[ "$RC" = "3" ] || fail "an empty tier should exit 3: rc=$RC $OUT"
RC=0; OUT=$(ttrun test run bogus) || RC=$?
[ "$RC" = "2" ] || fail "an unknown tier should exit 2: rc=$RC $OUT"
pass "an empty tier exits 3, an unknown tier 2"

ttcfg '{"test":{"fast":["echo FILES:{files}"]}}'
( cd "$TTP" && git checkout -q -b feat && echo new > "a b.txt" && git add "a b.txt" && git commit -q -m add )
RC=0; OUT=$(ttrun test run fast --base main) || RC=$?
[ "$RC" = "0" ] && case "$OUT" in *"echo FILES:'a b.txt'"*) ;; *) false ;; esac \
  || fail "{files} should expand to the changed files, quoted: rc=$RC $OUT"
pass "{files} expands to the files changed against base"
