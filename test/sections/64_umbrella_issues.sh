echo "Umbrella issue mode: per-sub-repo trackers"

TMP_UI="$(mktemp -d)"
trap 'rm -rf "$TMP_UI"' EXIT

U="$TMP_UI/umb"; mkdir -p "$U/.rota" "$TMP_UI/db"
echo '{"backlog":{"backend":"issues"},"issues":{"retryWaitSeconds":0}}' > "$U/.rota/config.json"
echo '{"repos":[{"name":"ghrepo","path":"ghrepo"},{"name":"glrepo","path":"glrepo"}]}' > "$U/.rota/repos.json"
for pair in "ghrepo:https://github.com/o/ghrepo.git" "glrepo:https://gitlab.com/o/glrepo.git"; do
  r="${pair%%:*}"; mkdir -p "$U/$r"
  (cd "$U/$r" && git init -q && git config user.email t@t && git config user.name t \
    && git commit -q --allow-empty -m seed && git remote add origin "${pair#*:}")
done
(
  cd "$U"
  export PATH="$TESTDIR/fakes:$PATH" FAKE_TRACKER_DB_DIR="$TMP_UI/db" FAKE_TRACKER_LOG="$TMP_UI/log"
  unset FAKE_TRACKER_DB
  eq() { [ "$2" = "$3" ] || fail "umbrella issues $1: expected [$2] got [$3]"; }
  # RC <rota call>: OUT is the envelope (errors included), RCV the exit code, ERR stderr
  RC() { local rc=0; OUT="$("$@" 2>"$TMP_UI/err")" || rc=$?; RCV=$rc; ERR="$(cat "$TMP_UI/err")"; }
  has() { case "$3" in *"$2"*) ;; *) fail "umbrella issues $1: [$3] lacks [$2]";; esac; }
  ID() { echo "$OUT" | jget data.id; }
  # DB <repo> <python over d>: evaluate against the fake tracker's own store for one sub-repo
  DB() { python3 -c "import json,sys; d=json.load(open('$TMP_UI/db/$1.json')); print($2)"; }
  # NATIVE <repo>: the repo's native milestones as title:state
  NATIVE() { DB "$1" '",".join(m["title"] + ":" + m["state"] for m in d["milestones"])'; }
  FG() { hvj item field get "$1" --name "$2" | jget data.value; }

  # --- capture routing (colliding numbers: both repos start at #1)
  RC hvj item create --kind features --title "Gh feat" --repos ghrepo
  eq "create field exit" "0" "$RCV"; eq "create field id" "ghrepo:1|F" "$(ID)|$(echo "$OUT" | jget data.type)"
  RC hvj item create --kind tasks --title "Gh task" --repos ghrepo
  eq "gh T2" "ghrepo:2|T" "$(ID)|$(echo "$OUT" | jget data.type)"
  RC hvj item create --kind features --title "Gl feat" --repos glrepo
  eq "gl F1" "glrepo:1|F" "$(ID)|$(echo "$OUT" | jget data.type)"
  RC hvj item create --kind bugs --tag P1 --title "Gl bug" --repos glrepo
  eq "gl B2" "glrepo:2|B" "$(ID)|$(echo "$OUT" | jget data.type)"
  RC hvj -C "$U/glrepo" item create --kind tasks --title 'Gl cwd task'
  eq "create cwd exit" "0" "$RCV"; eq "create cwd id" "glrepo:3|T" "$(ID)|$(echo "$OUT" | jget data.type)"
  RC hvj item create --kind tasks --title "Nowhere"
  eq "create missing repo exit" "3" "$RCV"
  RC hvj item create --kind tasks --title "Both" --repos ghrepo,glrepo
  eq "create multi-repo exit" "3" "$RCV"
  RC hvj item create --kind tasks --title "Bad" --repos nope
  eq "create unknown repo exit" "3" "$RCV"
  # each item went to its own tracker
  eq "gh store" "2" "$(DB ghrepo 'len(d["issues"])')"
  eq "gl store" "3" "$(DB glrepo 'len(d["issues"])')"

  # --- merged backlog: qualified IDs (plain numbers collide across sub-repos)
  RC hvj backlog list
  eq "backlog exit" "0" "$RCV$ERR"
  ALL() { echo "$OUT" | python3 -c 'import json,sys; d=json.load(sys.stdin)["data"]; print(",".join(sorted(r["id"] + "=" + r["title"] for k in ("bugs", "features", "tasks") for r in d[k])))'; }
  eq "backlog rows" "ghrepo:1=Gh feat,ghrepo:2=Gh task,glrepo:1=Gl feat,glrepo:2=Gl bug,glrepo:3=Gl cwd task" "$(ALL)"
  eq "Repos field gh" "ghrepo" "$(FG ghrepo:F1 repos)"
  eq "Repos field gl" "glrepo" "$(FG glrepo:F1 repos)"

  # --- refs: qualified
  eq "qualified colon" "Gl feat" "$(FG glrepo:F1 title)"
  eq "qualified colon, number" "Gl feat" "$(FG glrepo:1 title)"
  eq "qualified hash" "Gh feat" "$(FG ghrepo#1 title)"
  eq "qualified id" "glrepo:1|F" "$(hvj item field get glrepo:F1 --name title | jget data.id)|$(hvj item field get glrepo:F1 --name title | jget data.type)"
  # bare: unique (type letter or number exists once), ambiguous
  eq "bare unique T2" "Gh task" "$(FG T2 title)"
  eq "bare unique B2" "Gl bug" "$(FG B2 title)"
  eq "bare unique T3" "Gl cwd task" "$(FG '#3' title)"
  RC hvj item field get F1 --name title
  eq "bare ambiguous exit" "2" "$RCV"
  has "ambiguous lists ghrepo" "ghrepo:1" "$OUT"; has "ambiguous lists glrepo" "glrepo:1" "$OUT"
  RC hvj item field get '#2' --name title
  eq "bare #2 ambiguous" "2" "$RCV"
  RC hvj item field get F99 --name title
  eq "unknown exit" "3" "$RCV"
  RC hvj item field get nope:F1 --name title
  eq "unknown repo prefix" "3" "$RCV"

  # --- item claim
  RC hvj item claim F1 --as w1
  eq "claim ambiguous exit" "2" "$RCV"; has "claim ambiguous msg" "ghrepo:1" "$OUT"
  RC hvj item claim glrepo:F1 --as w1
  eq "claim qualified exit" "0" "$RCV"; eq "claim qualified out" "glrepo:1|w1|true" "$(ID)|$(echo "$OUT" | jget data.claimId)|$(echo "$OUT" | jget data.changed)"
  RC hvj item claim B2 --as w2
  eq "claim bare unique" "0|glrepo:2|w2" "$RCV|$(ID)|$(echo "$OUT" | jget data.claimId)"
  RC hvj item claim glrepo:F1 --as w3
  eq "claim lost exit" "4" "$RCV"
  has "claim only in gl store" "in-progress" "$(cat "$TMP_UI/db/glrepo.json")"
  case "$(cat "$TMP_UI/db/ghrepo.json")" in *in-progress*) fail "umbrella issues: claim leaked into ghrepo";; esac

  # --- item complete
  RC hvj item complete F1 --commit abc1234 --reason done --no-proof
  eq "complete ambiguous exit" "2" "$RCV"; has "complete ambiguous msg" "glrepo:1" "$OUT"
  RC hvj item complete ghrepo:F1 --commit abc1234 --reason done --no-proof
  eq "complete qualified exit" "0" "$RCV"
  RC hvj item complete T2 --commit abc1234 --reason done --no-proof
  eq "complete bare unique exit" "0" "$RCV"
  eq "gh F1 closed" "closed" "$(DB ghrepo '[i["state"] for i in d["issues"] if i["number"] == 1][0]')"
  eq "gl F1 still open" "open" "$(DB glrepo '[i["state"] for i in d["issues"] if i["number"] == 1][0]')"
  RC hvj backlog list
  case "$(ALL)" in *"Gh feat"*) fail "umbrella issues: closed ghrepo F1 still listed open";; esac
  has "open gl feat kept" "Gl feat" "$(ALL)"

  # --- backlog list: qualified IDs in the rows
  eq "backlog qualified glrepo" "glrepo:1,glrepo:2,glrepo:3" "$(ALL | tr ',' '\n' | sed 's/=.*//' | grep '^glrepo' | tr '\n' ',' | sed 's/,$//')"

  # --- milestones: ID minted over native milestones of ALL sub-repos, tracking issue on the home repo
  RC hvj tracker call --repo glrepo -- api -X POST projects/:id/milestones -f title="M04 — Legacy" -f description=
  eq "legacy milestone created" "0" "$RCV"
  RC hvj milestone add --title "Alpha" --summary "First"
  eq "milestone add mints over all repos" "M05" "$(ID)"
  RC hvj milestone add --title "Beta" --summary "Second" --depends M05
  eq "milestone add second" "M06" "$(ID)"
  eq "home native milestones" "M05 — Alpha:open,M06 — Beta:open" "$(NATIVE ghrepo)"
  eq "other repo untouched" "M04 — Legacy:open" "$(NATIVE glrepo)"
  RC hvj milestone list
  eq "milestone list exit" "0" "$RCV"
  eq "milestone list ids" "M05,M06" "$(echo "$OUT" | python3 -c 'import json,sys; print(",".join(m["id"] for m in json.load(sys.stdin)["data"]["milestones"]))')"
  has "milestone show" "id: M05" "$(hvj milestone show M05 | jget data.body)"

  # --- assigning an item creates the sub-repo native milestone once
  RC hvj item field set glrepo:F1 --name milestone --value M05
  eq "assign exit" "0" "$RCV"
  eq "gl milestone created" "M04 — Legacy:open,M05 — Alpha:open" "$(NATIVE glrepo)"
  RC hvj item field set glrepo:B2 --name milestone --value M05
  eq "second assign exit" "0" "$RCV"
  eq "gl milestone reused" "M04 — Legacy:open,M05 — Alpha:open" "$(NATIVE glrepo)"
  RC hvj item create --kind features --title "Gh ms feat" --repos ghrepo --milestone M05
  eq "capture with milestone" "0" "$RCV"; GHMS="$(ID)"; GHMS="${GHMS#ghrepo:}"
  eq "home milestone not duplicated" "M05 — Alpha:open,M06 — Beta:open" "$(NATIVE ghrepo)"
  RC hvj item field set glrepo:T3 --name milestone --value M99
  eq "unknown milestone exit" "3" "$RCV"

  # --- status aggregation
  LSTAT() { hvj milestone list | python3 -c 'import json,sys; print([m["status"] for m in json.load(sys.stdin)["data"]["milestones"] if m["id"] == "M05"][0])'; }
  eq "status planned" "planned" "$(LSTAT)"
  RC hvj milestone status M05 --to shipped
  eq "status shipped exit" "0" "$RCV"
  eq "shipped closes every repo" "M05 — Alpha:closed,M06 — Beta:open|M04 — Legacy:open,M05 — Alpha:closed" "$(NATIVE ghrepo)|$(NATIVE glrepo)"
  eq "list shipped" "shipped" "$(LSTAT)"
  # glab addresses a milestone by its global id, which the milestone list reports
  GLM="$(hvj tracker call --repo glrepo -- api projects/:id/milestones | jget data.stdout | python3 -c 'import json,sys; print([m["id"] for m in json.load(sys.stdin) if m["title"].startswith("M05")][0])')" || vfail
  RC hvj tracker call --repo glrepo -- api -X PUT "projects/:id/milestones/$GLM" -f state_event=activate
  eq "reopen one repo exit" "0" "$RCV"
  eq "list active while a repo is open" "active" "$(LSTAT)"
  RC hvj milestone status M05 --to planned
  eq "planned reopens all" "M05 — Alpha:open|M05 — Alpha:open" "$(NATIVE ghrepo | tr ',' '\n' | grep M05)|$(NATIVE glrepo | tr ',' '\n' | grep M05)"
  eq "list planned" "planned" "$(LSTAT)"

  # --- release per sub-repo
  RC hvj release milestone-check M05
  eq "gate without --repo exit" "2" "$RCV"
  RC hvj release notes --from issues M05
  eq "notes without --repo exit" "2" "$RCV"
  RC hvj release close-milestone M05 --release 1.0.0
  eq "close without --repo exit" "2" "$RCV"
  RC hvj release milestone-check M05 --repo glrepo
  eq "gate glrepo blocked (B2 in progress)" "1" "$RCV"; eq "gate lists B2" "false|in-progress" "$(echo "$OUT" | jget data.clear)|$(echo "$OUT" | jget 'data.blocked[0].label')"
  RC hvj release milestone-check M05 --repo nope
  eq "gate unknown repo exit" "3" "$RCV"
  RC hvj item complete glrepo:F1 --commit aaa1111 --reason done --no-proof;  eq "complete gl F1" "0" "$RCV"
  RC hvj item complete glrepo:B2 --commit bbb2222 --reason done --no-proof;  eq "complete gl B2" "0" "$RCV"
  RC hvj item complete "ghrepo:$GHMS" --commit ccc3333 --reason done --no-proof; eq "complete gh ms feat" "0" "$RCV"
  RC hvj release notes --from issues M05 --repo glrepo
  NOTES="$(echo "$OUT" | jget data.markdown)"
  has "gl notes new" "Gl feat" "$NOTES"; has "gl notes fixed" "Gl bug" "$NOTES"
  case "$NOTES" in *"Gh ms feat"*) fail "umbrella issues: ghrepo item in glrepo notes";; esac
  RC hvj release close-milestone M05 --release 1.0.0 --repo glrepo
  eq "close glrepo exit" "0" "$RCV"; eq "close glrepo data" "M05|1.0.0|v1.0.0|2|true" "$(echo "$OUT" | jget data.milestone)|$(echo "$OUT" | jget data.release)|$(echo "$OUT" | jget data.tag)|$(echo "$OUT" | jget data.issues)|$(echo "$OUT" | jget data.changed)"
  eq "gl native closed" "closed" "$(NATIVE glrepo | tr ',' '\n' | grep M05 | sed 's/.*://')"
  eq "tracking issue still open after one repo" "planned" "$(LSTAT)"
  # The Go port reports changed:false on a re-run; the shim cannot tell, so prove the no-op from the write log
  : > "$TMP_UI/log"
  RC hvj release close-milestone M05 --release 1.0.0 --repo glrepo
  eq "close idempotent" "0:0" "$RCV:$(grep -cE 'issue (edit|update|close|reopen|comment)|label (create|add)|api -X|milestone (create|edit)' "$TMP_UI/log" || true)"
  RC hvj release close-milestone M05 --release 1.0.0 --repo ghrepo
  eq "close ghrepo exit" "0" "$RCV"; eq "close ghrepo issues" "1" "$(echo "$OUT" | jget data.issues)"
  eq "list shipped after both" "shipped" "$(LSTAT)"

  # --- review queue + PRs across both repos
  RC hvj item create --kind tasks --title "Gh review" --repos ghrepo; GHQ="$(ID)"; GHQ="${GHQ#ghrepo:}"
  RC hvj item create --kind tasks --title "Gl review" --repos glrepo; GLQ="$(ID)"; GLQ="${GLQ#glrepo:}"
  hvj item state "ghrepo:$GHQ" --to needs-review >/dev/null; hvj item state "glrepo:$GLQ" --to needs-review >/dev/null
  hvj tracker call --repo ghrepo -- pr create --title "gh pr" --body "Closes #$GHQ" --base main --head feat/gh >/dev/null
  hvj tracker call --repo glrepo -- mr create --title "gl mr" --description "Closes #$GLQ" --source-branch feat/gl --target-branch main --yes >/dev/null
  RC hvj review queue
  QUEUE="$OUT"
  eq "queue exit" "0" "$RCV"
  QIDS() { hvj review queue | python3 -c 'import json,sys; print(",".join(x["id"] for x in json.load(sys.stdin)["data"]["items"]))'; }
  eq "queue repos" "ghrepo:$GHQ,glrepo:$GLQ" "$(QIDS)"
  eq "queue repo field + prs" "ghrepo:1,glrepo:1" "$(echo "$QUEUE" | python3 -c 'import json,sys; print(",".join("%s:%d" % (x["repo"], len(x["prs"])) for x in json.load(sys.stdin)["data"]["items"]))')"
  RC hvj ship pr-merge 1
  eq "pr-merge without --repo exit" "2" "$RCV"
  hvj proof add "glrepo:$GLQ" --check unit --result PASS --evidence ok --sha abc1234 >/dev/null
  PRN="$(echo "$QUEUE" | jget 'data.items[1].prs[0].number')"
  RC hvj ship pr-merge "$PRN" --repo glrepo
  eq "pr-merge glrepo exit" "0" "$RCV"; eq "pr-merge closed qualified" "[\"glrepo:$GLQ\"]" "$(echo "$OUT" | jget data.closed)"
  eq "gh queue entry remains" "ghrepo:$GHQ" "$(QIDS)"
  # ship pr --repo --items resolves the bare ID inside that sub-repo and pushes there
  git init -q --bare "$TMP_UI/gh-origin.git"
  git -C "$U/ghrepo" config "url.$TMP_UI/gh-origin.git.pushInsteadOf" "https://github.com/o/ghrepo.git"
  git -C "$U/ghrepo" checkout -q -b feat/pr2
  RC bash -c "printf 'Body' | '$ROTA_BIN' --json ship pr feat/pr2 --repo ghrepo --title 'Via ship pr' --body-file - --items $GHQ"
  eq "ship pr --repo exit" "0" "$RCV"
  has "ship pr --repo body" "Closes #$GHQ" "$(DB ghrepo 'd["prs"][-1]["body"]')"
  git -C "$U/ghrepo" checkout -q master 2>/dev/null || git -C "$U/ghrepo" checkout -q main 2>/dev/null || true

  # --- review/ship checkout verbs under --repo: run in the named sub-repo, not the umbrella root
  mkbranch() { # <repo> <branch> <file> <subject>
    local base; base="$(git -C "$U/$1" branch --show-current)"
    git -C "$U/$1" checkout -q -b "$2" && echo "$3" > "$U/$1/$3" \
      && git -C "$U/$1" add "$3" && git -C "$U/$1" commit -q -m "$4" && git -C "$U/$1" checkout -q "$base"
  }
  mkbranch glrepo feat/rv gl-only.txt "gl only change"
  mkbranch ghrepo feat/rv gh-only.txt "gh only change"
  git -C "$U/glrepo" checkout -q -b feat/pr3 && echo x > "$U/glrepo/gl3.txt" && git -C "$U/glrepo" add gl3.txt \
    && git -C "$U/glrepo" commit -q -m "gl pr3 change" && git -C "$U/glrepo" checkout -q -
  for v in "review scope" "review brief" "review scaffolding" "ship body"; do
    RC hvj $v feat/rv
    eq "$v at umbrella root exit" "2" "$RCV"
    RC hvj $v feat/rv --repo nope
    eq "$v unregistered repo exit" "3" "$RCV"
  done
  RC hvj review scope feat/rv --repo glrepo
  eq "scope glrepo exit" "0" "$RCV"
  eq "scope glrepo reads sub-repo commits" "1|gl only change|gl-only.txt" "$(echo "$OUT" | jget data.commitCount)|$(echo "$OUT" | jget 'data.commits[0].subject')|$(echo "$OUT" | jget 'data.touchedFiles[0]')"
  RC hvj review scope feat/rv --repo ghrepo
  eq "scope ghrepo touched file" "gh-only.txt" "$(echo "$OUT" | jget 'data.touchedFiles[0]')"
  RC hvj review scope feat/gone --repo glrepo
  eq "scope unknown branch exit" "3" "$RCV"
  RC hvj review brief feat/rv --repo glrepo
  eq "brief glrepo exit" "0" "$RCV"
  has "brief names sub-repo commit" "gl only change" "$(echo "$OUT" | jget data.brief)"
  case "$(echo "$OUT" | jget data.brief)" in *"gh only change"*) fail "umbrella issues: glrepo brief leaked ghrepo commit";; esac
  git -C "$U/glrepo" checkout -q feat/rv && printf '// Task 7 placeholder\n' >> "$U/glrepo/gl-only.txt" \
    && git -C "$U/glrepo" commit -q -am "gl scaffolding" && git -C "$U/glrepo" checkout -q -
  RC hvj review scaffolding feat/rv --repo glrepo
  eq "scaffolding glrepo finding" "0|gl-only.txt" "$RCV|$(echo "$OUT" | jget 'data.findings[0].file')"
  RC hvj review scaffolding feat/rv --repo ghrepo
  eq "scaffolding ghrepo clean" "0|0" "$RCV|$(echo "$OUT" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)["data"]["findings"]))')"
  RC hvj ship body feat/rv --repo glrepo
  eq "ship body glrepo exit" "0" "$RCV"
  has "body from sub-repo commits" "gl only change" "$(echo "$OUT" | jget data.body)"
  case "$(echo "$OUT" | jget data.body)" in *"gh only change"*) fail "umbrella issues: glrepo body leaked ghrepo commit";; esac
  RC hvj ship body master --repo glrepo
  eq "ship body base branch exit" "1" "$RCV"

  # review queue --repo narrows to one sub-repo
  RC hvj review queue --repo ghrepo
  eq "queue --repo ghrepo" "0|ghrepo" "$RCV|$(echo "$OUT" | python3 -c 'import json,sys; print(",".join(sorted({x["repo"] for x in json.load(sys.stdin)["data"]["items"]})))')"
  RC hvj review queue --repo glrepo
  eq "queue --repo glrepo excludes ghrepo" "0|0" "$RCV|$(echo "$OUT" | python3 -c 'import json,sys; print(sum(x["repo"] != "glrepo" for x in json.load(sys.stdin)["data"]["items"]))')"
  RC hvj review queue --repo nope
  eq "queue unregistered repo exit" "3" "$RCV"

  # ship pr/merge/pr-merge: exit 2 at root, exit 3 unregistered, effect lands in the named sub-repo
  RC bash -c "printf 'B' | '$ROTA_BIN' --json ship pr feat/rv --title T --body-file -"
  eq "ship pr at umbrella root exit" "2" "$RCV"
  RC bash -c "printf 'B' | '$ROTA_BIN' --json ship pr feat/rv --title T --body-file - --repo nope"
  eq "ship pr unregistered repo exit" "3" "$RCV"
  RC bash -c "printf 'merge: x\n' | '$ROTA_BIN' --json ship merge feat/rv --body-file -"
  eq "ship merge at umbrella root exit" "2" "$RCV"
  RC bash -c "printf 'merge: x\n' | '$ROTA_BIN' --json ship merge feat/rv --body-file - --repo nope"
  eq "ship merge unregistered repo exit" "3" "$RCV"
  RC hvj ship pr-merge 1 --repo nope
  eq "pr-merge unregistered repo exit" "3" "$RCV"
  GL_PRS="$(DB glrepo 'len(d["prs"])')"; GH_PRS="$(DB ghrepo 'len(d["prs"])')"
  git -C "$U/glrepo" config "url.$TMP_UI/gl-origin.git.pushInsteadOf" "https://gitlab.com/o/glrepo.git"
  git init -q --bare "$TMP_UI/gl-origin.git"
  RC bash -c "printf 'Body' | '$ROTA_BIN' --json ship pr feat/pr3 --repo glrepo --title 'Gl via ship pr' --body-file -"
  eq "ship pr glrepo exit" "0" "$RCV"
  eq "ship pr glrepo provider" "gitlab" "$(echo "$OUT" | jget data.provider)"
  eq "ship pr opened on glrepo forge only" "$((GL_PRS + 1))|$GH_PRS" "$(DB glrepo 'len(d["prs"])')|$(DB ghrepo 'len(d["prs"])')"
  eq "ship pr pushed to glrepo origin" "feat/pr3" "$(git -C "$TMP_UI/gl-origin.git" branch --format='%(refname:short)' | grep feat/pr3)"
  RC bash -c "printf 'merge: rv\n\n- gl\n' | '$ROTA_BIN' --json ship merge feat/rv --repo glrepo --body-file -"
  eq "ship merge glrepo exit" "0" "$RCV"
  eq "ship merge landed in glrepo only" "1|0" "$(git -C "$U/glrepo" log --oneline --grep='^merge: rv' | wc -l | tr -d ' ')|$(git -C "$U/ghrepo" log --oneline --grep='^merge: rv' | wc -l | tr -d ' ')"
  [ -f "$U/glrepo/gl-only.txt" ] || fail "umbrella issues: ship merge --repo glrepo did not land the file in glrepo"
) 2>"$TMP_UI/subshell.err" || { cat "$TMP_UI/subshell.err" >&2; fail "umbrella issue mode section failed"; }
rm -rf "$TMP_UI"
trap 'rm -rf "$TMP"' EXIT
pass "umbrella issue mode: per-repo stores, merged backlog, refs, claim, complete, capture routing, milestones, release, review queue"
