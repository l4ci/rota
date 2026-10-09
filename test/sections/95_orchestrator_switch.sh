echo "D4: the orchestrator moves to another account before a usage limit (#206)"

# Everything lives under $TMP_SW (a fresh mktemp -d): a git project, the real
# `rota keepalive run` as supervisor, and a fake orchestrator, a shell script that
# feeds the real statusline dump and the real Stop hook a session at 95% of its
# 5-hour window, then writes a handoff and exits. Account meters are
# ROTA_ACCOUNT_USAGE_DIR fixtures. No real claude, herdr, tmux or network.
TMP_SW="$(mktemp -d)"
trap 'rm -rf "${TMP_SW:?}"' EXIT
SWP="$TMP_SW/proj"
mkdir -p "$SWP/.rota" "$TMP_SW/usage" "$TMP_SW/cfg-a" "$TMP_SW/cfg-b"
(
  cd "$SWP" && git init -q -b main . && git -c user.email=a@b -c user.name=n commit -q --allow-empty -m init
) || fail "D4 fixture repo setup failed"
SWCD="$(git -C "$SWP" rev-parse --path-format=absolute --git-common-dir)"
SWSTATE="$SWCD/rota/keepalive.json"
SWREG="$SWP/.rota/workers.json"
SWHANDOFF="$SWP/.rota/handoff/main.md"
sw_cfg() { # sw_cfg <true|false|unset>
  local on=""
  [ "$1" != unset ] && on=",\"switchOnUsage\":$1"
  printf '{"git":{"baseBranch":"main"},"work":{"accounts":[{"name":"a","configDir":"%s/cfg-a"},{"name":"b","configDir":"%s/cfg-b"}]},"orchestrator":{"keepaliveBackoffSeconds":0%s}}\n' \
    "$TMP_SW" "$TMP_SW" "$on" > "$SWP/.rota/config.json"
}
sw_usage() { # sw_usage <b's five_hour utilization>
  printf '{"five_hour":{"utilization":10,"resets_at":null},"seven_day":{"utilization":10,"resets_at":null}}' > "$TMP_SW/usage/a.json"
  printf '{"five_hour":{"utilization":%s,"resets_at":"2099-01-01T00:00:00.000000+00:00"},"seven_day":{"utilization":5,"resets_at":null}}' "$1" > "$TMP_SW/usage/b.json"
}

# The fake orchestrator. $1 scratch dir. Run 1 reports 95% usage to the real
# dump and hook and writes a handoff; run 2 reports the same and consumes it.
SWCHILD="$TMP_SW/child.sh"
cat > "$SWCHILD" <<'CH'
#!/bin/bash
kd="$1"
n=$(cat "$kd/count" 2>/dev/null || echo 0); n=$((n+1)); echo "$n" > "$kd/count"
echo "${CLAUDE_CONFIG_DIR-unset}" >> "$kd/cfgdir.log"
payload="{\"session_id\":\"sw$n\",\"cwd\":\"$SW_PROJ\",\"context_window\":{\"used_percentage\":10},\"rate_limits\":{\"five_hour\":{\"used_percentage\":95,\"resets_at\":4070908800},\"seven_day\":{\"used_percentage\":10,\"resets_at\":4071000000}}}"
echo "$payload" | "$ROTA_BIN" statusline dump
echo "{\"session_id\":\"sw$n\",\"cwd\":\"$SW_PROJ\",\"stop_hook_active\":false}" | "$ROTA_BIN" hook stop > "$kd/hook.$n.out"
if [ "$n" = 1 ]; then mkdir -p "$(dirname "$SW_HANDOFF")"; echo "handoff" > "$SW_HANDOFF"; else rm -f "$SW_HANDOFF"; fi
exit 0
CH
sw_run() { # sw_run <scratch dir>: the supervisor starts under account a
  ( cd "$SWP" && env -u HERDR_PANE_ID -u TMUX_PANE -u HERDR_ENV PATH="$ROTA_POISON_BIN:$PATH" \
      ROTA_ACCOUNT_USAGE_DIR="$TMP_SW/usage" CLAUDE_CONFIG_DIR="$TMP_SW/cfg-a" SW_PROJ="$SWP" SW_HANDOFF="$SWHANDOFF" \
      "$ROTA_BIN" --json keepalive run --no-limits -- bash "$SWCHILD" "$1" )
}
swlim() { python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); print(eval(sys.argv[2]))' "$SWREG" "$1"; }
swks() { python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); print(eval(sys.argv[2]))' "$SWSTATE" "$1"; }

# --- off by default: D3 unchanged -----------------------------------------------------
sw_cfg unset; sw_usage 20; KD="$(mktemp -d "$TMP_SW/run.XXXXXX")"; rm -f "$SWHANDOFF"
RC=0; OUT="$(sw_run "$KD" 2>"$KD/err.log")" || RC=$?
[ "$RC" = "0" ] && [ "$(jget data.restarts <<<"$OUT")" = "1" ] && [ "$(jget data.switches <<<"$OUT")" = "0" ] || fail "D4: off by default, rc=$RC: $OUT $(cat "$KD/err.log")"
[ ! -s "$KD/hook.1.out" ] || fail "D4: with switchOnUsage unset the hook must pass on 95% usage: $(cat "$KD/hook.1.out")"
[ ! -e "$SWREG" ] || [ "$(swlim 'len(d.get("limits", []))')" = "0" ] || fail "D4: off must log nothing: $(cat "$SWREG")"
pass "D4: off by default, the hook passes on usage and nothing is logged"

# --- a usable account: the restart moves to it ---------------------------------------
sw_cfg true; sw_usage 20; KD="$(mktemp -d "$TMP_SW/run.XXXXXX")"; rm -f "$SWHANDOFF" "$SWREG"
RC=0; OUT="$(sw_run "$KD" 2>"$KD/err.log")" || RC=$?
[ "$RC" = "0" ] && [ "$(jget data.restarts <<<"$OUT")" = "1" ] && [ "$(jget data.switches <<<"$OUT")" = "1" ] || fail "D4: switch run, rc=$RC: $OUT $(cat "$KD/err.log")"
grep -q '"decision": *"block"' "$KD/hook.1.out" && grep -q 'Usage is at 95%' "$KD/hook.1.out" && grep -q 'five_hour' "$KD/hook.1.out" || fail "D4: the hook must block on usage and name it: $(cat "$KD/hook.1.out")"
[ "$(sed -n 1p "$KD/cfgdir.log")" = "$TMP_SW/cfg-a" ] && [ "$(sed -n 2p "$KD/cfgdir.log")" = "$TMP_SW/cfg-b" ] || fail "D4: the restart must run under account b: $(cat "$KD/cfgdir.log")"
[ "$(swks 'd["account"]')" = "b" ] && [ "$(swks 'd["switches"]')" = "1" ] && [ "$(swks '"switchHold" in d')" = "False" ] || fail "D4: keepalive state: $(cat "$SWSTATE")"
[ "$(swlim 'd["limits"][0]["action"]')" = "switch" ] && [ "$(swlim 'd["limits"][0]["status"]')" = "switched" ] && [ "$(swlim 'd["limits"][0]["session"]')" = "orchestrator" ] || fail "D4: limits entry: $(cat "$SWREG")"
NOTE="$(swlim 'd["limits"][0]["note"]')"
[ "$(swlim 'd["limits"][0]["account"]')" = "a" ] && grep -q 'switched to b' <<<"$NOTE" || fail "D4: the entry names both accounts: $(cat "$SWREG")"
OUT="$(cd "$SWP" && "$ROTA_BIN" --json limit status)" || vfail
[ "$(jget 'data.limits[0].action' <<<"$OUT")" = "switch" ] || fail "D4: rota limit status must show the switch: $OUT"
pass "D4: above the threshold the hook blocks, and the restart moves to account b and logs it"

# --- no usable account: same-account restart with a hold -------------------------------
sw_cfg true; sw_usage 100; KD="$(mktemp -d "$TMP_SW/run.XXXXXX")"; rm -f "$SWHANDOFF" "$SWREG"
RC=0; OUT="$(sw_run "$KD" 2>"$KD/err.log")" || RC=$?
[ "$RC" = "0" ] && [ "$(jget data.restarts <<<"$OUT")" = "1" ] && [ "$(jget data.switches <<<"$OUT")" = "0" ] || fail "D4: hold run, rc=$RC: $OUT $(cat "$KD/err.log")"
[ "$(sed -n 1p "$KD/cfgdir.log")" = "$TMP_SW/cfg-a" ] && [ "$(sed -n 2p "$KD/cfgdir.log")" = "$TMP_SW/cfg-a" ] || fail "D4: with no usable account the restart stays on a: $(cat "$KD/cfgdir.log")"
[ -s "$KD/hook.1.out" ] && [ ! -s "$KD/hook.2.out" ] || fail "D4: the hold must make the second session's hook pass: 1=$(cat "$KD/hook.1.out") 2=$(cat "$KD/hook.2.out")"
[ "$(swks 'd["account"]')" = "a" ] && [ "$(swks 'd["switchHold"]["window"]')" = "five_hour" ] && [ "$(swks 'd["switchHold"]["until"]')" = "2099-01-01T00:00:00Z" ] || fail "D4: the hold must be recorded: $(cat "$SWSTATE")"
[ "$(swlim 'd["limits"][0]["action"]')" = "restart" ] && [ "$(swlim 'd["limits"][0]["status"]')" = "resumed" ] || fail "D4: limits entry: $(cat "$SWREG")"
NOTE="$(swlim 'd["limits"][0]["note"]')"
grep -q 'no usable account' <<<"$NOTE" && grep -q 'b: cooling' <<<"$NOTE" || fail "D4: the entry says why: $(cat "$SWREG")"
pass "D4: with no usable account it restarts on the same account, holds the switch and logs a restart"

# --- doctor ------------------------------------------------------------------------------
mkdir -p "$TMP_SW/nobin"
DOC() { ( cd "$SWP" && ROTA_TEST_DOCTOR_PATH="$TMP_SW/nobin" "$ROTA_BIN" --json doctor 2>/dev/null ) || true; }
dd() { python3 -c 'import json,sys; d=json.load(sys.stdin)["data"]["checks"]; print([c[sys.argv[2]] for c in d if c["name"]==sys.argv[1]][0])' "$1" "$2"; }
sw_cfg unset
[ "$(DOC | dd switch status)" = "skip" ] || fail "D4: doctor switch must skip while the key is off"
sw_cfg true
[ "$(DOC | dd switch status)" = "fail" ] && [ "$(DOC | dd switch hint)" = "rota hook install" ] || fail "D4: doctor switch must fail without the Stop hook: $(DOC)"
pass "D4: doctor's switch check skips when off and fails without the Stop hook"
