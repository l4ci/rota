# rota documentation

Public user guide for rota, a dev workflow for Claude Code and Codex, with OpenCode as a round worker: skills plus a single-binary CLI. Start with one agent: capture an item, build it, ship it. When you have several issues ready, an orchestrator agent hands them to worker agents and a gate checks every merge.

## Contents

### 🚀 Getting started

- [Install](install.md): Homebrew, the install script, release binaries, the Claude Code plugin, upgrading, uninstalling
- [Getting started](getting-started.md): install and run your first cycle
- [How it works](how-it-works.md): the two ways to use rota (orchestrator, workers, gate), plus how each skill connects to the artifacts it touches
- [Where coding agents go wrong](agent-mistakes.md): seven common mistakes, enforced checks, written rules and remaining gaps

### Walkthroughs

- [Greenfield: what a project holds after a brief](walkthroughs/greenfield-from-brief.md). The files `/rota-vision`, `/rota-plan`, `/rota-capture` and `/rota-learn` leave behind in a new project, with the plan, milestones and knowledge entries shown in full.
- [Brownfield: what rota adds to an existing project](walkthroughs/brownfield-existing-project.md). The project map, config choices, backlog, decision and knowledge entry that appear when rota joins a codebase with history.

### 🔄 Rounds

- [Your first round](first-round.md): herdr or tmux, launching the orchestrator, a first round and wind-down, step by step
- [A round cycle](walkthroughs/round-cycle.md): shape the slate, run a round, wind down, release
- [Parallel rounds](usage/parallel-rounds.md): an orchestrator, standing workers in worktrees, the merge gate, solo mode
- [Unattended rounds](usage/unattended-rounds.md): hooks, statusline, keepalive, usage limits, the orchestrator switch
- [Doctor and reap](usage/doctor-and-reap.md): check the machine before a round, clear leftovers after
- [Codex workers](usage/codex-workers.md): run Codex as a worker in a round
- [OpenCode workers](usage/opencode-workers.md): run OpenCode, and any model it can reach, as a worker in a round
- [Orchestrator harnesses](usage/orchestrator-harnesses.md): run the orchestrator in Claude Code, Codex, Hermes or opencode
- [Skills in Codex](usage/codex-skills.md): install and call the skills from Codex

### 🧰 Skills

<!-- generated:skills:nav begin -->
<!-- edit docs/skills/*.md and run python3 scripts/skills-docs.py -->

- [All skills](skills/README.md): when to use each skill, and how they chain
- [`/rota-capture`](skills/rota-capture.md): Record bugs, features and tasks in the backlog without starting work
- [`/rota-brainstorm`](skills/rota-brainstorm.md): Negotiate the design of one Major item before planning it
- [`/rota-spike`](skills/rota-spike.md): Try one risky idea on a throwaway branch and keep only the finding
- [`/rota-plan`](skills/rota-plan.md): Write a signed-off implementation plan with verifiable tasks
- [`/rota-work`](skills/rota-work.md): Build the next item in small commits, one verified task at a time
- [`/rota-debug`](skills/rota-debug.md): Find the root cause of one bug, fix it atomically and prove it
- [`/rota-review`](skills/rota-review.md): Review a branch against its spec and conventions before it leaves
- [`/rota-ship`](skills/rota-ship.md): Review, then open the PR or merge, linking items by ID
- [`/rota-docs`](skills/rota-docs.md): Scaffold, update or reorganize the public docs
- [`/rota-orchestrate`](skills/rota-orchestrate.md): Run a parallel round: pick the slate, watch workers, merge their PRs
- [`/rota-answer`](skills/rota-answer.md): Answer every decision a round is waiting on, one question at a time
- [`/rota-learn`](skills/rota-learn.md): Save the session's durable lessons to KNOWLEDGE.md
- [`/rota-decide`](skills/rota-decide.md): Record a hard-boundary decision with explicit forbids and permits
- [`/rota-init`](skills/rota-init.md): Suggest config values from the repo and write the ones you confirm
- [`/rota-bugreport`](skills/rota-bugreport.md): File rota's own mistakes upstream, after your explicit yes
- [`/rota-pause`](skills/rota-pause.md): Stop cleanly and leave a handoff note for the next session
- [`/rota-vision`](skills/rota-vision.md): Turn an idea into milestones with dependencies and ready/blocked status
- [`/rota-refactor`](skills/rota-refactor.md): Review the architecture and file refactor issues; --map drafts the map
- [`/rota-qa`](skills/rota-qa.md): Run product-level QA against the built artifact and score it
- [`/rota-release`](skills/rota-release.md): Cut a release: checklist, version bump, notes, tag, publish
<!-- generated:skills:nav end -->

### 📥 Capture and backlog

- [Capturing work](usage/capturing-work.md): `/rota-capture`, mixed input, related links, detail files
- [Picking work](usage/picking-work.md): `/rota-work` (no argument), `/rota-work --preview`
- [Removing work](usage/removing-work.md): `/rota-capture --remove`, dry-run preview, batch removal, safety semantics

### 🔨 Execution

- [Running work](usage/running-work.md): `/rota-work` parallel cycles, branch vs worktree isolation, capture then work
- [Debugging](usage/debugging.md): `/rota-debug` systematic cycle
- [Pausing and resuming](usage/pausing-and-resuming.md): `/rota-pause`, recovering after `/clear`
- [Parallel work](usage/parallel-work.md): worktree mode, concurrent `/rota-work` sessions

### 📦 Shipping

- [Review and ship](usage/review-and-ship.md): `/rota-review` single-pass review and `/rota-ship` gates (second-opinion, QA)
- [Product QA](usage/qa.md): `/rota-qa` per-target strategy files and the `ship.qa` gate
- [Rolling back a cycle](usage/undo.md): `/rota-ship --undo` guided rollback, dry-run preview, manual confirmation
- [Learning](usage/learning.md): `/rota-learn` and `KNOWLEDGE.md`, including `--term <name>` for the project Glossary
- [External knowledge](usage/external-knowledge.md): a shared git repo of platform and technology learnings, read and written by every project
- [Decisions](usage/decisions.md): `/rota-decide` and hard-boundary commitments in `DECISIONS.md`

### Vision and planning

- [Vision and plans](usage/vision-and-plans.md): `/rota-vision`, `/rota-plan`, milestones
- [Brainstorming a design](usage/brainstorm.md): per-item design exploration with `/rota-brainstorm`, before `/rota-plan`
- [Spikes](usage/spikes.md): throwaway feasibility experiments via `/rota-spike`

### ⚙ Configuration

- [Configuration](usage/configuration.md): every key in `.rota/config.json` and what it does
- [Autonomy levels](usage/autonomy.md): how `off`, `roundtrip` and `auto` change skill chaining
- [Issue backend](usage/issue-backend.md): backlog on GitHub/GitLab issues, setup, labels, milestones, `rota migrate issues`
- [Umbrella mode](usage/umbrella-mode.md): coordinator at umbrella, work in sub-repos (M02 V1)

### 📖 Reference

- [Architecture](reference/architecture.md): how the CLI, the skills and `.rota/` fit together
- [The `.rota/` folder](reference/rota-folder.md): files and directories created by `rota init`
- [`rota` verb reference](reference/cli-helpers.md): every `rota` verb, with conventions and exit codes
- [Configuration options](reference/config-options.md): every config key and option label, set via `rota config set`
- [Tracker labels](reference/labels.md): label definitions, project declarations and dry-run sync
- [Upstream issues reference](reference/rota-issues.md): GitHub/GitLab issue backend and round-trip closing
- [Project check](reference/preflight.md): what `rota init check` verifies, plus exit-code meanings

### 🤝 Contributing

- [Rounds on rota itself](contributing/rounds.md): the gate, repo rules and roster for contributors
- [Skill-rule audit, October 2026](contributing/skill-rule-audit-2026-10.md): the complete never/must inventory, enforcement gaps and observed breakage
- [Release signing](contributing/release-signing.md): generating or rotating the minisign key, the Actions secret, verifying by hand
- [Market review, October 2026](contributing/market-review-2026-10.md): what comparable orchestrators' users ask for, where rota stands, and the ranked gaps behind issues #574 to #587; refreshed 2026-10-27 with gstack (#2170 to #2174) and new entrants
- [Plain orchestrator comparison, October 2026](contributing/plain-orchestrator-comparison-2026-10.md): what a bare Claude Code session would do to run a team against what rounds do, and the six gaps behind issues #897 to #902. The [re-judged user version](usage/rota-vs-plain-session.md) is the current one

### ⚖ Compare

- [How rota compares](compare/README.md): one page per tool, with the version checked
- [rota vs GSD](compare/gsd.md): spec-driven phases from discuss to ship
- [rota vs Superpowers](compare/superpowers.md): a skills methodology for many harnesses
- [rota vs OpenSpec](compare/openspec.md): living specs and delta change folders
- [rota vs ralph](compare/ralph.md): a fresh-context loop over a `prd.json`
- [rota vs Conductor](compare/conductor.md): a Mac app for agents in workspaces
- [rota vs Orca](compare/orca.md): a desktop app for fleets of agents
- [rota vs Vibe Kanban](compare/vibe-kanban.md): a kanban board that runs agents
- [rota vs Claude Squad](compare/claude-squad.md): a TUI for agents in tmux sessions
- [rota vs Gas Town](compare/gastown.md): a Mayor, polecats and a merge queue
- [rota vs Octo](compare/octo.md): several AI providers on one question
- [rota or a bare session](usage/rota-vs-plain-session.md): a row-by-row comparison with a plain Claude Code session that runs its own subagents

### Other

- [FAQ](faq.md): common questions
