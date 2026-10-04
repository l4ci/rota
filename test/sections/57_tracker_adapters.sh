echo "tracker adapters (through tracker call)"

TMP_TA="$(mktemp -d)"
trap 'rm -rf "$TMP_TA"' EXIT

for prov in github gitlab; do
  P="$TMP_TA/$prov"; mkdir -p "$P/.rota"
  echo "{\"issues\":{\"provider\":\"$prov\",\"retryWaitSeconds\":0}}" > "$P/.rota/config.json"
  (
    cd "$P"
    export PATH="$TESTDIR/fakes:$PATH" FAKE_TRACKER_DB="$P/db.json" FAKE_TRACKER_LOG="$P/log"
    # TCALL <args…>: runs the forge CLI through the verb and prints data.stdout
    TCALL() { local out; out=$(hvj tracker call -- "$@" </dev/null) || return $?; echo "$out" | jget data.stdout; }
    # field <json> <python expr over d>: assert the expression is truthy
    field() { printf '%s' "$1" | python3 -c "import json,sys; d=json.load(sys.stdin); sys.exit(0 if ($2) else 1)"; }
    if [ "$prov" = github ]; then
      TCALL api repos/fake/repo/milestones -f title=M07 >/dev/null
      for l in bug p1 extra; do TCALL label create "$l" >/dev/null; done
      TCALL issue create --title First --body "line1" --label bug --label p1 --milestone M07 >/dev/null
      TCALL issue create --title Second --body b2 --label bug >/dev/null
      LIST=(issue list --json number,title,labels,milestone,state)
      CLOSE=(issue close 2)
      EDIT=(issue edit 1 --title "First!" --add-label extra --remove-label p1)
      COMMENT=(issue comment 1 --body hello)
      VIEW=(issue view 1 --json title,labels,comments)
    else
      TCALL api projects/:id/milestones -f title=M07 >/dev/null
      TCALL issue create --title First --description "line1" --label "bug,p1" --milestone M07 -y >/dev/null
      TCALL issue create --title Second --description b2 --label bug -y >/dev/null
      LIST=(issue list --output json)
      CLOSE=(issue close 2)
      EDIT=(issue update 1 --title "First!" --label extra --unlabel p1)
      COMMENT=(issue note 1 --message hello)
      VIEW=(issue view 1 --output json --comments)
    fi
    field "$(TCALL "${LIST[@]}")" "len(d)==2" || fail "$prov: both created issues should be listed"
    TCALL "${CLOSE[@]}" >/dev/null
    field "$(TCALL "${LIST[@]}")" "len(d)==1 and d[0]['title']=='First'" || fail "$prov: closed issue should leave the open list"
    TCALL "${EDIT[@]}" >/dev/null
    TCALL "${COMMENT[@]}" >/dev/null
    out="$(TCALL "${VIEW[@]}")"
    field "$out" "d['title']=='First!'" || fail "$prov: edit should change the title: $out"
    field "$out" "len(d.get('comments') or d.get('notes'))==1" || fail "$prov: comment should be stored: $out"
    # a failing CLI call surfaces as exit 1 with the CLI's own exit code in data
    rc=0; out=$(FAKE_TRACKER_FAIL="issue view" hvj tracker call -- "${VIEW[@]}" </dev/null 2>/dev/null) || rc=$?
    [ "$rc" = 1 ] && [ "$(echo "$out" | jget data.exitCode)" = "1" ] || fail "$prov: forced CLI failure should exit 1 with exitCode 1 (rc=$rc): $out"
  )
  pass "$prov: create/list/close/edit/comment and CLI failure through tracker call"
done

# provider resolution failure: no issues.provider and no remote
(
  cd "$TMP_TA"; mkdir -p none/.rota; cd none; echo '{}' > .rota/config.json
  git init -q . 2>/dev/null
  rc=0; hvj tracker call -- issue list </dev/null >/dev/null 2>&1 || rc=$?
  [ "$rc" = 5 ] || fail "tracker call without a resolvable provider should exit 5 (got $rc)"
)
trap 'rm -rf "$TMP"' EXIT
pass "tracker call exits 5 when the provider cannot be resolved"

trap 'rm -rf "$TMP"' EXIT
