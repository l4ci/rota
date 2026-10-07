echo "proof record: runs the check and records it (#393)"

TMP_PR="$(mktemp -d "$TMP/proofrec.XXXXXX")"
PRP="$TMP_PR/proj"
mkdir -p "$PRP/.rota"
printf '# Backlog\n\n## Bugs\n\n- **[B07] [Major] Parser drops foo.** Small.\n' > "$PRP/.rota/BACKLOG.md"
(
  cd "$PRP" && git init -q -b main . && git config user.email t@t && git config user.name t \
    && echo seed > seed.txt && git add seed.txt .rota/BACKLOG.md && git commit -q -m seed
) || fail "proof-record fixture repo setup failed"
prrun() { ( cd "$PRP" && "$ROTA_BIN" --json "$@" 2>/dev/null ); }

RC=0; OUT=$(prrun proof record B07 -- "echo hi") || RC=$?
[ "$RC" = "0" ] && [ "$(echo "$OUT" | jget data.result)" = "PASS" ] && [ "$(echo "$OUT" | jget data.check)" = "echo hi" ] \
  || fail "a passing command should record PASS and exit 0: rc=$RC $OUT"
HEAD_SHA=$(cd "$PRP" && git log -1 --format=%h)
[ "$(echo "$OUT" | jget data.sha)" = "$HEAD_SHA" ] || fail "the row's sha should be HEAD: $OUT"
pass "a passing command is recorded PASS at HEAD, exit 0"

RC=0; OUT=$(prrun proof record B07 -- "sh -c 'exit 3'") || RC=$?
[ "$RC" = "1" ] && [ "$(echo "$OUT" | jget data.result)" = "FAIL" ] && [ "$(echo "$OUT" | jget data.exitCode)" = "3" ] \
  || fail "a failing command should record FAIL and exit 1: rc=$RC $OUT"
pass "a failing command is recorded FAIL, exit 1"

prrun proof record B07 -- "echo hi" > /dev/null || true
[ "$(prrun proof show B07 | jget data.count)" = "2" ] || fail "an identical re-run should not add a row"
pass "an identical re-run does not duplicate the row"

( cd "$PRP" && git checkout -q -b feat && echo new > "a b.txt" && git add "a b.txt" && git commit -q -m add )
OUT=$(prrun proof record B07 --base main -- "echo FILES:{files}") || fail "{files} record failed: $OUT"
case "$(echo "$OUT" | jget data.check)" in *"echo FILES:'a b.txt'"*) ;; *) fail "{files} should show expanded in the recorded check: $OUT" ;; esac
pass "{files} is recorded as expanded"

RC=0; prrun proof record B99 -- "touch ran.txt" > /dev/null || RC=$?
[ "$RC" = "3" ] && [ ! -f "$PRP/ran.txt" ] || fail "an unknown item should exit 3 without running: rc=$RC"
RC=0; prrun proof record B07 > /dev/null || RC=$?
[ "$RC" = "2" ] || fail "a missing command should exit 2: rc=$RC"
pass "an unknown item exits 3, a missing command 2"
