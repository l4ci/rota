echo "solo mode: a round with no host runs subagent workers on the same state (C8, #64)"

# A file-mode project with two ready items and no host in the environment
# (no HERDR_ENV, no TMUX), so round start resolves the host to solo. Nothing
# here spawns a pane or reaches a forge.
SO="$(mktemp -d "$TMP/solo.XXXXXX")"
(
  cd "$SO" && git init -q -b main . && git -c user.email=a@b -c user.name=n commit -q --allow-empty -m init \
    && mkdir -p .rota/milestones \
    && printf '# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n' > .rota/BACKLOG.md \
    && printf -- '---\nid: M01\ntitle: "m"\nstatus: active\ndepends: []\n---\n' > .rota/milestones/M01.md
) || fail "solo fixture setup failed"
SOENV="env -u HERDR_ENV -u HERDR_PANE_ID -u TMUX -u TMUX_PANE"
so() { ( cd "$SO" && $SOENV "$ROTA_BIN" --json "$@" 2>/dev/null ); }
reg() { python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); print(eval(sys.argv[2]))' "$SO/.rota/workers.json" "$1"; }
so item create --kind features --title First --milestone M01 --body-file - <<<$'## Acceptance\n- [ ] works\nTouches internal/a.go' >/dev/null
so item create --kind features --title Second --milestone M01 --body-file - <<<$'## Acceptance\n- [ ] works\nTouches internal/b.go' >/dev/null
printf 'stub worker contract\n' > "$SO/contract.md"
so config set round.brief "$SO/contract.md" >/dev/null
HOLD=$$

# start resolves and records the host.
so round start --holder-pid "$HOLD" --slots 2 >/dev/null || fail "solo round start failed"
[ "$(reg 'd.get("host")')" = "solo" ] || fail "round start with no host should record host solo: $(cat "$SO/.rota/workers.json")"
pass "round start with no host records host solo"

# Solo runs Claude subagents only: a codex worker is refused before anything is marked.
for t in light standard heavy; do so config set "round.tiers.codex.$t" "c-$t" >/dev/null; done
RC=0; so round assign F01 --agent ben --kind codex --holder-pid "$HOLD" >/dev/null || RC=$?
[ "$RC" = "2" ] || fail "a codex worker under solo should exit 2, got $RC"
[ "$(reg "[s for s in d['slots'] if s['name']=='ben'][0]['state']")" = "idle" ] || fail "a refused codex assign should mark nothing"

# assign hands back the brief and the worktree instead of dispatching.
for pair in F01:ben F02:dana; do
  id="${pair%%:*}"; agent="${pair##*:}"
  OUT=$(so round assign "$id" --agent "$agent" --holder-pid "$HOLD") || fail "solo assign $id failed: $OUT"
  [ "$(echo "$OUT" | jget data.host)" = "solo" ] && [ "$(echo "$OUT" | jget data.dispatched)" = "false" ] \
    || fail "solo assign should not dispatch: $OUT"
  [ "$(echo "$OUT" | jget data.worktree)" = "$SO/.worktrees/$agent" ] || fail "solo assign should name the slot worktree: $OUT"
  case "$(echo "$OUT" | jget data.brief)" in *"You are $agent."*) ;; *) fail "solo assign should return the pointer brief: $OUT" ;; esac
  [ "$(reg "[s for s in d['slots'] if s['name']=='$agent'][0]['state']")" = "busy" ] || fail "$agent should be busy"
  [ "$(reg "[s for s in d['slots'] if s['name']=='$agent'][0].get('handle')")" = "None" ] || fail "$agent should carry no handle"
done
pass "assign under solo returns the brief and the worktree for two issues, Claude only"

# wait never blocks under solo; report records what the subagent said.
RC=0; OUT=$(so round wait) || RC=$?
[ "$RC" = "1" ] && [ "$(echo "$OUT" | jget data.timedOut)" = "true" ] || fail "wait with every slot busy should time out at once: rc=$RC $OUT"
OUT=$(so round report ben --state done --pr https://github.com/o/r/pull/7 --evidence "opened PR 7") || fail "report failed: $OUT"
[ "$(echo "$OUT" | jget data.previous)" = "busy" ] && [ "$(echo "$OUT" | jget data.changed)" = "true" ] || fail "report should move ben from busy: $OUT"
[ "$(echo "$(so round report ben --state done --pr https://github.com/o/r/pull/7)" | jget data.changed)" = "false" ] || fail "the same report again should change nothing"
OUT=$(so round wait) || fail "wait should return the reported slot: $OUT"
[ "$(echo "$OUT" | jget data.slot)" = "ben" ] && [ "$(echo "$OUT" | jget data.source)" = "registry" ] || fail "wait should return ben from the registry: $OUT"
so round report dana --state done --pr 8 >/dev/null || fail "report dana failed"
[ "$(reg "[s for s in d['slots'] if s['name']=='ben'][0]['pr']")" = "https://github.com/o/r/pull/7" ] || fail "report should store the PR as poll does"
pass "wait reads the registry and report records state and PR"

# Pane verbs refuse, and reconcile accepts the state.
RC=0; so worker poll ben >/dev/null || RC=$?
[ "$RC" = "2" ] || fail "worker poll under a solo round should exit 2, got $RC"
[ "$(echo "$(so round status)" | jget data.host)" = "solo" ] || fail "round status should report host solo: $(so round status)"
OUT=$(so round reconcile)
case "$OUT" in *'"host"'*unavailable*|*'unavailable": ['*'"host"'*) fail "solo is not an unavailable host: $OUT" ;; esac
case "$OUT" in *dead-tab*|*unclaimed-tab*) fail "reconcile should find no tab drift under solo: $OUT" ;; esac
pass "pane verbs refuse under solo and reconcile accepts the solo state"
