echo "Issue mode: review queue, ship pr-merge, adapter PR ops"

TMP_RQ="$(mktemp -d)"
trap 'rm -rf "$TMP_RQ"' EXIT

for prov in github gitlab; do
  P="$TMP_RQ/$prov"; mkdir -p "$P"
  git init -q --bare "$P/origin.git"
  git clone -q "$P/origin.git" "$P/work" 2>/dev/null; mkdir -p "$P/work/.rota"
  printf '{"backlog":{"backend":"issues"},"issues":{"provider":"%s","retryWaitSeconds":0}}\n' "$prov" > "$P/work/.rota/config.json"
  (
    cd "$P/work"
    git config user.email t@t; git config user.name t
    git checkout -q -b main && git commit -q --allow-empty -m seed && git push -q origin main
    export PATH="$TESTDIR/fakes:$PATH" FAKE_TRACKER_DB="$P/db.json" FAKE_TRACKER_LOG="$P/log"
    DB="$P/db.json"
    eq() { [ "$2" = "$3" ] || fail "$prov $1: expected [$2] got [$3]"; }
    # PY <expr over d (the db)>: evaluate against the fake store
    DBQ() { python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); print(eval(sys.argv[2]))' "$DB" "$1"; }
    QUEUE() { hvj review queue | python3 -c 'import json,sys; d=json.load(sys.stdin)["data"]["items"]; print(eval(sys.argv[1]))' "$1"; }
    prnum() { sed 's:.*/::' <<<"$1"; }
    pr_open() { # <branch> <body> [target-base]: commit on a branch, push it, open a PR/MR by hand
      git checkout -q -b "$1" main; git commit -q --allow-empty -m "work $1"; git push -q origin "$1"
      if [ "$prov" = github ]; then gh pr create --title "PR $1" --body "$2" --base "${3:-main}" --head "$1"
      else glab mr create --title "PR $1" --description "$2" --source-branch "$1" --target-branch "${3:-main}" --yes; fi
    }

    for t in One Two Three Four Five; do "$ROTA_BIN" item create --kind features --title "$t" >/dev/null; done  # F1..F5
    for id in F1 F2 F3 F4; do "$ROTA_BIN" item state $id --to needs-review >/dev/null; done
    for id in F1 F2; do "$ROTA_BIN" proof add $id --check unit --result PASS --evidence ok --sha abc1234 >/dev/null; done

    # PR A via ship pr --items (base main); B and C base dev (host does not auto-close); D "Closes #40" must not link F4
    git checkout -q -b feat/a; git commit -q --allow-empty -m a
    A="$(printf 'Summary' | hvj ship pr feat/a --title "PR a" --body-file - --items F1 2>/dev/null | jget data.number)"
    git checkout -q main
    B="$(prnum "$(pr_open feat/b 'Resolves: #2' dev)")"
    git checkout -q main
    C="$(prnum "$(pr_open feat/c 'Fixes #3' dev)")"
    git checkout -q main
    D="$(prnum "$(pr_open feat/d 'Closes #40')")"
    git checkout -q main

    # --- queue listing
    eq "queue ids" "['1', '2', '3', '4']" "$(QUEUE '[x["id"] for x in d]')"
    eq "queue types" "['F', 'F', 'F', 'F']" "$(QUEUE '[x["type"] for x in d]')"
    eq "queue prs" "[[$A], [$B], [$C], []]" "$(QUEUE '[[p["number"] for p in x["prs"]] for x in d]')"
    eq "queue fields" "True" "$(QUEUE 'd[0]["title"]=="One" and d[0]["number"]==1 and d[0]["prs"][0]["branch"]=="feat/a" and d[0]["prs"][0]["url"].endswith("/'$A'") and "Closes #1" in d[0]["prs"][0]["body"]')"
    pass "$prov: review queue lists needs-review items with closing-keyword PRs"

    git checkout -q main


    # --- merge A: host auto-closes (base main)
    out="$(hvj ship pr-merge "$A")" || fail "$prov: pr-merge $A failed [$out]"
    eq "A pr" "$A" "$(jget data.pr <<<"$out")"
    [ -n "$(jget data.sha <<<"$out")" ] || fail "$prov: merge sha [$out]"
    eq "A closed" '["1"]' "$(jget data.closed <<<"$out")"
    eq "A state" "closed" "$(DBQ '[i for i in d["issues"] if i["number"]==1][0]["state"]')"
    eq "A labels cleared" "[]" "$(DBQ '[l for l in [i for i in d["issues"] if i["number"]==1][0]["labels"] if "review" in l or "progress" in l]')"
    eq "A pr state" "merged" "$(DBQ '[p for p in d["prs"] if p["number"]==int("'$A'")][0]["state"]')"
    pass "$prov: merge into main closes the issue"

    # --- merge B: base is dev, host does not close, fallback closes via complete
    out="$(hvj ship pr-merge "$B")" || fail "$prov: pr-merge $B failed [$out]"
    eq "B closed" '["2"]' "$(jget data.closed <<<"$out")"
    eq "B state" "closed" "$(DBQ '[i for i in d["issues"] if i["number"]==2][0]["state"]')"
    eq "B done comment" "True" "$(DBQ 'any(c["body"].startswith("Done in `") for c in [i for i in d["issues"] if i["number"]==2][0]["comments"])')"
    eq "B labels cleared" "[]" "$(DBQ '[l for l in [i for i in d["issues"] if i["number"]==2][0]["labels"] if "review" in l]')"
    pass "$prov: merge into a non-default base falls back to complete"

    # --- merge C: unproven → not merged (checked before merging, so a default-branch
    # merge can't let the host close it), exit 4 with the unproven item in the failure data
    rc=0; out="$(hvj ship pr-merge "$C" 2>/dev/null)" || rc=$?
    eq "C exit" 4 "$rc"
    eq "C merged" false "$(jget data.merged <<<"$out")"
    eq "C unproven" '["3"]' "$(jget data.unproven <<<"$out")"
    eq "C changesRequested" '["3"]' "$(jget data.changesRequested <<<"$out")"
    eq "C changed" true "$(jget data.changed <<<"$out")"
    if grep -q "pr merge $C\|mr merge $C" "$FAKE_TRACKER_LOG" 2>/dev/null; then fail "$prov: unproven PR $C was merged"; fi
    eq "C open" "open" "$(DBQ '[i for i in d["issues"] if i["number"]==3][0]["state"]')"
    eq "C labels" "['changes-requested']" "$(DBQ '[i for i in d["issues"] if i["number"]==3][0]["labels"][-1:]')"
    eq "C no needs-review" "False" "$(DBQ '"needs-review" in [i for i in d["issues"] if i["number"]==3][0]["labels"]')"
    eq "C feedback" "True" "$(DBQ 'any("rota:comment feedback" in c["body"] and "no proof recorded" in c["body"] for c in [i for i in d["issues"] if i["number"]==3][0]["comments"])')"
    pass "$prov: unproven item blocks the merge, stays open, changes-requested, exit 4"

    # --- queue after merges: only F4 (open PR D does not link it)
    eq "queue after" "['4']" "$(QUEUE '[x["id"] for x in d]')"
    eq "queue after prs" "[[]]" "$(QUEUE '[[p["number"] for p in x["prs"]] for x in d]')"

    # --- errors
    rc=0; hvj ship pr-merge 9999 2>/dev/null >&2 || rc=$?
    eq "unknown pr exit" 3 "$rc"
    rc=0; hvj ship pr-merge x 2>/dev/null >&2 || rc=$?
    eq "bad pr arg" 2 "$rc"
    rc=0; hvj ship pr-merge "$A" 2>/dev/null >&2 || rc=$?
    eq "already merged pr exit" 3 "$rc"
    # the forge refuses the merge itself (only the merge call carries this flag)
    git checkout -q main
    E="$(prnum "$(pr_open feat/e 'no linked items')")"
    git checkout -q main
    MERGE_FLAG="$([ $prov = github ] && echo delete-branch || echo remove-source-branch)"
    rc=0; out="$(FAKE_TRACKER_FAIL="$MERGE_FLAG" hvj ship pr-merge "$E" 2>/dev/null)" || rc=$?
    eq "failed merge exit" 4 "$rc"
    eq "failed merge data" "false false" "$(jget data.merged <<<"$out") $(jget data.changed <<<"$out")"
    eq "failed merge pr open" "open" "$(DBQ '[p for p in d["prs"] if p["number"]==int("'$E'")][0]["state"]')"
    rc=0; FAKE_TRACKER_FAIL="list" hvj review queue 2>/dev/null >&2 || rc=$?
    eq "queue tracker failure" 5 "$rc"
    pass "$prov: error exits"
  )
done

# --- file mode
F="$TMP_RQ/file"; mkdir -p "$F/.rota"
echo '{"backlog":{"backend":"file"}}' > "$F/.rota/config.json"
(
  cd "$F"; git init -q
  rc=0; out="$(hvj review queue 2>/dev/null)" || rc=$?
  [ "$rc" = 1 ] && [ "$(jget data.blockedBy <<<"$out")" = backend ] || fail "file-mode queue: rc=$rc out=[$out]"
  rc=0; out="$(hvj ship pr-merge 1 2>/dev/null)" || rc=$?
  [ "$rc" = 4 ] && [ "$(jget data.blockedBy <<<"$out")" = backend ] && [ "$(jget data.changed <<<"$out")" = false ] || fail "file-mode merge: rc=$rc out=[$out]"
)
pass "file mode: review queue and ship pr-merge refused (backend)"

trap 'rm -rf "$TMP"' EXIT
