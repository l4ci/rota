echo "receipt show: a lost response is answered from .rota/receipts.json as completed, pending or absent (#959)"

RS="$(mktemp -d "$TMP/receipt-show.XXXXXX")"
(
  cd "$RS" && git init -q -b main . && git config user.email a@b && git config user.name n \
    && "$ROTA_BIN" init >/dev/null 2>&1 \
    && git add -A . && git commit -q -m init
) || fail "receipt show fixture setup failed"
cat > "$RS/.rota/receipts.json" <<'JSON'
{"receipts": [
  {"id": "req-done", "verb": "round assign", "fingerprint": "f", "status": "done", "attempts": 1, "text": "assigned 42", "data": {"issue": "42", "changed": true}, "at": "2026-01-02T03:04:05Z"},
  {"id": "req-failed", "verb": "worker gate", "fingerprint": "f", "status": "failed", "attempts": 2, "exit": 5, "message": "gh: 502", "failedStage": "merge", "residualResources": ["branch"], "at": "2026-01-02T03:04:06Z"}
]}
JSON
rs() { ( cd "$RS" && "$ROTA_BIN" --json receipt show "$@" 2>/dev/null ); }
BEFORE=$(cksum < "$RS/.rota/receipts.json")

# (a) completed carries the stored result
rc=0; OUT=$(rs req-done) || rc=$?
[ "$rc" -eq 0 ] || fail "completed should exit 0, got $rc: $OUT"
[ "$(echo "$OUT" | jget data.state)" = "completed" ] || fail "want completed: $OUT"
[ "$(echo "$OUT" | jget data.result.issue)" = "42" ] || fail "completed should return the stored result: $OUT"
[ "$(echo "$OUT" | jget data.verb)" = "round assign" ] || fail "completed should name the verb: $OUT"
pass "completed returns the stored result"

# (b) pending names the stage the retry resumes from
rc=0; OUT=$(rs req-failed) || rc=$?
[ "$rc" -eq 0 ] || fail "pending should exit 0, got $rc: $OUT"
[ "$(echo "$OUT" | jget data.state)" = "pending" ] || fail "want pending: $OUT"
[ "$(echo "$OUT" | jget data.failedStage)" = "merge" ] || fail "pending should name the failed stage: $OUT"
pass "pending names the failed stage"

# (c) absent is an answer, and its text says it is not proof
rc=0; OUT=$(rs req-never) || rc=$?
[ "$rc" -eq 0 ] || fail "absent should exit 0, got $rc: $OUT"
[ "$(echo "$OUT" | jget data.state)" = "absent" ] || fail "want absent: $OUT"
TXT=$( cd "$RS" && "$ROTA_BIN" receipt show req-never 2>/dev/null ) || vfail
grep -q "not proof" <<<"$TXT" || fail "absent should say it is not proof nothing happened: $TXT"
pass "absent exits 0 and is documented as not proof"

# (d) no id is a usage error; a corrupt file is exit 5, not absent
rc=0; ( cd "$RS" && "$ROTA_BIN" receipt show >/dev/null 2>&1 ) || rc=$?
[ "$rc" -eq 2 ] || fail "a missing id should exit 2, got $rc"
[ "$(cksum < "$RS/.rota/receipts.json")" = "$BEFORE" ] || fail "receipt show must not write the receipts file"
printf '{not json' > "$RS/.rota/receipts.json"
rc=0; rs req-done >/dev/null || rc=$?
[ "$rc" -eq 5 ] || fail "a corrupt receipts file should exit 5, got $rc"
pass "usage error, read-only, corrupt file refused"
