echo "worker done: refuses a finished slot without a test.fast proof row (#394)"

TMP_WD="$(mktemp -d "$TMP/workerdone.XXXXXX")"
WDP="$TMP_WD/proj"
mkdir -p "$WDP/.rota"
printf '# Backlog\n\n## Bugs\n\n- **[B07] [Major] Parser drops foo.** Small.\n' > "$WDP/.rota/BACKLOG.md"
echo '{"test":{"fast":["echo fast"]}}' > "$WDP/.rota/config.json"
(
  cd "$WDP" && git init -q -b feat/x . && git config user.email t@t && git config user.name t \
    && echo seed > seed.txt && git add seed.txt .rota/BACKLOG.md .rota/config.json && git commit -q -m seed
) || fail "worker-done fixture repo setup failed"
echo '{"slots":[{"name":"ben","branch":"feat/x","task":"B07","state":"busy"}]}' > "$WDP/.rota/workers.json"
wdrun() { ( cd "$WDP" && "$ROTA_BIN" --json "$@" 2>/dev/null ); }

RC=0; OUT=$(wdrun worker done ben) || RC=$?
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.changed)" = "false" ] || fail "no proof row should exit 4: rc=$RC $OUT"
pass "a slot without a test.fast proof row is refused, exit 4"

wdrun proof record B07 -- "echo fast" > /dev/null || fail "proof record failed"
( cd "$WDP" && git commit -q --allow-empty -m more )
RC=0; wdrun worker done ben > /dev/null || RC=$?
[ "$RC" = "4" ] || fail "a PASS row at an older sha should exit 4: rc=$RC"
pass "a PASS row at an older sha does not count"

wdrun proof record B07 -- "echo fast" > /dev/null || fail "proof record at HEAD failed"
RC=0; OUT=$(wdrun worker done ben) || RC=$?
[ "$RC" = "0" ] && [ "$(echo "$OUT" | jget data.changed)" = "true" ] || fail "a PASS row at HEAD should exit 0: rc=$RC $OUT"
grep -q '"state": *"done"' "$WDP/.rota/workers.json" || fail "the slot should be recorded done"
pass "a PASS row at HEAD marks the slot done"

echo '{}' > "$WDP/.rota/config.json"
echo '{"slots":[{"name":"ben","branch":"feat/x","task":"B07","state":"busy"}]}' > "$WDP/.rota/workers.json"
RC=0; OUT=$(wdrun worker done ben) || RC=$?
[ "$RC" = "0" ] && [ "$(echo "$OUT" | jget data.proofSkipped)" = "true" ] || fail "unset test.fast should skip the proof half: rc=$RC $OUT"
pass "an unset test.fast skips the proof half"
