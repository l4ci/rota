echo "Section 178: jget and slot_field are shared by smoke and e2e (#3423)"
# test/lib.sh and test/e2e/lib.sh both source test/lib/common.sh; neither may carry
# its own copy, which is how slot_field drifted (e2e printed "" for [] and false).

DEFS="$(grep -rEl '^(jget|slot_field)\(\)' "$TESTDIR" --include='*.sh' | sed "s#^$TESTDIR/##" | sort | tr '\n' ' ')"
[ "$DEFS" = "lib/common.sh " ] || fail "jget/slot_field must be defined once, in lib/common.sh; found: $DEFS"
pass "jget and slot_field are defined once"

SF_TMP="$(mktemp -d)"
trap 'rm -rf "$SF_TMP"' EXIT
printf '{"slots":[{"name":"w1","state":"busy","list":[],"flag":false,"zero":0,"none":null,"pr":"u"}]}\n' > "$SF_TMP/workers.json"
slot_json="$SF_TMP/workers.json"
[ "$(slot_field "$slot_json" w1 state)" = busy ] || fail "slot_field lost a string"
[ "$(slot_field "$slot_json" w1 list)" = "[]" ] || fail "slot_field printed '$(slot_field "$slot_json" w1 list)' for an empty list"
[ "$(slot_field "$slot_json" w1 flag)" = "False" ] || fail "slot_field printed '$(slot_field "$slot_json" w1 flag)' for false"
[ "$(slot_field "$slot_json" w1 zero)" = "0" ] || fail "slot_field printed '$(slot_field "$slot_json" w1 zero)' for 0"
[ -z "$(slot_field "$slot_json" w1 none)" ] || fail "slot_field printed a value for null"
[ -z "$(slot_field "$slot_json" w1 missing)" ] || fail "slot_field printed a value for a missing field"
pass "slot_field prints falsy values ([], False, 0) and nothing for null or a missing field"

# The e2e library resolves the same functions, against its own workers.json.
OUT="$(E2E_ROOT="$SF_TMP/e2e" bash -c 'mkdir -p "$E2E_ROOT/.rota"; cp "$1" "$E2E_ROOT/.rota/workers.json"; . "$2"; e2e_slot w1 list; e2e_slot w1 flag' _ "$slot_json" "$TESTDIR/e2e/lib.sh")" \
  || fail "e2e lib could not source the shared helpers: $OUT"
[ "$OUT" = "$(printf '[]\nFalse')" ] || fail "e2e_slot disagrees with smoke on falsy values: $OUT"
pass "e2e_slot reads falsy values like the smoke helper"
trap 'rm -rf "$TMP"' EXIT
