echo "#576: each slot gets a port block, a DB suffix and its name in the environment"

TMP_SP="$(mktemp -d)"
trap 'rm -rf "$TMP_SP"' EXIT
mkdir -p "$TMP_SP/proj/.rota" "$TMP_SP/bin"
(
  cd "$TMP_SP/proj"
  git init -q -b main . && git config user.email t@t && git config user.name t
  printf '.worktrees/\n' > .gitignore
  printf '{"work":{"portBase":31000,"portBlock":50,"envSetup":"echo $ROTA_SLOT $ROTA_PORT_BASE $ROTA_PORT_BLOCK $ROTA_DB_SUFFIX >> %s/env.txt"}}\n' "$TMP_SP" > .rota/config.json
  git add .gitignore && git commit -q -m seed
) || fail "slot ports fixture setup failed"

( cd "$TMP_SP/proj" && "$ROTA_BIN" worker pool init --slots 2 --base main >/dev/null ) || fail "pool init failed"
[ "$(cat "$TMP_SP/env.txt")" = "$(printf 'w1 31000 50 _w1\nw2 31050 50 _w2')" ] \
  || fail "work.envSetup should see ROTA_SLOT, ROTA_PORT_BASE, ROTA_PORT_BLOCK and ROTA_DB_SUFFIX: $(cat "$TMP_SP/env.txt")"
pass "work.envSetup sees the four slot variables"

BASES=$(cd "$TMP_SP/proj" && python3 -c 'import json; print(",".join(str(s["portBase"]) for s in json.load(open(".rota/workers.json"))["slots"]))')
[ "$BASES" = "31000,31050" ] || fail "registry should record distinct port blocks: $BASES"
( cd "$TMP_SP/proj" && "$ROTA_BIN" worker pool reap w1 >/dev/null 2>&1 && "$ROTA_BIN" worker pool init --slots 2 --base main >/dev/null ) || fail "reap and re-init failed"
BASES=$(cd "$TMP_SP/proj" && python3 -c 'import json; print(",".join(str(s["portBase"]) for s in json.load(open(".rota/workers.json"))["slots"]))')
[ "$BASES" = "31000,31050" ] || fail "a reaped slot's block should be reusable and w2 keep its own: $BASES"
pass "port blocks are reserved per slot and freed on reap"

ln -s "$(command -v git)" "$TMP_SP/bin/git"
printf '#!/bin/sh\nexit 0\n' > "$TMP_SP/bin/jq"; chmod +x "$TMP_SP/bin/jq"
printf '#!/bin/sh\necho "LISTEN 0 128 *:31010 *:* users:((\\"node\\",pid=%s,fd=3))"\n' "$$" > "$TMP_SP/bin/ss"; chmod +x "$TMP_SP/bin/ss"
OUT=$( cd "$TMP_SP/proj" && ROTA_TEST_DOCTOR_PATH="$TMP_SP/bin" "$ROTA_BIN" doctor 2>&1 || true )
case "$OUT" in *$'warn\tports\t'*"port 31010 in slot w1's block 31000-31049"*) ;; *) fail "doctor should warn about the foreign listener: $OUT" ;; esac
pass "doctor warns about a foreign listener inside a slot's block"
rm -rf "$TMP_SP"
trap 'rm -rf "$TMP"' EXIT
