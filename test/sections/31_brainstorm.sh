echo "## design add / show / rm / list (brainstorm family)"

DSN_TMP="$(mktemp -d)"
trap 'rm -rf "$DSN_TMP"' EXIT
(
  cd "$DSN_TMP"
  mkdir -p .rota

  # 1. design add positive — exit 0, data.id is F00, file lands with id frontmatter
  OUT=$(hvj design add F00 --title "smoke design") || { echo "FAIL: design add F00 exited non-zero"; exit 1; }
  [ "$(jget data.id <<<"$OUT")" = "F00" ] || { echo "FAIL: design add did not report id 'F00', got '$OUT'"; exit 1; }
  [ "$(jget data.type <<<"$OUT")" = "F" ] || { echo "FAIL: design add type should be F: '$OUT'"; exit 1; }
  [ "$(jget data.changed <<<"$OUT")" = "true" ] || { echo "FAIL: design add should report changed: '$OUT'"; exit 1; }
  [ -f .rota/designs/F00.md ] || { echo "FAIL: .rota/designs/F00.md not created"; exit 1; }
  grep -q "^id: F00$" .rota/designs/F00.md || { echo "FAIL: frontmatter missing id: F00"; exit 1; }
  grep -q "^title: smoke design$" .rota/designs/F00.md || { echo "FAIL: frontmatter missing title"; exit 1; }

  # 2. design add rejects slice-shape ID — exit 2 (usage)
  rc=0; hvj design add S01 --title "x" >/dev/null 2>&1 || rc=$?
  [ "$rc" = "2" ] || { echo "FAIL: S01 should be rejected with exit 2, got $rc"; exit 1; }

  # 3. design add rejects milestone-shape ID — exit 2
  rc=0; hvj design add M01 --title "x" >/dev/null 2>&1 || rc=$?
  [ "$rc" = "2" ] || { echo "FAIL: M01 should be rejected with exit 2, got $rc"; exit 1; }

  # 4. design add conflict — exit 4 (refused), existing design untouched
  rc=0; OUT=$(hvj design add F00 --title "again" 2>/dev/null) || rc=$?
  [ "$rc" = "4" ] || { echo "FAIL: conflict on existing F00 should exit 4, got $rc"; exit 1; }
  [ "$(jget data.blockedBy <<<"$OUT")" = "exists" ] || { echo "FAIL: conflict blockedBy should be exists: '$OUT'"; exit 1; }
  grep -q "^title: smoke design$" .rota/designs/F00.md || { echo "FAIL: conflict overwrote the design"; exit 1; }

  # 5. design show positive — exit 0, body carries the id frontmatter (text mode prints it verbatim)
  OUT=$("$ROTA_BIN" design show F00) || { echo "FAIL: design show F00 exited non-zero"; exit 1; }
  grep -q "^id: F00$" <<<"$OUT" || { echo "FAIL: design show did not print id: F00"; exit 1; }
  OUT=$(hvj design show F00 | jget data.body) || { echo "FAIL: design show --json exited non-zero"; exit 1; }
  grep -q "^id: F00$" <<<"$OUT" || { echo "FAIL: design show data.body missing id: F00"; exit 1; }

  # 6. design show miss — exit 3
  rc=0; hvj design show F99 >/dev/null 2>&1 || rc=$?
  [ "$rc" = "3" ] || { echo "FAIL: design show F99 (missing) should exit 3, got $rc"; exit 1; }

  # 7. design list non-empty — one F00 entry
  OUT=$(hvj design list) || { echo "FAIL: design list exited non-zero"; exit 1; }
  python3 -c "
import json, sys
data = json.load(sys.stdin)['data']['designs']
assert isinstance(data, list), f'expected list, got {type(data).__name__}'
assert len(data) == 1, f'expected 1 entry, got {len(data)}: {data}'
assert data[0]['id'] == 'F00', f'id wrong: {data[0]}'
assert data[0]['title'] == 'smoke design', f'title wrong: {data[0]}'
assert data[0]['status'] == 'draft', f'status wrong: {data[0]}'
" <<<"$OUT" || { echo "FAIL: design list JSON shape wrong"; exit 1; }

  # 8. design rm positive — exit 0, file removed
  hvj design rm F00 >/dev/null || { echo "FAIL: design rm F00 exited non-zero"; exit 1; }
  [ ! -f .rota/designs/F00.md ] || { echo "FAIL: .rota/designs/F00.md still present after rm"; exit 1; }

  # 9. design rm miss — exit 3
  rc=0; hvj design rm F99 >/dev/null 2>&1 || rc=$?
  [ "$rc" = "3" ] || { echo "FAIL: design rm F99 (missing) should exit 3, got $rc"; exit 1; }

  # 10. design list empty after rm — designs is []
  OUT=$(hvj design list) || { echo "FAIL: design list (empty) exited non-zero"; exit 1; }
  [ "$(jget data.designs <<<"$OUT")" = "[]" ] || { echo "FAIL: design list did not report [] after rm: '$OUT'"; exit 1; }
) || fail "design family assertions"
trap 'rm -rf "$TMP"' EXIT
rm -rf "$DSN_TMP"
pass "design add / show / rm / list: writer + resolve + lookup contracts hold under happy + error paths"

echo "## design amend (T110 — section amend via shared artifact-amend lib)"

AMD_TMP="$(mktemp -d)"
trap 'rm -rf "$AMD_TMP"' EXIT
(
  cd "$AMD_TMP"
  mkdir -p .rota

  hvj design add F00 --title "amend smoke" >/dev/null || { echo "FAIL: seeding design failed"; exit 1; }

  # a. replace on an existing section — exit 0; new text shows, old placeholder gone, other section untouched
  printf 'Ship the amend helper.\n' > body-replace.md
  OUT=$(hvj design amend F00 --section Goal --mode replace --body-file body-replace.md) \
    || { echo "FAIL: design amend --mode replace exited non-zero"; exit 1; }
  [ "$(jget data.changed <<<"$OUT")" = "true" ] || { echo "FAIL: amend replace should report changed: '$OUT'"; exit 1; }
  OUT=$("$ROTA_BIN" design show F00) || { echo "FAIL: show after replace exited non-zero"; exit 1; }
  grep -q "Ship the amend helper." <<<"$OUT" || { echo "FAIL: replaced Goal text missing"; exit 1; }
  if grep -q "one sentence — what shipping" <<<"$OUT"; then echo "FAIL: old Goal placeholder still present after replace"; exit 1; fi
  grep -q "the chosen shape, the moving parts" <<<"$OUT" || { echo "FAIL: Design section was clobbered by Goal replace"; exit 1; }

  # b. append to a section — exit 0; both prior content and appended text present; next heading intact
  printf 'And keep it atomic.\n' > body-append.md
  hvj design amend F00 --section Goal --mode append --body-file body-append.md >/dev/null \
    || { echo "FAIL: design amend --mode append exited non-zero"; exit 1; }
  OUT=$("$ROTA_BIN" design show F00) || { echo "FAIL: show after append exited non-zero"; exit 1; }
  grep -q "Ship the amend helper." <<<"$OUT" || { echo "FAIL: prior Goal content lost after append"; exit 1; }
  grep -q "And keep it atomic." <<<"$OUT" || { echo "FAIL: appended Goal text missing"; exit 1; }
  grep -q "^## Design$" <<<"$OUT" || { echo "FAIL: ## Design heading not intact after append"; exit 1; }

  # c. non-existent section — exit 3
  rc=0; hvj design amend F00 --section Nope --mode replace --body-file body-replace.md >/dev/null 2>&1 || rc=$?
  [ "$rc" = "3" ] || { echo "FAIL: amend bogus section should exit 3, got $rc"; exit 1; }

  # d. missing design ID (no file) — exit 3
  rc=0; hvj design amend F99 --section Goal --mode replace --body-file body-replace.md >/dev/null 2>&1 || rc=$?
  [ "$rc" = "3" ] || { echo "FAIL: amend missing design should exit 3, got $rc"; exit 1; }

  # e. bad ID shape — exit 2
  rc=0; hvj design amend S01 --section Goal --mode replace --body-file body-replace.md >/dev/null 2>&1 || rc=$?
  [ "$rc" = "2" ] || { echo "FAIL: bad ID shape S01 should exit 2, got $rc"; exit 1; }
) || fail "design amend assertions"
trap 'rm -rf "$TMP"' EXIT
rm -rf "$AMD_TMP"
pass "design amend: replace/append section contracts hold; rejects bad section, missing design, bad ID"

echo "## /rota-plan integration with --design pointer"

PLN_TMP="$(mktemp -d)"
trap 'rm -rf "$PLN_TMP"' EXIT
(
  cd "$PLN_TMP"
  mkdir -p .rota

  # Seed a design artifact via the writer verb
  hvj design add F00 --title "design for F00" >/dev/null || { echo "FAIL: seeding design failed"; exit 1; }

  # 11. plan add --design writes a plan with design: frontmatter pointer
  KEY=$(hvj plan add M01-F00 --title "plan for F00" --design F00 | jget data.key) || { echo "FAIL: plan add --design exited non-zero"; exit 1; }
  [ "$KEY" = "M01-F00" ] || { echo "FAIL: plan key wrong: '$KEY'"; exit 1; }
  PLAN=".rota/plans/M01-F00.md"
  [ -f "$PLAN" ] || { echo "FAIL: plan file not created"; exit 1; }
  grep -q "^design: \.rota/designs/F00\.md$" "$PLAN" || { echo "FAIL: plan frontmatter missing design pointer"; exit 1; }

  # 12. plan add --design rejects a design that does not exist — exit 3
  rc=0; hvj plan add M01-B99 --title "x" --design B98 >/dev/null 2>&1 || rc=$?
  [ "$rc" = "3" ] || { echo "FAIL: missing design file should exit 3, got $rc"; exit 1; }
) || fail "/rota-plan --design integration assertions"
trap 'rm -rf "$TMP"' EXIT
rm -rf "$PLN_TMP"
pass "plan add --design records design pointer in frontmatter; rejects a missing design"

