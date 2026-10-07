echo "config: the unread issues keys are removed; old configs still work (#501)"

CF="$(mktemp -d)"
trap 'rm -rf "$CF"' EXIT
mkdir -p "$CF/.rota"
printf '{\n  "issues": {\n    "providers": {\n      "github": true,\n      "gitlab": true\n    },\n    "label": "in-progress",\n    "autoCreateLabel": true,\n    "filterMineOnly": true\n  }\n}\n' > "$CF/.rota/config.json"

OUT=$( cd "$CF" && hvj config check || true )
echo "$OUT" | grep -q 'issues.filterMineOnly' || fail "config check should name the removed keys: $OUT"
[ "$(echo "$OUT" | jget 'data.removed[2]')" = "issues.providers.gitlab" ] \
  || fail "data.removed should list the three keys: $OUT"

# A verb that reads config still works with the removed keys present.
( cd "$CF" && "$ROTA_BIN" config show issues.label >/dev/null 2>&1 ) || fail "config show fails on a config holding removed keys"

OUT=$( cd "$CF" && hvj config fill )
[ "$(echo "$OUT" | jget 'data.removed[0]')" = "issues.filterMineOnly" ] || fail "fill should report what it removed: $OUT"
python3 - "$CF/.rota/config.json" <<'PY' || fail "fill did not strip the removed keys"
import json, sys
cfg = json.load(open(sys.argv[1]))
assert list(cfg["issues"]) == ["label", "autoCreateLabel"], list(cfg["issues"])
PY
[ "$( cd "$CF" && hvj config check | jget 'data.removed' )" = "[]" ] || fail "config check still lists removed keys after fill"

# A fresh init no longer seeds them.
FI="$(mktemp -d)"
( cd "$FI" && git init -q . && "$ROTA_BIN" init --no-blocks >/dev/null 2>&1 )
! grep -qE 'filterMineOnly|"providers"' "$FI/.rota/config.json" || fail "rota init still seeds a removed key"
rm -rf "$FI"
pass "removed issues keys: check names them, fill strips them, init no longer seeds them"
