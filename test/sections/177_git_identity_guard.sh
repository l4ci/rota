echo "git identity guard: a bare git config user.* never lands in the real repo (#3133)"
# The runner snapshots the real repo's local config and fails the run if it
# changed; the git shim in lib.sh refuses an identity write outside the run's
# scratch root, naming the line.

TMP_GI="$(mktemp -d)"
trap 'rm -rf "$TMP_GI"' EXIT
GIREAL="$TMP_GI/real"
git init -q -b main "$GIREAL"

# 1. The snapshot guard passes when nothing changed and names a change.
repo_config_snapshot "$GIREAL" > "$TMP_GI/before"
RC=0; repo_config_guard_check "$TMP_GI/before" "$GIREAL" 2>/dev/null || RC=$?
[ "$RC" = 0 ] || fail "config guard should pass on an unchanged repo config: rc=$RC"
command git -C "$GIREAL" config user.name leaked
RC=0; ERR=$(repo_config_guard_check "$TMP_GI/before" "$GIREAL" 2>&1) || RC=$?
[ "$RC" != 0 ] && grep -q 'user.name=leaked' <<<"$ERR" || fail "config guard should fail and name user.name=leaked: rc=$RC $ERR"
pass "repo config guard fails the run when the local config changed"

# 2. The shim refuses a bare identity write outside the scratch root.
command git -C "$GIREAL" config --unset user.name
# $TMP_GI itself sits under the run root, so narrow the root to a sibling dir.
mkdir -p "$TMP_GI/allowed"
RC=0; ERR=$( cd "$GIREAL" && RUN_TMP="$TMP_GI/allowed" git config user.name leaked 2>&1 ) || RC=$?
[ "$RC" != 0 ] || fail "bare git config user.name outside the run root should be refused"
[ -z "$(command git -C "$GIREAL" config --local user.name)" ] || fail "the refused write must not land"
pass "bare git config user.* is refused outside the run's scratch root"

# 3. Scratch roots, -C and reads still work.
GISCR="$TMP/gi-scratch"; mkdir -p "$GISCR"
( cd "$GISCR" && git init -q && git config user.name ok && [ "$(git config user.name)" = ok ] ) || fail "identity write inside the scratch root should work"
git -C "$GISCR" config user.email ok@ok || fail "git -C should pass through"
pass "identity writes in scratch repos and git -C still work"

trap 'rm -rf "$TMP"' EXIT
