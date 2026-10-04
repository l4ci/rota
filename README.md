<div align="center">

<img src="docs/rota_logo.png" alt="rota logo" width="80" />

# rota

**Autonomous rounds for coding agents: an orchestrator hands issues to parallel workers, merges what passes the gate, and keeps going. Persistent knowledge, decisions and handoffs make that reliable.**

[![Release](https://img.shields.io/github/v/release/l4ci/rota?color=blue&sort=semver)](https://github.com/l4ci/rota/releases)
[![License](https://img.shields.io/github/license/l4ci/rota?color=green)](LICENSE)
[![Last commit](https://img.shields.io/github/last-commit/l4ci/rota)](https://github.com/l4ci/rota/commits)
[![Stars](https://img.shields.io/github/stars/l4ci/rota?style=social)](https://github.com/l4ci/rota/stargazers)
[![For Claude Code](https://img.shields.io/badge/for-Claude%20Code-8A2BE2)](https://claude.com/claude-code)

[Autonomous rounds](#autonomous-rounds) · [Install](#install) · [Skills](#skills) · [Docs](docs/)

</div>

---

## Autonomous rounds

The main reason to adopt rota. One always-on orchestrator drives the `rota` CLI. It starts a round, assigns each issue to a worker agent (Claude Code or Codex) in its own git worktree and herdr or tmux tab, waits on the workers without polling, runs the gate and merges. When it needs you, it notifies you and comments on the issue or PR, then works on other items until you answer. Without herdr or tmux it runs the workers as subagents. [Your first round](docs/first-round.md) goes from install to a finished round in herdr; [parallel rounds](docs/usage/parallel-rounds.md) has the details.

Rounds hold up over hours because state persists. `KNOWLEDGE.md` and `DECISIONS.md` carry what earlier work learned and committed to. Handoff notes carry a half-finished task across a `/clear` or a restart. Issues say what work exists; `.rota/` says who is doing it now.

## Install

From 0.9.0, `rota` is one binary and the skills ship inside it:

```bash
curl -fsSL https://raw.githubusercontent.com/l4ci/rota/main/install.sh | sh   # or: brew install l4ci/tap/rota
rota skills install     # skills for Claude Code and Codex
rota init               # once, at the project root
```

The script verifies the binary's minisign signature and `checksums.txt` and refuses a mismatch; it needs `minisign` installed. [Install](docs/install.md) has the options, upgrading, removal and migrating an older install; [getting started](docs/getting-started.md) has the first cycle.

## Skills

| | |
|---|---|
| Plan | `/rota-vision`, `/rota-brainstorm`, `/rota-plan`, `/rota-spike` |
| Build | `/rota-work`, `/rota-debug`, `/rota-refactor` |
| Check | `/rota-review`, `/rota-qa` |
| Rounds | `/rota-orchestrate` |
| Persist | `/rota-learn`, `/rota-decide`, `/rota-pause` |
| Intake and release | `/rota-capture`, `/rota-ship`, `/rota-release` |

Skills hold judgment; `rota` verbs enforce the rules. Settings live in `.rota/config.json` ([options](docs/usage/configuration.md)).

## Contributing

Issues and PRs welcome. Run `python3 test/validate-skills.py` and `bash test/smoke.sh` before a PR; add a smoke assertion when you touch a verb. Running a round on rota itself: [contributing: rounds](docs/contributing/rounds.md).

## License

[MIT](LICENSE)
