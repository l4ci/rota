echo "git guard feature-branch"

GUARD_TMP="$(mktemp -d)"
trap 'rm -rf "$GUARD_TMP"' EXIT
(
  cd "$GUARD_TMP"
  git init -q
  git config user.email t@t && git config user.name t
  git checkout -q -b main 2>/dev/null || git branch -m main
  mkdir -p .rota
  echo seed > seed.txt && git add seed.txt && git commit -q -m "seed"

  # guard <expected exit> <expected feature> [args]: runs the verb, checks exit and data.feature
  guard() {
    local want_rc="$1" want_feature="$2" rc=0 out; shift 2
    out=$(hvj git guard feature-branch "$@" 2>/dev/null) || rc=$?
    [ "$rc" = "$want_rc" ] || fail "guard feature-branch $*: want exit $want_rc, got $rc: $out"
    [ "$(echo "$out" | jget data.feature)" = "$want_feature" ] || fail "guard feature-branch $*: want feature=$want_feature: $out"
    GUARD_OUT="$out"
  }

  # --- on main → exit 1, reason base ---
  guard 1 false
  [ "$(echo "$GUARD_OUT" | jget data.reason)" = "base" ] || fail "main should refuse with reason base: $GUARD_OUT"
  [ "$(echo "$GUARD_OUT" | jget data.branch)" = "main" ] && [ "$(echo "$GUARD_OUT" | jget data.base)" = "main" ] \
    || fail "refusal should name branch and base: $GUARD_OUT"

  # --- on a feature branch → exit 0 ---
  git checkout -q -b rota/feature-x
  guard 0 true
  [ "$(echo "$GUARD_OUT" | jget data.branch)" = "rota/feature-x" ] && [ "$(echo "$GUARD_OUT" | jget data.base)" = "main" ] \
    || fail "feature branch data should carry branch and base: $GUARD_OUT"

  # --- explicit branch arg: passes feature, refuses main ---
  guard 0 true rota/feature-x
  guard 1 false main

  # --- detached HEAD → exit 1, reason detached, no branch ---
  git checkout -q --detach
  guard 1 false
  [ "$(echo "$GUARD_OUT" | jget data.reason)" = "detached" ] || fail "detached HEAD should refuse with reason detached: $GUARD_OUT"
  echo "$GUARD_OUT" | jget data.branch >/dev/null && fail "detached HEAD should carry no branch: $GUARD_OUT"
  git checkout -q rota/feature-x

  # --- master is also refused when it's the resolved base ---
  # The base resolves main first, then master — so to test master as base,
  # first delete main so master is the only conventional candidate left.
  git checkout -q -b master
  git branch -q -D main
  guard 1 false
  [ "$(echo "$GUARD_OUT" | jget data.base)" = "master" ] || fail "master should resolve as the base: $GUARD_OUT"
  # Restore main for the rest of the test
  git checkout -q -b main

  # --- custom git.baseBranch via config: the configured branch is the protected one ---
  git checkout -q main
  echo '{"git":{"baseBranch":"develop"}}' > .rota/config.json
  git checkout -q -b develop
  guard 1 false
  # main is no longer the base now that develop is configured AND exists
  git checkout -q main
  guard 0 true
)
trap 'rm -rf "$TMP"' EXIT
rm -rf "$GUARD_TMP"
pass "git guard feature-branch refuses base and detached HEAD / passes feature / honors git.baseBranch"

