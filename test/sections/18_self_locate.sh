echo "refactor targets"
mkdir rt-test && cd rt-test
mkdir -p .rota

# 1. Single-repo (umbrella.enabled = false): emits umbrella=null, subRepos=[]
echo '{"umbrella": {"enabled": false}}' > .rota/config.json
RESULT=$(hvj refactor targets) || fail "refactor targets failed: $RESULT"
UMBRELLA=$(echo "$RESULT" | jget data.umbrella)
[ "$UMBRELLA" = "null" ] || fail "single-repo: expected umbrella=null, got '$UMBRELLA'"
SUB_COUNT=$(echo "$RESULT" | python3 -c "import json,sys; print(len(json.load(sys.stdin)['data']['subRepos']))")
[ "$SUB_COUNT" = "0" ] || fail "single-repo: expected subRepos=[], got count=$SUB_COUNT"
pass "refactor targets returns umbrella=null when umbrella.enabled is false"

# 2. Umbrella with sub-repos: emits the registered list
mkdir -p web api
echo '{"umbrella": {"enabled": true}}' > .rota/config.json
echo '{"repos": [{"name": "web", "path": "./web"}, {"name": "api", "path": "./api"}]}' > .rota/repos.json
RESULT=$(hvj refactor targets) || fail "refactor targets failed: $RESULT"
SUB_COUNT=$(echo "$RESULT" | python3 -c "import json,sys; print(len(json.load(sys.stdin)['data']['subRepos']))")
[ "$SUB_COUNT" = "2" ] || fail "umbrella: expected 2 sub-repos, got $SUB_COUNT"
NAMES=$(echo "$RESULT" | python3 -c "import json,sys; print(','.join(r['name'] for r in json.load(sys.stdin)['data']['subRepos']))")
[ "$NAMES" = "api,web" ] || fail "umbrella: expected names api,web, got '$NAMES'"
pass "refactor targets lists registered sub-repos in umbrella mode"

# 3. Umbrella with no own code (only .rota/, registered sub-repos)
HAS_CODE=$(echo "$RESULT" | jget data.umbrella.hasCode)
[ "$HAS_CODE" = "false" ] || fail "no-code umbrella: expected hasCode=false, got '$HAS_CODE'"
pass "refactor targets reports hasCode=false when umbrella has only scaffolding + sub-repos"

# 4. Add umbrella-level code → hasCode flips
echo "x" > umbrella-thing.py
RESULT=$(hvj refactor targets) || fail "refactor targets failed: $RESULT"
HAS_CODE=$(echo "$RESULT" | jget data.umbrella.hasCode)
[ "$HAS_CODE" = "true" ] || fail "umbrella with code: expected hasCode=true, got '$HAS_CODE'"
pass "refactor targets reports hasCode=true when umbrella has its own code file"

# 5. Standard scaffolding (.gitignore) doesn't trigger hasCode by itself
rm umbrella-thing.py
echo "ignored-stuff" > .gitignore
RESULT=$(hvj refactor targets) || fail "refactor targets failed: $RESULT"
HAS_CODE=$(echo "$RESULT" | jget data.umbrella.hasCode)
[ "$HAS_CODE" = "false" ] || fail "scaffolding-only umbrella: expected hasCode=false, got '$HAS_CODE'"
pass "refactor targets ignores standard scaffolding (.gitignore) when computing hasCode"

cd ..

echo "F10 self-locate: verbs work from a sub-cwd"
# A verb run from a sub-cwd that lacks .rota/ must act on the enclosing project's
# .rota/, not the dev tree's.
mkdir -p subdir
BEFORE_BUGS=$(python3 -c 'import json; print(json.load(open(".rota/counters.json"))["bugs"])')
(
  cd subdir
  ID=$(hvj id next --kind bugs | jget data.id) || vfail
  [ -n "$ID" ] || { echo "FAIL: id next from subdir produced empty"; exit 1; }
  # The new ID lands in the umbrella's counters.json, not the subdir's.
  [ ! -f .rota/counters.json ] || { echo "FAIL: id next created subdir/.rota/"; exit 1; }
)
[ -f .rota/counters.json ] || fail "self-locate: umbrella counters.json missing"
AFTER_BUGS=$(python3 -c 'import json; print(json.load(open(".rota/counters.json"))["bugs"])')
[ "$AFTER_BUGS" -gt "$BEFORE_BUGS" ] || fail "self-locate: umbrella counters.json bugs did not increment ($BEFORE_BUGS -> $AFTER_BUGS)"
pass "id next self-locates from sub-cwd"

(
  cd subdir
  hvj summary >/dev/null
) || fail "summary failed from sub-cwd"
pass "summary self-locates from sub-cwd"

(
  cd subdir
  hvj backlog list >/dev/null
) || fail "backlog list failed from sub-cwd"
pass "backlog list self-locates from sub-cwd"

rm -rf subdir

echo "B02 umbrella-cwd guards"
UMB_TMP="$(mktemp -d)"
(
  cd "$UMB_TMP"
  mkdir -p .rota
  echo '{"umbrella":{"enabled":true},"git":{"baseBranch":""}}' > .rota/config.json
  # Umbrella signal of record is repos.json with >=1 entry (B15). The path
  # need not exist on disk for this fixture — the verbs under test here
  # don't dereference it; they just check whether umbrella mode is on.
  echo '{"repos":[{"name":"web","path":"./web"}]}' > .rota/repos.json
  echo '{"bugs":0,"features":0,"tasks":0,"milestones":0}' > .rota/counters.json
  echo '{"active":[]}' > .rota/status.json

  # git base: usage error at an umbrella root without --repo (exit 2)
  rc=0; "$ROTA_BIN" git base >/dev/null 2>&1 || rc=$?
  [ "$rc" = 2 ] || { echo "FAIL: git base should exit 2 on umbrella, got $rc"; exit 1; }

  # ship merge: refuses without --repo
  rc=0; echo "msg" | "$ROTA_BIN" ship merge feat-x --body-file - >/dev/null 2>&1 || rc=$?
  [ "$rc" = 2 ] || { echo "FAIL: ship merge should exit 2 on umbrella without --repo, got $rc"; exit 1; }

  # ship pr: refuses without --repo
  rc=0; echo "body" | "$ROTA_BIN" ship pr feat-x --title "title" --body-file - >/dev/null 2>&1 || rc=$?
  [ "$rc" = 2 ] || { echo "FAIL: ship pr should exit 2 on umbrella without --repo, got $rc"; exit 1; }

  # ship body: usage error at an umbrella root
  rc=0; "$ROTA_BIN" ship body feat-x >/dev/null 2>&1 || rc=$?
  [ "$rc" = 2 ] || { echo "FAIL: ship body should exit 2 on umbrella, got $rc"; exit 1; }

  # review scope: usage error at an umbrella root
  rc=0; "$ROTA_BIN" review scope feat-x >/dev/null 2>&1 || rc=$?
  [ "$rc" = 2 ] || { echo "FAIL: review scope should exit 2 on umbrella, got $rc"; exit 1; }
)
rm -rf "$UMB_TMP"
pass "B02 umbrella-cwd guards: 5 verbs refuse cleanly at an umbrella root"

echo "F42 self-locate: cwd-anchored walk-up wins over the helper's own location"
F42_TMP="$(mktemp -d)"
trap 'rm -rf "$F42_TMP"' EXIT
mkdir -p "$F42_TMP/.rota" "$F42_TMP/repo-a"
echo '{"bugs":7,"features":0,"tasks":0,"milestones":0}' > "$F42_TMP/.rota/counters.json"
(
  cd "$F42_TMP/repo-a"
  ID=$(hvj id next --kind bugs | jget data.id) || vfail
  [ "$ID" = "B08" ] || { echo "FAIL: F42: id next from sub-cwd: expected B08 (test umbrella), got '$ID'"; exit 1; }
)
AFTER_BUGS=$(python3 -c 'import json; print(json.load(open("'"$F42_TMP/.rota/counters.json"'"))["bugs"])')
[ "$AFTER_BUGS" = "8" ] || fail "F42: test umbrella counters.json not incremented (expected 8, got $AFTER_BUGS)"
trap 'rm -rf "$TMP"' EXIT
pass "id next prefers the cwd-anchored walk-up"
