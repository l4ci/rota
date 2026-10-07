echo "plan check: criteria map to tasks, tasks carry a Verify (#396)"

TMP_PC="$(mktemp -d "$TMP/plancheck.XXXXXX")"
(
  P="$TMP_PC/proj"; mkdir -p "$P/.rota"
  echo '{"backlog":{"backend":"issues"},"issues":{"provider":"github","retryWaitSeconds":0}}' > "$P/.rota/config.json"
  cd "$P"
  git init -q && git config user.email t@t && git config user.name t && git commit -q --allow-empty -m seed
  export PATH="$TESTDIR/fakes:$PATH" FAKE_TRACKER_DB="$P/db.json" FAKE_TRACKER_LOG="$P/log"
  eq() { [ "$2" = "$3" ] || fail "$1: expected [$2] got [$3]"; }
  RC() { local rc=0; "$@" >/dev/null 2>&1 || rc=$?; echo "$rc"; }

  printf 'Why.\n\n## Acceptance\n\n- [ ] it parses\n- [ ] it prints\n' > "$P/body.md"
  ID="$(hvj item create --kind features --title "Export" --tag Minor --body-file "$P/body.md" | jget data.id)"
  KEY="#$ID"
  hvj plan add "$KEY" --title "Export plan" > /dev/null || fail "plan add"
  PUT() { printf '## Goal\n\ng\n\n## Tasks\n\n%s\n' "$1" > "$P/plan.md"; hvj plan put "$KEY" --body-file "$P/plan.md" > /dev/null || fail "plan put"; }

  PUT '- **T1** — parse
  - Serves: AC-1
  - Verify: go test ./parse
- **T2** — print
  - Serves: AC-2
  - Verify: go test ./print'
  OUT="$(hvj plan check "$KEY")" || fail "covered plan should pass: $OUT"
  eq "covered ok" "true" "$(jget data.ok <<<"$OUT")"
  pass "plan check passes when every criterion has a task and every task a Verify"

  PUT '- **T1** — parse
  - Serves: AC-1
  - Verify: go test ./parse'
  OUT="$(hvj plan check "$KEY")" && fail "uncovered criterion should fail"
  eq "uncovered exit" "1" "$(RC hvj plan check "$KEY")"
  eq "uncovered" "AC-2" "$(jget data.uncovered[0] <<<"$OUT")"

  PUT '- **T1** — parse
  - Serves: AC-1, AC-2
  - Verify: go test ./parse
- **T2** — extra
  - Verify: go test ./x'
  OUT="$(hvj plan check "$KEY")" && fail "orphan task should fail"
  eq "orphan" "T2" "$(jget data.orphans[0] <<<"$OUT")"

  PUT '- **T1** — parse
  - Serves: AC-1, AC-2'
  OUT="$(hvj plan check "$KEY")" && fail "missing verify should fail"
  eq "noVerify" "T1" "$(jget data.noVerify[0] <<<"$OUT")"

  PUT '- **T1** — parse
  - Serves: AC-1, AC-2, AC-7
  - Verify: go test ./parse'
  OUT="$(hvj plan check "$KEY")" && fail "unknown AC should fail"
  eq "unknown" "T1 AC-7" "$(jget data.unknown[0].task <<<"$OUT") $(jget data.unknown[0].ids[0] <<<"$OUT")"
  pass "plan check reports uncovered criteria, orphan tasks, missing Verify and unknown ids (exit 1)"

  PUT '- **T1** — legacy
  - Verify: go test ./parse'
  SHOWN="$(hvj plan show "$KEY")"
  if grep -q "Serves:" <<<"$SHOWN"; then fail "legacy plan should have no Serves line"; fi
  eq "legacy plan reads as orphans" "1 T1" "$(RC hvj plan check "$KEY") $(jget data.orphans[0] <<<"$(hvj plan check "$KEY" 2>/dev/null)")"
  pass "legacy plan (no Serves: line) is detectable, so /rota-work skips the check"

  eq "no plan" "3" "$(RC hvj plan check "#9999")"
  eq "slice key" "2" "$(RC hvj plan check M01-S01)"
  eq "no arg" "2" "$(RC hvj plan check)"
  before="$(cat "$P/db.json")"
  hvj plan check "$KEY" > /dev/null 2>&1 || true
  eq "read-only" "$before" "$(cat "$P/db.json")"
  pass "plan check: missing plan 3, slice key 2, never writes"

  F="$TMP_PC/filemode"; mkdir -p "$F/.rota"
  echo '{"backlog":{"backend":"file"}}' > "$F/.rota/config.json"
  cd "$F"
  git init -q && git config user.email t@t && git config user.name t && git commit -q --allow-empty -m seed
  OUT="$(hvj plan check "#1" 2>/dev/null)" && fail "file backend should fail"
  eq "file backend" "1 backend" "$(RC hvj plan check "#1") $(jget data.blockedBy <<<"$OUT")"
  pass "plan check under the file backend exits 1 with blockedBy backend, never 4"
)
