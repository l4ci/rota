echo "round status/reconcile: rebuild a round from worktrees and the host snapshot (C2, #58)"

# The host is a FAKE herdr on PATH that only prints a canned snapshot; the real
# herdr must never be reachable from here. There is no origin, so the forge is
# reported unavailable and the host and worktree drift kinds are what we cover.
RS="$(mktemp -d "$TMP/round-status.XXXXXX")"
mkdir -p "$RS/fakebin"
cat > "$RS/fakebin/herdr" <<SH
#!/usr/bin/env bash
[ "\$1 \$2" = "api snapshot" ] || { echo "fake herdr: unexpected call \$*" >&2; exit 98; }
cat "$RS/snapshot.json"
SH
chmod +x "$RS/fakebin/herdr"

(
  cd "$RS" && git init -q -b main . && git -c user.email=a@b -c user.name=n commit -q --allow-empty -m init \
    && git worktree add -q -b park/ben .worktrees/ben main \
    && git worktree add -q -b dana/58-thing .worktrees/dana main
) || fail "round fixture repo setup failed"
mkdir -p "$RS/.rota"
printf '{"id":"cli","result":{"snapshot":{"agents":[{"agent":"claude","agent_status":"working","cwd":"%s/.worktrees/dana","name":"dana","tab_id":"w2:t1"},{"agent":"claude","agent_status":"idle","cwd":"%s/.worktrees/ghost","name":"ghost","tab_id":"w3:t1"},{"agent":"claude","agent_status":"working","cwd":"%s","name":"orchestrator","tab_id":"w4:t1"}]}}}\n' "$RS" "$RS" "$RS" > "$RS/snapshot.json"
RSENV="env HERDR_ENV=1 PATH=$RS/fakebin:$PATH"

# Empty registry: rows come from the worktrees, matched to the snapshot by cwd.
OUT=$( cd "$RS" && $RSENV "$ROTA_BIN" --json round status 2>/dev/null )
[ "$(echo "$OUT" | jget data.host)" = "herdr" ] || fail "status should name the host: $OUT"
[ "$(echo "$OUT" | jget data.slots[1].name)" = "dana" ] || fail "second row should be dana: $OUT"
[ "$(echo "$OUT" | jget data.slots[1].issue)" = "58" ] || fail "dana's issue should come from the branch: $OUT"
[ "$(echo "$OUT" | jget data.slots[1].hostState)" = "working" ] || fail "dana should match the live agent by cwd: $OUT"
[ "$(echo "$OUT" | jget data.slots[1].registered)" = "false" ] \
  || fail "dana is not registered: $OUT"
[ "$(echo "$OUT" | jget data.slots[2].name)" = "ghost" ] || fail "an unclaimed tab should be listed: $OUT"
pass "status derives rows from worktrees and matches the host snapshot"

# Reconcile reports and writes nothing.
OUT=$( cd "$RS" && $RSENV "$ROTA_BIN" --json round reconcile 2>/dev/null )
[ "$(echo "$OUT" | jget data.changed)" = "false" ] \
  || fail "reconcile without --apply must not change anything: $OUT"
grep -q 'unclaimed-tab' <<<"$OUT" || fail "reconcile should report the unclaimed tab: $OUT"
grep -q 'unregistered-worktree' <<<"$OUT" || fail "reconcile should report unregistered worktrees: $OUT"
[ ! -e "$RS/.rota/workers.json" ] || fail "reconcile without --apply wrote .rota/workers.json"

# --apply registers the worktrees and leaves the unclaimed tab alone.
OUT=$( cd "$RS" && $RSENV "$ROTA_BIN" --json round reconcile --apply 2>/dev/null )
[ -f "$RS/.rota/workers.json" ] || fail "--apply should register the worktrees"
grep -q '"dana/58-thing"' "$RS/.rota/workers.json" || fail "dana's slot should record its branch"
grep -q 'unclaimed-tab' <<<"$OUT" || fail "an unclaimed tab is never repaired: $OUT"
OUT=$( cd "$RS" && $RSENV "$ROTA_BIN" --json round status 2>/dev/null )
[ "$(echo "$OUT" | jget data.slots[1].registered)" = "true" ] \
  || fail "dana should be registered after --apply: $OUT"
pass "reconcile reports by default and --apply registers worktrees only"

# A dead tab: a slot whose recorded handle the host no longer reports.
( cd "$RS" && $RSENV "$ROTA_BIN" --json round reconcile --apply >/dev/null 2>&1 )
python3 - "$RS/.rota/workers.json" <<'PY' || fail "could not seed a dead handle"
import json, sys
p = sys.argv[1]
d = json.load(open(p))
for s in d["slots"]:
    if s["name"] == "ben":
        s["handle"] = "w9:t9"
json.dump(d, open(p, "w"))
PY
OUT=$( cd "$RS" && $RSENV "$ROTA_BIN" --json round reconcile --apply 2>/dev/null )
grep -q 'dead-tab' <<<"$OUT" || fail "a handle the host lacks should be a dead-tab: $OUT"
grep -q '"state": *"dead"' "$RS/.rota/workers.json" || fail "--apply should mark the dead slot"
pass "a dead tab is detected and its slot marked dead"

# Host and forge down: exit 0, both named unavailable. A fake tmux whose
# server is "not running" stands in for the host.
mkdir -p "$RS/downbin"
printf '#!/bin/sh\necho "no server running" >&2\nexit 1\n' > "$RS/downbin/tmux"
chmod +x "$RS/downbin/tmux"
OUT=$( cd "$RS" && env -u HERDR_ENV PATH="$RS/downbin:$PATH" "$ROTA_BIN" --json round status 2>/dev/null ) || fail "status must exit 0 with the host down"
[ "$(echo "$OUT" | jget data.unavailable)" = '["host","forge"]' ] || fail "unavailable sources should be listed: $OUT"
pass "unavailable sources degrade to warnings"
