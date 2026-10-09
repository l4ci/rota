#!/usr/bin/env bash
# #579: state-writing verbs fail loud. A registry that exists but does not parse
# is refused, not replaced by an empty pool, and the section lint rejects a
# bare verb capture.
echo "Section 147: fail-loud state writes"
TMP_FL="$(mktemp -d)"
trap 'rm -rf "$TMP_FL"' EXIT

# 1. The lint: a bare capture is a convention violation, a guarded one is not.
mkdir "$TMP_FL/bad" "$TMP_FL/good"
printf '%s\n' 'OUT=$(hvj item show B01)' > "$TMP_FL/bad/x.sh"
printf '%s\n' 'OUT=$(hvj item show B01) || fail "x"' 'rc=0; OUT=$(hvj item show B01) || rc=$?' > "$TMP_FL/good/x.sh"
rc=0
(check_section_conventions "$TMP_FL/bad") >/dev/null 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "lint accepted a bare verb capture"
rc=0
(check_section_conventions "$TMP_FL/good") >/dev/null 2>&1 || rc=$?
[ "$rc" -eq 0 ] || fail "lint rejected a guarded verb capture (rc=$rc)"
pass "section lint rejects a bare verb capture"

# 2. A truncated workers.json is refused by a registry write and left as found.
(
  cd "$TMP_FL"
  git init -q
  mkdir -p .rota
  echo '{"slots": [{"name": "ben"' > .rota/workers.json
  rc=0
  "$ROTA_BIN" --json round report ben --state done >"$TMP_FL/out" 2>"$TMP_FL/err" || rc=$?
  [ "$rc" -ne 0 ] || fail "round report succeeded on a corrupt registry"
  [ "$(cat .rota/workers.json)" = '{"slots": [{"name": "ben"' ] || fail "corrupt registry was overwritten"
) || exit 1
pass "corrupt registry is refused and kept"

trap 'rm -rf "$TMP"' EXIT
