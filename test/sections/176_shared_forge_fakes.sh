echo "shared fakes: gh rejects an unknown call, forge_state seeds the gate's PR, tmux kill_fails (#2826)"
# The gate sections share test/fakes/gh and test/fakes/tmux instead of heredocing stubs. A
# stub that answers every call hides a wrong one; the shared fake must refuse what it does
# not know.
. "$TESTDIR/lib/forge_state.sh"
TMP_SF="$(mktemp -d "$TMP/sf.XXXXXX")"
sfgh() { FAKE_TRACKER_DB="$TMP_SF/db.json" bash "$TESTDIR/fakes/gh" "$@"; }

forge_state "$TMP_SF/db.json" "$(printf 'b%.0s' {1..40})" "Closes #5" '"risk:high"' 5 5
rc=0; OUT=$(sfgh repo view 2>&1) || rc=$?
[ "$rc" = "2" ] && case "$OUT" in *"unsupported: repo view"*) ;; *) false ;; esac \
  || fail "gh must reject an unknown call with exit 2 and name it: rc=$rc $OUT"
rc=0; OUT=$(sfgh pr view 99 --json headRefName 2>&1) || rc=$?
[ "$rc" != "0" ] || fail "gh pr view of a PR the store lacks must fail: $OUT"

OUT=$(sfgh pr view 7 --json headRefName,body) || fail "pr view of the seeded PR failed"
[ "$(jget headRefName <<<"$OUT")" = "ben/5-thing" ] || fail "seeded PR head: $OUT"
case "$(jget body <<<"$OUT")" in *"Closes #5"*"## Assumptions"*"## Rulings"*) ;; *) fail "seeded body lacks the closing line or decision headings: $OUT" ;; esac
OUT=$(sfgh issue view 5 --json labels) || fail "issue view of the seeded issue failed"
case "$OUT" in *'"risk:high"'*) ;; *) false ;; esac || fail "seeded issue labels: $OUT"
OUT=$(sfgh api repos/o/r/pulls/7/comments) || fail "api pulls comments failed"
[ "$OUT" = "[]" ] || fail "a PR with no inline review comments should list []: $OUT"
pass "gh fake rejects an unknown call; forge_state seeds the PR, its issue and the pulls routes"

FK="$TMP_SF/tmux"; mkdir -p "$FK"; echo "w1 4194001" > "$FK/windows"
tm() { FAKE_TMUX="$FK" bash "$TESTDIR/fakes/tmux" "$@"; }
touch "$FK/kill_fails"; tm kill-window -t rota:w1
[ -s "$FK/windows" ] || fail "kill_fails must leave the window in place"
rm -f "$FK/kill_fails"; tm kill-window -t rota:w1
[ ! -s "$FK/windows" ] || fail "without kill_fails the window must go: $(cat "$FK/windows")"
pass "tmux fake keeps a window alive under kill_fails and kills it otherwise"
rm -rf "${TMP_SF:?}"
