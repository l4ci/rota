echo "Issue mode: claim lock, readiness check, state labels"

TMP_CL="$(mktemp -d)"
trap 'rm -rf "$TMP_CL"' EXIT

for prov in github gitlab; do
  P="$TMP_CL/$prov"; mkdir -p "$P/.rota"
  echo "{\"backlog\":{\"backend\":\"issues\"},\"issues\":{\"provider\":\"$prov\",\"retryWaitSeconds\":0}}" > "$P/.rota/config.json"
  (
    cd "$P"
    git init -q && git config user.email t@t && git config user.name t && git commit -q --allow-empty -m seed
    export PATH="$TESTDIR/fakes:$PATH" FAKE_TRACKER_DB="$P/db.json" FAKE_TRACKER_LOG="$P/log"
    eq() { [ "$2" = "$3" ] || fail "$prov claim $1: expected [$2] got [$3]"; }
    J() { hvj "$@" 2>/dev/null; }
    rcof() { local rc=0; "$@" >/dev/null 2>&1 || rc=$?; echo "$rc"; }
    WRITES() { grep -c 'issue \(edit\|update\)\|api -X' "$P/log" || true; }
    # The fake tracker's store is the tracker's own state: no verb exposes raw labels or comment markers.
    DBQ() { python3 -c '
import json, sys
mode, n = sys.argv[1], int(sys.argv[2])
issue = next(i for i in json.load(open(sys.argv[3]))["issues"] if i["number"] == n)
if mode == "labels":
    print(",".join(sorted(issue["labels"])))
elif mode == "markers":
    print("|".join(c["body"].split("\n")[0] for c in issue["comments"]))
else:
    print(len(issue["assignees"]))' "$1" "$2" "$P/db.json"; }
    LABELS() { DBQ labels "$1"; }
    # MARKERS <n>: first line of every comment, joined by |
    MARKERS() { DBQ markers "$1"; }
    ASSIGNEES() { DBQ assignees "$1"; }

    "$ROTA_BIN" item create --kind tasks --title "Race" >/dev/null   # T1
    "$ROTA_BIN" item create --kind tasks --title "Other" >/dev/null  # T2

    # --- two claims race: A wins, B loses and releases
    OUT="$(J item claim T1 --as ann/t1)"
    eq "claim A" "1|T|ann/t1" "$(echo "$OUT" | jget data.id)|$(echo "$OUT" | jget data.type)|$(echo "$OUT" | jget data.claimId)"
    eq "A labels" "in-progress,type:task" "$(LABELS 1)"
    eq "A assigned" 1 "$(ASSIGNEES 1)"
    eq "A marker" "<!-- rota:claim ann/t1 -->" "$(MARKERS 1)"
    rc=0; OUT="$(J item claim '#1' --as bob)" || rc=$?
    eq "B loses" "4" "$rc"
    grep -q 'ann/t1' <<<"$(jget error.message <<<"$OUT")" || fail "$prov claim B loses: the refusal must name the holder: $OUT"
    eq "B posted claim then release" "<!-- rota:claim ann/t1 -->|<!-- rota:claim bob -->|<!-- rota:release bob -->" "$(MARKERS 1)"
    eq "loser leaves labels" "in-progress,type:task" "$(LABELS 1)"
    : > "$P/log"
    OUT="$(J item claim T1 --as ann/t1)"
    eq "A re-claim" "1|ann/t1" "$(echo "$OUT" | jget data.id)|$(echo "$OUT" | jget data.claimId)"
    eq "A re-claim posts nothing" "<!-- rota:claim ann/t1 -->|<!-- rota:claim bob -->|<!-- rota:release bob -->" "$(MARKERS 1)"
    eq "A re-claim writes nothing" 0 "$(WRITES)"

    # --- release then B claims
    "$ROTA_BIN" item release T1 --as bob >/dev/null   # no open claim by bob: no-op
    eq "release no-op" "<!-- rota:claim ann/t1 -->|<!-- rota:claim bob -->|<!-- rota:release bob -->" "$(MARKERS 1)"
    OUT="$(J item release T1 --as ann/t1)"
    eq "release data" "1|ann/t1" "$(echo "$OUT" | jget data.id)|$(echo "$OUT" | jget data.claimId)"
    eq "A released" "type:task" "$(LABELS 1)"
    eq "release marker" "<!-- rota:claim ann/t1 -->|<!-- rota:claim bob -->|<!-- rota:release bob -->|<!-- rota:release ann/t1 -->" "$(MARKERS 1)"
    OUT="$(J item claim T1 --as bob)"
    eq "B claims after release" "1|bob" "$(echo "$OUT" | jget data.id)|$(echo "$OUT" | jget data.claimId)"
    eq "B labels" "in-progress,type:task" "$(LABELS 1)"
    eq "A now loses" "4" "$(rcof "$ROTA_BIN" item claim T1 --as ann/t1)"

    # --- claim clears review states
    "$ROTA_BIN" item release T1 --as bob >/dev/null
    "$ROTA_BIN" item state T1 --to changes-requested >/dev/null
    eq "state cr" "changes-requested,type:task" "$(LABELS 1)"
    "$ROTA_BIN" item claim T1 --as ann/t1 >/dev/null
    eq "claim swaps state" "in-progress,type:task" "$(LABELS 1)"

    # --- item show reads state, claim, assignee and comments back, writing nothing
    printf 'Which db?\nsecond line\n' | "$ROTA_BIN" item comment add T1 --kind question --body-file - >/dev/null
    : > "$P/log"
    SHOW="$(J item show T1)"
    eq "show writes nothing" 0 "$(WRITES)"
    eq "show head" "1|T|Race|open|in-progress|ann/t1" "$(for k in id type title status state claimedBy; do echo "$SHOW" | jget "data.$k"; done | paste -sd'|')"
    eq "show assignee" '["fake-user"]' "$(echo "$SHOW" | jget data.assignees)"
    eq "show tail" "null|[]" "$(echo "$SHOW" | jget data.milestone)|$(echo "$SHOW" | jget data.notes)"
    eq "show comment row" "fake-user|question" "$(echo "$SHOW" | jget 'data.comments[0].who')|$(echo "$SHOW" | jget 'data.comments[0].kind')"
    eq "show comment continuation" "$(printf 'Which db?\nsecond line')" "$(echo "$SHOW" | jget 'data.comments[0].text')"
    eq "list kind filter" "[]" "$(J item comment list T1 --kind answer | jget data.comments)"
    eq "list matches show" "$(echo "$SHOW" | jget data.comments)" "$(J item comment list T1 --kind question | jget data.comments)"
    "$ROTA_BIN" item release T1 --as ann/t1 >/dev/null
    eq "show after release" "null|null" "$(J item show T1 | jget data.state)|$(J item show T1 | jget data.claimedBy)"
    "$ROTA_BIN" item claim T1 --as ann/t1 >/dev/null
    eq "show unknown" "3" "$(rcof "$ROTA_BIN" item show T99)"
    eq "list bad kind" "2" "$(rcof "$ROTA_BIN" item comment list T1 --kind bogus)"
    eq "list with body" "2" "$(rcof "$ROTA_BIN" item comment list T1 --body-file x)"

    # --- errors
    eq "claim unknown" "3" "$(rcof "$ROTA_BIN" item claim T99 --as ann)"
    eq "claim no --as" 2 "$(rcof "$ROTA_BIN" item claim T1)"
    eq "release bad id" 2 "$(rcof "$ROTA_BIN" item release T1 --as "a b")"
    eq "state bad" 2 "$(rcof "$ROTA_BIN" item state T1 --to bogus)"
    "$ROTA_BIN" tracker call -- issue close 2 </dev/null >/dev/null 2>&1 || true
    pass "$prov: claim race, re-claim, release, re-claim by the other, error paths"

    # --- state machine keeps one state label
    OUT="$(J item state T1 --to needs-review)"
    eq "state to needs-review" "1|needs-review" "$(echo "$OUT" | jget data.id)|$(echo "$OUT" | jget data.state)"
    eq "nr" "needs-review,type:task" "$(LABELS 1)"
    : > "$P/log"
    "$ROTA_BIN" item state T1 --to needs-review >/dev/null
    eq "state no-op writes nothing" 0 "$(WRITES)"
    : > "$P/log"
    "$ROTA_BIN" item state T1 --to in-progress >/dev/null
    eq "one edit per transition" 1 "$(WRITES)"
    eq "ip" "in-progress,type:task" "$(LABELS 1)"
    "$ROTA_BIN" item state T1 --to changes-requested >/dev/null
    eq "cr" "changes-requested,type:task" "$(LABELS 1)"
    eq "state none is null" "null" "$(J item state T1 --to none | jget data.state)"
    eq "none" "type:task" "$(LABELS 1)"
    "$ROTA_BIN" item state T1 --to none >/dev/null
    eq "state unknown" 3 "$(rcof "$ROTA_BIN" item state T99 --to none)"
    pass "$prov: item state keeps exactly one state label"

    # --- readiness
    for t in bare accept box plan; do "$ROTA_BIN" item create --kind tasks --title "Ready $t" >/dev/null; done  # T3..T6
    printf 'Intro\n\n## Acceptance criteria\n\n- it works\n' > "$P/a.md"
    printf 'Intro\n\n- [ ] first\n- [x] second\n' > "$P/c.md"
    "$ROTA_BIN" item create --kind tasks --title "Ready accept body" --body-file "$P/a.md" >/dev/null   # T7
    "$ROTA_BIN" item create --kind tasks --title "Ready box body" --body-file "$P/c.md" >/dev/null      # T8
    printf '## Plan\n\n1. do it\n' | "$ROTA_BIN" item note add T6 --kind plan --body-file - >/dev/null
    both='["no acceptance criteria in the issue body","no design or plan note"]'
    READY() { local rc=0 out; out="$(J item ready "$1")" || rc=$?; echo "$rc:$(echo "$out" | jget data.ready):$(echo "$out" | jget data.reasons)"; }
    eq "bare" "1:false:$both" "$(READY T3)"
    eq "accept heading" "0:true:[]" "$(READY T7)"
    eq "checkbox" "0:true:[]" "$(READY T8)"
    eq "plan note" "0:true:[]" "$(READY '#6')"
    "$ROTA_BIN" item note add T3 --kind design --body-file "$P/a.md" >/dev/null
    eq "design note" "0:true:[]" "$(READY T3)"
    eq "ready unknown" 3 "$(rcof "$ROTA_BIN" item ready T99)"
    pass "$prov: item ready reasons for bare, acceptance heading, checkbox, design/plan note"
  )
done

# --- file mode: claim/release/state are silent no-ops, ready reads the detail file and designs/plans
TMP_CLF="$(mktemp -d)"
trap 'rm -rf "$TMP_CL" "$TMP_CLF"' EXIT
mkdir -p "$TMP_CLF/.rota/tasks" "$TMP_CLF/.rota/designs" "$TMP_CLF/.rota/plans"
(
  cd "$TMP_CLF"
  git init -q && git config user.email t@t && git config user.name t && git commit -q --allow-empty -m seed
  printf '# Backlog\n\n## Bugs\n\n## Features\n\n## Tasks\n\n- **[T01] Bare.** x.\n- **[T02] Accept.** x.\n- **[T03] Boxes.** x.\n- **[T04] Designed.** x.\n- **[T05] Planned.** x.\n\n## Completed\n' > .rota/BACKLOG.md
  printf '## Acceptance\n\n- works\n' > .rota/tasks/T02.md
  printf -- '- [ ] one\n' > .rota/tasks/T03.md
  printf 'design\n' > .rota/designs/T04.md
  printf 'plan\n' > .rota/plans/M01-T05.md
  cp .rota/BACKLOG.md before.md
  fe() { [ "$2" = "$3" ] || fail "file-mode claim $1: expected [$2] got [$3]"; }
  J() { hvj "$@" 2>/dev/null; }
  OUT="$(J item claim T01 --as ann)"; fe "claim silent" "T01|false" "$(echo "$OUT" | jget data.id)|$(echo "$OUT" | jget data.changed)"
  OUT="$(J item release T01 --as ann)"; fe "release silent" "false" "$(echo "$OUT" | jget data.changed)"
  OUT="$(J item state T01 --to needs-review)"; fe "state silent" "false" "$(echo "$OUT" | jget data.changed)"
  "$ROTA_BIN" item claim T01 --as bob >/dev/null
  cmp -s before.md .rota/BACKLOG.md || fail "file-mode claim/release/state wrote BACKLOG.md"
  both='["no acceptance criteria in the issue body","no design or plan note"]'
  rc=0; OUT="$(J item ready T01)" || rc=$?; fe "bare" "1:false:$both" "$rc:$(echo "$OUT" | jget data.ready):$(echo "$OUT" | jget data.reasons)"
  for id in T02 T03 T04 T05; do
    rc=0; OUT="$(J item ready $id)" || rc=$?; fe "ready $id" "0:true:[]" "$rc:$(echo "$OUT" | jget data.ready):$(echo "$OUT" | jget data.reasons)"
  done
  rc=0; "$ROTA_BIN" item ready T99 >/dev/null 2>&1 || rc=$?; fe "unknown" "3" "$rc"
)
trap 'rm -rf "$TMP"' EXIT
pass "file backend: claim/release/state are silent no-ops, item ready reads detail file, designs and plans"
