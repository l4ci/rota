echo "projects remove drops one entry; projects --ui is refused off a terminal (#543)"

GP="$(mktemp -d "$TMP/prm.XXXXXX")"
OLD_XDG=$XDG_CONFIG_HOME
export XDG_CONFIG_HOME="$GP/xdg"
mkdir -p "$GP/alpha" "$GP/beta"
gpv() { ( cd "$1" && shift && "$ROTA_BIN" --json "$@" 2>/dev/null ); }
gpv "$GP/alpha" init --no-blocks >/dev/null || fail "init alpha failed"
gpv "$GP/beta" init --no-blocks >/dev/null || fail "init beta failed"

OUT=$(gpv "$GP" projects remove "$GP/alpha") || fail "projects remove failed: $OUT"
[ "$(echo "$OUT" | jget data.changed)" = "true" ] || fail "remove should change: $OUT"
[ "$(echo "$OUT" | jget 'data.removed[0].name')" = "alpha" ] || fail "remove should name the entry: $OUT"
[ -d "$GP/alpha/.rota" ] || fail "remove must leave the directory alone"
OUT=$(gpv "$GP" projects) || fail "projects failed: $OUT"
[ "$(echo "$OUT" | jget 'data.projects' | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')" = "1" ] || fail "one entry should remain: $OUT"

OUT=$(gpv "$GP" projects remove "$GP/alpha") || fail "second remove should be a noop, not a failure: $OUT"
[ "$(echo "$OUT" | jget data.changed)" = "false" ] || fail "second remove should not change: $OUT"
case "$(cd "$GP" && "$ROTA_BIN" projects remove "$GP/alpha" 2>/dev/null)" in "noop: not registered: "*) ;; *) fail "text mode should say noop" ;; esac

rc=0; ( cd "$GP" && "$ROTA_BIN" projects remove >/dev/null 2>&1 ) || rc=$?
[ "$rc" = 2 ] || fail "remove without a directory should exit 2, got $rc"

# --ui: smoke has no tty, so the screen is refused before it opens, with a hint naming the plain verb.
rc=0; ERR=$( cd "$GP" && "$ROTA_BIN" projects --ui 2>&1 >/dev/null ) || rc=$?
[ "$rc" = 2 ] || fail "projects --ui should exit 2, got $rc: $ERR"
case "$ERR" in *"--ui needs an interactive terminal"*"hint: run: rota projects"*) ;; *) fail "projects --ui should be refused with a hint: $ERR" ;; esac
rc=0; ERR=$( cd "$GP" && "$ROTA_BIN" projects remove x --ui 2>&1 >/dev/null ) || rc=$?
[ "$rc" = 2 ] || fail "projects remove --ui should exit 2, got $rc"
case "$ERR" in *"no --ui view for this verb"*) ;; *) fail "remove has no view: $ERR" ;; esac
rc=0; OUT=$( gpv "$GP" projects --ui ) || rc=$?
[ "$rc" = 2 ] || fail "projects --ui --json should exit 2, got $rc"
export XDG_CONFIG_HOME="$OLD_XDG"
pass "projects remove drops one entry and noops on an unknown one; projects --ui refusals hold"
