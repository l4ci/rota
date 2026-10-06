# rota cheat sheet

What each `/rota-*` skill does, one line each. For details: [`reference/slash-commands.md`](reference/slash-commands.md).

## Capture & pick
- **`/rota-capture`**: add bugs, features, tasks to the backlog (`.rota/BACKLOG.md` or GitHub/GitLab issues, per `backlog.backend`; see [issue backend](usage/issue-backend.md)). No code yet. Prints the new IDs and stops. Flag: `--remove`.
- **`/rota-pause`**: stop cleanly; leave a handoff note for the next session.

## Plan & build
- **`/rota-brainstorm`**: design exploration before planning. For big items.
- **`/rota-plan`**: write the implementation plan with verifiable tasks.
- **`/rota-spike`**: throwaway experiment on a dedicated branch. Only findings come back.
- **`/rota-work`**: execute the plan in parallel with per-task commits. `--preview` for a read-only peek. No argument: reconcile the backlog and suggest the next item.
- **`/rota-debug`**: systematic bug cycle. Reproduce, hypothesize, fix.
- **`/rota-orchestrate`**: run a parallel round: choose the slate, route workers, merge. The `rota round` verbs do the mechanics.

## Review & ship
- **`/rota-review`**: one review pass (spec match and code quality).
- **`/rota-qa`**: product-level QA. Playwright, smoke, lighthouse, axe, ZAP.
- **`/rota-ship`**: open a PR or direct merge. `--undo` rolls back; `--docs` syncs public docs.

## Persist
- **`/rota-learn`**: capture reusable lessons in `KNOWLEDGE.md`.
- **`/rota-decide`**: lock in a hard-boundary decision. Manual only.

## Vision & shape
- **`/rota-vision`**: brainstorm milestones and the project roadmap.
- **`/rota-refactor`**: architecture review; files findings as refactor issues (`--fix` to implement).

## Maintenance
- **`/rota-release`**: cut a release.

## Verbs, not skills
- **`rota`** (bare, in a terminal): runs `rota setup` in a directory without `.rota/`, launches the orchestrator in an initialized project.
- **`rota orchestrate`**: run `rota doctor`, then start the orchestrator under `rota keepalive run`.
- **`rota init`** (`rota init umbrella`): scaffold `.rota/` and fill config defaults.
- **`rota projects`**: list every project `rota init` registered on this machine (`$XDG_CONFIG_HOME/rota/projects.json`, default `~/.config/rota`). Paths that no longer exist are marked `(missing)`. `rota projects cleanup` deletes, at once, every entry whose directory is gone or no longer holds `.rota/`, and prints each.
- **`rota setup`**: `rota init` plus a short config walkthrough on a terminal. `--yes` takes the defaults, `--set key=value` answers one question, `--list` prints them.
- **`rota config show` / `rota config set` / `rota config edit`**: read and change settings; `edit` is an interactive editor (terminal only). `rota config save-global` saves this project's config as the defaults for new projects.
- **`rota update`**: check for a newer release.
- **`rota migrate issues`**: move the backlog to GitHub or GitLab issues.
- **`rota skills install` / `update` / `status`**: write the skills for Claude Code and Codex, refresh them after an upgrade, compare with the binary.
- **`rota round start` / `assign` / `wait` / `status` / `wind-down`**: run a round: take the lease, hand an issue to a slot, block until a worker needs you, list slots, park everything and release the lease.
- **`rota worker`**: slot registry, worktrees, dispatch, polling and the merge gate (`rota worker gate`).
- **`rota doctor`**: preflight for git, jq, host, forge, accounts, hooks, skills and Codex.
- **`rota reap`**: preview leftovers a round left behind; `--apply` removes those holding no work.
- **`rota layout split` / `tabs`**: fold a round's herdr panes into one split view for a wide screen, or back into tabs for a narrow one; bare `rota layout` shows which.
- **`rota keepalive run`**: restart the orchestrator in its pane when it exits with a fresh handoff.
- **`rota limit watch`**: sleep until a usage limit resets, or switch accounts.
- **`rota hook` / `rota statusline`**: Claude Code hooks and the statusline command that hand the orchestrator off before its context fills.
- **`rota verdict add` / `route` / `show`**: record typed review, second-opinion and QA verdicts and route on them.

See [parallel rounds](usage/parallel-rounds.md) and [unattended rounds](usage/unattended-rounds.md).
