echo "fake gh/glab (test/fakes)"

TMP_FT="$(mktemp -d)"
trap 'rm -rf "$TMP_FT"' EXIT
FAKES="$TESTDIR/fakes"
mkdir -p "$TMP_FT/proj/.rota"
echo '{"issues":{"provider":"github","retryWaitSeconds":0}}' > "$TMP_FT/proj/.rota/config.json"

# py <json> <python expr over d> : assert the expression is truthy
py() { printf '%s' "$1" | python3 -c "import json,sys; d=json.load(sys.stdin); sys.exit(0 if ($2) else 1)"; }

(
  export PATH="$FAKES:$PATH"
  export FAKE_TRACKER_DB="$TMP_FT/gh.json" FAKE_TRACKER_LOG="$TMP_FT/gh.log"

  gh auth status || fail "gh auth status should exit 0"
  out="$(gh api -X POST repos/o/r/milestones -f title="M01 — One" -f description=d)"
  py "$out" "d['number']==1 and d['state']=='open' and d['title'].startswith('M01')" || fail "gh milestone create shape: $out"
  py "$(gh api --paginate 'repos/o/r/milestones?state=all')" "len(d)==1 and d[0]['number']==1" || fail "gh milestone list"
  gh label create type:feature --force --color 00ff00
  gh label create prio:1
  py "$(gh label list --json name)" "[x['name'] for x in d]==['type:feature','prio:1']" || fail "gh label list shape"
  if gh label create prio:1 2>/dev/null; then fail "gh label create dup without --force should fail"; fi

  u1="$(gh issue create --title "First" --body "body one" --label type:feature --label prio:1 --milestone "M01 — One")"
  [ "$u1" = "https://github.com/fake/repo/issues/1" ] || fail "gh create should print URL (got $u1)"
  u2="$(printf 'from stdin' | gh issue create --title "Second" --body-file - --label type:feature)"
  [ "$u2" = "https://github.com/fake/repo/issues/2" ] || fail "gh issue numbers should auto-increment"
  if gh issue create --title X --body Y --milestone nope 2>"$TMP_FT/err"; then fail "unknown milestone should fail"; fi
  grep -q "milestone" "$TMP_FT/err" || fail "gh unknown milestone message"
  if gh issue create --title X --body Y --label ghost 2>/dev/null; then fail "gh unknown label should fail"; fi
  pass "gh: auth, milestones, labels, create"

  out="$(gh issue list --json number,title,labels,milestone,state)"
  py "$out" "[x['number'] for x in d]==[2,1] and set(d[0])=={'number','title','labels','milestone','state'}" || fail "gh list newest-first/only requested fields: $out"
  py "$out" "d[1]['labels']==[{'name':'type:feature'},{'name':'prio:1'}] and d[1]['milestone']=={'title':'M01 — One','number':1} and d[0]['milestone'] is None and d[1]['state']=='OPEN'" || fail "gh list field shapes: $out"
  out="$(gh issue view 2 --json body,url,closedAt,assignees,comments)"
  py "$out" "d['body']=='from stdin' and d['url'].endswith('/issues/2') and d['closedAt'] is None and d['assignees']==[] and d['comments']==[]" || fail "gh view shape: $out"
  py "$(gh issue list --label prio:1 --json number)" "[x['number'] for x in d]==[1]" || fail "gh list --label filter"
  py "$(gh issue list --milestone 'M01 — One' --json number)" "[x['number'] for x in d]==[1]" || fail "gh list --milestone filter"
  py "$(gh issue list --limit 1 --json number)" "len(d)==1" || fail "gh list --limit"
  pass "gh: list and view shapes"

  gh issue edit 1 --body "edited" --add-label prio:1 --remove-label type:feature --add-assignee bob >/dev/null
  gh issue edit 2 --milestone "M01 — One" >/dev/null
  out="$(gh issue view 1 --json body,labels,assignees)"
  py "$out" "d['body']=='edited' and d['labels']==[{'name':'prio:1'}] and d['assignees']==[{'login':'bob'}]" || fail "gh edit: $out"
  py "$(gh issue view 2 --json milestone)" "d['milestone']['number']==1" || fail "gh edit --milestone"
  gh issue edit 2 --remove-milestone >/dev/null
  py "$(gh issue view 2 --json milestone)" "d['milestone'] is None" || fail "gh edit --remove-milestone"
  printf 'new body' | gh issue edit 2 --body-file - >/dev/null
  py "$(gh issue view 2 --json body)" "d['body']=='new body'" || fail "gh edit --body-file -"
  pass "gh: edit"

  gh issue comment 1 --body "hello" >/dev/null
  printf 'two' | gh issue comment 1 --body-file - >/dev/null
  out="$(gh api --paginate repos/o/r/issues/1/comments)"
  py "$out" "[c['body'] for c in d]==['hello','two'] and d[0]['user']['login'] and d[1]['id']==2" || fail "gh api comments list: $out"
  gh api -X PATCH repos/o/r/issues/comments/2 -f body=changed >/dev/null
  py "$(gh issue view 1 --json comments)" "d['comments'][1]['body']=='changed' and d['comments'][0]['author']['login']" || fail "gh comment PATCH"
  gh api repos/o/r/milestones/1 -X PATCH -f state=closed >/dev/null
  py "$(gh api repos/o/r/milestones)" "d[0]['state']=='closed'" || fail "gh milestone close"
  pass "gh: comments and api PATCH"

  gh issue close 1 --reason "not planned" --comment "bye" >/dev/null
  py "$(gh issue list --json number)" "[x['number'] for x in d]==[2]" || fail "gh closed issue leaves open list"
  out="$(gh issue list --state closed --json number,state,closedAt)"
  py "$out" "len(d)==1 and d[0]['state']=='CLOSED' and d[0]['closedAt']" || fail "gh closed list: $out"
  py "$(gh issue list --state all --json number)" "[x['number'] for x in d]==[2,1]" || fail "gh --state all"
  py "$(gh issue view 1 --json comments)" "len(d['comments'])==3" || fail "gh close --comment adds a comment"
  gh issue reopen 1
  py "$(gh issue view 1 --json state,closedAt)" "d=={'state':'OPEN','closedAt':None}" || fail "gh reopen"
  rc=0; gh pr review 1 2>"$TMP_FT/err" || rc=$?
  [ "$rc" = 2 ] && grep -q "fake gh: unsupported: pr review" "$TMP_FT/err" || fail "gh unsupported should exit 2 with message"
  FAKE_TRACKER_FAIL="issue view 99" rc=0 gh issue view 99 --json number 2>/dev/null || rc=$?
  [ "$rc" = 1 ] || fail "FAKE_TRACKER_FAIL should force exit 1"
  grep -q "^issue create --title First" "$TMP_FT/gh.log" || fail "argv log should record calls"
  pass "gh: close/reopen, failure injection, unsupported"
)

(
  export PATH="$FAKES:$PATH"
  export FAKE_TRACKER_DB="$TMP_FT/gl.json" FAKE_TRACKER_LOG="$TMP_FT/gl.log"

  glab auth status || fail "glab auth status should exit 0"
  out="$(glab api -X POST projects/:id/milestones -f title="M01 — One" -f description=d)"
  py "$out" "d['iid']==1 and d['state']=='active'" || fail "glab milestone create: $out"
  glab label create --name type:feature --color '#00ff00'
  py "$(glab label list --output json)" "[x['name'] for x in d]==['type:feature']" || fail "glab label list"
  u1="$(glab issue create --title First --description "body one" --label "type:feature,prio:1" --milestone "M01 — One" -y)"
  [ "$u1" = "https://gitlab.com/fake/repo/-/issues/1" ] || fail "glab create should print web URL (got $u1)"
  u2="$(glab issue create --title Second --description "d2" --yes)"
  [ "$u2" = "https://gitlab.com/fake/repo/-/issues/2" ] || fail "glab numbering"
  if glab issue create --title X --description Y --milestone nope -y 2>/dev/null; then fail "glab unknown milestone should fail"; fi
  pass "glab: auth, milestones, labels, create"

  out="$(glab issue list --output json)"
  py "$out" "[x['iid'] for x in d]==[2,1] and d[1]['labels']==['type:feature','prio:1'] and d[1]['milestone']=={'title':'M01 — One','iid':1} and d[0]['milestone'] is None" || fail "glab list shape: $out"
  py "$out" "d[1]['state']=='opened' and d[1]['description']=='body one' and d[1]['web_url'].endswith('/-/issues/1') and d[1]['closed_at'] is None and d[1]['assignees']==[]" || fail "glab list fields: $out"
  py "$(glab issue list -O json --label prio:1)" "[x['iid'] for x in d]==[1]" || fail "glab label filter"
  py "$(glab issue list -O json --milestone 'M01 — One')" "[x['iid'] for x in d]==[1]" || fail "glab milestone filter"
  py "$(glab issue view 1 --output json)" "d['iid']==1 and 'notes' not in d" || fail "glab view without comments"
  pass "glab: list and view shapes"

  glab issue update 1 --description edited --label extra --unlabel prio:1 --assignee bob >/dev/null
  out="$(glab issue view 1 --output json)"
  py "$out" "d['description']=='edited' and d['labels']==['type:feature','extra'] and d['assignees']==[{'username':'bob'}]" || fail "glab update: $out"
  glab issue update 1 --unassign >/dev/null
  py "$(glab issue view 1 -O json)" "d['assignees']==[]" || fail "glab --unassign"
  glab issue note 1 --message hello >/dev/null
  glab issue note 1 --message two >/dev/null
  out="$(glab issue view 1 --output json --comments)"
  py "$out" "[n['body'] for n in d['notes']]==['hello','two'] and d['notes'][0]['author']['username']" || fail "glab view --comments: $out"
  py "$(glab api --paginate projects/:id/issues/1/notes)" "len(d)==2" || fail "glab api notes list"
  glab api -X PUT projects/:id/issues/1/notes/2 -f body=changed >/dev/null
  py "$(glab api projects/fake%2Frepo/issues/1/notes)" "d[1]['body']=='changed'" || fail "glab note PUT"
  glab api -X PUT projects/:id/milestones/1001 -f state_event=close >/dev/null
  py "$(glab api projects/:id/milestones)" "d[0]['state']=='closed'" || fail "glab milestone close"
  pass "glab: update, notes, api PUT"

  glab issue close 1
  py "$(glab issue list -O json)" "[x['iid'] for x in d]==[2]" || fail "glab default list is open only"
  out="$(glab issue list --closed -O json)"
  py "$out" "len(d)==1 and d[0]['state']=='closed' and d[0]['closed_at']" || fail "glab --closed: $out"
  py "$(glab issue list --all -O json)" "[x['iid'] for x in d]==[2,1]" || fail "glab --all"
  glab issue reopen 1
  py "$(glab issue view 1 -O json)" "d['state']=='opened' and d['closed_at'] is None" || fail "glab reopen"
  rc=0; glab mr approve 1 2>"$TMP_FT/err" || rc=$?
  [ "$rc" = 2 ] && grep -q "fake glab: unsupported: mr approve" "$TMP_FT/err" || fail "glab unsupported should exit 2"
  rc=0; FAKE_TRACKER_FAIL="issue close" glab issue close 2 2>/dev/null || rc=$?
  [ "$rc" = 1 ] || fail "glab FAKE_TRACKER_FAIL"
  pass "glab: close/reopen, failure injection, unsupported"
)

# pull / merge requests
(
  export PATH="$FAKES:$PATH"
  export FAKE_TRACKER_DB="$TMP_FT/pr.json"
  gh label create t >/dev/null
  gh issue create --title A --body b --label t >/dev/null
  [ "$(gh pr create --title "PR one" --body-file - --base main --head feat/x <<<"body text")" = "https://github.com/fake/repo/pull/2" ] \
    || fail "gh pr create should print the URL and share the issue counter"
  py "$(gh pr list --json number,title,body,headRefName,url,state)" \
    "d==[{'number':2,'title':'PR one','body':'body text\n','headRefName':'feat/x','url':'https://github.com/fake/repo/pull/2','state':'OPEN'}]" \
    || fail "gh pr list shape"
  py "$(gh pr view feat/x --json number,headRefName)" "d=={'number':2,'headRefName':'feat/x'}" || fail "gh pr view by branch"
  [ "$(glab mr create --title "MR one" --description D --source-branch feat/y --target-branch main --yes)" = "https://gitlab.com/fake/repo/-/merge_requests/1" ] \
    || fail "glab mr create should print the URL with its own counter"
  py "$(glab mr list --output json)" \
    "d==[{'iid':1,'title':'MR one','description':'D','source_branch':'feat/y','target_branch':'main','sha':'58821b0ea283839e92c7ca4034e6e41f9ae1e942','state':'opened','web_url':'https://gitlab.com/fake/repo/-/merge_requests/1'}]" \
    || fail "glab mr list shape"
  py "$(glab mr view 1 --output json)" "d['source_branch']=='feat/y'" || fail "glab mr view"
  pass "fake gh pr / glab mr: create, list, view"

  # checkout / merge / comment / note: merge records a sha and closes linked issues only on base main
  gh pr create --title "PR two" --body "Closes #1" --base dev --head feat/dev >/dev/null
  gh pr merge 3 --merge --delete-branch
  py "$(gh pr view 3 --json state,mergeCommit)" "d['state']=='MERGED' and len(d['mergeCommit']['oid'])==40" || fail "gh pr merge state/sha"
  py "$(gh issue view 1 --json state)" "d['state']=='OPEN'" || fail "merge into a non-default base must not auto-close"
  gh pr create --title "PR three" --body "Fixes: #1" --head feat/z >/dev/null
  gh pr merge 4 --merge
  py "$(gh issue view 1 --json state)" "d['state']=='CLOSED'" || fail "merge into main should auto-close"
  gh pr comment 2 --body-file - <<<"note" && py "$(cat "$FAKE_TRACKER_DB")" "d['prs'][0]['comments']==['note\n']" || fail "gh pr comment"
  glab mr note 1 --message "n1" && glab mr merge 1 --yes --remove-source-branch
  py "$(glab mr view 1 --output json)" "d['state']=='merged' and len(d['merge_commit_sha'])==40" || fail "glab mr merge"
  pass "fake gh pr / glab mr: merge, comment, auto-close on main"
)

# tracker call drives the fakes
(
  cd "$TMP_FT/proj"
  export PATH="$FAKES:$PATH" FAKE_TRACKER_DB="$TMP_FT/tc.json" FAKE_TRACKER_LOG="$TMP_FT/tc.log"
  gh label create t >/dev/null
  gh issue create --title A --body b --label t >/dev/null
  : > "$TMP_FT/tc.log"
  out="$(hvj tracker call --provider github -- issue list --json number </dev/null)" || vfail
  py "$(echo "$out" | jget data.stdout)" "d==[{'number':1}]" || fail "tracker call over fake gh: $out"
  [ "$(cat "$TMP_FT/tc.log")" = "issue list --json number --limit 1000" ] || fail "tracker-call should inject --limit 1000 (log: $(cat "$TMP_FT/tc.log"))"
  pass "tracker call works against the fakes"
)

trap 'rm -rf "$TMP"' EXIT
