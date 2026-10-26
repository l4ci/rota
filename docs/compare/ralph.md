# rota vs ralph

[snarktank/ralph](https://github.com/snarktank/ralph) is a bash loop that starts a fresh Amp or Claude Code session per iteration and repeats until every user story in `prd.json` has `passes: true`. It is based on Geoffrey Huntley's Ralph pattern. "Ralph loop" is also the name of that wider technique (run an agent in a loop, fresh context each pass, state kept in files and git), and many tools implement it. This page compares against snarktank/ralph only. Checked 2026-10-26 against the `main` head, commit 6c53cb0 (2026-02-02). The repo has no tagged releases.

## The short version

- Pick ralph when you have one feature with a clear list of small stories, want something you can read in ten minutes, and are happy to run it on your own branch.
- Pick rota when you have a standing backlog, want several agents in parallel, or want a review and test gate before anything reaches `main`.
- ralph is a loop. rota is a workflow: a solo cycle (`/rota-work`) and parallel rounds.

## Side by side

| | ralph | rota |
|---|---|---|
| What it is | `ralph.sh`, a prompt template, `prd.json`, two skills (`/prd`, `/ralph`) | A single Go binary plus `/rota-*` skills inside Claude Code or Codex |
| Unit of work | A user story in `prd.json`, sized for one context window | A backlog item or tracker issue |
| Parallel agents | One at a time; one story per iteration | An orchestrator plus two to five standing workers |
| Isolation | A feature branch named by `branchName`; no worktree documented | One git worktree per worker; a branch or worktree per `/rota-work` cycle |
| Verification before merge | The agent runs your typecheck, lint and tests before committing a story; the README says CI must stay green | Targeted checks per worker, then `test.full` on a scratch merge in `rota worker gate`, plus review |
| Who merges | No push, PR or merge step in the script or prompt | The orchestrator through the gate, or `/rota-ship` in solo mode |
| Backlog / tracker | `prd.json`, generated from a markdown PRD | `.rota/BACKLOG.md`, GitHub, GitLab, Jira, Linear or It's a Plan |
| Memory across sessions | `progress.txt`, git history and `AGENTS.md` or `CLAUDE.md` updates | `.rota/KNOWLEDGE.md`, `DECISIONS.md`, handoff notes |
| Agents and models | Amp (default) and Claude Code | Claude Code and Codex, with OpenCode as a worker |
| Platforms | Anything with bash, `jq` and git | Linux and macOS; Windows through WSL2 |
| Install | Copy files, or the Claude Code plugin marketplace | Homebrew, install script, release binary |
| License | MIT | MIT |

## Where ralph is stronger

- **Size and legibility.** The whole loop is a short script and one prompt file. You can read it, change it and know what runs. rota is a binary with many verbs and skills; its [first round](../first-round.md) takes setup.
- **Little setup.** An agent CLI, `jq` and git. No config questions, no terminal host.
- **Fresh context by construction.** Every iteration is a new process. rota workers start a fresh session per issue, but a solo `/rota-work` session carries context until you pause it.
- **Amp support.** ralph defaults to Amp. rota lists Claude Code, Codex and OpenCode.
- **Small stories as a rule.** The README insists each story fit one context window and shows how to split them.
- **Reach.** About 22k stars on 2026-10-26.

## Where rota is stronger

- **Parallel work.** `rota round` runs several workers in separate worktrees on separate issues ([parallel rounds](../usage/parallel-rounds.md)). ralph runs one story at a time.
- **A gate and a review.** ralph trusts the agent's own run of the checks. rota merges only after the gate passes on a scratch merged tree, with a read-only review before it ([review and ship](../usage/review-and-ship.md)).
- **Getting work onto `main`.** `/rota-ship` and the gate open PRs and merge them. ralph stops at commits on a branch.
- **Real backlogs.** Issues stay in your tracker, with dependencies and acceptance criteria ([issue backend](../usage/issue-backend.md)), instead of a JSON file you regenerate per feature.
- **Usage limits.** Several accounts, `rota limit watch` and `rota keepalive` ([unattended rounds](../usage/unattended-rounds.md)). ralph users have asked for auto-wait on limits (issue #82).
- **Curated knowledge.** `/rota-learn` and `/rota-decide` keep reviewed, queryable notes. `progress.txt` is an append-only log.

## Using both

The loop idea fits inside rota's model: a worker that takes small, well-specified issues one after another is close to a ralph run with a gate behind it. Running ralph's script inside a rota worktree is untested here. Its Claude Code mode passes `--dangerously-skip-permissions`, so check your safety settings first.

## Sources

- [snarktank/ralph README](https://github.com/snarktank/ralph): workflow, key files, prerequisites, install options, stop condition.
- [ralph.sh](https://github.com/snarktank/ralph/blob/main/ralph.sh) and [CLAUDE.md prompt](https://github.com/snarktank/ralph/blob/main/CLAUDE.md): tools supported, permission flags, commit step, absence of a push or merge step.
- [Geoffrey Huntley, Ralph](https://ghuntley.com/ralph/): the underlying technique.
- [rota market review, October 2026](../contributing/market-review-2026-10.md): ralph's open requests and star count.

More comparisons: [how rota compares](README.md).
