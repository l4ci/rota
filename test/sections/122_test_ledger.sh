echo "test ledger: exclusion ledger of known-red tests (#387)"

TMP_TL="$(mktemp -d "$TMP/testledger.XXXXXX")"
TLP="$TMP_TL/proj"
mkdir -p "$TLP/.rota"
printf '{}\n' > "$TLP/.rota/config.json"
tlcheck() { ( cd "$TLP" && "$ROTA_BIN" --json test ledger check 2>/dev/null ); }
tlwrite() { printf '%s\n' "$1" > "$TLP/.rota/test-ledger.json"; }

RC=0; OUT=$(tlcheck) || RC=$?
[ "$RC" = "0" ] && [ "$(echo "$OUT" | jget data.verdict)" = "ok" ] || fail "a missing ledger should be ok: rc=$RC $OUT"
tlwrite '[]'
RC=0; OUT=$(tlcheck) || RC=$?
[ "$RC" = "0" ] && [ "$(echo "$OUT" | jget data.entries)" = "0" ] || fail "an empty ledger should be ok: rc=$RC $OUT"
pass "a missing or empty ledger means no exclusions"

tlwrite '[{"test":"TestFlaky","owner":"dana","receipt":"#378","expires":"2999-01-01"}]'
RC=0; OUT=$(tlcheck) || RC=$?
[ "$RC" = "0" ] && [ "$(echo "$OUT" | jget data.entries)" = "1" ] || fail "an unexpired entry should be ok: rc=$RC $OUT"
pass "an unexpired entry passes the check"

tlwrite '[{"test":"TestFlaky","owner":"dana","receipt":"#378","expires":"2000-01-01"}]'
RC=0; OUT=$(tlcheck) || RC=$?
[ "$RC" = "1" ] && [ "$(echo "$OUT" | jget data.verdict)" = "expired" ] && [ "$(echo "$OUT" | jget data.expired[0].owner)" = "dana" ] \
  || fail "an expired entry should exit 1 and be named: rc=$RC $OUT"
pass "an expired entry exits 1 and names its owner"

tlwrite '[{"test":"TestFlaky","owner":"dana"}]'
RC=0; OUT=$(tlcheck) || RC=$?
[ "$RC" = "1" ] && [ "$(echo "$OUT" | jget data.verdict)" = "malformed" ] || fail "a malformed entry should exit 1: rc=$RC $OUT"
pass "a malformed entry exits 1 with verdict malformed"
