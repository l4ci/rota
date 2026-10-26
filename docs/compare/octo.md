# rota vs Octo

Octo is Claude Octopus ([`nyldn/claude-octopus`](https://github.com/nyldn/claude-octopus)), a plugin that runs the same research, design or coding task past several AI models and flags where they disagree. Claude is the host; Codex, Copilot, Qwen, Ollama, Perplexity, OpenRouter, Grok, Kimi Code and others are optional extra seats. Its commands live under `/octo:*` and stay dormant until you call them. Checked 2026-10-26 against Claude Octopus v11.15.1 and rota 0.26.0.

## The short version

- Pick Octo when the risk is one model's blind spot: you want a debate, a council, adversarial review or a scored PRD from several providers before committing to a design.
- Pick rota when the risk is throughput and integration: many issues in flight, each in its own worktree, merged only after the full suite passes on the merged tree.
- They answer different questions. Octo asks "do several models agree?" and rota asks "is this PR current, signed off and green when merged?"

## Side by side

| | Octo | rota |
|---|---|---|
| What it is | Claude Code plugin (also installable in Codex) with 54 commands, 63 skills and 31 personas | A Go binary plus `/rota-*` skills for Claude Code and Codex |
| Unit of work | A task or phase: `/octo:embrace` runs Discover, Define, Develop, Deliver | A backlog item or tracker issue |
| Parallel agents | `/octo:parallel` splits a compound task into work packages, each a separate `claude -p` process | A round: an orchestrator plus 2 to 5 standing workers, one issue each |
| Isolation | README lists parallel workstreams in git worktrees (v8) and a `worktree.bgIsolation` flag; per-package detail not documented in the command page | One worktree per worker, reused across issues |
| Verification before merge | A 75% consensus gate across providers; two-stage review | `test.full` on a scratch merged tree, then `/rota-review` Spec and Standards reviewers |
| Who merges | The reaction engine notifies you when a PR is approved with CI green | The orchestrator, through the gate. Workers never merge |
| Backlog / tracker | Not documented | `.rota/BACKLOG.md`, GitHub, GitLab, Jira, Linear or It's a Plan |
| Memory across sessions | Session preferences in Claude's auto-memory; optional claude-mem, agentmemory or deja-vu | `KNOWLEDGE.md`, `DECISIONS.md` and handoff notes in `.rota/` |
| Agents and models | Claude host plus up to twelve external providers, with per-role model routing | Claude Code, Codex, OpenCode; several accounts balanced by usage |
| Interface | Slash commands, a `bin/octopus` CLI, `/octo:setup` | Slash commands, a terminal palette, workers in herdr or tmux tabs |
| Platforms | Linux and macOS; Windows via WSL | Linux and macOS; Windows through WSL2 |
| Install | `claude plugin install octo@nyldn-plugins` | `brew install l4ci/tap/rota` or `install.sh` |
| License | MIT | MIT |

## Where Octo is stronger

- Multiple models on one question. `/octo:debate`, `/octo:council` and the research modes put several providers on the same problem and report disagreement. rota has no cross-model consensus feature; a rota round runs different issues, not one issue through several models.
- Breadth of provider seats. Twelve external integrations against rota's three harnesses (Claude Code, Codex, OpenCode), including local Ollama and search-backed Perplexity.
- Pre-build analysis. PRD scoring (`/octo:prd`), personas and the Discover/Define phases cover research and requirements. rota's equivalents are `/rota-brainstorm`, `/rota-plan` and `/rota-spike`, and they are per item.
- Reacting after the PR opens. The reaction engine forwards CI logs and review comments back to the agent and escalates when it is stuck. In rota review feedback reaches the worker through the orchestrator's relay.
- Cost visibility. `/octo:costs` projects a run's cost and `/octo:usage` breaks down spend per provider and skill.

## Where rota is stronger

- Merge safety. [`rota worker gate`](../usage/parallel-rounds.md) runs the full test suite on a scratch merge of the PR and merges only on a pass. Octo's gate measures model agreement, not test results.
- Tracker-driven work. Issues from GitHub, GitLab, Jira or Linear are the spec ([issue backend](../usage/issue-backend.md)). Octo documents no tracker link.
- Standing workers and resumability. Slots, a lease, a ledger and handoff notes survive context limits ([unattended rounds](../usage/unattended-rounds.md), [pausing and resuming](../usage/pausing-and-resuming.md)).
- Usage limits. `round.accounts` and `rota limit watch` move an issue to another account on a limit.
- Fewer moving parts. Octo's illustrative figure for a full multi-provider `/octo:embrace` run is $1 to $6 or more in API tokens; rota calls no external providers.

## Using both

They compose. Run an Octo debate or council to settle a design, record the outcome with `/rota-decide` or in the issue, then let a round build it. Octo stays dormant unless you call `/octo:*`. Neither tool documents this pairing; it is a suggested use.

## Sources

- [claude-octopus README](https://github.com/nyldn/claude-octopus): v11.15.1, MIT, providers, install, platforms, consensus gate, reaction engine, memory, cost table.
- [`/octo:parallel` command](https://github.com/nyldn/claude-octopus/blob/main/commands/parallel.md): work packages as separate `claude -p` processes.
- [Command reference](https://github.com/nyldn/claude-octopus/blob/main/docs/COMMAND-REFERENCE.md): parallel and multi-provider commands.

More comparisons: [how rota compares](README.md).
