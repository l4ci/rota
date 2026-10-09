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
printf '%s\n' 'OUT="$(hvj item show B01)"' > "$TMP_FL/bad/q.sh"
printf '%s\n' 'OUT=$(hvj item show B01 \' '  --json)' > "$TMP_FL/bad/m.sh"
printf '%s\n' 'OUT=$(hvj item show B01) || vfail' 'rc=0; OUT="$(hvj item show B01)" || rc=$?' \
  'OUT=$(hvj item show B01 \' '  --json) || vfail' 'OUT=$(hvj item show NOPE) && fail "x"' > "$TMP_FL/good/x.sh"
for f in x q m; do
  mkdir "$TMP_FL/bad_$f"; cp "$TMP_FL/bad/$f.sh" "$TMP_FL/bad_$f/"
  rc=0
  (check_section_conventions "$TMP_FL/bad_$f") >/dev/null 2>&1 || rc=$?
  [ "$rc" -ne 0 ] || fail "lint accepted a bare capture in the $f form"
done
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

# 3. reap refuses to guess from an unreadable registry, and removes nothing.
(
  cd "$TMP_FL"
  rc=0
  "$ROTA_BIN" --json reap --apply >"$TMP_FL/out" 2>"$TMP_FL/err" || rc=$?
  [ "$rc" -eq 5 ] || fail "reap --apply on a corrupt registry exited $rc, want 5"
  grep -q 'reap refused' "$TMP_FL/err" "$TMP_FL/out" || fail "reap did not say why it refused"
) || exit 1
pass "reap --apply refuses on an unreadable registry"

# 4. vfail names the verb and the exit code.
rc=0
msg=$( ( f() { false || vfail; }; f ) 2>&1 ) || rc=$?
[ "$rc" -ne 0 ] || fail "vfail did not fail"
case "$msg" in *"verb exited 1"*) ;; *) fail "vfail message: $msg" ;; esac
pass "vfail reports the exit code"

rm -rf "$TMP_FL"
trap 'rm -rf "$TMP"' EXIT
