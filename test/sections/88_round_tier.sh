echo "round tiers: worker model per tier, above-default reason, codex map (C9, #75)"

# Same shape as section 85: a file-mode project, one active milestone, one
# ready item. Only --check-only and refusals run: starting a worker needs a
# host, which no section may reach. A fake tmux that is "not running" stands in
# for the host that round start and status ask about.
RT="$(mktemp -d "$TMP/round-tier.XXXXXX")"
(
  cd "$RT" && git init -q -b main . && git -c user.email=a@b -c user.name=n commit -q --allow-empty -m init \
    && mkdir -p .rota/milestones \
    && printf '# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n' > .rota/BACKLOG.md \
    && printf -- '---\nid: M01\ntitle: "m"\nstatus: active\ndepends: []\n---\n' > .rota/milestones/M01.md
) || fail "round tier fixture setup failed"
mkdir -p "$RT/downbin"
printf '#!/bin/sh\necho "no server running" >&2\nexit 1\n' > "$RT/downbin/tmux"
chmod +x "$RT/downbin/tmux"
# A codex start runs codex --version first: the fake stands in, never the real one.
cp "$TESTDIR/fakes/codex" "$RT/downbin/codex"
RTENV="env -u HERDR_PANE_ID -u TMUX_PANE -u HERDR_ENV PATH=$RT/downbin:$PATH"
rt() { ( cd "$RT" && $RTENV "$ROTA_BIN" --json "$@" 2>/dev/null ); }
rtrc() { RC=0; OUT=$( cd "$RT" && $RTENV "$ROTA_BIN" --json "$@" 2>/dev/null ) || RC=$?; }
rt item create --kind features --title First --milestone M01 --body-file - <<<$'## Acceptance\n- [ ] works' >/dev/null
HOLDER=$$
# Tab mode, explicitly: with no work.dispatch a round with no host would be solo (C8).
rt config set work.dispatch tmux >/dev/null
rt round start --holder-pid "$HOLDER" --slots 1 >/dev/null

# Default tier: standard, on the claude map; standard follows models.worker.
OUT=$(rt round assign F01 --check-only --holder-pid "$HOLDER")
[ "$(echo "$OUT" | jget data.tier)" = "standard" ] || fail "default tier should be standard: $OUT"
[ "$(echo "$OUT" | jget data.kind)" = "claude" ] || fail "default kind should be claude: $OUT"
[ "$(echo "$OUT" | jget data.model)" = "sonnet" ] || fail "standard should be sonnet by default: $OUT"
rt config set models.worker opus >/dev/null
OUT=$(rt round assign F01 --check-only --holder-pid "$HOLDER")
[ "$(echo "$OUT" | jget data.model)" = "opus" ] || fail "standard should follow models.worker: $OUT"
rt config set round.tiers.claude.standard sonnet >/dev/null
OUT=$(rt round assign F01 --check-only --holder-pid "$HOLDER")
[ "$(echo "$OUT" | jget data.model)" = "sonnet" ] || fail "an explicit tiers value should win: $OUT"
pass "the default tier resolves on the claude map and standard follows models.worker"

# Above the default: a reason is required, and it is reported back.
rtrc round assign F01 --check-only --tier heavy --holder-pid "$HOLDER"
[ "$RC" = "2" ] || fail "heavy above standard without a reason should exit 2, got $RC: $OUT"
OUT=$(rt round assign F01 --check-only --tier heavy --tier-reason "touches the lease" --holder-pid "$HOLDER")
[ "$(echo "$OUT" | jget data.model)" = "opus" ] || fail "heavy should be opus: $OUT"
[ "$(echo "$OUT" | jget data.tierReason)" = "touches the lease" ] || fail "the reason should be reported: $OUT"
OUT=$(rt round assign F01 --check-only --tier light --holder-pid "$HOLDER")
[ "$(echo "$OUT" | jget data.model)" = "haiku" ] || fail "light needs no reason: $OUT"
rtrc round assign F01 --check-only --tier ultra --holder-pid "$HOLDER"
[ "$RC" = "2" ] || fail "an unknown tier should exit 2, got $RC"
pass "a tier above the default needs --tier-reason; below it does not"

# Codex: no map is no model (Codex picks its own, #68), unless a custom command
# holds {model}; a full map resolves, a partial map is a config error.
rtrc round assign F01 --check-only --kind codex --holder-pid "$HOLDER"
[ "$RC" = "0" ] && [ "$(echo "$OUT" | jget data.kind)" = "codex" ] && ! echo "$OUT" | jget data.model >/dev/null || fail "unconfigured codex should resolve to no model: $RC $OUT"
rt config set work.codexCommand 'codex -m {model}' >/dev/null
rtrc round assign F01 --check-only --kind codex --holder-pid "$HOLDER"
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.blockedBy)" = "no tier map" ] || fail "a {model} command with no map should be refused: $RC $OUT"
rt config set work.codexCommand '' >/dev/null
rt config set round.tiers.codex.light c-light >/dev/null
rtrc round assign F01 --check-only --kind codex --holder-pid "$HOLDER"
[ "$RC" = "70" ] || fail "a partial codex map is a config error (70), got $RC: $OUT"
rt config set round.tiers.codex.standard c-std >/dev/null
rt config set round.tiers.codex.heavy c-heavy >/dev/null
OUT=$(rt round assign F01 --check-only --kind codex --holder-pid "$HOLDER")
[ "$(echo "$OUT" | jget data.model)" = "c-std" ] || fail "codex standard should resolve: $OUT"
rtrc round assign F01 --kind codex --holder-pid "$HOLDER"
[ "$RC" = "5" ] || fail "a codex worker under tmux should exit 5 (herdr only), got $RC: $OUT"
[ "$(git -C "$RT/.worktrees/ben" symbolic-ref --short HEAD)" = "park/ben" ] || fail "the refusal must leave the slot parked"
pass "codex resolves its model (none when unset), is refused before marking anything outside herdr, and needs a full map"

# Status shows what a slot was assigned with.
python3 - "$RT/.rota/workers.json" <<'PY' || fail "could not seed slot tier fields"
import json, sys
p = sys.argv[1]
d = json.load(open(p))
s = d["slots"][0]
s.update({"kind": "claude", "tier": "heavy", "model": "opus", "tierReason": "touches the lease"})
json.dump(d, open(p, "w"))
PY
OUT=$(rt round status)
[ "$(echo "$OUT" | jget data.slots[0].tier)" = "heavy" ] || fail "status should show the tier: $OUT"
[ "$(echo "$OUT" | jget data.slots[0].model)" = "opus" ] || fail "status should show the model: $OUT"
[ "$(echo "$OUT" | jget data.slots[0].tierReason)" = "touches the lease" ] || fail "status should show the reason: $OUT"
pass "round status shows each slot's tier, model and reason"
