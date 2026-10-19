echo "debug session: put, show, list and rm keep /rota-debug notes in a gitignored .rota/debug/ (#925)"

DS="$(mktemp -d "$TMP/debug-session.XXXXXX")"
(
  cd "$DS" && git init -q -b main . && git config user.email a@b && git config user.name n \
    && "$ROTA_BIN" init >/dev/null 2>&1 \
    && git add -A . && git commit -q -m init
) || fail "debug session fixture setup failed"
ds() { ( cd "$DS" && "$ROTA_BIN" --json "$@" 2>/dev/null ); }
dsrc() { ( cd "$DS" && "$ROTA_BIN" "$@" >/dev/null 2>&1 ); echo $?; }

# (a) put replaces, show reads it back, list names it
ds debug session put fix-7 --body-file - <<<$'## Symptom\nfirst\n' >/dev/null || vfail
ds debug session put fix-7 --body-file - <<<$'## Symptom\nsecond\n' >/dev/null || vfail
OUT=$(ds debug session show fix-7) || vfail
grep -q second <<<"$OUT" && ! grep -q first <<<"$OUT" || fail "put should replace the file: $OUT"
OUT=$(ds debug session list) || vfail
[ "$(echo "$OUT" | jget data.sessions[0])" = "fix-7" ] || fail "list should name fix-7: $OUT"
pass "put replaces and show/list read it back"

# (b) the session is invisible to git
[ -z "$(cd "$DS" && git status --porcelain)" ] || fail "a session file dirtied the tree: $(cd "$DS" && git status --porcelain)"
pass "git status stays clean after a session is written"

# (c) unknown slug exits 3, bad slug exits 2
[ "$(dsrc debug session show nope)" = 3 ] || fail "show of an unknown slug should exit 3"
[ "$(dsrc debug session rm nope)" = 3 ] || fail "rm of an unknown slug should exit 3"
[ "$(dsrc debug session put ../x --body-file "$DS/.rota/debug/fix-7.md")" = 2 ] || fail "'..' slug should exit 2"
[ "$(dsrc debug session put a/b --body-file "$DS/.rota/debug/fix-7.md")" = 2 ] || fail "'/' slug should exit 2"
pass "unknown slug exits 3, unsafe slug exits 2"

# (d) rm removes it
ds debug session rm fix-7 >/dev/null || vfail
[ "$(dsrc debug session show fix-7)" = 3 ] || fail "rm should delete the session"
pass "rm deletes the session"

# (e) a second rota init restores a missing .rota/debug/ gitignore line
grep -v '^\.rota/debug/$' "$DS/.gitignore" > "$DS/.gitignore.new" && mv "$DS/.gitignore.new" "$DS/.gitignore"
( cd "$DS" && "$ROTA_BIN" init >/dev/null 2>&1 )
grep -qx '\.rota/debug/' "$DS/.gitignore" || fail "rota init should re-add .rota/debug/ to the gitignore block"
pass "rota init keeps .rota/debug/ ignored"
