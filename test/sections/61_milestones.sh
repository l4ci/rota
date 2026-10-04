echo "Issue mode: native milestones, tracking issues, milestone verbs, slice plan notes"

TMP_MS="$(mktemp -d)"
trap 'rm -rf "$TMP_MS"' EXIT

for prov in github gitlab; do
  P="$TMP_MS/$prov"; mkdir -p "$P/.rota"
  echo "{\"backlog\":{\"backend\":\"issues\"},\"issues\":{\"provider\":\"$prov\",\"retryWaitSeconds\":0}}" > "$P/.rota/config.json"
  (
    cd "$P"
    git init -q && git config user.email t@t && git config user.name t && git commit -q --allow-empty -m seed
    printf '# Project\n' > CLAUDE.md
    export PATH="$TESTDIR/fakes:$PATH" FAKE_TRACKER_DB="$P/db.json" FAKE_TRACKER_LOG="$P/log"
    eq() { [ "$2" = "$3" ] || fail "$prov $1: expected [$2] got [$3]"; }
    # RC <rota call>: exit code in RCV, stdout (the envelope) in OUT
    RC() { local rc=0; OUT="$("$@" 2>/dev/null)" || rc=$?; RCV=$rc; }
    # The fake tracker's own store stands in for reading the forge.
    # ISSUE n -> state|reason|sorted labels|native milestone
    ISSUE() { PROV="$prov" DBF="$P/db.json" python3 -c '
import json, os, sys
d = json.load(open(os.environ["DBF"]))
i = next(x for x in d["issues"] if x["number"] == int(sys.argv[1]))
r = (i["state_reason"] or "").lower().replace(" ", "_") or None
if i["state"] == "closed" and os.environ["PROV"] == "gitlab":
    r = "not_planned" if "not-planned" in i["labels"] else "completed"
ms = i["milestone"]
print("|".join([i["state"], str(r), ",".join(sorted(i["labels"])), str(ms[0] if isinstance(ms, list) else ms)]))' "$1"; }
    # NATIVE MNN -> state of the native milestone whose title starts with MNN
    NATIVE() { DBF="$P/db.json" python3 -c '
import json, os, sys
d = json.load(open(os.environ["DBF"]))
print(",".join(m["state"] for m in d["milestones"] if m["title"].startswith(sys.argv[1] + " ")))' "$1"; }
    # SUMMARY -> id:status:ready:depends per milestone
    SUMMARY() { hvj milestone list 2>/dev/null | python3 -c '
import json, sys
print(" ".join("%s:%s:%s:%s" % (m["id"], m["status"], str(m["ready"]).lower(), "+".join(m["depends"])) for m in json.load(sys.stdin)["data"]["milestones"]))'; }
    # SEED_TRACKER <title> <body>: a tracking issue made straight on the forge
    SEED_TRACKER() {
      if [ "$prov" = github ]; then
        hvj tracker call -- issue create --title "$1" --body "$2" --label milestone-tracker >/dev/null
      else
        hvj tracker call -- issue create --title "$1" --description "$2" --label milestone-tracker -y >/dev/null
      fi
    }

    # --- ID minting, add, list shape
    eq "empty list" "[]" "$(hvj milestone list | jget data.milestones)"
    eq "first id" "M01" "$(hvj milestone add --title "Alpha" --summary "First summary" | jget data.id)"
    eq "second id" "M02" "$(hvj milestone add --title "Beta" --summary "Second summary" --depends M01 | jget data.id)"
    eq "list shape" "M01:planned:true: M02:planned:false:M01" "$(SUMMARY)"
    eq "list keys" "id,title,status,depends,ready" "$(hvj milestone list | python3 -c 'import json,sys; print(",".join(json.load(sys.stdin)["data"]["milestones"][0]))')"
    eq "list titles" "Alpha|Beta" "$(hvj milestone list | python3 -c 'import json,sys; print("|".join(m["title"] for m in json.load(sys.stdin)["data"]["milestones"]))')"
    eq "tracking issue" "open|None|milestone-tracker,status:planned|M01 — Alpha" "$(ISSUE 1)"
    eq "tracking issue with deps" "open|None|milestone-tracker,status:planned|M02 — Beta" "$(ISSUE 2)"
    eq "native open" "open" "$(NATIVE M01)"
    python3 - "$P/db.json" <<'PY' || fail "$prov native milestone fields"
import json, sys
d = json.load(open(sys.argv[1]))
ms = {m["title"]: m for m in d["milestones"]}
assert ms["M01 — Alpha"]["description"] == "First summary", ms
assert isinstance(ms["M01 — Alpha"]["number"], int)
issues = {i["number"]: i for i in d["issues"]}
body = issues[2]["body"]
assert body.startswith("---\nid: M02\ntitle: Beta\nstatus: planned\ndepends: [M01]\n"), body
assert "\n# M02 — Beta\n\n## Goal\n\nSecond summary\n" in body, body
assert body.endswith("<!-- rota:fields\nDepends: M01\n-->"), body
assert issues[1]["body"].count("rota:fields") == 0
PY

    # --- status transitions
    eq "status changed" "true" "$(hvj milestone status M01 --to active | jget data.changed)"
    eq "active labels" "open|None|milestone-tracker,status:active|M01 — Alpha" "$(ISSUE 1)"
    eq "active listed" '["M01"]' "$(hvj milestone active | jget data.ids)"
    OUT="$(hvj milestone show M01)" || fail "$prov milestone show M01 failed"
    OUT="$(jget data.body <<<"$OUT")"
    eq "frontmatter synced" "status: active" "$(grep -m1 "^status:" <<<"$OUT")"
    hvj milestone status M01 --to shipped >/dev/null || fail "$prov status shipped failed"
    eq "shipped closes completed" "closed|completed|milestone-tracker,status:shipped|M01 — Alpha" "$(ISSUE 1)"
    eq "shipped closes native milestone" "closed" "$(NATIVE M01)"
    OUT="$(hvj milestone show M01)" || fail "$prov milestone show M01 failed"
    OUT="$(jget data.body <<<"$OUT")"
    eq "shipped frontmatter" "status: shipped" "$(grep -m1 "^status:" <<<"$OUT")"
    case "$(SUMMARY)" in "M01:shipped:true: M02:planned:true:M01") ;; *) fail "$prov ready after ship: $(SUMMARY)" ;; esac
    hvj milestone status M02 --to archived >/dev/null || fail "$prov status archived failed"
    eq "archived closes not planned" "closed|not_planned" "$(ISSUE 2 | cut -d'|' -f1,2)"
    eq "archived closes native milestone" "closed" "$(NATIVE M02)"
    hvj milestone status M02 --to planned >/dev/null || fail "$prov status planned failed"
    eq "planned reopens" "open|None|milestone-tracker,status:planned|M02 — Beta" "$(ISSUE 2)"
    eq "planned reopens native milestone" "open" "$(NATIVE M02)"
    hvj milestone status M02 --to shipped >/dev/null || fail "$prov status shipped (M02) failed"
    eq "archived -> planned -> shipped reads completed" "closed|completed|milestone-tracker,status:shipped|M02 — Beta" "$(ISSUE 2)"
    hvj milestone status M02 --to active >/dev/null || fail "$prov status active (M02) failed"
    hvj milestone status M01 --to active >/dev/null || fail "$prov status active (M01) failed"
    eq "shipped -> active reopens issue" "open|None|milestone-tracker,status:active|M01 — Alpha" "$(ISSUE 1)"
    eq "shipped -> active reopens milestone" "open" "$(NATIVE M01)"
    hvj milestone status M01 --to shipped >/dev/null || fail "$prov status shipped (M01) failed"
    STATE_CALLS() { grep -cE 'issue (close|reopen)' "$P/log" || true; }
    before="$(STATE_CALLS)"; state="$(ISSUE 1)"
    eq "re-status reports unchanged" "false" "$(hvj milestone status M01 --to shipped | jget data.changed)"
    eq "re-status keeps state" "$state" "$(ISSUE 1)"
    eq "re-status adds no close/reopen" "$before" "$(STATE_CALLS)"
    RC hvj milestone status M99 --to active
    eq "status unknown id" "3" "$RCV"
    RC hvj milestone status M01 --to bogus
    eq "status bad value" "2" "$RCV"

    # --- show / put round trip
    "$ROTA_BIN" milestone show M02 > "$P/m02.md" || fail "$prov milestone show M02 failed"
    eq "show starts with frontmatter" "---" "$(head -1 "$P/m02.md")"
    case "$(cat "$P/m02.md")" in *'rota:fields'*) fail "$prov show leaks the fields block" ;; esac
    printf '\n## Extra section\n\nLonger plan text.\n' >> "$P/m02.md"
    sed -i 's/^depends: .*/depends: [M01, M05]/' "$P/m02.md"
    eq "put changed" "true" "$(hvj milestone put M02 --body-file "$P/m02.md" | jget data.changed)"
    eq "put round trip" "$(sed 's/^status: .*/status: active/' "$P/m02.md")" "$("$ROTA_BIN" milestone show M02)"
    eq "put updates depends" "M02:active:false:M01+M05" "$(SUMMARY | tr ' ' '\n' | grep '^M02')"
    sed -i 's/^status: .*/status: shipped/' "$P/m02.md"
    hvj milestone put M02 --body-file - < "$P/m02.md" >/dev/null || fail "$prov put from stdin failed"
    OUT="$(hvj milestone show M02)" || fail "$prov milestone show M02 failed"
    OUT="$(jget data.body <<<"$OUT")"
    eq "put keeps status label authoritative" "status: active" "$(grep -m1 "^status:" <<<"$OUT")"
    eq "put of the same body is unchanged" "false" "$(hvj milestone put M02 --body-file - < "$P/m02.md" | jget data.changed)"
    RC hvj milestone put M02 --body-file "$P/missing.md"
    eq "put unreadable body" "2" "$RCV"
    sed 's/^id: M02/id: M03/' "$P/m02.md" > "$P/bad.md"
    RC hvj milestone put M02 --body-file "$P/bad.md"
    eq "put id mismatch" "4" "$RCV"
    printf 'no frontmatter\n' > "$P/nofm.md"
    RC hvj milestone put M02 --body-file "$P/nofm.md"
    eq "put without frontmatter" "4" "$RCV"
    sed 's/^id: M02/id: M77/' "$P/m02.md" > "$P/m77.md"
    RC hvj milestone put M77 --body-file "$P/m77.md"
    eq "put unknown milestone" "3" "$RCV"
    RC hvj milestone show M77
    eq "show unknown milestone" "3" "$RCV"
    RC hvj milestone put M02
    eq "put usage" "2" "$RCV"

    # --- duplicate tracking issues: lowest open wins
    SEED_TRACKER "M02 — duplicate" $'---\nid: M02\n---\n' || fail "$prov seeding the duplicate tracking issue failed"
    RC hvj milestone list
    eq "duplicate list exits 0" "0" "$RCV"
    eq "duplicate leaves winner" "M02:active:false:M01+M05" "$(SUMMARY | tr ' ' '\n' | grep '^M02')"
  )
done

# --- verb round trip with its own project: add -> put -> active -> index -> slice plans -> shipped
for prov in github gitlab; do
  P="$TMP_MS/rt-$prov"; mkdir -p "$P/.rota"
  echo "{\"backlog\":{\"backend\":\"issues\"},\"issues\":{\"provider\":\"$prov\",\"retryWaitSeconds\":0}}" > "$P/.rota/config.json"
  (
    cd "$P"
    git init -q && git config user.email t@t && git config user.name t && git commit -q --allow-empty -m seed
    printf '# Project\n\nIntro.\n' > CLAUDE.md
    export PATH="$TESTDIR/fakes:$PATH" FAKE_TRACKER_DB="$P/db.json" FAKE_TRACKER_LOG="$P/log"
    eq() { [ "$2" = "$3" ] || fail "$prov $1: expected [$2] got [$3]"; }
    RC() { local rc=0; OUT="$("$@" 2>/dev/null)" || rc=$?; RCV=$rc; }
    HAS() { grep -qF -- "$2" "$1" || fail "$prov $3: [$2] not in $1: $(cat "$1")"; }
    NOT() { if grep -qF -- "$2" "$1"; then fail "$prov $3: [$2] unexpectedly in $1"; fi; }
    # EMPTY_ACTIVE: active milestones that no open item carries, from milestone active + backlog ids
    EMPTY_ACTIVE() {
      local id out=""
      for id in $(hvj milestone active | python3 -c 'import json,sys; print(" ".join(json.load(sys.stdin)["data"]["ids"]))'); do
        if [ "$(hvj backlog ids --milestone "$id" | jget data.ids)" = "[]" ]; then out="$out $id"; fi
      done
      echo "${out# }"
    }

    eq "add" "M01" "$(hvj milestone add --title "Launch" --summary "Ship the thing" | jget data.id)"
    eq "add dep" "M02" "$(hvj milestone add --title "Scale" --summary "Grow it" --depends M01 | jget data.id)"
    [ ! -e .rota/milestones ] || fail "$prov issue mode wrote .rota/milestones"
    OUT="$("$ROTA_BIN" milestone show M01)" || fail "$prov milestone show M01 failed"
    sed 's/_(define what shipped looks like)_/Users can sign up./' <<<"$OUT" > body.md
    hvj milestone put M01 --body-file body.md >/dev/null || fail "$prov put failed"
    "$ROTA_BIN" milestone show M01 > shown.md || fail "$prov show failed"
    HAS shown.md "Users can sign up." "put body"
    hvj milestone status M01 --to active >/dev/null || fail "$prov status active failed"
    HAS .rota/MILESTONES.md "- M01 — Launch" "active list"
    NOT .rota/MILESTONES.md "M02 —" "planned milestone not in the active list"
    HAS .rota/MILESTONES.md "# Milestones" "seeded H1"
    HAS .rota/MILESTONES.md "_(no vision yet" "seeded vision paragraph"
    HAS .rota/MILESTONES.md "## Milestones" "seeded milestones heading"
    HAS CLAUDE.md "- **M01** — Launch (depends: —)" "CLAUDE.md block"
    HAS CLAUDE.md "the tracking issues" "CLAUDE.md issue-mode pointer"
    HAS CLAUDE.md "Intro." "CLAUDE.md prose kept"
    NOT .rota/MILESTONES.md "### M01" "no per-milestone overview section"
    eq "index idempotent" "false" "$(hvj milestone index | jget data.changed)"
    eq "index keeps one block" "1" "$(grep -c 'rota-vision-start' CLAUDE.md)"
    hvj milestone status M02 --to active >/dev/null || fail "$prov status active (M02) failed"
    HAS CLAUDE.md "- **M02** — Scale (depends: M01) ⚠ blocked" "blocked flag"
    eq "active ids" '["M01","M02"]' "$(hvj milestone active | jget data.ids)"
    eq "active milestones without items" "M01 M02" "$(EMPTY_ACTIVE)"
    hvj item create --kind tasks --title "In M01" --milestone M01 >/dev/null || fail "$prov item create failed"
    eq "active milestones skip those with items" "M02" "$(EMPTY_ACTIVE)"

    SEED_M1X() {
      if [ "$prov" = github ]; then
        hvj tracker call -- issue create --title "M1x stuff" --body "" --label milestone-tracker >/dev/null
      else
        hvj tracker call -- issue create --title "M1x stuff" --description "" --label milestone-tracker -y >/dev/null
      fi
    }
    SEED_M1X || fail "$prov seeding the M1x tracking issue failed"
    eq "M1x title ignored" "M01 M02" "$(hvj milestone list | python3 -c 'import json,sys; print(" ".join(m["id"] for m in json.load(sys.stdin)["data"]["milestones"]))')"

    # slice plans
    eq "slice 1" "M01-S01" "$(hvj plan add --milestone M01 --slice --title "First slice" | jget data.key)"
    eq "slice 2" "M01-S02" "$(hvj plan add --milestone M01 --slice --title "Second slice" | jget data.key)"
    hvj plan show M01-S01 | jget data.body > shown.md || fail "$prov plan show failed"
    HAS shown.md "# M01-S01 — First slice" "slice show"
    HAS shown.md "unitKind: slice" "slice frontmatter"
    eq "slice design pointer" "M01-S03" "$(hvj plan add --milestone M01 --slice --design F07 --title "Designed" | jget data.key)"
    hvj plan show M01-S03 | jget data.body > shown.md || fail "$prov plan show S03 failed"
    HAS shown.md "design: note:F07:design" "slice design pointer"
    RC hvj plan add --milestone M01 --slice --design M01 --title "Bad design"
    eq "slice design needs an item id" "2" "$RCV"
    hvj plan rm M01-S03 >/dev/null || fail "$prov plan rm S03 failed"
    [ ! -e .rota/plans/M01-S01.md ] || fail "$prov slice plan written as a file"
    RC hvj plan add M01-S01 --title "dup"
    eq "slice duplicate" "4" "$RCV"
    RC hvj plan add --milestone M09 --slice --title "no tracker"
    eq "slice on unknown milestone" "3" "$RCV"
    OUT="$("$ROTA_BIN" plan show M01-S01)" || fail "$prov plan show M01-S01 failed"
    sed 's/^status: planned/status: active/' <<<"$OUT" > plan.md
    hvj plan put M01-S01 --body-file plan.md >/dev/null || fail "$prov plan put failed"
    eq "slice put/show" "$(cat plan.md)" "$("$ROTA_BIN" plan show M01-S01)"
    RC hvj plan put M01-S07 --body-file plan.md
    eq "slice put missing" "3" "$RCV"
    hvj plan list > plan-list.json 2>/dev/null || fail "$prov plan list failed"
    eq "plan-list note" "item plans live on their issues" "$(jget 'warnings[0]' < plan-list.json | cut -c1-31)"
    eq "plan-list" "M01-S01:slice:active:First slice M01-S02:slice:planned:Second slice" "$(python3 -c '
import json
print(" ".join("%s:%s:%s:%s" % (p["key"], p["unitKind"], p["status"], p["title"]) for p in json.load(open("plan-list.json"))["data"]["plans"]))')"
    eq "plan-list keys" "key,milestone,unit,unitKind,title,status,created,repos" "$(python3 -c 'import json; print(",".join(json.load(open("plan-list.json"))["data"]["plans"][0]))')"
    eq "plan-list filter other milestone" "[]" "$(hvj plan list --milestone M02 2>/dev/null | jget data.plans)"
    eq "plan-list filter" "2" "$(hvj plan list --milestone M01 2>/dev/null | python3 -c 'import json,sys; print(len(json.load(sys.stdin)["data"]["plans"]))')"
    hvj plan rm M01-S02 >/dev/null || fail "$prov plan rm S02 failed"
    RC hvj plan show M01-S02
    eq "slice rm" "3" "$RCV"
    RC hvj plan rm M01-S02
    eq "slice rm twice" "3" "$RCV"
    eq "slice re-mint" "M01-S02" "$(hvj plan add --milestone M01 --slice --title "Again" | jget data.key)"

    # ship
    hvj milestone status M01 --to shipped >/dev/null || fail "$prov ship M01 failed"
    hvj milestone status M02 --to shipped >/dev/null || fail "$prov ship M02 failed"
    HAS .rota/MILESTONES.md "_(none active" "active list emptied"
    HAS CLAUDE.md "all shipped or archived" "CLAUDE.md after ship"
    eq "nothing active" "[]" "$(hvj milestone active | jget data.ids)"
  )
done

# --- file mode: milestone put
P="$TMP_MS/file"; mkdir -p "$P/.rota/milestones"
(
  cd "$P"
  git init -q && git config user.email t@t && git config user.name t && git commit -q --allow-empty -m seed
  echo '{}' > .rota/counters.json
  eq() { [ "$2" = "$3" ] || fail "file mode $1: expected [$2] got [$3]"; }
  RC() { local rc=0; OUT="$("$@" 2>/dev/null)" || rc=$?; RCV=$rc; }
  eq "add" "M01" "$(hvj milestone add --title "Local" --summary "On disk" | jget data.id)"
  sed 's/^title: Local/title: Local renamed/' .rota/milestones/M01.md > "$P/new.md"
  eq "put changed" "true" "$(hvj milestone put M01 --body-file "$P/new.md" | jget data.changed)"
  eq "put writes the file" "$(cat "$P/new.md")" "$(cat .rota/milestones/M01.md)"
  printf -- '---\nid: M01\ntitle: Via stdin\nstatus: planned\n---\n# body\n' | hvj milestone put M01 --body-file - >/dev/null || fail "file mode put from stdin failed"
  eq "put from stdin" "title: Via stdin" "$(sed -n 3p .rota/milestones/M01.md)"
  cp .rota/milestones/M01.md before.md
  printf -- '---\nid: M02\ntitle: x\n---\n' > wrong.md
  RC hvj milestone put M01 --body-file wrong.md
  eq "id mismatch" "4" "$RCV"
  printf 'no frontmatter\n' > nofm.md
  RC hvj milestone put M01 --body-file nofm.md
  eq "no frontmatter" "4" "$RCV"
  eq "refused put leaves the file" "$(cat before.md)" "$(cat .rota/milestones/M01.md)"
  printf -- '---\nid: M09\n---\n' > m09.md
  RC hvj milestone put M09 --body-file m09.md
  eq "unknown milestone" "3" "$RCV"
  [ ! -e .rota/milestones/M09.md ] || fail "file mode put created M09"
  RC hvj milestone put notanid --body-file m09.md
  eq "bad id" "2" "$RCV"
  RC hvj milestone put M01 --body-file "$P/nope.md"
  eq "unreadable body" "2" "$RCV"
  RC hvj milestone put M01
  eq "usage" "2" "$RCV"
)

rm -rf "$TMP_MS"
trap 'rm -rf "$TMP"' EXIT
pass "milestones: native milestone + tracking issue, status transitions, milestone and slice-plan verbs (github, gitlab), milestone put (file)"
