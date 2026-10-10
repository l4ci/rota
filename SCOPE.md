# Scope

rota is a small tool for capturing, building and shipping work with a coding agent, alone or in a parallel round. It is 0.x: the surface still moves, and focused issues are more useful than broad ones.

## What rota does

- Skills (`/rota-*`) and a `rota` binary that keep backlog, plans, knowledge and decisions in `.rota/`.
- Parallel rounds: an orchestrator and standing workers, each in its own git worktree, run from herdr or tmux.
- Claude Code and Codex as agent harnesses, mixed in one round.
- GitHub and GitLab as the forge, and the repo's own files or its issues as the backlog.

## What rota does not do

- Other harnesses (Cursor, Aider, Gemini CLI and the like).
- Trackers beyond GitHub and GitLab (Jira, Linear and the like).
- Hosted dashboards, cloud runners or any service that holds your work. It runs in your terminal and your repo.
- Replace your CI, your review process or your judgment about what to merge.

## How issues are judged

An issue is in scope when it names something that went wrong or got in the way, and fixing it fits the lists above. Bugs need a way to reproduce. Ideas should say which failure you hit and what the idea would change; a request with no observed failure behind it is likely to wait or be declined. Each issue gets a reason, whatever the answer.

Use the issue templates. For a pull request, run the checks in the README's Contributing section first.
