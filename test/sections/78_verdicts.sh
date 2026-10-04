echo "verdicts: typed review, qa and debug verdicts route in code (B2, #55)"

VD="$(mktemp -d "$TMP/verdicts.XXXXXX")"
(
  cd "$VD" && git init -q -b main && git -c user.email=a@b -c user.name=n commit -q --allow-empty -m init \
    && git switch -q -c feat/v && git -c user.email=a@b -c user.name=n commit -q --allow-empty -m work
) || fail "verdict fixture repo setup failed"
mkdir -p "$VD/.rota"

# Malformed input is exit 2 and writes nothing.
printf '{"findings": [{"severity": "huge", "title": "t"}]}' > "$VD/bad.json"
RC=0; ( cd "$VD" && hvj verdict add --kind qa --verdict FAIL --body-file bad.json >/dev/null 2>&1 ) || RC=$?
[ "$RC" = "2" ] || fail "verdict add with a bad severity should exit 2, got $RC"
RC=0; ( cd "$VD" && hvj verdict add --kind review-spec --verdict INFRA-FAIL >/dev/null 2>&1 ) || RC=$?
[ "$RC" = "2" ] || fail "review-spec INFRA-FAIL should exit 2, got $RC"
[ ! -e "$VD/.rota/verdicts.json" ] || fail "a rejected verdict wrote .rota/verdicts.json"
pass "verdict add rejects malformed bodies and verdicts with exit 2"

# A missing verdict is exit 3 for a consumer.
RC=0; ( cd "$VD" && hvj verdict route --for ship-review >/dev/null 2>&1 ) || RC=$?
[ "$RC" = "3" ] || fail "route with no recorded verdict should exit 3, got $RC"

# Spec CONCERNS then quality PASS combines to CONCERNS; ship asks, loop addresses.
OUT=$( cd "$VD" && hvj verdict add --kind review-spec --verdict CONCERNS )
[ "$(echo "$OUT" | jget data.next)" = "quality" ] || fail "spec CONCERNS should route to quality: $OUT"
printf '{"verdict": "PASS", "summary": "ok", "findings": [{"severity": "minor", "title": "nit", "file": "a.go", "line": 2}]}' > "$VD/q.json"
OUT=$( cd "$VD" && hvj verdict add --kind review-quality --verdict PASS --body-file q.json )
[ "$(echo "$OUT" | jget data.combined)" = "CONCERNS" ] || fail "combined should be the worse stage: $OUT"
[ "$( cd "$VD" && hvj verdict route --for ship-review | jget data.next )" = "ask" ] \
  || fail "CONCERNS outside loop should route to ask"
printf '{"autonomy": {"level": "loop"}}\n' > "$VD/.rota/config.json"
[ "$( cd "$VD" && hvj verdict route --for ship-review | jget data.next )" = "address" ] \
  || fail "CONCERNS in loop should route to address"
pass "review stages combine worst-of and route by autonomy"

# A spec FAIL short-circuits and stops the ship.
OUT=$( cd "$VD" && hvj verdict add --kind review-spec --verdict FAIL )
[ "$(echo "$OUT" | jget data.next)" = "report" ] || fail "spec FAIL should route to report: $OUT"
[ "$( cd "$VD" && hvj verdict route --for ship-review | jget data.next )" = "stop" ] \
  || fail "a newer spec FAIL should stop the ship"

# QA: advisory by default, blocking stops on FAIL, INFRA-FAIL never blocks.
( cd "$VD" && hvj verdict add --kind qa --verdict FAIL >/dev/null ) || fail "qa FAIL add failed"
[ "$( cd "$VD" && hvj verdict route --for ship-qa | jget data.next )" = "surface" ] \
  || fail "advisory qa FAIL should surface"
printf '{"qa": {"gate": "blocking"}}\n' > "$VD/.rota/config.json"
[ "$( cd "$VD" && hvj verdict route --for ship-qa | jget data.next )" = "stop" ] \
  || fail "blocking qa FAIL should stop"
( cd "$VD" && hvj verdict add --kind qa --verdict INFRA-FAIL >/dev/null ) || fail "qa INFRA-FAIL add failed"
[ "$( cd "$VD" && hvj verdict route --for ship-qa | jget data.next )" = "surface" ] \
  || fail "INFRA-FAIL should surface even under a blocking gate"
[ "$( cd "$VD" && hvj verdict show | jget 'data.records[0].kind' )" = "review-spec" ] \
  || fail "verdict show should list review-spec first"
pass "spec FAIL stops the ship; qa routes per qa.gate"

# Debug: three failed fixes on one item halt; another item counts alone.
for want in hypothesize hypothesize halt; do
  OUT=$( cd "$VD" && hvj debug verdict B07 --verdict FAIL )
  [ "$(echo "$OUT" | jget data.next)" = "$want" ] || fail "debug verdict should route to $want: $OUT"
done
[ "$( cd "$VD" && hvj debug verdict B07 --verdict FAIL | jget data.failedFixes )" = "4" ] \
  || fail "failed fixes should keep counting per item"
[ "$( cd "$VD" && hvj debug verdict B08 --verdict PASS | jget data.next )" = "complete" ] \
  || fail "a passing fix on another item should route to complete"
pass "debug verdict counts failed fixes per item and halts at three"
