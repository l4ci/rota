echo "T118: config show reports value + source layer from one defaults table"

CS="$(mktemp -d)"
trap 'rm -rf "$CS"' EXIT
mkdir -p "$CS/.rota"
printf '{"work":{"dispatch":"tmux"},"autonomy":{"level":"auto"}}\n' > "$CS/.rota/config.json"
printf '{"autonomy":{"level":"off"}}\n' > "$CS/.rota/config.local.json"

# show [<key>…]: run in the fixture project and print the envelope.
show() { ( cd "$CS" && hvj config show "$@" ); }

OUT=$(show work.dispatch)
[ "$(echo "$OUT" | jget 'data.entries[0].value')" = "tmux" ] \
  || fail "T118: project value not reported: $OUT"
[ "$(echo "$OUT" | jget 'data.entries[0].source')" = "project" ] \
  || fail "T118: project value not reported as source project: $OUT"
OUT=$(show autonomy.level)
[ "$(echo "$OUT" | jget 'data.entries[0].value')" = "off" ] \
  || fail "T118: config.local.json should win: $OUT"
[ "$(echo "$OUT" | jget 'data.entries[0].source')" = "local" ] \
  || fail "T118: config.local.json should report source local: $OUT"
OUT=$(show ship.secondOpinionRunner)
[ "$(echo "$OUT" | jget 'data.entries[0].value')" = "subagent" ] \
  || fail "T118: unset key should report the default: $OUT"
[ "$(echo "$OUT" | jget 'data.entries[0].source')" = "default" ] \
  || fail "T118: unset key should report source default: $OUT"
OUT=$(show work.accounts)
[ "$(echo "$OUT" | jget 'data.entries[0].value')" = "[]" ] \
  || fail "T118: array default not returned as a JSON array: $OUT"
[ "$(echo "$OUT" | jget 'data.entries[0].source')" = "default" ] \
  || fail "T118: array default should report source default: $OUT"
[ "$(echo "$OUT" | jget 'data.entries[0].key')" = "work.accounts" ] \
  || fail "T118: entry should name its key: $OUT"
pass "T118: config show resolves local > project > default"

# Schema check derives from the same table: dropping one required key names it.
printf '{}\n' > "$CS/.rota/config.json"
RC=0; V=$( cd "$CS" && hvj config check 2>/dev/null ) || RC=$?
[ "$RC" = "1" ] || fail "T118: config check on an empty config should exit 1, got $RC"
[ "$(echo "$V" | jget data.status)" = "stale" ] \
  || fail "T118: config check on an empty config should be stale: $V"
echo "$V" | python3 -c '
import json, sys
missing = json.load(sys.stdin)["data"]["missing"]
for want in ("work.dispatch", "ship.secondOpinionRunner"):
    assert want in missing, (want, missing)
for k in missing:
    assert not k.startswith("release.") and k != "issues.label", ("non-required key leaked", k)
' || fail "T118: config check missing list should name required table keys, got: $V"
pass "T118: config check names the required keys of the same table"

# The config screen is opt-in and terminal-only (#542): off a terminal every way in refuses
# before anything is read or written (stdout is a pipe here, since /dev/null counts as a
# character device), and plain `config show` carries no ANSI.
printf '{}\n' > "$CS/.rota/config.json"
RC=0; ERR=$( cd "$CS" && "$ROTA_BIN" config edit 2>&1 ) || RC=$?
[ "$RC" = "4" ] || fail "config edit off a terminal should exit 4, got $RC: $ERR"
case "$ERR" in *"rota config set <key> <value>"*) ;; *) fail "config edit should point at config set: $ERR" ;; esac
for args in "--ui" "show --ui"; do
  RC=0; ERR=$( cd "$CS" && "$ROTA_BIN" config $args 2>&1 ) || RC=$?
  [ "$RC" = "2" ] || fail "config $args off a terminal should exit 2, got $RC: $ERR"
  case "$ERR" in *"hint: run: rota config show"*) ;; *) fail "config $args should hint the plain verb: $ERR" ;; esac
done
RC=0; ERR=$( cd "$CS" && "$ROTA_BIN" --json config edit 2>&1 ) || RC=$?
[ "$RC" = "4" ] || fail "config edit --json should exit 4, got $RC: $ERR"
case "$( cd "$CS" && "$ROTA_BIN" config show )" in *"$(printf '\033')"*) fail "plain config show carries an ESC byte" ;; esac
[ "$(cat "$CS/.rota/config.json")" = "{}" ] || fail "a refused screen wrote config.json"
pass "config edit and config --ui refuse off a terminal and write nothing; plain show carries no ANSI"

trap 'rm -rf "$TMP"' EXIT
rm -rf "$CS"
