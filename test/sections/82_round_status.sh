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

# round summary folds .rota/ledger.jsonl: no file is "no ledger yet" at exit 0;
# a seeded round prints the table with the quota share column and the audit
# heading; a round the ledger lacks exits 3. Calls end in `|| true`: a verb
# captured in $(...) that exits non-zero would end the section silently.
OUT=$( cd "$RS" && $RSENV "$ROTA_BIN" round summary 2>/dev/null ) || fail "round summary must exit 0 with no ledger"
grep -q 'no ledger yet' <<<"$OUT" || fail "round summary without a ledger should say so: $OUT"
# A writing verb appends the line itself: `round bounce` records the event with
# the slot, account and harness read from the registry, not from the caller.
( cd "$RS" && $RSENV "$ROTA_BIN" round bounce 58 >/dev/null 2>&1 ) || fail "round bounce 58 failed"
LINE=$(grep -E '"kind": ?"bounce"' "$RS/.rota/ledger.jsonl" || true)
[ -n "$LINE" ] || fail "round bounce should append a bounce entry to .rota/ledger.jsonl"
grep -qE '"issue": ?"58"' <<<"$LINE" || fail "the bounce entry should name the issue: $LINE"
grep -qE '"slot": ?"dana"' <<<"$LINE" || fail "the bounce entry should name the slot holding the issue: $LINE"
printf '%s\n' \
  '{"ts":"2026-10-08T09:00:00Z","kind":"assign","round":4,"issue":"58","slot":"dana","account":"work","harness":"claude","detail":{"headroom":80.0}}' \
  '{"ts":"2026-10-08T09:30:00Z","kind":"done","round":4,"issue":"58","slot":"dana","pr":"#9","detail":{"headroom":60.0}}' \
  '{"ts":"2026-10-08T09:31:00Z","kind":"gate","round":4,"issue":"58","slot":"dana","pr":"#9","detail":{"verdict":"pass"}}' \
  '{"ts":"2026-10-08T09:31:00Z","kind":"merge","round":4,"issue":"58","slot":"dana","pr":"#9"}' \
  '{"ts":"2026-10-08T09:00:00Z","kind":"assign","round":4,"issue":"59","slot":"ben","harness":"codex"}' \
  > "$RS/.rota/ledger.jsonl"
OUT=$( cd "$RS" && $RSENV "$ROTA_BIN" round summary 2>/dev/null ) || true
grep -q 'quota share' <<<"$OUT" || fail "round summary should explain the quota share column: $OUT"
grep -q 'Gate audit' <<<"$OUT" || fail "round summary should end with the gate audit: $OUT"
grep -q '20%' <<<"$OUT" || fail "dana spent 20 headroom points: $OUT"
grep -q 'n/a' <<<"$OUT" || fail "a codex row has no meter, so its quota share is n/a: $OUT"
OUT=$( cd "$RS" && $RSENV "$ROTA_BIN" --json round summary 2>/dev/null ) || true
[ "$(echo "$OUT" | jget data.round)" = "4" ] || fail "round summary should default to the highest round: $OUT"
[ "$(echo "$OUT" | jget data.issues[0].quotaShare)" = "20.0" ] || [ "$(echo "$OUT" | jget data.issues[0].quotaShare)" = "20" ] \
  || fail "quotaShare should be the headroom delta: $OUT"
rc=0
( cd "$RS" && $RSENV "$ROTA_BIN" round summary --round 9 >/dev/null 2>&1 ) || rc=$?
[ "$rc" = "3" ] || fail "an unknown --round should exit 3, got $rc"
pass "round summary folds the ledger, says n/a for unmetered rows and exits 3 for an unknown round"
# An adopted (external) slot: no host drives it, so its row says `external`,
# the state is derived (unknown while the forge is down, as here: no origin),
# and neither a stale handle nor a missing agent is a dead-tab.
( cd "$RS" && git worktree add -q -b codex/12-ext .worktrees/ext-1 main \
    && cd .worktrees/ext-1 && git -c user.email=a@b -c user.name=n commit -q --allow-empty -m work ) \
  || fail "external fixture setup failed"
python3 - "$RS/.rota/workers.json" <<'PY' || fail "could not seed an external slot"
import json, sys
p = sys.argv[1]
d = json.load(open(p))
d["slots"].append({"name": "ext-1", "branch": "codex/12-ext", "worktree": p.rsplit("/.rota", 1)[0] + "/.worktrees/ext-1",
                   "base": "main", "kind": "external", "task": "12", "handle": "w9:t8", "state": "idle"})
json.dump(d, open(p, "w"))
PY
OUT=$( cd "$RS" && $RSENV "$ROTA_BIN" --json round status 2>/dev/null )
[ "$(echo "$OUT" | python3 -c 'import json,sys; r=[s for s in json.load(sys.stdin)["data"]["slots"] if s["name"]=="ext-1"][0]; print(r["hostState"], r["state"], r.get("kind",""))')" = "external unknown " ] \
  || fail "an external slot should read external/unknown with the forge down: $OUT"
OUT=$( cd "$RS" && $RSENV "$ROTA_BIN" --json round reconcile 2>/dev/null )
python3 - "$OUT" <<'PY' || fail "reconcile reported a host finding for the external slot: $OUT"
import json, sys
d = json.loads(sys.argv[1])["data"]
fs = [f for k in ("findings", "drift", "repaired") for f in d.get(k, [])]
bad = [f for f in fs if f.get("slot") == "ext-1" and f.get("kind") in ("dead-tab", "unclaimed-tab", "stalled", "item-timeout")]
sys.exit(1 if bad else 0)
PY
pass "an external slot reads derived state and draws no host findings"
