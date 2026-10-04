echo "ship pr: GitHub PR / GitLab MR, --items lines"

TMP_PR="$(mktemp -d)"
trap 'rm -rf "$TMP_PR"' EXIT

# PRBODY <db>: body of the newest stored PR/MR
PRBODY() { python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["prs"][-1]["body"], end="")' "$1"; }
PRFIELD() { python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["prs"][-1][sys.argv[2]], end="")' "$1" "$2"; }

for prov in github gitlab; do
  for mode in file issues; do
    P="$TMP_PR/$prov-$mode"; mkdir -p "$P"
    git init -q --bare "$P/origin.git"
    git clone -q "$P/origin.git" "$P/work" 2>/dev/null; mkdir -p "$P/work/.rota"
    printf '{"backlog":{"backend":"%s"},"issues":{"provider":"%s","retryWaitSeconds":0}}\n' \
      "$([ "$mode" = issues ] && echo issues || echo file)" "$prov" > "$P/work/.rota/config.json"
    (
      cd "$P/work"
      git config user.email t@t; git config user.name t
      git checkout -q -b main && git commit -q --allow-empty -m seed && git push -q origin main
      git checkout -q -b feat/x && git commit -q --allow-empty -m work
      export PATH="$TESTDIR/fakes:$PATH" FAKE_TRACKER_DB="$P/db.json" FAKE_TRACKER_LOG="$P/log"
      DB="$P/db.json"
      if [ "$mode" = issues ]; then
        "$ROTA_BIN" item create --kind features --title "One" >/dev/null   # F1
        "$ROTA_BIN" item create --kind bugs --title "Two" --tag P1 >/dev/null  # B2
      fi
      env="$(printf 'Summary line' | hvj ship pr feat/x --title "My title" --body-file - --items F1,2 2>/dev/null)" || fail "$prov/$mode: ship pr failed"
      url="$(jget data.url <<<"$env")"
      [ "$(jget data.provider <<<"$env")" = "$prov" ] || fail "$prov/$mode: provider [$env]"
      [ "$(jget data.branch <<<"$env")" = feat/x ] && [ "$(jget data.items <<<"$env")" = '["F1","2"]' ] || fail "$prov/$mode: branch/items echo [$env]"
      [ "$(jget data.number <<<"$env")" = "${url##*/}" ] || fail "$prov/$mode: number [$env]"
      [ "$(jget data.changed <<<"$env")" = true ] || fail "$prov/$mode: changed [$env]"
      if [ "$prov" = github ]; then
        want_n=$([ "$mode" = issues ] && echo 3 || echo 1)
        [ "$url" = "https://github.com/fake/repo/pull/$want_n" ] || fail "$prov/$mode: url [$url]"
      else
        [ "$url" = "https://gitlab.com/fake/repo/-/merge_requests/1" ] || fail "$prov/$mode: url [$url]"
        [ "$(PRFIELD "$DB" head)" = feat/x ] && [ "$(PRFIELD "$DB" base)" = main ] || fail "$prov/$mode: MR branches"
        grep -q -- '--source-branch feat/x --target-branch main --yes' "$P/log" || fail "$prov/$mode: glab argv"
      fi
      [ "$(PRFIELD "$DB" title)" = "My title" ] || fail "$prov/$mode: title"
      git -C "$P/origin.git" rev-parse --verify -q refs/heads/feat/x >/dev/null || fail "$prov/$mode: branch not pushed"
      if [ "$mode" = issues ]; then
        want="$(printf 'Summary line\n\nCloses #1\nCloses #2')"
      else
        want="Summary line"
      fi
      [ "$(PRBODY "$DB")" = "$want" ] || fail "$prov/$mode: body [$(PRBODY "$DB")]"
    )
    pass "ship pr $prov / $mode mode"
  done
done

# an --items item that does not exist fails before anything is pushed
P="$TMP_PR/github-issues"
(
  cd "$P/work"
  git checkout -q -b feat/y && git commit -q --allow-empty -m more
  export PATH="$TESTDIR/fakes:$PATH" FAKE_TRACKER_DB="$P/db.json"
  rc=0; err="$(printf b | "$ROTA_BIN" ship pr feat/y --title T --body-file - --items F99 2>&1)" || rc=$?
  [ "$rc" = 3 ] || fail "unknown --items should exit 3 (got $rc)"
  case "$err" in *"F99"*) ;; *) fail "error should name F99: $err" ;; esac
  git -C "$P/origin.git" rev-parse --verify -q refs/heads/feat/y >/dev/null && fail "nothing should be pushed on a bad --items" || true
  # usage errors: no title, empty body, unknown branch
  rc=0; printf b | "$ROTA_BIN" ship pr feat/y --body-file - >/dev/null 2>&1 || rc=$?
  [ "$rc" = 2 ] || fail "missing --title should exit 2 (got $rc)"
  rc=0; printf '' | "$ROTA_BIN" ship pr feat/y --title T --body-file - >/dev/null 2>&1 || rc=$?
  [ "$rc" = 2 ] || fail "empty body should exit 2 (got $rc)"
  rc=0; printf b | "$ROTA_BIN" ship pr no/such --title T --body-file - >/dev/null 2>&1 || rc=$?
  [ "$rc" = 3 ] || fail "unknown branch should exit 3 (got $rc)"
)
pass "ship pr rejects an unknown --items item before pushing, and bad usage"

trap 'rm -rf "$TMP"' EXIT
