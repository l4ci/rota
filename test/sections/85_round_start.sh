echo "round start/candidates: lease, roster slots, scope and readiness (C3, #59)"

# A file-mode project with one active milestone and three items: F01 is ready,
# F02 waits on F01, T01 has no acceptance criteria. No host or forge is
# touched: start provisions worktrees and a lease only.
RN="$(mktemp -d "$TMP/round-start.XXXXXX")"
(
  cd "$RN" && git init -q -b main . && git -c user.email=a@b -c user.name=n commit -q --allow-empty -m init \
    && mkdir -p .rota/milestones \
    && printf '# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n' > .rota/BACKLOG.md \
    && printf -- '---\nid: M01\ntitle: "m"\nstatus: active\ndepends: []\n---\n' > .rota/milestones/M01.md
) || fail "round fixture setup failed"
# start counts drift the way `round status` does, which asks the host: a fake
# tmux whose server is "not running" stands in, so no real host is reachable.
mkdir -p "$RN/downbin"
printf '#!/bin/sh\necho "no server running" >&2\nexit 1\n' > "$RN/downbin/tmux"
chmod +x "$RN/downbin/tmux"
RNENV="env -u HERDR_PANE_ID -u TMUX_PANE -u HERDR_ENV PATH=$RN/downbin:$PATH"
rn() { ( cd "$RN" && $RNENV "$ROTA_BIN" --json "$@" 2>/dev/null ); }
rn item create --kind features --title First --milestone M01 --body-file - <<<$'## Acceptance\n- [ ] works\nTouches internal/a.go' >/dev/null
rn item create --kind features --title Second --milestone M01 --body-file - <<<$'## Acceptance\n- [ ] ok\n\n## Depends on\n- F01\n' >/dev/null
rn item create --kind tasks --title Third --milestone M01 >/dev/null

# candidates: the milestone's items with their readiness checks.
OUT=$(rn round candidates)
[ "$(echo "$OUT" | jget data.scope)" = "milestone" ] || fail "default scope should be milestone: $OUT"
[ "$(echo "$OUT" | jget data.candidates[0].ready)" = "true" ] || fail "F01 should be ready: $OUT"
[ "$(echo "$OUT" | jget data.candidates[1].checks[1].detail[0])" = "F01 is not done" ] || fail "F02 should wait on F01: $OUT"
[ "$(echo "$OUT" | jget data.candidates[2].checks[0].ok)" = "false" ] || fail "T01 lacks criteria: $OUT"
pass "candidates reports criteria and dependency checks per item"

# start: lease, roster slots on park branches, candidates. Tab mode, explicitly:
# with no work.dispatch a round with no host would be solo (C8).
rn config set work.dispatch tmux >/dev/null
HOLDER=$$
OUT=$(rn round start --holder-pid "$HOLDER" --slots 2)
[ "$(echo "$OUT" | jget data.round)" = "1" ] || fail "first start should be round 1: $OUT"
[ "$(echo "$OUT" | jget data.slots[0].name)" = "ben" ] || fail "roster starts with ben: $OUT"
[ "$(echo "$OUT" | jget data.slots[1].branch)" = "park/dana" ] || fail "slots park on park/<agent>: $OUT"
[ -d "$RN/.worktrees/ben" ] || fail "slot worktree should be .worktrees/<agent>"
[ "$(echo "$OUT" | jget data.lease.state)" = "live" ] || fail "start should hold a live lease: $OUT"
[ "$(echo "$OUT" | jget data.candidates[0].id)" = "F01" ] || fail "start should list candidates: $OUT"
LEASE="$(git -C "$RN" rev-parse --path-format=absolute --git-common-dir)/rota/round-lease.json"
[ -f "$LEASE" ] || fail "the lease should live in the git common dir: $LEASE"
pass "start takes the lease and provisions roster slots on park/<agent>"

# The same holder renews; a second worktree's orchestrator is refused.
OUT=$(rn round start --holder-pid "$HOLDER" --slots 2)
[ "$(echo "$OUT" | jget data.round)" = "1" ] || fail "a restart by the holder keeps the round: $OUT"
[ "$(echo "$OUT" | jget data.changed)" = "false" ] || fail "a restart must change nothing: $OUT"
sleep 30 & OTHER=$!
RC=0; OUT=$( cd "$RN/.worktrees/ben" && $RNENV "$ROTA_BIN" --json round start --holder-pid "$OTHER" 2>/dev/null ) || RC=$?
kill "$OTHER" 2>/dev/null; wait "$OTHER" 2>/dev/null || true
[ "$RC" = "4" ] || fail "a second orchestrator should be refused with exit 4, got $RC: $OUT"
[ "$(echo "$OUT" | jget data.blockedBy)" = "lease held" ] || fail "refusal should say the lease is held: $OUT"
[ "$(echo "$OUT" | jget data.lease.pid)" = "$HOLDER" ] || fail "refusal should name the holder: $OUT"
pass "the lease is per repo: a second worktree is refused and the holder is named"

# A dead holder leaves a stale lease: reconcile reports it, start reclaims it.
true & DEAD=$!; wait "$DEAD" || true
python3 - "$LEASE" "$DEAD" <<'PY' || fail "could not age the lease"
import json, sys
p, pid = sys.argv[1], int(sys.argv[2])
d = json.load(open(p))
d["pid"], d["start"] = pid, 1
json.dump(d, open(p, "w"))
PY
OUT=$(rn round reconcile)
grep -q 'lease-stale' <<<"$OUT" || fail "reconcile should report the stale lease: $OUT"
OUT=$(rn round start --holder-pid "$HOLDER")
[ "$(echo "$OUT" | jget data.reclaimed)" = "true" ] || fail "start should reclaim a stale lease: $OUT"
[ "$(echo "$OUT" | jget data.round)" = "2" ] || fail "a reclaimed lease is a new round: $OUT"
pass "a stale lease is reported by reconcile and reclaimed by start"

# Scope: slate needs --items and limits candidates to them.
RC=0; OUT=$( cd "$RN" && $RNENV "$ROTA_BIN" --json round start --holder-pid "$HOLDER" --scope slate 2>/dev/null ) || RC=$?
[ "$RC" = "2" ] || fail "scope slate without --items should be a usage error, got $RC: $OUT"
OUT=$(rn round start --holder-pid "$HOLDER" --scope slate --items F02,T01)
[ "$(echo "$OUT" | jget data.candidates[0].id)" = "F02" ] || fail "slate candidates are the slate: $OUT"
[ "$(echo "$OUT" | jget data.candidates[2].id)" = "" ] || fail "only the slate should be listed: $OUT"
OUT=$(rn round candidates)
[ "$(echo "$OUT" | jget data.scope)" = "slate" ] || fail "candidates should follow the round's scope: $OUT"
OUT=$(rn round candidates --scope milestone)
[ "$(echo "$OUT" | jget data.candidates[2].id)" = "T01" ] || fail "--scope should override for one call: $OUT"
pass "scope slate limits candidates to the approved slate"

# assign: readiness, scope and the lease are checked before anything is marked.
# The worker contract is not in this fixture, so a ready item stops at the
# brief; a not-ready one and an out-of-scope one stop earlier. No host is used.
rn round start --holder-pid "$HOLDER" --scope milestone >/dev/null
OUT=$(rn round assign F01 --check-only --holder-pid "$HOLDER")
[ "$(echo "$OUT" | jget data.ready)" = "true" ] || fail "F01 should be ready: $OUT"
[ "$(echo "$OUT" | jget data.agent)" = "ben" ] || fail "check-only should name the first idle slot: $OUT"
[ "$(echo "$OUT" | jget data.branch)" = "ben/f01-first" ] || fail "branch is <agent>/<issue>-<slug>: $OUT"
RC=0; OUT=$( cd "$RN" && $RNENV "$ROTA_BIN" --json round assign T01 --check-only --holder-pid "$HOLDER" 2>/dev/null ) || RC=$?
[ "$RC" = "1" ] || fail "check-only on an unready item should exit 1, got $RC: $OUT"
[ "$(echo "$OUT" | jget data.ready)" = "false" ] || fail "T01 is not ready: $OUT"
RC=0; OUT=$( cd "$RN" && $RNENV "$ROTA_BIN" --json round assign T01 --holder-pid "$HOLDER" 2>/dev/null ) || RC=$?
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.blockedBy)" = "not ready" ] || fail "assign of an unready item should be refused: $RC $OUT"
grep -q 'no acceptance criteria' <<<"$OUT" || fail "the refusal should say why: $OUT"
RC=0; OUT=$( cd "$RN" && $RNENV "$ROTA_BIN" --json round assign F01 --holder-pid 1 2>/dev/null ) || RC=$?
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.blockedBy)" = "no round" ] || fail "a process without the lease is refused: $RC $OUT"
RC=0; OUT=$( cd "$RN" && $RNENV "$ROTA_BIN" --json round assign F01 --holder-pid "$HOLDER" 2>/dev/null ) || RC=$?
[ "$RC" = "4" ] && [ "$(echo "$OUT" | jget data.blockedBy)" = "brief missing" ] || fail "a missing worker contract is refused before marking: $RC $OUT"
[ "$(git -C "$RN/.worktrees/ben" symbolic-ref --short HEAD)" = "park/ben" ] || fail "a refused assign must leave the slot parked"
pass "assign refuses before marking: not ready, no lease, missing contract; check-only writes nothing"

# wind-down: verify, park, release. A slot holding work is reported and kept.
echo "scratch" > "$RN/.worktrees/ben/wip.txt"
RC=0; OUT=$( cd "$RN" && $RNENV "$ROTA_BIN" --json round wind-down --holder-pid "$HOLDER" 2>/dev/null ) || RC=$?
[ "$RC" = "4" ] || fail "wind-down with a dirty slot should exit 4, got $RC: $OUT"
[ "$(echo "$OUT" | jget data.blockedBy)" = "slot holds work" ] || fail "refusal should name the cause: $OUT"
[ "$(echo "$OUT" | jget data.slots[0].outcome)" = "retained" ] || fail "ben should be retained: $OUT"
[ "$(echo "$OUT" | jget data.slots[1].outcome)" = "unchanged" ] || fail "dana should be unchanged: $OUT"
[ -f "$LEASE" ] || fail "a refused wind-down must keep the lease"
rm "$RN/.worktrees/ben/wip.txt"
OUT=$(rn round wind-down --holder-pid "$HOLDER")
[ "$(echo "$OUT" | jget data.verdict)" = "clean" ] || fail "wind-down should be clean: $OUT"
[ "$(echo "$OUT" | jget data.verifySkipped)" = "true" ] || fail "no verify commands means skipped: $OUT"
[ "$(echo "$OUT" | jget data.slots[0].outcome)" = "unchanged" ] || fail "both slots end parked: $OUT"
[ ! -f "$LEASE" ] || fail "a clean wind-down should release the lease"
[ "$(git -C "$RN/.worktrees/ben" symbolic-ref --short HEAD)" = "park/ben" ] || fail "ben should be on park/ben"
RC=0; OUT=$( cd "$RN" && $RNENV "$ROTA_BIN" --json round wind-down --holder-pid "$HOLDER" 2>/dev/null ) || RC=$?
[ "$RC" = "3" ] || fail "wind-down without a lease should exit 3, got $RC: $OUT"
OUT=$(rn round start --holder-pid "$HOLDER")
[ "$(echo "$OUT" | jget data.round)" -ge 3 ] || fail "a new start after wind-down is a later round: $OUT"
pass "wind-down parks every slot, keeps the lease while one holds work, then releases it"
