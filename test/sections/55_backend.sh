echo "backlog.backend config keys and accessors"

TMP_BK="$(mktemp -d)"
trap 'rm -rf "$TMP_BK"' EXIT
mkdir -p "$TMP_BK/proj/.rota"
(
  cd "$TMP_BK/proj"
  CS() { hvj config show backlog.backend | jget "data.entries[0].$1"; }
  [ "$(CS value):$(CS source)" = "file:default" ] || fail "default backlog.backend: got '$(CS value):$(CS source)'"
  echo '{"backlog":{"backend":"issues"}}' > .rota/config.json
  [ "$(CS value):$(CS source)" = "issues:project" ] || fail "project backlog.backend: got '$(CS value):$(CS source)'"
  echo '{"backlog":{"backend":"file"}}' > .rota/config.local.json
  [ "$(CS value):$(CS source)" = "file:local" ] || fail "local backlog.backend: got '$(CS value):$(CS source)'"
  pass "config show reports backlog.backend default/project/local"
)

echo "FileBackend: create/read verbs byte-identical"

TMP_GB="$(mktemp -d)"
trap 'rm -rf "$TMP_BK" "$TMP_GB"' EXIT
mkdir -p "$TMP_GB/.rota"
(
  cd "$TMP_GB"
  git init -q && git config user.email t@t && git config user.name t
  cat > .rota/BACKLOG.md <<'MD'
# Backlog

## Bugs

- **[B01] [P1] First bug.** Desc one. Related: [F01] Milestone: M01 Since: abc1234
- **[B02] [P0] Second bug.** Desc two. Since: abc1234

## Features

- **[F01] [Major] Big feature.** Needs work. Related: [B01] Milestone: M01, M02 Repos: api, web Subsystem: core Since: abc1234
- **[F02] [Minor] Small feature.** No fields.

## Tasks

- **[T01] Chore.** Do it. Related: [F01]

## Completed

- ~~**[B03] [P1] Done thing.** old desc. Milestone: M01 Since: abc1234~~ Done 2026-01-01 [`abc1234`]
- ~~**[T02] Skipped.** x.~~ Done 2026-01-02 [`def5678`] (dropped: not needed)
MD
  printf '# Archive\n\n- ~~**[B05] [P2] Archived.** old. Related: [B01]~~ Done 2025-12-01 [`1111111`] (blocked: waiting)\n' > .rota/ARCHIVE.md
  echo '{"active":[{"items":["B02"],"branch":"fix/b02","startedAt":"2026-01-01T00:00:00Z"}]}' > .rota/status.json
  git add -A && git commit -qm seed
  cp .rota/BACKLOG.md "$TMP_GB/orig.md"

  eq() { # label, expected, actual
    [ "$2" = "$3" ] || fail "golden $1: expected '$2' got '$3'"
  }
  rcof() { local rc=0; "$@" >/dev/null 2>&1 || rc=$?; echo "$rc"; }

  # item field get
  FG() { hvj item field get "$1" --name "$2" | jget data.value; }
  eq "field title" "First bug" "$(FG B01 title)"
  eq "field milestone" "M01, M02" "$(FG F01 milestone)"
  eq "field reason" "dropped" "$(FG T02 reason)"
  eq "field note archived" "waiting" "$(FG B05 note)"
  eq "field empty" "" "$(FG T01 milestone)"
  eq "field type" "F" "$(hvj item field get F01 --name title | jget data.type)"
  eq "dump" '{"title":"Big feature","detail":"","related":"[B01]","milestone":"M01, M02","repos":"api, web","subsystem":"core","since":"abc1234","reason":"","note":""}' "$(hvj item field list F01 | jget data.fields)"
  eq "dump done" '{"title":"Done thing","detail":"","related":"","milestone":"M01","repos":"","subsystem":"","since":"abc1234","reason":"done","note":""}' "$(hvj item field list B03 | jget data.fields)"
  eq "field unknown" "3" "$(rcof hvj item field get B99 --name title)"
  eq "field bad" "2" "$(rcof hvj item field get B01 --name bogus)"
  pass "item field get / list golden"

  # item field set
  SFV() { hvj item field set "$1" --name "$2" --value "$3"; }
  eq "set-field changed" "true" "$(SFV F02 milestone M03 | jget data.changed)"
  eq "set-field line" '- **[F02] [Minor] Small feature.** No fields. Milestone: M03' "$(grep -F '[F02]' .rota/BACKLOG.md)"
  eq "set-field same value" "false" "$(SFV F02 milestone M03 | jget data.changed)"
  SFV F02 milestone "" >/dev/null
  eq "set-field clear" '- **[F02] [Minor] Small feature.** No fields.' "$(grep -F '[F02]' .rota/BACKLOG.md)"
  cmp -s .rota/BACKLOG.md "$TMP_GB/orig.md" || fail "set-field round trip changed BACKLOG.md"
  eq "set-field unknown" "3" "$(rcof hvj item field set B99 --name milestone --value M1)"
  eq "set-field completed" "4" "$(rcof hvj item field set B03 --name milestone --value M1)"
  eq "set-field archived" "4" "$(rcof hvj item field set B05 --name milestone --value M1)"
  eq "set-field bad field" "2" "$(rcof hvj item field set B01 --name title --value X)"
  eq "set-field usage" "2" "$(rcof hvj item field set B01 --name milestone)"
  cmp -s .rota/BACKLOG.md "$TMP_GB/orig.md" || fail "rejected item field set changed BACKLOG.md"
  pass "item field set golden"

  # backlog list
  IDS() { hvj backlog list "${@:2}" | jget "data.$1" | python3 -c 'import json,sys; print(",".join(r["id"] for r in json.load(sys.stdin)))'; }
  eq "backlog in progress" "B02" "$(IDS inProgress)"
  OUT="$(hvj backlog list)"
  eq "in progress row" "B|fix/b02|2026-01-01T00:00:00Z" "$(echo "$OUT" | jget 'data.inProgress[0].type')|$(echo "$OUT" | jget 'data.inProgress[0].branch')|$(echo "$OUT" | jget 'data.inProgress[0].startedAt')"
  eq "backlog bugs" "B01" "$(IDS bugs)"
  eq "backlog bug row" "P1|First bug|[\"F01\"]|M01" "$(echo "$OUT" | jget 'data.bugs[0].priority')|$(echo "$OUT" | jget 'data.bugs[0].title')|$(echo "$OUT" | jget 'data.bugs[0].related')|$(echo "$OUT" | jget 'data.bugs[0].milestone')"
  eq "backlog features" "F02,F01" "$(IDS features)"
  eq "backlog feature row" "Major|Big feature|[\"B01\"]|M01, M02" "$(echo "$OUT" | jget 'data.features[1].size')|$(echo "$OUT" | jget 'data.features[1].title')|$(echo "$OUT" | jget 'data.features[1].related')|$(echo "$OUT" | jget 'data.features[1].milestone')"
  eq "backlog tasks" "T01" "$(IDS tasks)"
  eq "backlog clusters" '[["B01","F01","T01"]]' "$(echo "$OUT" | jget data.clusters)"
  eq "backlog grep keeps In Progress" "B02||" "$(IDS inProgress --grep zzzznomatch)|$(IDS bugs --grep zzzznomatch)|$(IDS features --grep zzzznomatch)"
  eq "backlog bad arg" "2" "$(rcof hvj backlog list --bogus)"
  pass "backlog list golden"

  # backlog.backend = issues: item create --raw-file refuses (exit 4), BACKLOG.md untouched
  echo '{"backlog":{"backend":"issues"}}' > .rota/config.json
  rc=0; OUT="$(echo '- **[B10] x.**' | hvj item create --kind bugs --raw-file - 2>/dev/null)" || rc=$?
  eq "raw-file issues refusal" "4:refused" "$rc:$(echo "$OUT" | jget error.code)"
  eq "raw-file refusal data" "false" "$(echo "$OUT" | jget data.changed)"
  cmp -s .rota/BACKLOG.md "$TMP_GB/orig.md" || fail "item create --raw-file wrote BACKLOG.md under issues backend"
  pass "issues backend refused by item create --raw-file (exit 4, file unchanged)"

  # bogus backend: the verb fails, nothing is written
  echo '{"backlog":{"backend":"bogus"}}' > .rota/config.json
  eq "bogus raw-file" "1" "$(echo '- **[B10] x.**' | hvj item create --kind bugs --raw-file - >/dev/null 2>&1 && echo 0 || echo 1)"
  eq "bogus field get" "1" "$(hvj item field get B01 --name title >/dev/null 2>&1 && echo 0 || echo 1)"
  eq "bogus field set" "1" "$(hvj item field set B01 --name milestone --value M09 >/dev/null 2>&1 && echo 0 || echo 1)"
  eq "bogus backlog list" "1" "$(hvj backlog list >/dev/null 2>&1 && echo 0 || echo 1)"
  cmp -s .rota/BACKLOG.md "$TMP_GB/orig.md" || fail "bogus backend wrote BACKLOG.md"
  pass "bogus backlog.backend fails for create, field get/set and list"
)
trap 'rm -rf "$TMP_BK" "$TMP_GB"' EXIT

echo "FileBackend complete/reopen and file-only verbs"

TMP_CU="$(mktemp -d)"
trap 'rm -rf "$TMP_BK" "$TMP_GB" "$TMP_CU"' EXIT
(
  cd "$TMP_CU"
  git init -q && git config user.email t@t && git config user.name t
  echo a > a && git add a && git commit -qm "feat: work"
  C1="$(git log -1 --format=%h)"
  echo b >> a && git commit -qam "refactor(core): tidy"
  R1="$(git log -1 --format=%h)"
  TODAY="$(date +%Y-%m-%d)"
  mkdir -p .rota
  cat > .rota/BACKLOG.md <<MD
# Backlog

## Bugs

- **[B01] [P1] First bug.** Desc one.
- **[B02] [P0] Second bug.** Desc two.
- **[B04] [P2] No proof.** Desc.

## Features

- **[F01] [Major] Big.** Needs work.

## Tasks

## Completed

- ~~**[B03] [P1] Done thing.** old.~~ Done 2026-01-01 [\`$C1\`]
- ~~**[T02] Skipped.** x.~~ Done 2026-01-02 [\`$C1\`] (dropped: not needed)
MD
  printf '# Archive\n\n- ~~**[B05] [P2] Archived.** old.~~ Done 2025-12-01 [`%s`] (blocked: waiting)\n' "$C1" > .rota/ARCHIVE.md
  echo '{"since_refactor":{"features":3,"bugs":3}}' > .rota/counters.json
  hvj proof add B01 --check t --result PASS --evidence x --sha "$C1" >/dev/null
  hvj proof add B02 --check t --result PASS --evidence x --sha "$C1" >/dev/null
  cp .rota/BACKLOG.md orig.md; cp .rota/ARCHIVE.md orig.arch; cp .rota/counters.json orig.cnt
  eq() { [ "$2" = "$3" ] || fail "$1: expected [$2] got [$3]"; }
  rcof() { local rc=0; "$@" >/dev/null 2>&1 || rc=$?; echo "$rc"; }
  cnt() { python3 -c 'import json;d=json.load(open(".rota/counters.json"))["since_refactor"];print(d["features"],d["bugs"])'; }

  # complete: proof row present, default Done line, counter bumped
  OUT="$(hvj item complete B01 --commit "$C1")"
  eq "complete data" "B|done|$C1|true" "$(echo "$OUT" | jget data.type)|$(echo "$OUT" | jget data.reason)|$(echo "$OUT" | jget data.commit)|$(echo "$OUT" | jget data.changed)"
  eq "complete line" "- ~~**[B01] [P1] First bug.** Desc one.~~ Done $TODAY [\`$C1\`]" "$(grep -F '[B01]' .rota/BACKLOG.md)"
  eq "complete counter" "3 4" "$(cnt)"
  # already completed: success no-op, no second bump
  rc=0; OUT="$(hvj item complete B01 --commit "$C1")" || rc=$?
  eq "complete noop" "0:false" "$rc:$(echo "$OUT" | jget data.changed)"; eq "noop counter" "3 4" "$(cnt)"
  # no proof: refused (exit 4), nothing written
  cp .rota/BACKLOG.md pre.md
  rc=0; OUT="$(hvj item complete B04 --commit "$C1" 2>/dev/null)" || rc=$?
  eq "no proof" "4:proof missing:false" "$rc:$(echo "$OUT" | jget data.blockedBy):$(echo "$OUT" | jget data.changed)"
  cmp -s .rota/BACKLOG.md pre.md || fail "no-proof close wrote BACKLOG.md"
  # --no-proof with reason and note
  hvj item complete B04 --commit "$C1" --no-proof --reason blocked --note "waiting on X" >/dev/null
  eq "reason/note line" "- ~~**[B04] [P2] No proof.** Desc.~~ Done $TODAY [\`$C1\`] (blocked: waiting on X)" "$(grep -F '[B04]' .rota/BACKLOG.md | head -1)"
  # refactor: commit leaves counters alone
  hvj item complete B02 --commit "$R1" >/dev/null
  eq "refactor counter" "3 5" "$(cnt)"
  # unknown ID / bad reason
  eq "complete unknown" "3" "$(rcof hvj item complete B99 --commit "$C1")"
  eq "complete bad reason" "2" "$(rcof hvj item complete B01 --commit "$C1" --reason bogus)"
  pass "item complete golden"

  # reopen: from Completed, rewinds counter; from ARCHIVE.md; refactor; active no-op
  cp orig.md .rota/BACKLOG.md; cp orig.arch .rota/ARCHIVE.md; cp orig.cnt .rota/counters.json
  eq "reopen changed" "true" "$(hvj item reopen B03 | jget data.changed)"
  eq "reopen line" "- **[B03] [P1] Done thing.** old." "$(grep -F '[B03]' .rota/BACKLOG.md)"
  if grep -qF '~~**[B03]' .rota/BACKLOG.md; then fail "B03 Done line left in BACKLOG"; fi
  eq "reopen counter" "3 2" "$(cnt)"
  hvj item reopen B05 >/dev/null
  eq "archive restore" "- **[B05] [P2] Archived.** old." "$(grep -F '[B05]' .rota/BACKLOG.md)"
  eq "archive emptied" "# Archive" "$(grep -v '^$' .rota/ARCHIVE.md)"
  eq "archive counter" "3 1" "$(cnt)"
  rc=0; OUT="$(hvj item reopen B03)" || rc=$?
  eq "reopen noop" "0:false" "$rc:$(echo "$OUT" | jget data.changed)"
  eq "noop counter" "3 1" "$(cnt)"
  hvj item reopen T02 >/dev/null
  eq "task restore" "- **[T02] Skipped.** x." "$(grep -F '[T02]' .rota/BACKLOG.md)"
  eq "task counter" "3 1" "$(cnt)"
  eq "reopen unknown" "3" "$(rcof hvj item reopen B99)"
  pass "item reopen golden"

  # backlog.backend = issues from here on
  cp orig.md .rota/BACKLOG.md; cp orig.arch .rota/ARCHIVE.md; cp orig.cnt .rota/counters.json
  echo '{"backlog":{"backend":"issues"}}' > .rota/config.json

  # file-only verbs refuse in issue mode (exit 4, backend), writing nothing
  # (a mutating verb exits 4; a read-only one, backlog drift, exits 1)
  while IFS='|' read -r label want call; do
    # shellcheck disable=SC2086
    rc=0; OUT="$(hvj $call 2>/dev/null)" || rc=$?
    eq "$label issues refusal" "$want:backend" "$rc:$(echo "$OUT" | jget data.blockedBy)"
  done <<'EOF'
id next|4|id next --kind bugs
item rm|4|item rm B01 --apply
backlog archive|4|backlog archive --days 0
backlog backfill|4|backlog backfill
backlog drift|1|backlog drift
EOF
  cmp -s .rota/BACKLOG.md orig.md && cmp -s .rota/ARCHIVE.md orig.arch && cmp -s .rota/counters.json orig.cnt || fail "file-only verb wrote under issues backend"
  pass "file-only verbs refuse under issues backend (exit 4, read-only drift 1, no writes)"

  # bogus backend: every verb fails, nothing is written
  echo '{"backlog":{"backend":"bogus"}}' > .rota/config.json
  for call in "item complete B01 --commit $C1" "item reopen B03" "id next --kind bugs" "item rm B01 --apply" "backlog archive --days 0" "backlog backfill" "backlog drift"; do
    # shellcheck disable=SC2086
    rc=0; hvj $call >/dev/null 2>&1 || rc=$?
    [ "$rc" != 0 ] || fail "rota $call succeeded under a bogus backlog.backend"
  done
  cmp -s .rota/BACKLOG.md orig.md && cmp -s .rota/counters.json orig.cnt || fail "bogus backend wrote"
  pass "bogus backlog.backend fails for all seven verbs"

  # file mode: file-only verbs still work
  rm .rota/config.json
  OUT="$(hvj id next --kind bugs)"
  eq "id next file mode" "B06:true" "$(echo "$OUT" | jget data.id):$(echo "$OUT" | jget data.changed)"
  pass "id next unchanged in file mode"
)
trap 'rm -rf "$TMP_BK" "$TMP_GB" "$TMP_CU"' EXIT

echo "IssueBackend: reads served from the tracker"

TMP_IB="$(mktemp -d)"
trap 'rm -rf "$TMP_BK" "$TMP_GB" "$TMP_CU" "$TMP_IB"' EXIT

for prov in github gitlab; do
  P="$TMP_IB/$prov"; mkdir -p "$P/.rota"
  echo "{\"backlog\":{\"backend\":\"issues\"},\"issues\":{\"provider\":\"$prov\",\"retryWaitSeconds\":0}}" > "$P/.rota/config.json"
  (
    cd "$P"
    git init -q && git config user.email t@t && git config user.name t
    export PATH="$TESTDIR/fakes:$PATH" FAKE_TRACKER_DB="$P/db.json" FAKE_TRACKER_LOG="$P/log"
    eq() { [ "$2" = "$3" ] || fail "$prov issue mode $1: expected [$2] got [$3]"; }
    rcof() { local rc=0; "$@" >/dev/null 2>&1 || rc=$?; echo "$rc"; }
    TC() { hvj tracker call -- "$@" </dev/null >/dev/null; }
    FB="$(printf 'Adds the thing.\n\nSecond paragraph.\n\n<!-- rota:fields\nRelated: F3, B1\nRepos: web\n-->')"
    if [ "$prov" = github ]; then
      TC api repos/fake/repo/milestones -f "title=M07 — Issue backend"
      for l in type:bug type:feature type:task p1 size:Major milestone-tracker; do TC label create "$l"; done
      IC() { TC issue create --title "$1" --body "$2" ${3:+--label "$3"} ${4:+--milestone "$4"}; }
      CLOSE_DONE() { TC issue close "$1"; }
      CLOSE_DROP() { TC issue close "$1" --reason "not planned"; }
    else
      TC api projects/:id/milestones -f "title=M07 — Issue backend"
      IC() { TC issue create --title "$1" --description "$2" ${3:+--label "$3"} ${4:+--milestone "$4"} -y; }
      CLOSE_DONE() { TC issue close "$1"; }
      CLOSE_DROP() { TC issue close "$1"; TC issue update "$1" --label not-planned; }
    fi
    IC "Crash on start" "Crashes when config is missing." "type:bug,p1"
    IC "Big feature" "$FB" "type:feature,size:Major" "M07 — Issue backend"
    IC "Other feature" "" "type:feature"
    IC "Chore" "do the \`thing\`" ""
    IC "M07 tracking" "tracker" "milestone-tracker"
    IC "Old bug" "fixed long ago" "type:bug"
    IC "Dropped task" "never mind" "type:task"
    CLOSE_DONE 6
    sleep 1
    CLOSE_DROP 7

    BIG="$(python3 -c 'print("word " * 80)')"
    IC "Long one" "$BIG" "type:task"

    # backlog list
    IDS() { echo "$OUT" | jget "data.$1" | python3 -c 'import json,sys; print(",".join(sorted(r["id"] for r in json.load(sys.stdin))))'; }
    OUT="$(hvj backlog list)"
    eq "list bugs" "1" "$(IDS bugs)"
    eq "list bug row" "P1|Crash on start|[]" "$(echo "$OUT" | jget 'data.bugs[0].priority')|$(echo "$OUT" | jget 'data.bugs[0].title')|$(echo "$OUT" | jget 'data.bugs[0].related')"
    eq "list features" "2,3" "$(IDS features)"
    big="$(echo "$OUT" | python3 -c 'import json,sys; print(json.dumps([r for r in json.load(sys.stdin)["data"]["features"] if r["id"] == "2"][0], separators=(",", ":")))')"
    eq "list feature row" "Major|Big feature|3,1|M07" "$(echo "$big" | jget size)|$(echo "$big" | jget title)|$(echo "$big" | jget related | tr -dc '0-9,')|$(echo "$big" | jget milestone)"
    eq "list other feature" "Other feature" "$(echo "$OUT" | python3 -c 'import json,sys; print([r for r in json.load(sys.stdin)["data"]["features"] if r["id"] == "3"][0]["title"])')"
    eq "list tasks" "4,8" "$(IDS tasks)"
    eq "list no closed or tracker issue" "0" "$(echo "$OUT" | grep -c 'Old bug\|tracking')"
    pass "$prov: backlog list renders tracker items"

    # item field get across ID spellings
    FG() { hvj item field get "$1" --name "$2" | jget data.value; }
    for ref in F2 '#2' 2; do
      eq "title $ref" "Big feature" "$(FG "$ref" title)"
      eq "milestone $ref" "M07" "$(FG "$ref" milestone)"
    done
    eq "id and type" "2|F" "$(hvj item field get F2 --name title | jget data.id)|$(hvj item field get '#2' --name title | jget data.type)"
    eq "related" "[F3], [B1]" "$(FG F2 related)"
    eq "repos" "web" "$(FG F2 repos)"
    eq "subsystem" "" "$(FG F2 subsystem)"
    eq "since" "" "$(FG F2 since)"
    eq "reason open" "" "$(FG F2 reason)"
    eq "reason done" "done" "$(FG B6 reason)"
    eq "reason dropped" "dropped" "$(FG '#7' reason)"
    eq "note" "" "$(FG F2 note)"
    case "$(FG F2 detail)" in http*/2) ;; *) fail "$prov detail is not the issue URL";; esac
    DUMP="$(hvj item field list '#2')" python3 - <<'PY' || fail "$prov item field list"
import json, os
d = json.loads(os.environ["DUMP"])["data"]["fields"]
assert list(d) == ["title","detail","related","milestone","repos","subsystem","since","reason","note"], list(d)
assert d["title"] == "Big feature" and d["related"] == "[F3], [B1]" and d["milestone"] == "M07", d
assert d["repos"] == "web" and d["detail"].endswith("/2"), d
PY
    for bad in B2 T2 B99 9999 F5; do
      eq "unknown $bad" "3" "$(rcof hvj item field get "$bad" --name title)"
    done
    pass "$prov: item field get resolves F2/#2/2, rejects type mismatch and tracker issues"

    # summary, milestone readers
    OUT="$(hvj summary)"
    eq "summary counts" '{"bugs":1,"features":2,"tasks":2}' "$(echo "$OUT" | jget data.backlog)"
    eq "summary recent" "7|T|dropped|6|B" "$(echo "$OUT" | jget 'data.recent[0].id')|$(echo "$OUT" | jget 'data.recent[0].type')|$(echo "$OUT" | jget 'data.recent[0].reason')|$(echo "$OUT" | jget 'data.recent[1].id')|$(echo "$OUT" | jget 'data.recent[1].type')"
    eq "ids by milestone" '{"milestone":"M07","ids":["2"]}' "$(hvj backlog ids --milestone M07 | jget data)"
    eq "ids by milestone none" '[]' "$(hvj backlog ids --milestone M08 | jget data.ids)"
    eq "milestones of items" '["M07"]' "$(hvj backlog milestones B1 F2 '#3' | jget data.milestones)"
    eq "milestones of items none" '[]' "$(hvj backlog milestones B1 T4 | jget data.milestones)"
    pass "$prov: summary / backlog ids / backlog milestones"

    # plan uncertain: F2 is Major with a prose-only body (no code span)
    for ref in F2 '#2'; do
      rc=0; OUT="$(hvj plan uncertain "$ref")" || rc=$?
      eq "uncertain $ref" "0:true:[\"no concrete identifiers (unknown surface)\"]" "$rc:$(echo "$OUT" | jget data.uncertain):$(echo "$OUT" | jget data.reasons)"
    done
    rc=0; OUT="$(hvj plan uncertain B1)" || rc=$?
    eq "uncertain non-major" "1:false" "$rc:$(echo "$OUT" | jget data.uncertain)"
    eq "uncertain unknown" "3" "$(rcof hvj plan uncertain B2)"
    pass "$prov: plan uncertain reads the issue body as the detail text"

    # tracker failure: exit 5 (unavailable)
    for call in "backlog list" "summary" "backlog ids --milestone M07" "backlog milestones F2"; do
      # shellcheck disable=SC2086
      eq "$call list failure rc" 5 "$(FAKE_TRACKER_FAIL=list rcof hvj $call)"
    done
    # plan uncertain resolves F2 with one view by number and never lists
    eq "plan uncertain F2 view failure rc" 5 "$(FAKE_TRACKER_FAIL=view rcof hvj plan uncertain F2)"
    eq "field get view failure" 5 "$(FAKE_TRACKER_FAIL=view rcof hvj item field get F2 --name title)"
    pass "$prov: tracker failures surface as exit 5"
  )
done
trap 'rm -rf "$TMP_BK" "$TMP_GB" "$TMP_CU" "$TMP_IB"' EXIT

echo "item create: capture in both backends, issue-mode field writes"

TMP_IC="$(mktemp -d)"
trap 'rm -rf "$TMP_BK" "$TMP_GB" "$TMP_CU" "$TMP_IB" "$TMP_IC"' EXIT

# --- file mode: byte-identical to the golden bullets ------------------------
mkdir -p "$TMP_IC/new/.rota"
(
  cd "$TMP_IC/new"
  git init -q && git config user.email t@t && git config user.name t
  printf '# Backlog\n\n## Bugs\n\n- **[B01] [P1] Old bug.** d. Since: abc1234\n\n## Features\n\n## Tasks\n\n## Completed\n' > .rota/BACKLOG.md
  echo '{"bugs": 1}' > .rota/counters.json
  printf 'Body for {ID}\n\nsecond {ID} line, no trailing newline' > "$TMP_IC/body.md"
  git add -A && git commit -q -m seed
  head="$(git rev-parse --short HEAD)"
  IC() { hvj item create "$@"; }
  OUT="$(IC --kind bugs --title "Crash on start" --tag P1 --desc "It crashes." --related '[F01]' --milestone M01 --repos web)"
  eq() { [ "$2" = "$3" ] || fail "item create $1: expected [$2] got [$3]"; }
  eq "bug data" "B02|B|bugs|true" "$(echo "$OUT" | jget data.id)|$(echo "$OUT" | jget data.type)|$(echo "$OUT" | jget data.kind)|$(echo "$OUT" | jget data.changed)"
  OUT="$(IC --kind features --title "Big thing" --tag Major --desc "Does stuff." --body-file "$TMP_IC/body.md" --subsystem core)"
  eq "feature data" "F02|F|.rota/features/F02.md" "$(echo "$OUT" | jget data.id)|$(echo "$OUT" | jget data.type)|$(echo "$OUT" | jget data.detail)"
  eq "task id" "T01" "$(IC --kind tasks --title "Is it done?" | jget data.id)"
  eq "bug 2 id" "B03" "$(IC --kind bugs --title "Cosmetic glitch." --tag P3 --desc "Minor." --captured 2026-10-01 | jget data.id)"
  cat > "$TMP_IC/expected.md" <<MD
# Backlog

## Bugs

- **[B01] [P1] Old bug.** d. Since: abc1234
- **[B02] [P1] Crash on start.** It crashes. Related: [F01] Milestone: M01 Repos: web Since: $head
- **[B03] [P3] Cosmetic glitch.** Minor. Captured: 2026-10-01 Since: $head

## Features
- **[F02] [Major] Big thing.** Does stuff. Detail: \`.rota/features/F02.md\` Subsystem: core Since: $head

## Tasks
- **[T01] Is it done?** Since: $head

## Completed
MD
  cmp -s "$TMP_IC/expected.md" .rota/BACKLOG.md || fail "file-mode item create BACKLOG.md differs from golden: $(diff "$TMP_IC/expected.md" .rota/BACKLOG.md)"
  eq "counters" '{"bugs":3,"features":2,"tasks":1}' "$(python3 -c 'import json;print(json.dumps(json.load(open(".rota/counters.json")),separators=(",",":")))')"
  [ "$(sed -n '3,$p' .rota/features/F02.md | head -c 100 | tr -d '\n')" = "second F02 line, no trailing newline" ] || fail "detail {ID} substitution"
  [ "$(sed -n 1p .rota/features/F02.md)" = "Body for F02" ] || fail "detail {ID} substitution on line 1"
)
# id next mints from the same counters: F02 above only follows B02's `Related: [F01]` reference
cp -a "$TMP_IC/new" "$TMP_IC/idn"
(
  cd "$TMP_IC/idn"
  git checkout -q -- . && git clean -qfd
  [ "$(hvj id next --kind bugs | jget data.id) $(hvj id next --kind features | jget data.id) $(hvj id next --kind tasks | jget data.id) $(hvj id next --kind bugs | jget data.id)" = "B02 F01 T01 B03" ] || fail "id next sequence"
  [ "$(python3 -c 'import json;print(json.dumps(json.load(open(".rota/counters.json")),separators=(",",":")))')" = '{"bugs":3,"features":1,"tasks":1}' ] || fail "id next counters"
)
pass "file mode: item create == golden bullets, detail file, counters and IDs"

(
  cd "$TMP_IC/new"
  cp -a .rota "$TMP_IC/before.hv"
  rcof() { local rc=0; "$@" >/dev/null 2>&1 || rc=$?; echo "$rc"; }
  bad() { [ "$(rcof hvj item create "$@")" = 2 ] || fail "item create $*: expected exit 2"; }
  bad --kind bugs --title x --tag Major
  bad --kind features --title x --tag P1
  bad --kind tasks --title x --tag P1
  bad --kind bugs --title x --since abc
  bad --kind bugs --title x --detail abc
  bad --kind bugs --tag P1
  bad --kind milestones --title x
  [ "$(rcof hvj item create --kind bugs --title x --body-file /nonexistent)" = 3 ] || fail "item create with a missing --body-file: expected exit 3"
  bad --kind bugs --title x --desc
  diff -r .rota "$TMP_IC/before.hv" >/dev/null || fail "rejected item create changed .rota"
  # relative --body-file resolves against the caller's cwd
  mkdir -p sub; printf 'rel {ID}' > sub/b.md
  ( cd sub && hvj item create --kind tasks --title Rel --body-file b.md >/dev/null )
  [ "$(cat .rota/tasks/T02.md)" = "rel T02" ] || fail "relative --body-file"
)
pass "item create validates tag/fields/title/body-file without writing"

# --- issue mode -------------------------------------------------------------
for prov in github gitlab; do
  P="$TMP_IC/$prov"; mkdir -p "$P/.rota"
  CFG() { echo "{\"backlog\":{\"backend\":\"issues\"},\"issues\":{\"provider\":\"$prov\",\"retryWaitSeconds\":0$1}}" > "$P/.rota/config.json"; }
  CFG ""
  (
    cd "$P"
    git init -q && git config user.email t@t && git config user.name t
    export PATH="$TESTDIR/fakes:$PATH" FAKE_TRACKER_DB="$P/db.json" FAKE_TRACKER_LOG="$P/log"
    eq() { [ "$2" = "$3" ] || fail "$prov item create $1: expected [$2] got [$3]"; }
    rcof() { local rc=0; "$@" >/dev/null 2>&1 || rc=$?; echo "$rc"; }
    TC() { hvj tracker call -- "$@" </dev/null >/dev/null; }
    # IV <n> <key>: one issue key from the fake tracker's store (labels sorted, joined by comma;
    # the store keeps a milestone as [title, number])
    IV() { python3 -c '
import json, sys
i = next(i for i in json.load(open(sys.argv[3]))["issues"] if i["number"] == int(sys.argv[1]))
v = i[sys.argv[2]]
if sys.argv[2] == "milestone":
    v = v[0] if v else None   # the store keeps [title, number]
print(",".join(sorted(v)) if isinstance(v, list) else (v if v is not None else "-"))' "$1" "$2" "$P/db.json"; }
    EDITS() { grep -c 'issue \(edit\|update\)' "$P/log" || true; }

    if [ "$prov" = github ]; then
      CFG ',"autoCreateLabel":false'
      [ "$(rcof hvj item create --kind bugs --title Nope --tag P1)" != 0 ] || fail "$prov item create: a missing label with issues.autoCreateLabel off should fail"
      eq "no issue created" "[]" "$(hvj tracker call -- issue list --json number </dev/null | jget data.stdout | tr -d ' \n')"
      CFG ""
      TC api 'repos/{owner}/{repo}/milestones' -f "title=M07 — Issue backend"
    else
      TC api 'projects/:id/milestones' -f "title=M07 — Issue backend"
    fi

    eq "missing milestone" "3" "$(rcof hvj item create --kind bugs --title Nope --milestone M99)"
    [ "$(rcof hvj item create --kind bugs --title Nope --milestone "M07, M08")" != 0 ] || fail "$prov item create: two milestones should fail"

    printf 'Detail for {ID}.\n' > "$P/body.md"
    B="$(hvj item create --kind bugs --title "Crash on start" --tag P1 --desc "It crashes." --related 'F1, B2' --repos web --milestone M07)"
    F="$(hvj item create --kind features --title "Big thing" --tag Major --desc "Does stuff." --body-file "$P/body.md" --subsystem core)"
    T="$(hvj item create --kind tasks --title "Chore")"
    eq "ids" "1 2 3" "$(echo "$B" | jget data.id) $(echo "$F" | jget data.id) $(echo "$T" | jget data.id)"
    eq "types" "B F T" "$(echo "$B" | jget data.type) $(echo "$F" | jget data.type) $(echo "$T" | jget data.type)"
    eq "bug labels" "p1,type:bug" "$(IV 1 labels)"
    eq "bug milestone" "M07 — Issue backend" "$(IV 1 milestone)"
    eq "bug title" "Crash on start" "$(IV 1 title)"
    eq "bug body" "$(printf 'It crashes.\n\n<!-- rota:fields\nRelated: F1, B2\nRepos: web\n-->')" "$(IV 1 body)"
    eq "feature labels" "size:Major,type:feature" "$(IV 2 labels)"
    eq "feature milestone" "-" "$(IV 2 milestone)"
    eq "feature body" "$(printf 'Does stuff.\n\nDetail for F2.\n\n<!-- rota:fields\nSubsystem: core\n-->')" "$(IV 2 body)"
    eq "task labels" "type:task" "$(IV 3 labels)"
    eq "task body" "" "$(IV 3 body)"
    FG() { hvj item field get "$1" --name "$2" | jget data.value; }
    eq "field get" "M07|[F1], [B2]|web|core" "$(FG B1 milestone)|$(FG '#1' related)|$(FG B1 repos)|$(FG F2 subsystem)"
    OUT="$(hvj backlog list)"
    eq "list ids" "1|2|3" "$(echo "$OUT" | jget 'data.bugs[0].id')|$(echo "$OUT" | jget 'data.features[0].id')|$(echo "$OUT" | jget 'data.tasks[0].id')"
    rc=0; OUT="$(echo '- **[B9] x.**' | hvj item create --kind bugs --raw-file - 2>/dev/null)" || rc=$?
    eq "raw-file refused" "4:refused:backend" "$rc:$(echo "$OUT" | jget error.code):$(echo "$OUT" | jget data.blockedBy)"
    pass "$prov: item create makes labelled issues with fields, milestone and body; --raw-file is refused"

    # item field set
    SF() { hvj item field set "$1" --name "$2" --value "$3"; }
    e0="$(EDITS)"
    eq "set related changed" "true" "$(SF T3 related "[B1]" | jget data.changed)"
    eq "set related" "$(printf '<!-- rota:fields\nRelated: [B1]\n-->')" "$(IV 3 body)"
    SF T3 related "[B1]" >/dev/null
    SF T3 Related "[B1]" >/dev/null 2>&1 || true
    eq "related no-op: one edit" "$((e0 + 1))" "$(EDITS)"
    SF T3 repos api >/dev/null; SF T3 related "" >/dev/null
    eq "clear related keeps repos" "$(printf '<!-- rota:fields\nRepos: api\n-->')" "$(IV 3 body)"
    SF T3 repos "" >/dev/null
    eq "all cleared" "" "$(IV 3 body)"
    e1="$(EDITS)"; SF T3 repos "" >/dev/null; eq "clear absent is no-op" "$e1" "$(EDITS)"
    SF F2 related "[B1]" >/dev/null
    eq "feature keeps text" "$(printf 'Does stuff.\n\nDetail for F2.\n\n<!-- rota:fields\nSubsystem: core\nRelated: [B1]\n-->')" "$(IV 2 body)"
    SF T3 milestone M07 >/dev/null
    eq "set milestone" "M07 — Issue backend" "$(IV 3 milestone)"
    e2="$(EDITS)"; SF '#3' milestone M07 >/dev/null; eq "milestone no-op" "$e2" "$(EDITS)"
    eq "field get milestone" "M07" "$(FG T3 milestone)"
    SF T3 milestone "" >/dev/null
    eq "clear milestone" "-" "$(IV 3 milestone)"
    e3="$(EDITS)"; SF T3 milestone "" >/dev/null; eq "clear milestone no-op" "$e3" "$(EDITS)"
    eq "set missing milestone" "3" "$(rcof hvj item field set T3 --name milestone --value M99)"
    eq "detail not settable" "4" "$(rcof hvj item field set T3 --name detail --value x)"
    eq "unknown item" "3" "$(rcof hvj item field set T99 --name related --value x)"
    eq "type mismatch" "3" "$(rcof hvj item field set B3 --name related --value x)"
    TC issue close 3
    eq "closed item" "4" "$(rcof hvj item field set T3 --name related --value "[B1]")"
    pass "$prov: item field set writes fields/milestone on issues, no-ops when unchanged, rejects bad input"
  )
done
trap 'rm -rf "$TMP_BK" "$TMP_GB" "$TMP_CU" "$TMP_IB" "$TMP_IC"' EXIT

echo "Issue mode: item complete / reopen close reasons, labels, proof gate"

TMP_IL="$(mktemp -d)"
trap 'rm -rf "$TMP_BK" "$TMP_GB" "$TMP_CU" "$TMP_IB" "$TMP_IC" "$TMP_IL"' EXIT
for prov in github gitlab; do
  P="$TMP_IL/$prov"; mkdir -p "$P/.rota"
  echo "{\"backlog\":{\"backend\":\"issues\"},\"issues\":{\"provider\":\"$prov\",\"retryWaitSeconds\":0}}" > "$P/.rota/config.json"
  (
    cd "$P"
    git init -q && git config user.email t@t && git config user.name t && git commit -q --allow-empty -m seed
    export PATH="$TESTDIR/fakes:$PATH" FAKE_TRACKER_DB="$P/db.json" FAKE_TRACKER_LOG="$P/log"
    eq() { [ "$2" = "$3" ] || fail "$prov lifecycle $1: expected [$2] got [$3]"; }
    rcof() { local rc=0; "$@" >/dev/null 2>&1 || rc=$?; echo "$rc"; }
    # IV <n>: "state|state_reason|sorted labels (comma)|comments (joined by ' // ')", read from the fake
    # tracker's store. glab has no close reason: a closed issue is completed unless it carries not-planned.
    IV() { python3 -c '
import json, re, sys
i = next(i for i in json.load(open(sys.argv[2]))["issues"] if i["number"] == int(sys.argv[1]))
reason = (i["state_reason"] or (("not_planned" if "not-planned" in i["labels"] else "completed") if i["state"] == "closed" else "")).replace(" ", "_")
# the trailing rota marker line (rota:blocked|done|closed) is hidden here; MARKERS asserts it
strip = lambda b: re.sub(r"\n\n<!-- rota:(?:blocked|done|closed) -->$", "", b)
print("%s|%s|%s|%s" % (i["state"], reason, ",".join(sorted(i["labels"])),
                       " // ".join(strip(c["body"]).replace("\n", " ") for c in i["comments"] if not c["body"].startswith("<!-- rota:proof"))))' "$1" "$P/db.json"; }
    MARKERS() { python3 -c '
import json, sys
i = next(i for i in json.load(open(sys.argv[2]))["issues"] if i["number"] == int(sys.argv[1]))
print(",".join(c["body"].rsplit("\n", 1)[-1] for c in i["comments"] if not c["body"].startswith("<!-- rota:proof")))' "$1" "$P/db.json"; }
    WRITES() { grep -c "$1" "$P/log" || true; }
    PROOF() { hvj proof add "$1" --check smoke --result PASS --evidence ok --sha abc1234 >/dev/null; }
    DONE() { hvj item complete "$@"; }
    DASH="$(printf '\xe2\x80\x94')"
    NPL="not-planned,"; [ "$prov" = github ] && NPL=""   # glab has no close reason: a label stands in

    for t in a b c d e f; do hvj item create --kind tasks --title "Task $t" >/dev/null; done   # T1..T6
    for n in 1 2 3 4 5 6; do hvj item state "T$n" --to in-progress >/dev/null; done
    eq "seeded labels" "open||in-progress,type:task|" "$(IV 1)"

    # proof gate: unproven done is refused (exit 4) and leaves the issue alone
    before="$(IV 1)"
    rc=0; OUT="$(DONE T1 --commit abc1234 2>/dev/null)" || rc=$?
    eq "gate rc" "4:proof missing" "$rc:$(echo "$OUT" | jget data.blockedBy)"
    eq "gate leaves issue" "$before" "$(IV 1)"

    # done: proven, closed completed, state labels cleared, comment
    PROOF T1
    OUT="$(DONE T1 --commit abc1234)"
    eq "done data" "1|T|done|abc1234|true" "$(echo "$OUT" | jget data.id)|$(echo "$OUT" | jget data.type)|$(echo "$OUT" | jget data.reason)|$(echo "$OUT" | jget data.commit)|$(echo "$OUT" | jget data.changed)"
    eq "done" "closed|completed|type:task|Done in \`abc1234\`" "$(IV 1)"
    eq "done marker" "<!-- rota:done -->" "$(MARKERS 1)"
    : > "$P/log"
    eq "done idempotent changed" "false" "$(DONE T1 --commit abc1234 | jget data.changed)"
    eq "done idempotent writes nothing" "0" "$(WRITES 'issue \(edit\|update\|close\)\|api -X')"

    # --no-proof + note
    DONE T2 --commit abc1234 --no-proof --note "shipped in PR" >/dev/null
    eq "done note" "closed|completed|type:task|Done in \`abc1234\` $DASH shipped in PR" "$(IV 2)"

    # dropped / handed-off: not planned (gitlab: label), no gate
    DONE T3 --commit abc1234 --reason dropped --note "not needed" >/dev/null
    eq "dropped" "closed|not_planned|${NPL}type:task|Closed: dropped $DASH not needed" "$(IV 3)"
    DONE T4 --commit abc1234 --reason handed-off >/dev/null
    eq "handed-off" "closed|not_planned|${NPL}type:task|Closed: handed-off" "$(IV 4)"

    # blocked: stays open, label + comment, other state labels kept; idempotent
    DONE T5 --commit abc1234 --reason blocked --note "waiting on X" >/dev/null
    eq "blocked" "open||blocked,in-progress,type:task|Blocked $DASH waiting on X" "$(IV 5)"
    eq "blocked marker" "<!-- rota:blocked -->" "$(MARKERS 5)"
    DONE T5 --commit abc1234 --reason blocked --note "again" >/dev/null
    eq "blocked idempotent" "open||blocked,in-progress,type:task|Blocked $DASH waiting on X" "$(IV 5)"
    # blocked then done: closes and clears blocked + in-progress
    DONE T5 --commit abc1234 --reason dropped >/dev/null
    eq "blocked then dropped" "closed|not_planned|${NPL}type:task|Blocked $DASH waiting on X // Closed: dropped" "$(IV 5)"

    # unknown / malformed refs: exit 3
    eq "unknown complete" "3" "$(rcof hvj item complete T99 --commit abc1234 --no-proof)"
    eq "unknown reopen" "3" "$(rcof hvj item reopen T99)"
    eq "type mismatch" "3" "$(rcof hvj item complete B1 --commit abc1234 --no-proof)"

    # reopen: reopens, removes not-planned/blocked; open+blocked unblocks; else no-op
    eq "reopen dropped changed" "true" "$(hvj item reopen T3 | jget data.changed)"
    eq "reopen dropped" "open||type:task|Closed: dropped $DASH not needed" "$(IV 3)"
    hvj item reopen T1 >/dev/null
    eq "reopen done" "open||type:task|Done in \`abc1234\`" "$(IV 1)"
    DONE T6 --commit abc1234 --reason blocked >/dev/null
    hvj item reopen T6 >/dev/null
    eq "unblock" "open||in-progress,type:task|Blocked" "$(IV 6)"
    : > "$P/log"
    rc=0; OUT="$(hvj item reopen T6)" || rc=$?
    eq "reopen noop rc" "0:false" "$rc:$(echo "$OUT" | jget data.changed)"
    eq "reopen noop writes nothing" "0" "$(WRITES 'issue \(edit\|update\|close\|reopen\)\|api -X')"
    # complete again after reopen works
    PROOF T1; DONE T1 --commit def5678 >/dev/null
    eq "recomplete" "closed|completed|type:task|Done in \`abc1234\` // Done in \`def5678\`" "$(IV 1)"

    # custom blocked label name
    echo "{\"backlog\":{\"backend\":\"issues\"},\"issues\":{\"provider\":\"$prov\",\"retryWaitSeconds\":0,\"labels\":{\"blocked\":\"stuck\"}}}" > "$P/.rota/config.json"
    DONE T6 --commit abc1234 --reason blocked >/dev/null
    eq "custom blocked label" "open||in-progress,stuck,type:task|Blocked // Blocked" "$(IV 6)"
    pass "$prov: item complete / reopen close reasons, labels, gate, idempotency"
  )
done
trap 'rm -rf "$TMP"' EXIT
