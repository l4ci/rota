echo "item field set: detail"

TMP_SD="$(mktemp -d)"
trap 'rm -rf "$TMP_SD"' EXIT
mkdir -p "$TMP_SD/.rota/features"
printf '# F10\n' > "$TMP_SD/.rota/features/F10.md"
printf '# F11\n' > "$TMP_SD/.rota/features/F11.md"
cat > "$TMP_SD/.rota/BACKLOG.md" <<'BL'
# TODO

## Features
- **[F10] [Major] With fields.** Summary. Related: [F11] Since: abc1234
- **[F12] [Minor] Bare.** Summary only.

## Completed
BL
SD() { ( cd "$TMP_SD" && "$ROTA_BIN" item field set "$1" --name detail --value "$2" ) >/dev/null; }
LINE() { grep -F "[$1]" "$TMP_SD/.rota/BACKLOG.md"; }

SD F10 .rota/features/F10.md
[ "$(LINE F10)" = '- **[F10] [Major] With fields.** Summary. Detail: `.rota/features/F10.md` Related: [F11] Since: abc1234' ] \
  || fail "detail[set]: not inserted before Related: $(LINE F10)"
SD F12 .rota/features/F10.md
[ "$(LINE F12)" = '- **[F12] [Minor] Bare.** Summary only. Detail: `.rota/features/F10.md`' ] \
  || fail "detail[set]: not appended on a bare bullet: $(LINE F12)"
pass "detail[set]: backticked path, before the other fields (or at the end)"

SD F10 .rota/features/F11.md
[ "$(LINE F10)" = '- **[F10] [Major] With fields.** Summary. Detail: `.rota/features/F11.md` Related: [F11] Since: abc1234' ] \
  || fail "detail[replace]: $(LINE F10)"
BEFORE="$(md5sum "$TMP_SD/.rota/BACKLOG.md")"
SD F10 .rota/features/F11.md || fail "detail[idempotent]: errored"
SD F10 '`.rota/features/F11.md`' || fail "detail[idempotent]: backticked input errored"
[ "$BEFORE" = "$(md5sum "$TMP_SD/.rota/BACKLOG.md")" ] || fail "detail[idempotent]: file rewritten"
pass "detail[replace]: replaced in place; same value (bare or backticked) is a no-op"

SD F10 ""
[ "$(LINE F10)" = '- **[F10] [Major] With fields.** Summary. Related: [F11] Since: abc1234' ] \
  || fail "detail[clear]: $(LINE F10)"
SD F12 ""
[ "$(LINE F12)" = '- **[F12] [Minor] Bare.** Summary only.' ] || fail "detail[clear]: $(LINE F12)"
pass "detail[clear]: empty value drops the pointer, neighbours intact"

BEFORE="$(md5sum "$TMP_SD/.rota/BACKLOG.md")"
rc=0; SD F10 .rota/features/F99.md >/dev/null 2>&1 || rc=$?
[ "$rc" = 3 ] || fail "detail[missing]: expected exit 3, got $rc"
[ "$BEFORE" = "$(md5sum "$TMP_SD/.rota/BACKLOG.md")" ] || fail "detail[missing]: file changed"
pass "detail[missing]: nonexistent file refused, backlog untouched"

BEFORE="$(md5sum "$TMP_SD/.rota/BACKLOG.md")"
rc=0; SD F10 '``' >/dev/null 2>&1 || rc=$?
[ "$rc" = 2 ] || fail "detail[empty]: backticks-only value expected exit 2, got $rc"
[ "$BEFORE" = "$(md5sum "$TMP_SD/.rota/BACKLOG.md")" ] || fail "detail[empty]: backticks-only value changed the backlog"
pass "detail[empty]: a backticks-only path is rejected, backlog untouched"

# the issues backend has no detail files: refused (exit 4) before any tracker call
TMP_SDI="$(mktemp -d)"
trap 'rm -rf "$TMP_SD" "$TMP_SDI"' EXIT
mkdir -p "$TMP_SDI/.rota"
echo '{"backlog":{"backend":"issues"},"issues":{"provider":"github","retryWaitSeconds":0}}' > "$TMP_SDI/.rota/config.json"
(
  cd "$TMP_SDI"
  git init -q && git config user.email t@t && git config user.name t && git commit -q --allow-empty -m seed
  export PATH="$TESTDIR/fakes:$PATH" FAKE_TRACKER_DB="$TMP_SDI/db.json" FAKE_TRACKER_LOG="$TMP_SDI/log"
  rc=0; OUT=$(hvj item field set 1 --name detail --value x 2>/dev/null) || rc=$?
  [ "$rc" = 4 ] || { echo "FAIL detail[issues]: issues backend must refuse detail with exit 4, got $rc"; exit 1; }
  [ "$(echo "$OUT" | jget data.changed)" = "false" ] || { echo "FAIL detail[issues]: refusal must report changed=false: $OUT"; exit 1; }
  [ ! -s "$TMP_SDI/log" ] || { echo "FAIL detail[issues]: refusal must not call the tracker: $(cat "$TMP_SDI/log")"; exit 1; }
) || fail "detail[issues]: issues backend must refuse detail"
pass "detail[issues]: issues backend still refuses detail, with no tracker call"

# the parsed field round-trips (value keeps its backticks, like capture's entries)
SD F10 .rota/features/F10.md
[ "$(cd "$TMP_SD" && "$ROTA_BIN" --json item field get F10 --name detail | jget data.value)" = '`.rota/features/F10.md`' ] \
  || fail "detail[parse]: item field get does not read it back"
pass "detail[parse]: item field get reads the pointer back"

trap 'rm -rf "$TMP"' EXIT
rm -rf "$TMP_SD" "$TMP_SDI"
