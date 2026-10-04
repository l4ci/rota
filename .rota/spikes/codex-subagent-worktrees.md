---
name: codex-subagent-worktrees
branch: spike/codex-subagent-worktrees
status: done
finished: 2026-10-03
created: 2026-10-03
---

# spike/codex-subagent-worktrees

## Question

Can Codex CLI (0.159.x) subagents each run in their own isolated git worktree, so hv's solo mode (C8) works with a Codex orchestrator?

## What was tried

All runs on 2026-10-03, Codex CLI 0.159.2 (codex-cli, npm), herdr 0.9.3, the maintainer's own login in `~/.codex` (CODEX_HOME unset, per the orchestrator's relay; no `auth.json` was copied anywhere).

1. Static: `codex --help`, `codex features list` (with CODEX_HOME pointed at an empty mktemp dir), and `strings` on the 0.159.2 binary to read the `spawn_agent` tool schema and its tool text.
2. Upstream: openai/codex#18969 ("Support `cwd` for `spawn_agent`", open, last comment 2026-09-24) and #23095 (same ask, closed as a duplicate).
3. Fixture: a fresh mktemp dir with `git init repo`, one commit (`a.txt`, `b.txt`), and two worktrees `wt-a` (`agent/a`) and `wt-b` (`agent/b`) beside it.
4. Live, one interactive TUI session at a time, in a herdr tab created for the spike in the worker's own workspace (`herdr tab create --cwd <fixture>/repo --label e3-probe`, then `herdr agent start e3-probe --kind codex`), low reasoning effort, four prompts in total:
   - Run A, default permissions (`codex -c model_reasoning_effort="low"`). Prompt: spawn exactly one subagent (`task_name wb`, `fork_turns none`) that runs `pwd` and `git rev-parse --show-toplevel`, then appends a line to `<fixture>/wt-b/b.txt` with a shell command. The first try was eaten by a hooks-review screen (below); the second ran.
   - Run B, the E1 default flags (`--dangerously-bypass-approvals-and-sandbox --no-daemon --no-alt-screen`). The prompt was eaten by the hooks-review screen again.
   - Run C, as B plus `--dangerously-bypass-hook-trust -c check_for_update_on_startup=false`. Prompt: spawn one subagent whose prose says its worktree is `<fixture>/wt-b`, run `pwd`, add a line to `b.txt` in that worktree with `apply_patch` (not a shell redirect), then `git -C <wt-b> status --short`.
5. Checked `b.txt` and `git status` in `repo`, `wt-a` and `wt-b` after each run.

Started and closed: the herdr tab `w1W:t2` (label `e3-probe`) and the codex agent `e3-probe` in it, restarted in the same pane three times with `/quit`. After the last run, `/quit` and `herdr tab close w1W:t2`; `tab get` then failed and `herdr agent list` no longer showed `e3-probe`. Nothing else was opened or closed. Side effects in `~/.codex`: the sessions and history of these runs, and one `[projects."<fixture>/repo"] trust_level = "trusted"` entry from accepting the folder-trust dialog in run A. Hook trust was never persisted (`esc`, then the bypass flag) and the update prompt was skipped with `esc`, which saves nothing.

## Findings

**Answer: partly.** Codex subagents cannot be *placed* in a worktree; they can be *told* to work in one, and they do.

- **No per-spawn working directory.** `spawn_agent` in 0.159.2 takes `task_name`, `message`, `model`, `fork_turns` and `agent_type` (a role from config). There is no `cwd`, `workdir` or worktree parameter, and the request for one (#18969) is open. A subagent is a new thread in the same process and starts in the parent's cwd: run C's subagent printed `pwd` = `<fixture>/repo`.
- **Prose isolation held in the one run that could write.** Given the absolute path of `wt-b` in its message, run C's subagent put its `apply_patch` edit in `wt-b/b.txt` and ran `git -C` there; `repo` and `wt-a` stayed clean. That is one sample on a two-file repo. Upstream reports (#23095) say workers in bigger trees "often search, edit, or apply patches relative to the coordinator's worktree before correcting themselves", which is the failure to expect.
- **Nothing else separates subagents.** They share the parent's process, `CODEX_HOME`, login, rate limit and session store; concurrency is capped by `agents.max_threads`. AGENTS.md and skills are the parent's (resolved from its cwd), not the worktree's.
- **Sandbox and approvals.** On this host Codex's sandbox (bwrap) fails on every command (`bwrap: loopback: Failed RTM_NEWADDR: Operation not permitted`), so under default permissions each command needs an escalation approval. A subagent's first approval is shown in the parent's pane (`Thread: Agent (<id>)`); later ones only as a footer (`! Approval needed in Agent (<id>) · /subagents to switch threads`), and keys sent to the pane go to the parent's composer, so an unattended orchestrator cannot answer them. Under workspace-write, a sibling worktree outside the parent's root is outside the writable area anyway. In practice solo mode on Codex means `--dangerously-bypass-approvals-and-sandbox`, which is what run C used.
- **Concurrency and sessions.** One subagent was run, to save quota; parallel subagents were not tried. They write to one session store as threads of the parent session, so they are not separable for `round reconcile` the way tabs are.

Findings for E1 (#68) from the same session, recorded here because they came out of it:

- herdr's codex detection is the terminal title (`osc_title_idle`, `osc_title_blocked` with evidence `[ . ] Action Required | <task> | repo`). Observed: `idle` at the prompt, `working` during a turn, `blocked` on an approval, `done` after `/quit` and after a turn finished. The usage-limit text was not seen.
- Three startup screens are invisible to herdr's state: the folder-trust dialog (`Trust this folder? ... › 1. Trust and continue  2. Quit`), the update prompt (`Update available · 0.159.2 → 0.160.0 ... 1. Update now  2. Skip  3. Skip until next version`) and the hooks review (`⚠ 1 hook needs review before it can run. ... t trust all · enter review · esc close`). herdr reported `idle` on the first two and `idle` or `working` on the third, never `blocked`, so `agent start` succeeds and a brief would land in the dialog. The hooks review opened on the first submitted prompt, not at start, and swallowed that prompt both times. It appears even with `--dangerously-bypass-approvals-and-sandbox`, and the herdr SessionStart hook in the maintainer's own `~/.codex` is installed but untrusted (`0/1`).
- `-c check_for_update_on_startup=false` suppresses the update prompt; `--dangerously-bypass-hook-trust` suppresses the hooks review.
- Codex runs in the alternate screen by default, and herdr refuses `agent read --source recent-unwrapped --lines N` while it works (`agent_not_idle`). With `--no-alt-screen` the read works mid-turn.
- `herdr agent read` prints plain text on herdr 0.9.3, not JSON; hv's herdr host parses `result.read.text`, and `test/fakes/herdr` returns JSON. That is a host bug outside E1.
- `/quit` exits Codex cleanly; herdr reports `done`, then drops the agent.
- Codex 0.160.0 was already out on the day of the test.

## Decision

depends-on-X: solo mode on Codex works only with prose isolation plus a guard, until Codex ships a per-spawn `cwd` (#18969).

## Recommended approach

What C8 (#64) needs to change for Codex:

1. Do not treat a Codex subagent as isolated. Solo mode with a Codex orchestrator puts the slot's absolute worktree path in the brief and tells the worker to use absolute paths and `git -C <worktree>` for everything.
2. Add a guard that does not trust the prose: after each subagent returns, the orchestrator's own tree must be clean (`hv git guard clean` on the main checkout), else the slot's work is refused and the stray edits reported. `worker gate` already re-verifies the slot worktree.
3. Run Codex solo workers with `--dangerously-bypass-approvals-and-sandbox` and say so: subagent approvals cannot be answered unattended and the sandbox cannot reach a sibling worktree.
4. Prefer a host when one exists: on herdr, a Codex worker is a separate session in its own tab and `CODEX_HOME` (E1), which isolates cwd, login and session state for real. Solo-on-Codex is the fallback, not the default.
5. Revisit when #18969 lands: a `cwd` on `spawn_agent` would make step 1 a parameter and step 2 a safety net.
