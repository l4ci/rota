---
name: herdr-worktree-grouping
branch: spike/herdr-worktree-grouping
status: done
finished: 2026-10-15
created: 2026-10-15
---

# spike/herdr-worktree-grouping

## Question

Can round workers be grouped under their project in herdr by opening each slot's worktree as a linked workspace of the primary workspace, and should rota do it? (#805)

## What was tried

herdr 0.9.5, live session. Throwaway repo `<scratchpad>/spike805/repo` (`git init`, one empty commit, linked worktrees `.worktrees/agent1`, `.worktrees/agent2` on `park/agent1|2`). Every workspace and tab I made was labelled `spike-805-...`. I only ran read-only commands (`workspace list`, `worktree list`) against the maintainer's `rota` (`w2E`) and `Vault` (`w2F`) workspaces, never touched a `.worktrees/<agent>` of this repo, and closed all my own workspaces at the end (`workspace list` afterwards shows only `rota` and `Vault`).

Commands, in order:

1. `herdr workspace create --cwd <repo> --label spike-805-primary --no-focus` -> `w2G`.
2. `herdr worktree open --workspace w2G --path <repo>/.worktrees/agent1 --label spike-805-agent1 --no-focus` -> `w2H`, `is_linked_worktree: true`.
3. Same with the relative path `.worktrees/agent2` -> `worktree_not_found: worktree path not found`. With the absolute path -> `w2J`, linked. Repeating the open on an open worktree returns `already_open: true`.
4. `herdr workspace list` / `herdr worktree list --workspace w2G`.
5. `herdr tab create --workspace w2H --cwd <agent1> --label spike-805-envtab --env ROTA_SLOT=agent1 --env CLAUDE_CONFIG_DIR=/x/y --no-focus`, then `herdr pane run w2H:p2 'echo ...'` + `herdr pane read`.
6. Same `pane run` in the root pane (`w2H:p1`) that `worktree open` made.
7. `herdr agent start spike-805-a1 --kind claude --pane w2H:p1 --timeout 40000`, then `herdr agent list`, `herdr agent read`, `herdr agent wait spike-805-a1 --until blocked` from this pane in another workspace.
8. `herdr tab close w2H:t2`; `herdr workspace close w2G` (primary, linked open); `herdr workspace close w2J`; `herdr workspace close w2H` (with the claude still blocked on its trust dialog); `herdr workspace close w2G`.
9. Docs: `herdr --skill`, `herdr worktree|workspace|tab|pane --help`, `https://herdr.dev/llms-full.txt` (first 100 000 of 336 195 chars read), `gh release view` for v0.9.4, v0.9.5 and the 2026-10-14 preview.

## Findings

Marks: **[verified]** on the real herdr, **[inferred]** from docs or code, **[untested]**.

**Q1. Does `worktree open` nest the slot under the project?**
- **[verified, API level]** The opened workspace carries `worktree: {is_linked_worktree: true, repo_key: <repo>/.git, repo_name, checkout_path}`, the primary carries the same `repo_key` with `is_linked_worktree: false`, and `worktree list --workspace <primary>` shows each worktree's `open_workspace_id`. A plain `tab create` carries none of this, which is why today's tabs are not grouped. Two projects have different `repo_key`s, so several projects in one session stay separate groups.
- **[not verified: pixels]** I have no way to capture the TUI sidebar from a worker pane, so there is no screenshot. The sidebar nesting itself rests on the herdr docs ("groups it with the parent repo workspace") and the maintainer's own check in round 4 (#79, `docs/contributing/rounds.md`). Maintainer: please eyeball it once.
- **[verified, doc bug]** The recipe in `rounds.md` uses the relative `--path .worktrees/<agent>`. That fails with `worktree_not_found` here; an absolute path works. (Probably resolved against the server's cwd, not the workspace's. Not confirmed why.)

**Q2. Can rota start the agent like it does in a tab?**
- **[verified]** `worktree open` has **no `--env`** (flags: `--workspace --cwd --path --branch --label --focus --no-focus --trust-repository`). Its root pane has an empty `ROTA_SLOT`. rota's account and slot env (`ROTA_SLOT`, `ROTA_PORT_*`, `ROTA_DB_SUFFIX`, `CLAUDE_CONFIG_DIR`/`CODEX_HOME`) therefore cannot ride on the open. `agent start` has no env flag either.
- **[verified]** `tab create --workspace <linked ws> --cwd <worktree> --env K=V` works, and the env reaches the pane (`ENV=agent1 /x/y <worktree>`). So the shape is: `worktree open` (grouping), then `tab create --env` inside the linked workspace (what `Spawn` does today, with a different `--workspace`), then close the root tab `1` the open created, else it lingers as a plain shell (doctor's `tab` check flags exactly that).
- **[verified]** `agent start --kind claude --pane w2H:p1` works in a linked workspace and reached `blocked` on the trust dialog, the same state `Spawn` already handles via `DialogKeys`.
- **[verified]** `agent list` is server-wide: the agent in `w2H` showed up beside the orchestrator's agents in `w2E`. `agent wait`, `agent read` and `pane read/run` by name or pane id worked from another workspace. So name- and pane-id-based verbs do not care which workspace holds the pane.
- **[inferred]** `round watch`, `round wait`, relays and `worker poll` address agents by `rota-<slot>-<hash of tab id>` and pane/tab ids (`internal/host/herdr.go` `AgentName`, `Send`); tab ids embed the workspace id, so they stay unique. I did not run rota's own verbs, as a round is live. The code that assumes one workspace: `Spawn` and `OpenTab` read `HERDR_WORKSPACE_ID` (`herdr.go:252`, `open.go:38`); `host/label.go:48` reports the `round` token on the orchestrator workspace only; `host/layout.go` maps workspace labels; doctor/reap's `tab` check looks for tabs inside `.worktrees/`. All need to follow a slot to its own workspace.

**Q3. Park, reclaim, wind-down; the `--group` rule.**
- **[verified]** Closing a linked workspace works while its agent is live (`workspace close w2H` with claude blocked: agent gone from `agent list`). `workspace close` only closes herdr state.
- **[verified]** Closing the primary while linked workspaces are open fails with `workspace_group_close_required` (`--group` / `close_group=true`). `--group` closes the primary **and every linked workspace**.
- **[verified]** `worktree open` is idempotent (`already_open: true`), so reclaim can call it every time without checking.
- **[inferred]** Consequence for rota: the orchestrator's workspace is the primary. Closing it by hand (or via API) while slots are open needs `--group`, which also kills every worker workspace. Today closing it kills the worker tabs too, so no regression, but the refusal is now a speed bump that a human will meet. rota never closes the primary, so no rota change is forced.
- **[inferred, recommended]** Keep the linked workspace open between issues (park only closes the agent's tab, as now) and close it at wind-down by label. A workspace per slot is long-lived and cheap, and `open` being idempotent makes re-creation safe either way.
- **[untested]** What closing the *last tab* of a linked workspace does (close the workspace or leave an empty one; v0.9.4 notes say the TUI now asks before closing a workspace's only pane, the CLI is documented to skip the prompt). Settle this first in the follow-up.
- **Do not use `herdr worktree remove`**: it runs `git worktree remove`. rota owns worktree lifecycle through git (`round start`/wind-down); only `workspace close` is safe.

**Q4. A tab- or metadata-level link instead of a workspace per slot?**
- **[inferred, docs]** None. `tab create` takes `--workspace --cwd --label --env --focus/--no-focus` only. `workspace report-metadata` sets display tokens (`$name` in Space rows), not a parent. The docs name exactly one grouping mechanism: a worktree-backed workspace linked to the primary. `llms-full.txt` read to char 100 000 of 336 195; the rest may hold more.
- **[verified, release notes]** v0.9.4 and v0.9.5 and the 2026-10-14 preview notes add no tab or metadata link. v0.9.4 only fixes worktree-group restore (#4926 and others), which helps this approach.

**Q5. Recommendation: opt-in setting, tabs stay the default.**
- Why not switch the default: workers stop sitting next to the orchestrator (today the maintainer sees all slots as tabs in one workspace), N extra workspaces appear per round, closing the orchestrator's workspace now needs `--group`, and the open + tab + close-root-tab dance doubles the herdr calls in `Spawn`. Why not do nothing: the maintainer wants grouping with several projects in one session, and it works.
- Code that would change: `internal/host/herdr.go` (`Spawn` at ~252; `Close`/reclaim and wind-down paths that call `tab close`), `internal/host/open.go:38` (`OpenTab`, operator tab), `internal/host/label.go` and `layout.go` (workspace-aware labels), doctor/reap `tab` check, a config key under `work.*` (name TBD in the follow-up), `skills/rota-orchestrate/references/herdr-dispatch.md`, `docs/contributing/rounds.md` (absolute-path fix), `docs/usage/configuration.md`, and a smoke section with a fake herdr.

## Decision

viable (opt-in). Tabs stay the default.

## Recommended approach

Add an opt-in config key (default off) under which herdr `Spawn` does: `worktree open --workspace $HERDR_WORKSPACE_ID --path <absolute slot path> --label <slot> --no-focus`, then `tab create --workspace <opened id> --env ...` as today, then close the root tab the open created; remember the workspace id on the slot so reclaim and wind-down close it by id (never `--group`, never `worktree remove`). Settle the last-tab-close behaviour first. Follow-up issue filed with acceptance criteria.
