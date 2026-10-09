echo "config fill: missing required keys get schema defaults (A9 G1, G7)"

CF="$(mktemp -d)"
trap 'rm -rf "$CF"' EXIT
mkdir -p "$CF/.rota"
RC=0; ( cd "$CF" && hvj config fill >/dev/null 2>&1 ) || RC=$?
[ "$RC" = "0" ] || fail "config fill on a project without config.json should exit 0, got $RC"
[ "$( cd "$CF" && hvj config check | jget data.status )" = "upToDate" ] \
  || fail "config check after fill on a fresh project should be upToDate"

# The rota init seed (issues-only keys, schema order) filled out stays in schema order.
printf '{\n  "issues": {\n    "label": "in-progress",\n    "autoCreateLabel": true\n  }\n}\n' > "$CF/.rota/config.json"
OUT=$( cd "$CF" && hvj config fill ) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
[ "$(echo "$OUT" | jget data.changed)" = "true" ] || fail "fill on the seed should report changed: $OUT"
[ "$(echo "$OUT" | jget 'data.filled[0]')" = "models.orchestrator" ] \
  || fail "filled should list keys in schema order: $OUT"
python3 - "$CF/.rota/config.json" <<'PY' || fail "filled seed is not in schema order"
import json, sys
cfg = json.load(open(sys.argv[1]))
want = ["models", "work", "refactor", "learn", "ship", "qa", "autonomy", "docs", "git", "umbrella", "rota", "issues", "test"]
assert list(cfg) == want, list(cfg)
assert list(cfg["issues"]) == ["label", "autoCreateLabel"], list(cfg["issues"])
assert cfg["work"]["isolation"] == "branch" and cfg["work"]["accounts"] == [], cfg["work"]
PY
pass "config fill completes a fresh and a seeded config in schema order"

# Present values are kept, a second fill writes nothing.
( cd "$CF" && "$ROTA_BIN" config set models.worker haiku >/dev/null )
BEFORE="$(cat "$CF/.rota/config.json")"
OUT=$( cd "$CF" && hvj config fill ) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
[ "$(echo "$OUT" | jget data.changed)" = "false" ] || fail "second fill should not change anything: $OUT"
[ "$(echo "$OUT" | jget data.filled)" = "[]" ] || fail "second fill should fill nothing: $OUT"
[ "$(cat "$CF/.rota/config.json")" = "$BEFORE" ] || fail "second fill rewrote config.json"

# A corrupt file is refused (70) and left as it was.
printf '{oops\n' > "$CF/.rota/config.json"
RC=0; ( cd "$CF" && hvj config fill >/dev/null 2>&1 ) || RC=$?
[ "$RC" = "70" ] || fail "fill on a corrupt config should exit 70, got $RC"
[ "$(cat "$CF/.rota/config.json")" = "{oops" ] || fail "fill touched a corrupt config"
pass "config fill keeps present keys, is idempotent and refuses a corrupt file"

trap 'rm -rf "$TMP"' EXIT
rm -rf "$CF"

# A config that only holds the pre-rename hv.version moves it to rota.version.
CF="$(mktemp -d)"
trap 'rm -rf "$CF"' EXIT
mkdir -p "$CF/.rota"
printf '{"hv": {"version": "4.2.0"}}\n' > "$CF/.rota/config.json"
OUT=$( cd "$CF" && hvj config fill ) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
[ "$(echo "$OUT" | jget data.changed)" = "true" ] || fail "fill should migrate the legacy stamp: $OUT"
python3 - "$CF/.rota/config.json" <<'PY' || fail "legacy hv.version was not moved to rota.version"
import json, sys
cfg = json.load(open(sys.argv[1]))
assert "hv" not in cfg, list(cfg)
assert cfg["rota"] == {"version": "4.2.0"}, cfg["rota"]
PY
[ "$( cd "$CF" && hvj config check | jget data.status )" = "upToDate" ] \
  || fail "config check after migrating the legacy stamp should be upToDate"
pass "config fill moves hv.version to rota.version"

trap 'rm -rf "$TMP"' EXIT
rm -rf "$CF"

# refactor.verifyCommands moved to test.full: copied, old key dropped, listed in filled.
CF="$(mktemp -d)"
trap 'rm -rf "$CF"' EXIT
mkdir -p "$CF/.rota"
printf '{"refactor": {"verifyCommands": ["x"]}}\n' > "$CF/.rota/config.json"
OUT=$( cd "$CF" && hvj config fill ) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
[ "$(echo "$OUT" | jget data.changed)" = "true" ] || fail "fill should move refactor.verifyCommands: $OUT"
echo "$OUT" | python3 -c 'import json, sys; sys.exit("test.full" not in json.load(sys.stdin)["data"]["filled"])' \
  || fail "filled should list test.full: $OUT"
python3 - "$CF/.rota/config.json" <<'PY' || fail "refactor.verifyCommands was not moved to test.full"
import json, sys
cfg = json.load(open(sys.argv[1]))
assert cfg["test"]["full"] == ["x"], cfg.get("test")
assert "verifyCommands" not in cfg["refactor"], cfg["refactor"]
PY
OUT=$( cd "$CF" && hvj config fill ) || fail "verb call failed at ${BASH_SOURCE[0]##*/}:$LINENO"
[ "$(echo "$OUT" | jget data.changed)" = "false" ] || fail "second fill after the move should not change anything: $OUT"
pass "config fill moves refactor.verifyCommands to test.full and is idempotent"

# check reports the legacy key as stale, even with every required key present, until fill moves it.
( cd "$CF" && hvj config fill >/dev/null )
python3 - "$CF/.rota/config.json" <<'PY'
import json, sys
p = sys.argv[1]
cfg = json.load(open(p))
cfg["test"]["full"] = []
cfg.setdefault("refactor", {})["verifyCommands"] = ["x"]
json.dump(cfg, open(p, "w"))
PY
RC=0; OUT=$( cd "$CF" && hvj config check ) || RC=$?
[ "$RC" = "1" ] || fail "config check with only refactor.verifyCommands should exit 1, got $RC: $OUT"
[ "$(echo "$OUT" | jget data.status)" = "stale" ] || fail "legacy key should make check stale: $OUT"
[ "$(echo "$OUT" | jget 'data.missing[0]')" = "refactor.verifyCommands" ] || fail "check should name refactor.verifyCommands: $OUT"
( cd "$CF" && hvj config fill >/dev/null )
[ "$( cd "$CF" && hvj config check | jget data.status )" = "upToDate" ] \
  || fail "config check after fill moved the legacy key should be upToDate"
pass "config check reports a legacy refactor.verifyCommands as stale until fill"

# A non-empty test.full wins over the legacy key, which is still dropped.
printf '{"test": {"full": ["keep"]}, "refactor": {"verifyCommands": ["old"]}}\n' > "$CF/.rota/config.json"
( cd "$CF" && hvj config fill >/dev/null )
python3 - "$CF/.rota/config.json" <<'PY' || fail "existing test.full was not kept over refactor.verifyCommands"
import json, sys
cfg = json.load(open(sys.argv[1]))
assert cfg["test"]["full"] == ["keep"], cfg["test"]
assert "verifyCommands" not in cfg["refactor"], cfg["refactor"]
PY
pass "config fill keeps an existing test.full when the legacy key is present"

trap 'rm -rf "$TMP"' EXIT
rm -rf "$CF"
