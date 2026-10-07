# A worker whose output is slow but alive is not reported as stalled. The
# control: the same slot with nothing moving for longer than the window is.
e2e_fixture w1
printf '{"git":{"baseBranch":"main"},"work":{"dispatch":"tmux"},"round":{"stallMinutes":10}}\n' >"$E2E_ROOT/.rota/config.json"
python3 - "$E2E_ROOT/.rota/workers.json" <<'PY'
import json, sys
p = sys.argv[1]; d = json.load(open(p))
d["slots"][0]["activeAt"] = "2026-01-01T00:00:00Z"   # last state change: long ago
json.dump(d, open(p, "w"))
PY
stub silent w1
RC=0; OUT="$(rota_j round reconcile)" || RC=$?
expect "$RC" 0 "reconcile"
expect "$(jget 'data.drift[0].kind' <<<"$OUT")" stalled "control: a silent worker with no activity is stalled"

stub slow w1
OUT="$(rota_j worker poll w1 --settle 0)" || e2e_fail "worker poll failed: $OUT"
expect "$(jget 'data.slots[0].state' <<<"$OUT")" busy "a pane that keeps changing is BUSY"
echo "wip" >"$E2E_ROOT/.worktrees/w1/progress.txt"   # the slow worker's latest edit
OUT="$(rota_j round reconcile)" || e2e_fail "reconcile failed: $OUT"
expect "$(jget data.drift <<<"$OUT")" "[]" "slow output with fresh work is not stalled"
e2e_pass "slow output reads busy and is not reported as stalled; silence is"
