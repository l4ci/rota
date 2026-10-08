echo "ship.review: depth policy shapes, config check, printed depth (#581)"

RD="$(mktemp -d)"
trap 'rm -rf "$RD"' EXIT
(
  cd "$RD"
  git init -q
  git config user.email t@t && git config user.name t
  git checkout -q -b main 2>/dev/null || git branch -m main
  mkdir -p .rota
  echo ok > a.txt
  git add a.txt && git commit -q -m init
  git checkout -q -b feat
  printf 'one\ntwo\nthree\n' > b.txt
  git add b.txt && git commit -q -m "feat: b"

  # legacy booleans and depth strings
  "$ROTA_BIN" config set ship.review false >/dev/null || fail "config set ship.review false"
  OUT=$(hvj review depth feat)
  [ "$(echo "$OUT" | jget data.depth)" = "none" ] || fail "legacy false should map to none: $OUT"
  "$ROTA_BIN" config set ship.review true >/dev/null
  [ "$(hvj review depth feat | jget data.depth)" = "full" ] || fail "legacy true should map to full"
  "$ROTA_BIN" config set ship.review light >/dev/null || fail "config set ship.review light"
  [ "$(hvj review depth feat | jget data.depth)" = "light" ] || fail "light should print light"

  # policy object: size rule, label override, strictest label wins
  "$ROTA_BIN" config set ship.review '{"default":"full","lightBelow":50,"labels":{"risk:high":"full","partial-slice":"none"}}' >/dev/null \
    || fail "config set rejected a valid policy object"
  OUT=$(hvj review depth feat)
  [ "$(echo "$OUT" | jget data.depth)" = "light" ] || fail "3 changed lines under lightBelow 50 should be light: $OUT"
  [ "$(echo "$OUT" | jget data.changedLines)" = "3" ] || fail "changedLines should be 3: $OUT"
  [ "$(hvj review depth feat --labels partial-slice | jget data.depth)" = "none" ] || fail "partial-slice label should map to none"
  [ "$(hvj review depth feat --labels partial-slice,risk:high | jget data.depth)" = "full" ] || fail "risk:high should force full"
  TXT=$("$ROTA_BIN" review depth feat)
  grep -q '^REVIEW-DEPTH feat — light' <<<"$TXT" || fail "text mode should print the depth line: $TXT"

  # a malformed policy is refused by set and flagged by config check
  if "$ROTA_BIN" config set ship.review '{"default":"deep"}' >/dev/null 2>&1; then fail "config set accepted default deep"; fi
  printf '{"ship":{"review":{"lightBelow":"many"}}}\n' > .rota/config.json
  if "$ROTA_BIN" config check >/dev/null 2>&1; then fail "config check passed a malformed ship.review"; fi
  if "$ROTA_BIN" review depth feat >/dev/null 2>&1; then fail "review depth ran on a malformed policy"; fi
) || exit 1
pass "ship.review: legacy booleans, depth strings and policy objects resolve; malformed shapes fail"
