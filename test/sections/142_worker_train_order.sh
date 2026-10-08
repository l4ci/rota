echo "worker train: orders members by shared paths and diff size, prints why, --order overrides, an unavoidable conflict stops before landing (#583)"
# Local slots only, as section 111: no PRs, no forge.

TMP_TO="$(mktemp -d)"
TOPROJ="$TMP_TO/proj"
mkdir -p "$TOPROJ/.rota"
(
  cd "$TOPROJ" && git init -q -b main . && git config user.email t@t && git config user.name t \
    && seq 1 20 > shared.txt && echo seed > seed.txt && git add shared.txt seed.txt && git commit -q -m seed
  git checkout -q -b o1 main && seq 1 20 | sed '1,5s/^/A/' > shared.txt && git commit -q -am o1
  git checkout -q -b o2 main && seq 1 20 | sed '20s/^/Z/' > shared.txt && git commit -q -am o2
  git checkout -q -b o3 main && echo o3 > o3.txt && git add o3.txt && git commit -q -m o3
  git checkout -q -b c1 main && echo c1 > clash.txt && git add clash.txt && git commit -q -m c1
  git checkout -q -b c2 main && echo c2 > clash.txt && git add clash.txt && git commit -q -m c2
  git checkout -q main
) || fail "train order fixture repo setup failed"
printf '{"slots":[{"name":"o1","branch":"o1"},{"name":"o2","branch":"o2"},{"name":"o3","branch":"o3"},{"name":"c1","branch":"c1"},{"name":"c2","branch":"c2"}]}\n' > "$TOPROJ/.rota/workers.json"
printf '{"test":{"full":["true"]}}\n' > "$TOPROJ/.rota/config.json"
tor() { ( cd "$TOPROJ" && PATH="$TESTDIR/fakes:$ROTA_POISON_BIN:$PATH" "$ROTA_BIN" --json "$@" 2>"$TMP_TO/err" ); }

# --order must name every member exactly once: exit 2, nothing landed.
RC=0; tor worker train o1 o2 o3 --base main --order o1,o2 >/dev/null || RC=$?
[ "$RC" = "2" ] && [ ! -f "$TOPROJ/o3.txt" ] || fail "--order missing a member should exit 2 and land nothing: rc=$RC"
RC=0; tor worker train o1 o2 o3 --base main --order o1,o2,o2 >/dev/null || RC=$?
[ "$RC" = "2" ] || fail "--order repeating a member should exit 2: rc=$RC"
pass "--order that does not name every member once is a usage error"

# The computed order puts the isolated member first, then the smaller shared diff, and says why.
RC=0; OUT=$(tor worker train o1 o2 o3 --base main) || RC=$?
[ "$RC" = "0" ] && [ "$(echo "$OUT" | jget 'data.order[0]')" = "o3: no shared paths" ] \
  || fail "the isolated member should go first: rc=$RC $OUT"
case "$(echo "$OUT" | jget 'data.order[1]')" in "o2: shares 1 paths with o1") ;; *) fail "o2 (smaller diff) should come second: $OUT" ;; esac
case "$(cat "$TMP_TO/err")" in *"ORDER o3: no shared paths"*"ORDER o2: shares 1 paths with o1"*) ;; *) fail "the order report should print on stderr" ;; esac
[ "$(echo "$OUT" | jget 'data.landed[0]')" = "o3" ] || fail "o3 should land first: $OUT"
pass "the train orders isolated members first, then the smallest shared diff, and prints the reason"

# A conflict under every order is exit 4, blockedBy order, nothing landed.
RC=0; OUT=$(tor worker train c1 c2 --base main) || RC=$?
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.blockedBy)" = "order" ] && [ "$(echo "$OUT" | jget data.changed)" = "false" ] \
  && [ ! -f "$TOPROJ/clash.txt" ] || fail "an unavoidable conflict should stop with blockedBy order: rc=$RC $OUT"
pass "a train that conflicts under every order stops before landing"

rm -rf "${TMP_TO:?}"
