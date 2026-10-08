# rota documentation

Public user guide for rota, a dev workflow for Claude Code and Codex: skills plus a single-binary CLI. Start with one agent: capture an item, build it, ship it. When you have several issues ready, an orchestrator agent hands them to worker agents and a gate checks every merge.

## Contents

### 🚀 Getting started

- [Cheat sheet](cheatsheet.md): one-line summary of every `/rota-*` skill (rapid scan)
- [Install](install.md): Homebrew, the install script, release binaries, upgrading, uninstalling
- [Getting started](getting-started.md): install and run your first cycle
- [How it works](how-it-works.md): the two ways to use rota (orchestrator, workers, gate), plus how each skill connects to the artifacts it touches

### Walkthroughs

- [Greenfield: from a brief to a shipped milestone](walkthroughs/greenfield-from-brief.md). Empty repo plus a one-page brief, taken end-to-end through `/rota-vision`, `/rota-plan`, `/rota-work`, `/rota-debug`, `/rota-ship`, `/rota-learn`.
- [Brownfield: dropping rota into an existing project](walkthroughs/brownfield-existing-project.md). Established codebase with open issues and a mental bug list, walked through `rota init`, `/rota-capture`, then a P0 cycle plus a debug cycle.

### 🔄 Rounds

- [Your first round](first-round.md): install, skills, herdr or tmux, launching the orchestrator, a first round and wind-down, step by step
- [Parallel rounds](usage/parallel-rounds.md): an orchestrator, standing workers in worktrees, the merge gate, solo mode
- [Unattended rounds](usage/unattended-rounds.md): hooks, statusline, keepalive, usage limits, the orchestrator switch
- [Doctor and reap](usage/doctor-and-reap.md): check the machine before a round, clear leftovers after
- [Codex workers](usage/codex-workers.md): run Codex as a worker in a round
- [Orchestrator harnesses](usage/orchestrator-harnesses.md): run the orchestrator in Claude Code, Codex, Hermes or opencode
- [Skills in Codex](usage/codex-skills.md): install and call the skills from Codex

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
- [Decisions](usage/decisions.md): `/rota-decide` and hard-boundary commitments in `DECISIONS.md`

### Vision and planning

- [Vision and plans](usage/vision-and-plans.md): `/rota-vision`, `/rota-plan`, milestones
- [Brainstorming a design](usage/brainstorm.md): per-item design exploration with `/rota-brainstorm`, before `/rota-plan`
- [Spikes](usage/spikes.md): throwaway feasibility experiments via `/rota-spike`

### ⚙ Configuration

- [Configuration](usage/configuration.md): every key in `.rota/config.json` and what it does
- [Autonomy levels](usage/autonomy.md): how `off` / `auto` change skill chaining
- [Issue backend](usage/issue-backend.md): backlog on GitHub/GitLab issues, setup, labels, milestones, `rota migrate issues`
- [Umbrella mode](usage/umbrella-mode.md): coordinator at umbrella, work in sub-repos (M02 V1)

### 📖 Reference

- [Architecture](reference/architecture.md): how the CLI, the skills and `.rota/` fit together
- [Slash commands](reference/slash-commands.md): every `/rota-*` command, alphabetical
- [The `.rota/` folder](reference/rota-folder.md): files and directories created by `rota init`
- [`rota` verb reference](reference/cli-helpers.md): every `rota` verb, with conventions and exit codes
- [Configuration options](reference/config-options.md): every config key and option label, set via `rota config set`
- [Upstream issues reference](reference/rota-issues.md): GitHub/GitLab issue backend and round-trip closing
- [Project check](reference/preflight.md): what `rota init check` verifies, plus exit-code meanings

### 🤝 Contributing

- [Rounds on rota itself](contributing/rounds.md): the gate, repo rules and roster for contributors
- [Release signing](contributing/release-signing.md): generating or rotating the minisign key, the Actions secret, verifying by hand
- [Market review, October 2026](contributing/market-review-2026-10.md): what comparable orchestrators' users ask for, where rota stands, and the ranked gaps behind issues #574 to #587

### Other

- [FAQ](faq.md): common questions
