echo "rota skills get and list serve the embedded skills (#821)"
TMP_SG="$(mktemp -d)"
trap 'rm -rf "$TMP_SG"' EXIT

rc=0
OUT=$(cd "$TMP_SG" && "$ROTA_BIN" skills list 2>/dev/null) || rc=$?
[ "$rc" -eq 0 ] || fail "skills list exited $rc"
case $OUT in *rota-pause*) ;; *) fail "skills list lacks rota-pause: $OUT" ;; esac

rc=0
OUT=$(cd "$TMP_SG" && "$ROTA_BIN" skills get rota-pause 2>/dev/null) || rc=$?
[ "$rc" -eq 0 ] || fail "skills get exited $rc"
case $OUT in "---"*"name: rota-pause"*) ;; *) fail "skills get text is not the SKILL.md: ${OUT:0:120}" ;; esac

rc=0
OUT=$(cd "$TMP_SG" && "$ROTA_BIN" --json skills get rota-pause 2>/dev/null) || rc=$?
[ "$rc" -eq 0 ] || fail "skills get --json exited $rc"
case $OUT in *'"text"'*'name: rota-pause'*) ;; *) fail "skills get --json lacks text: ${OUT:0:120}" ;; esac

rc=0
OUT=$(cd "$TMP_SG" && "$ROTA_BIN" skills get rota-pause --references 2>/dev/null) || rc=$?
[ "$rc" -eq 0 ] || fail "skills get --references exited $rc"
REF=$(printf '%s\n' "$OUT" | head -1)
[ -n "$REF" ] || fail "rota-pause lists no references"

rc=0
OUT=$(cd "$TMP_SG" && "$ROTA_BIN" skills get rota-pause --reference "$REF" 2>/dev/null) || rc=$?
[ "$rc" -eq 0 ] || fail "skills get --reference $REF exited $rc"
[ -n "$OUT" ] || fail "reference $REF printed nothing"

rc=0
OUT=$(cd "$TMP_SG" && "$ROTA_BIN" skills get no-such-skill 2>&1) || rc=$?
[ "$rc" -eq 2 ] || fail "unknown skill: want exit 2, got $rc"
case $OUT in *rota-pause*) ;; *) fail "unknown skill does not list known names: $OUT" ;; esac

rm -rf "$TMP_SG"
trap 'rm -rf "$TMP"' EXIT
pass "skills list and get serve the embedded text; unknown name exits 2"
