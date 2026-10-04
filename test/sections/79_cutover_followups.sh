echo "F29: a dangling --repo / --repos exits 2 and leaves state untouched"

# Verb-level replacement for the old check that grepped nine helpers for the
# strict ${2:?usage:} extraction. What mattered: a flag with no value must error
# out, never default silently and write state for the wrong repo (status.json
# with repo:null when the caller meant a sub-repo). Section 47 covers the
# knowledge and glossary verbs; this covers the ones that write branches,
# status, spikes and PRs. Each call is otherwise complete, so the exit-2 usage
# error can only come from the dangling flag.
RF_TMP="$(mktemp -d)"
trap 'rm -rf "$RF_TMP"' EXIT
(
  cd "$RF_TMP"
  git init -q -b main . && git config user.email t@t && git config user.name t
  mkdir web
  hvj init >/dev/null 2>&1
  printf '{"repos":[{"name":"web","path":"./web"}]}\n' > .rota/repos.json
  git add -A && git commit -q -m seed
) || fail "F29: fixture setup failed"

# Everything a verb could have written: tracked and untracked files, .rota
# contents (status.json and the spike file are ignored or new) and the branches.
rf_state() { ( cd "$RF_TMP" && git status --short && git branch --list && find .rota -type f | sort | xargs sha256sum ); }
BEFORE="$(rf_state)"

for v in "ship body" "ship merge b" "ship pr b" "review scope" "spike add x" \
         "status add b --items X-1" "status rm b" "git worktree-path b"; do
  rc=0
  ( cd "$RF_TMP" && hvj $v --repo </dev/null >/dev/null 2>&1 ) || rc=$?
  [ "$rc" -eq 2 ] || fail "F29: rota $v accepted a dangling --repo (rc=$rc; expected 2)"
done

for v in "status add b --items X-1" "git branch b"; do
  rc=0
  ( cd "$RF_TMP" && hvj $v --repos </dev/null >/dev/null 2>&1 ) || rc=$?
  [ "$rc" -eq 2 ] || fail "F29: rota $v accepted a dangling --repos (rc=$rc; expected 2)"
done

[ "$(rf_state)" = "$BEFORE" ] || fail "F29: a dangling --repo / --repos changed project state"

trap 'rm -rf "$TMP"' EXIT
pass "F29: dangling --repo / --repos exits 2 on ship, review, spike, status and git verbs, state untouched"
