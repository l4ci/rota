# A train of three workers' branches whose second member breaks the tree:
# worker train bisects to it, lands nothing, and --land-green lands the
# verified prefix only.
e2e_fixture t1 t2 t3
for s in t1 t2 t3; do stub commit $s "$s.txt"; done
printf '{"git":{"baseBranch":"main"},"work":{"dispatch":"tmux"},"test":{"full":["test ! -f t2.txt"]}}\n' >"$E2E_ROOT/.rota/config.json"

RC=0; OUT="$(rota_j worker train t1 t2 t3 --base main)" || RC=$?
expect "$RC" 1 "a red train exits 1"
expect "$(jget data.verdict <<<"$OUT")" verify-failed "the verdict"
expect "$(jget data.culprit <<<"$OUT")" t2 "bisect isolates the failing member"
expect "$(jget data.changed <<<"$OUT")" false "a red train lands nothing"
[ ! -f "$E2E_ROOT/t1.txt" ] || e2e_fail "t1 must not be on main after a red train"
e2e_pass "a red train bisects to its failing member and lands nothing"

RC=0; OUT="$(rota_j worker train t1 t2 t3 --base main --land-green)" || RC=$?
expect "$RC" 1 "still red"
expect "$(jget data.culprit <<<"$OUT")" t2 "same culprit"
expect "$(jget data.changed <<<"$OUT")" true "the verified prefix landed"
[ -f "$E2E_ROOT/t1.txt" ] && [ ! -f "$E2E_ROOT/t2.txt" ] && [ ! -f "$E2E_ROOT/t3.txt" ] || e2e_fail "--land-green must land t1 only"
e2e_pass "--land-green lands the members before the culprit"
