<div align="center">

<img src="docs/images/rota-icon.svg" alt="rota icon" width="64">
<br><br>
<img src="docs/images/rota-logo.svg" alt="rota" width="309">

# rota

**Coding agents write fast. rota makes sure what they merge works.**

Capture work as issues. rota hands them to Claude Code, Codex or OpenCode, reviews every branch and merges only what passes your tests, one issue at a time or a round in parallel. Lessons and decisions stay in the repo, so the next session starts ahead.

[![Release](https://img.shields.io/github/v/release/l4ci/rota?color=blue&sort=semver)](https://github.com/l4ci/rota/releases)
[![License](https://img.shields.io/github/license/l4ci/rota?color=green)](LICENSE)
[![Last commit](https://img.shields.io/github/last-commit/l4ci/rota)](https://github.com/l4ci/rota/commits)
[![Stars](https://img.shields.io/github/stars/l4ci/rota?style=social)](https://github.com/l4ci/rota/stargazers)
[![skills.sh](https://img.shields.io/badge/skills.sh-l4ci%2Frota-black)](https://skills.sh/l4ci/rota)
[![For Claude Code](https://img.shields.io/badge/for-Claude%20Code-8A2BE2)](https://claude.com/claude-code)
[![For Codex](https://img.shields.io/badge/for-Codex-000000)](https://developers.openai.com/codex)
[![OpenCode workers](https://img.shields.io/badge/workers-OpenCode-555555)](docs/usage/opencode-workers.md)

[Website](https://volkerotto.net/rota/) · [Features](#-features) · [Quick start](#-quick-start) · [How it works](#-how-it-works) · [Skills](#-skills) · [Docs](docs/)

</div>

---

## ⚡ Features

- [**Issues where you already keep them**](https://volkerotto.net/rota/docs/usage/issue-backend/): BACKLOG.md, GitHub, GitLab, Jira, Linear
- [**Design before code**](https://volkerotto.net/rota/docs/usage/brainstorm/): brainstorm, plan, spike
- [**Small, verified commits**](https://volkerotto.net/rota/docs/usage/running-work/)
- [**Every branch reviewed against its spec**](https://volkerotto.net/rota/docs/usage/review-and-ship/)
- [**A merge gate**](https://volkerotto.net/rota/docs/usage/parallel-rounds/#merge-approval): only green merges land
- [**Parallel rounds**](https://volkerotto.net/rota/docs/usage/parallel-rounds/): an orchestrator plus workers, each in its own worktree
- **Mix agents**: Claude Code, [Codex](https://volkerotto.net/rota/docs/usage/codex-workers/), [OpenCode](https://volkerotto.net/rota/docs/usage/opencode-workers/)
- [**In your terminal**](https://volkerotto.net/rota/docs/usage/parallel-rounds/#layout): herdr or tmux, or subagents
- [**Several accounts, balanced**](https://volkerotto.net/rota/docs/usage/unattended-rounds/#-usage-limits)
- [**Autonomy you choose**](https://volkerotto.net/rota/docs/usage/autonomy/): off, roundtrip, auto
- **Memory that stays in the repo**: [knowledge](https://volkerotto.net/rota/docs/usage/learning/), [decisions](https://volkerotto.net/rota/docs/usage/decisions/), [handoff notes](https://volkerotto.net/rota/docs/usage/pausing-and-resuming/)
- [**Knowledge shared across projects**](https://volkerotto.net/rota/docs/usage/external-knowledge/): lessons that hold everywhere live in their own git repo
- [**Root-cause debugging**](https://volkerotto.net/rota/docs/usage/debugging/)
- [**Product QA**](https://volkerotto.net/rota/docs/usage/qa/)
- [**Milestones and vision**](https://volkerotto.net/rota/docs/usage/vision-and-plans/)
- [**Undo the last cycle**](https://volkerotto.net/rota/docs/usage/undo/)
- [**Multi-repo umbrella**](https://volkerotto.net/rota/docs/usage/umbrella-mode/)
- [**A second support machine**](https://volkerotto.net/rota/docs/usage/support-machine/)

---

## 🚀 Quick start

One agent, one item at a time. You need git and [Claude Code](https://claude.com/claude-code) or Codex. No herdr, tmux, `gh` or second account. The steps below are written for Claude Code. In Codex, type `$rota-capture` where it says `/rota-capture`; see [skills in Codex](docs/usage/codex-skills.md).

```bash
brew install l4ci/tap/rota   # or: curl -fsSL https://raw.githubusercontent.com/l4ci/rota/main/install.sh | sh
rota skills install          # adds the /rota-* skills to Claude Code and Codex
cd your-project && rota setup
```

`rota setup` asks the main config questions (backlog, isolation, merge strategy, autonomy, review and QA gates) and creates `.rota/`. Commit it. For scripts and CI, `rota init` does the same without asking and writes the defaults. Then, in Claude Code:

```text
/rota-capture fix the login redirect loop; add a dark mode toggle
/rota-work      # picks the next item, plans it, builds it in small commits
/rota-ship      # reviews the branch, then opens a PR or merges
```

[Install](docs/install.md) lists every way to get the binary (the script checks the sha256 and, if `minisign` is installed, the signature). [Getting started](docs/getting-started.md) walks through setup and the loop.

How each install route updates:

| Route | Update with |
|---|---|
| [Claude Code plugin](docs/install.md#-plugin-marketplace) | `claude plugin update rota@rota` |
| `rota skills install` | `rota skills update` |
| `npx skills add l4ci/rota` | run it again |
| Homebrew or install script | `rota update` prints the command |

<p align="center"><img src="docs/images/rota-round.gif" alt="Animated demo: running rota opens its palette; picking Orchestrate starts a round in herdr's split view, where the orchestrator assigns three issues to Claude and Codex workers, escalates a worker's product question, gates and merges a finished PR, and places the next issue on the account with the most headroom" width="100%"></p>
<p align="center"><sub>A round in herdr's split view (illustration with a sample project; the palette is rota's real first screen).</sub></p>

## 🔄 How it works

rota has two parts: **skills**, slash commands like `/rota-work` that tell the agent how to do a job well, and the **`rota` CLI**, which the skills call to do the bookkeeping and enforce the rules (who holds which issue, what may merge).

There are two ways to use it. The [quick start](#-quick-start) above is the first; move to the second when several issues are ready at once.

**1. One agent, one item at a time.** You stay in the conversation.

```text
/rota-capture  →  /rota-work  →  /rota-ship
 write it down     build it      review, then PR or merge
```

**2. A round: several agents in parallel.** You stay available for questions.

```text
                        ┌─► worker ben  ─► PR ─┐
issues ─► orchestrator ─┼─► worker dana ─► PR ─┼─► gate ─► main
                        └─► worker nia  ─► PR ─┘
```

- **Orchestrator**: one agent that picks the next issues, hands them out, answers workers' questions, and merges. It asks you only for product decisions.
- **Workers**: agents that each take one issue in their own git worktree and terminal tab, build it, and open a PR. They never merge.
- **Gate**: the only merge path. It checks the PR is current and properly signed off, runs your full test suite on a scratch merge of the PR and the base, and merges only on a pass. A red result sends the PR back to its worker. With `test.fullWhere ci` the suite runs in CI instead.

Both ways share a memory in `.rota/`: what the project has learned (`KNOWLEDGE.md`), the lines it must not cross (`DECISIONS.md`), and handoff notes, so a fresh session picks up where the last one stopped.

## 🧰 Skills

In the order you'd usually reach for them:

<!-- generated:skills:readme begin -->
<!-- edit docs/skills/*.md and run python3 scripts/skills-docs.py -->

| When | Skill | What it does |
|---|---|---|
| Something to do | `/rota-capture` | Record bugs, features and tasks in the backlog without starting work |
| Not sure how | `/rota-brainstorm`, `/rota-spike` | Negotiate the design of one Major item before planning it; Try one risky idea on a throwaway branch and keep only the finding |
| Ready to build | `/rota-plan`, `/rota-work` | Write a signed-off implementation plan with verifiable tasks; Build the next item in small commits, one verified task at a time |
| Quick solo fix | `/rota-go` | Implement an issue and land it with only gofmt, vet and build; no slow tests, no review |
| Something broke | `/rota-debug` | Find the root cause of one bug, fix it atomically and prove it |
| Done | `/rota-review`, `/rota-ship` | Review a branch against its spec and conventions before it leaves; Review, then open the PR or merge, linking items by ID |
| Docs drifted | `/rota-docs` | Scaffold, update or reorganize the public docs |
| Many issues at once | `/rota-orchestrate`, `/rota-answer` | Run a parallel round: pick the slate, watch workers, merge their PRs; Answer every decision a round is waiting on, one question at a time |
| Worth remembering | `/rota-learn`, `/rota-decide` | Save the session's durable lessons to KNOWLEDGE.md; Record a hard-boundary decision with explicit forbids and permits |
| Fresh project | `/rota-init` | Suggest config values from the repo and write the ones you confirm |
| rota itself misbehaved | `/rota-bugreport` | File rota's own mistakes upstream, after your explicit yes |
| Out of context | `/rota-pause` | Stop cleanly and leave a handoff note for the next session |
| Bigger picture | `/rota-vision`, `/rota-refactor`, `/rota-qa`, `/rota-release` | Turn an idea into milestones with dependencies and ready/blocked status; Review the architecture and file refactor issues; --map drafts the map; Run product-level QA against the built artifact and score it; Cut a release: checklist, version bump, notes, tag, publish |
<!-- generated:skills:readme end -->

Every skill: [skills](docs/skills/README.md). Settings: [configuration](docs/usage/configuration.md).

## ⚖ How rota compares

See how rota compares to [GSD](docs/compare/gsd.md), [Superpowers](docs/compare/superpowers.md), [OpenSpec](docs/compare/openspec.md), [ralph](docs/compare/ralph.md), [Conductor](docs/compare/conductor.md), [Orca](docs/compare/orca.md), [Vibe Kanban](docs/compare/vibe-kanban.md), [Claude Squad](docs/compare/claude-squad.md), [Gas Town](docs/compare/gastown.md), [Octo](docs/compare/octo.md) and [a bare Claude Code session](docs/usage/rota-vs-plain-session.md).

## 📚 Docs

- [Getting started](docs/getting-started.md): install and your first item, end to end
- [Your first round](docs/first-round.md): orchestrator, workers and the gate, step by step
- [How it works](docs/how-it-works.md): the roles, the map of skills and files, and how skills chain
- [Where coding agents go wrong](docs/agent-mistakes.md): enforced checks, written rules and remaining gaps
- [How rota compares](docs/compare/README.md): one page per comparable tool
- [FAQ](docs/faq.md) · [All docs](docs/)

## 🤝 Contributing

Issues and PRs welcome; [SCOPE.md](SCOPE.md) says what rota does and how issues are judged. Run `python3 test/validate-skills.py`, `bash test/doclint.sh` and `bash test/smoke.sh` before a PR; add a smoke assertion when you touch a verb. Running a round on rota itself: [contributing: rounds](docs/contributing/rounds.md).

## License

[MIT](LICENSE)
