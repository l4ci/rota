echo "plan check: criteria map to tasks, tasks carry a Verify (#396)"

TMP_PC="$(mktemp -d "$TMP/plancheck.XXXXXX")"
(
  P="$TMP_PC/proj"; mkdir -p "$P/.rota"
  echo '{"issues":{"source":"github"}}' > "$P/.rota/config.json"
  cd "$P"
  git init -q && git config user.email t@t && git config user.name t && git commit -q --allow-empty -m seed
  export PATH="$TESTDIR/fakes:$PATH" FAKE_TRACKER_DB="$P/db.json" FAKE_TRACKER_LOG="$P/log"
  eq() { [ "$2" = "$3" ] || fail "$1: expected [$2] got [$3]"; }
  RC() { local rc=0; "$@" >/dev/null 2>&1 || rc=$?; echo "$rc"; }

  printf 'Why.\n\n## Acceptance\n\n- [ ] it parses\n- [ ] it prints\n' > "$P/body.md"
  ID="$(hvj item create --kind features --title "Export" --tag Minor --body-file "$P/body.md" | jget data.id)" || vfail
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
  SHOWN="$(hvj plan show "$KEY")" || vfail
  if grep -q "Serves:" <<<"$SHOWN"; then fail "legacy plan should have no Serves line"; fi
  eq "legacy plan reads as orphans" "1 T1" "$(RC hvj plan check "$KEY") $(jget data.orphans[0] <<<"$(hvj plan check "$KEY" 2>/dev/null)")"
  pass "legacy plan (no Serves: line) is detectable, so /rota-work skips the check"

  PUT '- **T1** — a
  - Serves: AC-1
  - Files: a, b, c, d, e, f
  - Verify: go test ./a
- **T2** — b
  - Serves: AC-2
  - Verify: go test ./b
- **T3** — c
  - Serves: AC-2
  - Verify: go test ./c
- **T4** — d
  - Serves: AC-2
  - Verify: go test ./d'
  OUT="$(hvj plan check "$KEY")" || fail "size warnings must not fail the check: $OUT"
  eq "size ok" "true" "$(jget data.ok <<<"$OUT")"
  jget data.warnings[1] <<<"$OUT" > /dev/null || fail "want two warnings in data.warnings: $OUT"
  jget data.warnings[2] <<<"$OUT" > /dev/null && fail "want only two warnings in data.warnings: $OUT"
  TXT="$("$ROTA_BIN" plan check "$KEY")" || fail "text mode must exit 0 too: $TXT"
  grep -q '^warn: plan has 4 tasks' <<<"$TXT" || fail "no tasks warn line: $TXT"
  grep -q '^warn: T1 names 6 files' <<<"$TXT" || fail "no files warn line: $TXT"
  hvj config set plan.maxTasks 0 > /dev/null || fail "config set plan.maxTasks"
  hvj config set plan.maxFilesPerTask 0 > /dev/null || fail "config set plan.maxFilesPerTask"
  OUT="$(hvj plan check "$KEY")" || vfail
  eq "size off" "[]" "$(jget data.warnings <<<"$OUT")"
  eq "negative rejected" "1" "$([ "$(RC hvj config set plan.maxTasks -1)" != 0 ] && echo 1)"
  hvj config set plan.maxTasks 3 > /dev/null; hvj config set plan.maxFilesPerTask 5 > /dev/null
  pass "plan check warns on too many tasks or too wide a task, exit 0; a threshold of 0 turns it off"

  PUT '- **T1** — parse
  - Serves: AC-1, AC-2
  - Verify: go test ./parse'
  OUT="$(hvj plan check "$KEY")" || fail "no decision comment, same result as before: $OUT"
  jget data.decisions <<<"$OUT" > /dev/null 2>&1 && fail "no decision comment must add no decisions key: $OUT"
  grep -q 'D1' <<<"$("$ROTA_BIN" plan check "$KEY")" && fail "no decision comment must add no text output"
  pass "plan check: an item with no decision comment gets no new output"

  printf 'Use JSON, never YAML, for the export format because tooling already reads it\n' | hvj item comment add "$KEY" --kind decision --body-file - > /dev/null || fail "add decision 1"
  printf 'Keep it read-only\n' | hvj item comment add "$KEY" --kind decision --body-file - > /dev/null || fail "add decision 2"
  OUT="$(hvj plan check "$KEY")" && fail "uncovered decisions should fail: $OUT"
  eq "uncovered exit" "1" "$(RC hvj plan check "$KEY")"
  eq "uncovered decisions" "D1 D2" "$(jget data.uncoveredDecisions[0] <<<"$OUT") $(jget data.uncoveredDecisions[1] <<<"$OUT")"
  eq "decision printed, 60 chars" "D1: Use JSON, never YAML, for the export format because tooling " "$(jget data.decisions[0] <<<"$OUT")"
  eq "decision 2 printed" "D2: Keep it read-only" "$(jget data.decisions[1] <<<"$OUT")"
  TXT="$("$ROTA_BIN" plan check "$KEY" 2>&1)" || true
  grep -q '^D2 is honored by no task' <<<"$TXT" || fail "no uncovered text line: $TXT"
  grep -q '^D1: Use JSON' <<<"$TXT" || fail "decision not listed in text: $TXT"

  PUT '- **T1** — parse
  - Serves: AC-1, AC-2
  - Honors: D1
  - Verify: go test ./parse'
  OUT="$(hvj plan check "$KEY")" && fail "D2 still uncovered"
  eq "only D2 left" "D2" "$(jget data.uncoveredDecisions[0] <<<"$OUT")"

  PUT '- **T1** — parse
  - Serves: AC-1, AC-2
  - Honors: D1, D2, D5
  - Verify: go test ./parse'
  OUT="$(hvj plan check "$KEY")" && fail "unknown decision should fail"
  eq "unknown decision" "T1 D5" "$(jget data.unknownDecisions[0].task <<<"$OUT") $(jget data.unknownDecisions[0].ids[0] <<<"$OUT")"

  PUT '- **T1** — parse
  - Serves: AC-1
  - Honors: D1
  - Verify: go test ./parse
- **T2** — print
  - Serves: AC-2
  - Honors: D2
  - Verify: go test ./print'
  OUT="$(hvj plan check "$KEY")" || fail "all decisions honored should pass: $OUT"
  eq "honored ok" "true" "$(jget data.ok <<<"$OUT")"
  pass "plan check numbers decision comments, fails on uncovered or unknown Honors ids (exit 1)"

  eq "no plan" "3" "$(RC hvj plan check "#9999")"
  eq "slice key" "2" "$(RC hvj plan check M01-S01)"
  eq "no arg" "2" "$(RC hvj plan check)"
  before="$(cat "$P/db.json")"
  hvj plan check "$KEY" > /dev/null 2>&1 || true
  eq "read-only" "$before" "$(cat "$P/db.json")"
  pass "plan check: missing plan 3, slice key 2, never writes"

  F="$TMP_PC/filemode"; mkdir -p "$F/.rota"
  echo '{"issues":{"source":"file"}}' > "$F/.rota/config.json"
  cd "$F"
  git init -q && git config user.email t@t && git config user.name t && git commit -q --allow-empty -m seed
  OUT="$(hvj plan check "#1" 2>/dev/null)" && fail "file backend should fail"
  eq "file backend" "1 backend" "$(RC hvj plan check "#1") $(jget data.blockedBy <<<"$OUT")"
  pass "plan check under the file backend exits 1 with blockedBy backend, never 4"
)
