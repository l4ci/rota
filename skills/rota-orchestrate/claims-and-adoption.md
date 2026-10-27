# Stale claims and adopted work

Loaded by `skills/rota-orchestrate/SKILL.md` section 2 when a candidate shows `blockedBy: stale-claim`, or when work another tool started must join the round.

## Stale claims

A candidate with `blockedBy: stale-claim` needs the maintainer's yes at every autonomy level. Show its `staleHolder`, `lastActivity`, and that reclaim releases the old claim marker and claim label before claiming for a new worker. Record the answer as the maintainer's own answer through its real channel, never as an orchestrator relay. Only after yes, run `rota round assign <ID> --reclaim-stale <claim-id>` (plus any separately approved readiness overrides). Assign rechecks activity and refuses `not-stale` if the holder woke up. Never pass this flag from the tick or reclaim automatically.

## Adopting work

Work another tool already started (a Codex worktree, an agent-team branch, a cloud session's branch) joins the round with `rota worker adopt <branch|worktree> --issue <N>`. Adopt it when the maintainer points at it, or when `round reconcile` lists an `unregistered-branch` (`round.adoptPattern` names which branches count). It runs the overlap check; read an exit 4 (`overlap`, `registered`, `held`) as you would on `assign`. The slot has no host: do not dispatch to it, wait on it or reclaim it. `round status` derives its state from the PR, and you gate it like any other (`rota worker gate <slot>`). After the merge it is released and its worktree and branch stay; leave them for the maintainer unless they ask for `--prune`.
