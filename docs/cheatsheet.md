# rota cheat sheet

What each `/rota-*` skill does, one line each. For details: [`skills/README.md`](skills/README.md).

<!-- generated:skills:cheatsheet begin -->
<!-- edit docs/skills/*.md and run python3 scripts/skills-docs.py -->

## Capture
- **[`/rota-capture`](skills/rota-capture.md)**: Record bugs, features and tasks in the backlog without starting work. Flags: `--remove <ID>`, `--triage <N>`.

## Plan
- **[`/rota-brainstorm`](skills/rota-brainstorm.md)**: Negotiate the design of one Major item before planning it. Flags: `--grill`.
- **[`/rota-spike`](skills/rota-spike.md)**: Try one risky idea on a throwaway branch and keep only the finding. Flags: `done <name>`.
- **[`/rota-plan`](skills/rota-plan.md)**: Write a signed-off implementation plan with verifiable tasks.
- **[`/rota-vision`](skills/rota-vision.md)**: Turn an idea into milestones with dependencies and ready/blocked status.

## Execute
- **[`/rota-work`](skills/rota-work.md)**: Build the next item in small commits, one verified task at a time. Flags: `--preview <ID>`.
- **[`/rota-debug`](skills/rota-debug.md)**: Find the root cause of one bug, fix it atomically and prove it.
- **[`/rota-refactor`](skills/rota-refactor.md)**: Review the architecture and file refactor issues; --map drafts the map. Flags: `--fix`, `--designs`, `--interactive`, `--map`.

## Ship
- **[`/rota-review`](skills/rota-review.md)**: Review a branch against its spec and conventions before it leaves. Flags: `--since <sha>`, `--queue`.
- **[`/rota-ship`](skills/rota-ship.md)**: Review, then open the PR or merge, linking items by ID. Flags: `--undo`, `--docs`.
- **[`/rota-docs`](skills/rota-docs.md)**: Scaffold, update or reorganize the public docs. Flags: `restructure`.
- **[`/rota-qa`](skills/rota-qa.md)**: Run product-level QA against the built artifact and score it. Flags: `first-run`, `run [<target>]`, `restructure`, `uat <ID>`.

## Persist
- **[`/rota-learn`](skills/rota-learn.md)**: Save the session's durable lessons to KNOWLEDGE.md. Flags: `--strict`, `--retro [<session-id|path>]`, `--term <name>`, `--promote | --deprecate | --amend <topic> "<title>"`.
- **[`/rota-decide`](skills/rota-decide.md)**: Record a hard-boundary decision with explicit forbids and permits. Flags: `--from-learning <topic>`, `--from-spike <name>`, `--supersede <topic> "<title>"`, `--retire <topic> "<title>"`.
- **[`/rota-pause`](skills/rota-pause.md)**: Stop cleanly and leave a handoff note for the next session. Flags: `--report`.

## Rounds
- **[`/rota-orchestrate`](skills/rota-orchestrate.md)**: Run a parallel round: pick the slate, watch workers, merge their PRs.
- **[`/rota-answer`](skills/rota-answer.md)**: Answer every decision a round is waiting on, one question at a time.

## Maintenance
- **[`/rota-init`](skills/rota-init.md)**: Suggest config values from the repo and write the ones you confirm.
- **[`/rota-bugreport`](skills/rota-bugreport.md)**: File rota's own mistakes upstream, after your explicit yes.
- **[`/rota-release`](skills/rota-release.md)**: Cut a release: checklist, version bump, notes, tag, publish. Flags: `major | minor | patch | <semver>`, `--dry-run`.
<!-- generated:skills:cheatsheet end -->

## Verbs, not skills
- **`rota`** (bare, in a terminal): opens a palette. Enter on the preselected entry runs `rota setup` in a directory without `.rota/` and launches the orchestrator in an initialized project. Outside a terminal it prints usage.
- **`--ui`**: open a verb's terminal screen instead of printing its result: `rota config --ui`, `rota projects --ui`, `rota round status --ui` (refreshes every 5 s), and read-only views for `rota doctor`, `rota worker account list`, `rota backlog list`, `rota knowledge stats` and `rota decisions stats`. Terminal only; refused with `--json`. Plain output never changes.
- **`rota orchestrate`**: run `rota doctor`, then start the orchestrator under `rota keepalive run`.
- **`rota init`** (`rota init umbrella`): scaffold `.rota/` and fill config defaults.
- **`rota projects`**: list every project `rota init` registered on this machine (`projects.json` in the rota state dir: `$XDG_STATE_HOME/rota`, default `~/.local/state/rota`). Paths that no longer exist are marked `(missing)`. `rota projects cleanup` deletes, at once, every entry whose directory is gone or no longer holds `.rota/`, and prints each. `rota projects remove <dir>` drops one entry (the directory stays). `rota projects --ui` opens a terminal screen over the registry (also the palette's Projects entry).
- **`rota setup`**: `rota init` plus a short config walkthrough on a terminal. `--yes` takes the defaults, `--set key=value` answers one question, `--list` prints them.
- **`rota config show` / `rota config set` / `rota config edit`**: read and change settings; `edit` opens the config screen (terminal only): keys by section with their description, `Enter` edits, `l` writes to `config.local.json`, `r` resets, `g` switches a key between inheriting the global value and overriding it here. `--global` on `show`/`set`/`edit` works on the global config itself. `rota config save-global` merges this project's config into the global config, which every project inherits.
- **`rota update`**: check for a newer release.
- **`rota migrate issues`**: move the backlog to GitHub or GitLab issues.
- **`rota migrate milestones`**: checkbox the `## Acceptance` bullets in milestone files so criteria number as AC-n. Previews by default; `--apply` writes.
- **`rota milestone check <MNN>`**: check a milestone's `## Capabilities` against its feature issues' `## Change` sections and list the `## Acceptance` criteria no feature serves. `--strict` fails on uncovered criteria.
- **`rota skills install` / `update` / `status`**: write the skills for Claude Code and Codex, refresh them after an upgrade, compare with the binary.
- **`rota round start` / `assign` / `wait` / `status` / `wind-down`**: run a round: take the lease, hand an issue to a slot, block until a worker needs you, list slots, park everything and release the lease.
- **`rota round summary`**: what a round cost: a row per issue, totals per slot and per account, and the gate audit lines. Wind-down prints it too.
- **`rota round pick`**: for a `best-of:2` issue, name the attempt whose PR may merge; the other is closed with the reason.
- **`rota worker`**: slot registry, worktrees, dispatch, polling and the merge gate (`rota worker gate`).
- **`rota worker gate <PR>`**: gate any open PR by number, even one no slot owns. It refuses an empty `test.full` (unless `--no-verify`), a body without `Closes #N` (unless `partial-slice`), a recorded FAIL verdict and an unpicked best-of attempt.
- **`rota agents write`**: write `.claude/agents/rota-*.md` (and `.codex/agents/rota-*.toml` or `.opencode/agents/rota-*.md` when Codex or OpenCode is configured) from the role config. `rota init` runs it.
- **`rota doctor`**: preflight for git, jq, host, forge, accounts, hooks, skills, Codex and OpenCode.
- **`rota reap`**: preview leftovers a round left behind; `--apply` removes those holding no work.
- **`rota layout split` / `tabs`**: fold a round's herdr panes into one split view for a wide screen, or back into tabs for a narrow one; bare `rota layout` shows which.
- **`rota keepalive run`**: restart the orchestrator in its pane when it exits with a fresh handoff.
- **`rota limit watch`**: sleep until a usage limit resets, or switch accounts.
- **`rota hook` / `rota statusline`**: Claude Code hooks and the statusline command that hand the orchestrator off before its context fills.
- **`rota verdict add` / `route` / `show`**: record typed review, second-opinion and QA verdicts and route on them.

See [parallel rounds](usage/parallel-rounds.md) and [unattended rounds](usage/unattended-rounds.md).
