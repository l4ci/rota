echo "config set"

CFG_TMP=$(mktemp -d)
trap 'rm -rf "$CFG_TMP"' EXIT
(
  cd "$CFG_TMP"
  mkdir -p .rota

  # --- Fresh config: nested key, boolean JSON value ---
  echo '{}' > .rota/config.json
  OUT=$(hvj config set ship.review true) || { echo "FAIL: simple set"; exit 1; }
  [ "$(echo "$OUT" | jget data.changed)" = "true" ] || { echo "FAIL: first set should report changed: $OUT"; exit 1; }
  [ "$(echo "$OUT" | jget data.value)" = "true" ] || { echo "FAIL: set should echo the stored value: $OUT"; exit 1; }
  echo "$OUT" | jget data.previous >/dev/null && { echo "FAIL: previous must be absent for an unset key: $OUT"; exit 1; }
  python3 -c "import json; d=json.load(open('.rota/config.json')); assert d == {'ship':{'review':True}}, d" || { echo "FAIL: ship.review true"; exit 1; }

  # --- Nested dotted path (creates intermediate dicts) ---
  hvj config set models.orchestrator opus >/dev/null || { echo "FAIL: nested set"; exit 1; }
  python3 -c "import json; d=json.load(open('.rota/config.json')); assert d['models']['orchestrator']=='opus', d" || { echo "FAIL: nested models.orchestrator"; exit 1; }

  # --- Idempotency: set same value twice, file unchanged, changed false ---
  before=$(cat .rota/config.json)
  OUT=$(hvj config set models.orchestrator opus) || { echo "FAIL: re-set"; exit 1; }
  after=$(cat .rota/config.json)
  [ "$before" = "$after" ] || { echo "FAIL: idempotent re-set changed file"; exit 1; }
  [ "$(echo "$OUT" | jget data.changed)" = "false" ] || { echo "FAIL: idempotent re-set should report changed false: $OUT"; exit 1; }
  [ "$(echo "$OUT" | jget data.previous)" = "opus" ] || { echo "FAIL: re-set should report previous: $OUT"; exit 1; }

  # --- Preserves other keys ---
  hvj config set learn.verify true >/dev/null || { echo "FAIL: add new section"; exit 1; }
  python3 -c "
import json
d = json.load(open('.rota/config.json'))
assert d['ship']['review'] is True, d
assert d['models']['orchestrator'] == 'opus', d
assert d['learn']['verify'] is True, d
" || { echo "FAIL: preservation"; exit 1; }

  # --- JSON value types: number, false, array, string ---
  hvj config set work.workerSlots 42 >/dev/null || { echo "FAIL: number"; exit 1; }
  python3 -c "import json; d=json.load(open('.rota/config.json')); assert d['work']['workerSlots'] == 42 and isinstance(d['work']['workerSlots'], int)" || { echo "FAIL: int parsing"; exit 1; }

  OUT=$(hvj config set ship.review false) || { echo "FAIL: false"; exit 1; }
  python3 -c "import json; d=json.load(open('.rota/config.json')); assert d['ship']['review'] is False" || { echo "FAIL: false parsing"; exit 1; }
  [ "$(echo "$OUT" | jget data.previous)" = "true" ] || { echo "FAIL: false should report previous true: $OUT"; exit 1; }

  hvj config set work.accounts '["a","b"]' >/dev/null || { echo "FAIL: array"; exit 1; }
  python3 -c "import json; d=json.load(open('.rota/config.json')); assert d['work']['accounts'] == ['a','b'], d" || { echo "FAIL: array parsing"; exit 1; }

  # --- Bare identifier falls back to string ---
  hvj config set autonomy.level auto >/dev/null || { echo "FAIL: string fallback"; exit 1; }
  python3 -c "import json; d=json.load(open('.rota/config.json')); assert d['autonomy']['level'] == 'auto'" || { echo "FAIL: string-auto"; exit 1; }

  # --- A string that looks like JSON needs shell quoting to stay a string ---
  hvj config set work.workerCommand '"true"' >/dev/null || { echo "FAIL: quoted string"; exit 1; }
  python3 -c "import json; d=json.load(open('.rota/config.json')); assert d['work']['workerCommand'] == 'true', d" || { echo "FAIL: quoted string value"; exit 1; }

  # --- Empty string is a valid value ---
  hvj config set work.workerCommand '' >/dev/null || { echo "FAIL: empty value"; exit 1; }
  python3 -c "import json; d=json.load(open('.rota/config.json')); assert d['work']['workerCommand'] == '', d" || { echo "FAIL: empty value stored"; exit 1; }

  # --- Bad inputs exit 2 and leave the file alone ---
  before=$(cat .rota/config.json)
  rc=0; "$ROTA_BIN" config set >/dev/null 2>&1 || rc=$?
  [ "$rc" = 2 ] || { echo "FAIL: missing args should exit 2, got $rc"; exit 1; }
  rc=0; "$ROTA_BIN" config set ship.review >/dev/null 2>&1 || rc=$?
  [ "$rc" = 2 ] || { echo "FAIL: missing value should exit 2, got $rc"; exit 1; }
  rc=0; "$ROTA_BIN" config set "" "x" >/dev/null 2>&1 || rc=$?
  [ "$rc" = 2 ] || { echo "FAIL: empty key should exit 2, got $rc"; exit 1; }
  rc=0; "$ROTA_BIN" config set ".foo" "x" >/dev/null 2>&1 || rc=$?
  [ "$rc" = 2 ] || { echo "FAIL: leading dot should exit 2, got $rc"; exit 1; }
  rc=0; "$ROTA_BIN" config set "foo." "x" >/dev/null 2>&1 || rc=$?
  [ "$rc" = 2 ] || { echo "FAIL: trailing dot should exit 2, got $rc"; exit 1; }
  # A key outside the schema is rejected (maintainer decision; old accepted any key).
  rc=0; "$ROTA_BIN" config set version 17 >/dev/null 2>&1 || rc=$?
  [ "$rc" = 2 ] || { echo "FAIL: key outside the schema should exit 2, got $rc"; exit 1; }
  rc=0; "$ROTA_BIN" config set no.such.key 1 >/dev/null 2>&1 || rc=$?
  [ "$rc" = 2 ] || { echo "FAIL: unknown dotted key should exit 2, got $rc"; exit 1; }
  [ "$before" = "$(cat .rota/config.json)" ] || { echo "FAIL: rejected sets changed the file"; exit 1; }

  # --- Missing config.json: verb creates it ---
  rm -f .rota/config.json
  hvj config set autonomy.level auto >/dev/null || { echo "FAIL: missing file"; exit 1; }
  python3 -c "import json; d=json.load(open('.rota/config.json')); assert d == {'autonomy':{'level':'auto'}}" || { echo "FAIL: missing file content"; exit 1; }

  # --- A config that is not a JSON object is an internal error (70), file untouched ---
  echo '[1]' > .rota/config.json
  rc=0; "$ROTA_BIN" config set autonomy.level auto >/dev/null 2>&1 || rc=$?
  [ "$rc" = 70 ] || { echo "FAIL: non-object config should exit 70, got $rc"; exit 1; }
  [ "$(cat .rota/config.json)" = "[1]" ] || { echo "FAIL: non-object config was rewritten"; exit 1; }
) || fail "config set assertions"
trap 'rm -rf "$TMP"' EXIT
rm -rf "$CFG_TMP"
pass "config set nested / idempotent / typed values / preservation / errors / autocreate"

echo "F78: config check reports the new keys stale on an older config"
CFG_F78="$(mktemp -d)"
trap 'rm -rf "$CFG_F78"' EXIT
mkdir -p "$CFG_F78/.rota"
python3 - "$CFG_F78/.rota/config.json" <<'PYEOF'
import json, sys
# A pre-F78 config: complete for its era, missing only the new work.* keys.
json.dump({
    "models": {"orchestrator": "opus", "worker": "sonnet"},
    "work": {"isolation": "branch", "mergeStrategy": "direct"},
    "refactor": {"confirmBeforeExecute": True, "verifyCommands": []},
    "learn": {"verify": True, "promoteThreshold": 3},
    "ship": {"review": True, "secondOpinion": False, "qa": False},
    "qa": {"gate": "advisory", "afterWork": False},
    "autonomy": {"level": "off"},
    "debug": {"competingHypotheses": False},
    "docs": {"path": "docs", "autoCreate": False, "afterWork": False},
    "git": {"baseBranch": ""},
    "umbrella": {"enabled": False},
    "issues": {"providers": {"github": True, "gitlab": True}},
    "rota": {"version": "4.5.0"},
}, open(sys.argv[1], "w"))
PYEOF
rc=0; VERDICT=$( cd "$CFG_F78" && hvj config check 2>/dev/null ) || rc=$?
[ "$rc" = 1 ] || fail "F78: config check on a pre-F78 config should exit 1, got $rc"
[ "$(echo "$VERDICT" | jget data.status)" = "stale" ] \
  || fail "F78: expected stale on a pre-F78 config, got '$VERDICT'"
echo "$VERDICT" | python3 -c '
import json, sys
missing = json.load(sys.stdin)["data"]["missing"]
for want in ("work.dispatch", "work.workerSlots"):
    assert want in missing, (want, missing)
' || fail "F78: stale verdict omits work.dispatch or work.workerSlots: '$VERDICT'"
# A fully populated config is up to date and exits 0.
python3 - "$CFG_F78/.rota/config.json" <<'PYEOF'
import json, sys
p = sys.argv[1]
cfg = json.load(open(p))
cfg["work"].update({"dispatch": "subagent", "workerSlots": 3, "workerCommand": "",
                    "accounts": [], "operatorCommand": ""})
cfg["ship"]["secondOpinionRunner"] = "subagent"
json.dump(cfg, open(p, "w"))
PYEOF
rc=0; VERDICT=$( cd "$CFG_F78" && hvj config check 2>/dev/null ) || rc=$?
[ "$rc" = 0 ] || fail "F78: config check on a complete config should exit 0, got $rc: $VERDICT"
[ "$(echo "$VERDICT" | jget data.upToDate)" = "true" ] || fail "F78: expected upToDate: $VERDICT"
# No config.json at all is fresh, and a broken one is corrupt; both exit 1.
rm "$CFG_F78/.rota/config.json"
rc=0; VERDICT=$( cd "$CFG_F78" && hvj config check 2>/dev/null ) || rc=$?
[ "$rc" = 1 ] && [ "$(echo "$VERDICT" | jget data.status)" = "fresh" ] \
  || fail "F78: a missing config should be fresh (exit 1), got $rc: $VERDICT"
echo '{not json' > "$CFG_F78/.rota/config.json"
rc=0; VERDICT=$( cd "$CFG_F78" && hvj config check 2>/dev/null ) || rc=$?
[ "$rc" = 1 ] && [ "$(echo "$VERDICT" | jget data.status)" = "corrupt" ] \
  || fail "F78: an unparseable config should be corrupt (exit 1), got $rc: $VERDICT"
trap 'rm -rf "$TMP"' EXIT
rm -rf "$CFG_F78"
pass 'F78: pre-F78 configs report stale so `rota init` backfills the new keys'
