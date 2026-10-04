echo "init umbrella (--list, register, idempotence) and version --drift"

# the scan: immediate children with a .git entry, hidden and plain dirs ignored
TMP_U="$(mktemp -d)"
trap 'rm -rf "$TMP_U"' EXIT
mkdir -p "$TMP_U/web/.git" "$TMP_U/api/.git" "$TMP_U/docs" "$TMP_U/.hidden/.git" "$TMP_U/wt"
echo "gitdir: elsewhere" > "$TMP_U/wt/.git"

OUT=$(cd "$TMP_U" && hvj init umbrella --list) || fail "init umbrella --list failed: $OUT"
[ "$(echo "$OUT" | jget data.candidates)" = '["api","web","wt"]' ] || fail "--list candidates wrong: $OUT"
[ "$(echo "$OUT" | jget data.isGitRepo)" = "false" ] || fail "--list isGitRepo should be false: $OUT"
[ ! -e "$TMP_U/.rota" ] || fail "--list must write nothing"
pass "init umbrella --list returns the git children and writes nothing"

rc=0; (cd "$TMP_U" && "$ROTA_BIN" init umbrella >/dev/null 2>&1) || rc=$?
[ "$rc" = 2 ] || fail "init umbrella with no selector should exit 2, got $rc"
rc=0; (cd "$TMP_U" && "$ROTA_BIN" init umbrella --all --list >/dev/null 2>&1) || rc=$?
[ "$rc" = 2 ] || fail "init umbrella --all --list should exit 2, got $rc"
EMPTY="$(mktemp -d)"
rc=0; (cd "$EMPTY" && "$ROTA_BIN" init umbrella --all >/dev/null 2>&1) || rc=$?
[ "$rc" = 3 ] || fail "init umbrella with no git children should exit 3, got $rc"
[ ! -e "$EMPTY/.rota" ] || fail "exit 3 must leave nothing behind"
OUT=$(cd "$EMPTY" && hvj init umbrella --list) || fail "--list on an empty dir should exit 0: $OUT"
[ "$(echo "$OUT" | jget data.candidates)" = '[]' ] || fail "empty --list wrong: $OUT"
pass "init umbrella exits 2 without exactly one selector, 3 without git children"

mkdir -p "$TMP_U/.git"
OUT=$(cd "$TMP_U" && hvj init umbrella --repos web,nope 2>/dev/null) || fail "init umbrella --repos failed: $OUT"
[ "$(echo "$OUT" | jget data.registered)" = '["web"]' ] || fail "registered wrong: $OUT"
[ "$(echo "$OUT" | jget data.umbrellaIsGitRepo)" = "true" ] || fail "umbrellaIsGitRepo wrong: $OUT"
[ "$(echo "$OUT" | jget data.changed)" = "true" ] || fail "first run should report changed: $OUT"
[ -d "$TMP_U/.rota/knowledge/web" ] || fail "knowledge dir for web missing"
RESULT=$(cat "$TMP_U/.gitignore")
case "$RESULT" in *"/web/"*".rota/"*|*".rota/"*"/web/"*) ;; *) fail ".gitignore block missing: $RESULT" ;; esac
pass "init umbrella --repos registers the named repos, ignores unknown names, writes the .gitignore block"

OUT=$(cd "$TMP_U" && hvj init umbrella --repos web) || fail "rerun failed: $OUT"
[ "$(echo "$OUT" | jget data.changed)" = "false" ] || fail "rerun should be idempotent: $OUT"
OUT=$(cd "$TMP_U" && hvj init umbrella --repos "" 2>/dev/null) || fail "--repos '' failed: $OUT"
[ "$(echo "$OUT" | jget data.registered)" = '["web"]' ] || fail "prior registration should be kept: $OUT"
pass "init umbrella is idempotent and keeps prior registrations"

# version --drift: stamped (merged config) against the running binary
TMP_V="$(mktemp -d)"
trap 'rm -rf "$TMP_U" "$EMPTY" "$TMP_V"' EXIT
mkdir -p "$TMP_V/.rota"
echo '{}' > "$TMP_V/.rota/config.json"
OUT=$(cd "$TMP_V" && hvj version --drift) || fail "version --drift failed: $OUT"
[ "$(echo "$OUT" | jget data.status)" = "unknown" ] || fail "no stamp should be unknown: $OUT"
INSTALLED="$(echo "$OUT" | jget data.installed)"
echo '{"rota": {"version": "0.0.1-stale"}}' > "$TMP_V/.rota/config.json"
OUT=$(cd "$TMP_V" && hvj version --drift) || fail "version --drift failed: $OUT"
if [ -n "$INSTALLED" ]; then
  [ "$(echo "$OUT" | jget data.status)" = "drift" ] || fail "stale stamp should drift: $OUT"
  [ "$(echo "$OUT" | jget data.drift)" = "true" ] || fail "drift flag wrong: $OUT"
fi
rc=0; (cd "$EMPTY" && "$ROTA_BIN" version --drift >/dev/null 2>&1) || rc=$?
[ "$rc" = 3 ] || fail "version --drift without .rota/ should exit 3, got $rc"
trap 'rm -rf "$TMP"' EXIT
pass "version --drift reports unknown/drift from the stamp and exits 3 without .rota/"

rm -rf "${TMP_U:?}" "${EMPTY:?}" "${TMP_V:?}"
