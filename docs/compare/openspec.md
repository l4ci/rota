# rota vs OpenSpec

[OpenSpec](https://github.com/Fission-AI/OpenSpec) is a spec-driven workflow for AI coding assistants. A repo keeps living specs in `openspec/specs/`; each proposed change gets a folder in `openspec/changes/` with a proposal, delta specs, a design and a task list, and archiving a change merges its deltas into the specs. Checked 2026-11-13 against OpenSpec v2.0.0 (released 2026-11-11) and rota 0.34.2. Version 2.0 removed the `/opsx:*` command files; every step is now a skill, such as `/openspec-propose` (`$openspec-propose` in Codex).

## The short version

- Pick OpenSpec when the main problem is agreeing on what to build, and you want a reviewable, versioned record of system behavior that outlives each change.
- Pick rota when the main problem is getting many well-specified items built, verified and merged, possibly in parallel.
- They address different stages. OpenSpec stops at the spec and the task list; rota starts from an issue and ends at a merge.

## Side by side

| | OpenSpec | rota |
|---|---|---|
| What it is | Spec layer plus CLI, driven by `openspec-*` skills | Single Go binary plus `/rota-*` skills |
| Unit of work | A change folder with proposal, delta specs, design and tasks | A backlog item or tracker issue |
| Core loop | `openspec-propose`, `openspec-apply-change`, `openspec-archive-change`; the default profile adds `explore`, `update-change` and `sync-specs`, the expanded one `verify-change` and others | `/rota-capture`, `/rota-work`, `/rota-ship`; rounds for parallel work |
| Parallel agents | Several changes can be open at once as folders; running agents in parallel is not documented | Orchestrator plus two to five standing workers |
| Isolation | Spec-level: each change is a folder until archived. No worktree handling documented | One git worktree per worker |
| Verification before merge | `openspec-verify-change` (expanded profile, optional) checks completeness, correctness and coherence against the artifacts; it does not block archive | Gate runs `test.full` on a scratch merge before merging |
| Who merges | Not documented for code; archive merges spec deltas into `specs/` | The orchestrator, through the gate |
| Backlog / tracker | Task checklist per change; tracker integration not documented | `.rota/BACKLOG.md`, GitHub, GitLab, Jira, Linear and others |
| Memory across sessions | `specs/` plus an `archive/` of past changes | `KNOWLEDGE.md`, `DECISIONS.md`, handoff notes |
| Agents and models | About 40 tools listed, including Claude Code, Codex, Cursor, Gemini CLI, Copilot, Kiro, OpenCode; skills install to `.agents/skills/` | Claude Code and Codex; OpenCode workers |
| Interface | Skills plus an `openspec` CLI | Skills plus the `rota` CLI, terminal only |
| Install | Standalone installer (macOS, Linux, Windows), Homebrew, or npm (Node 22.12+); then `openspec init`. `openspec update` upgrades older projects | Homebrew or install script, then `rota skills install` and `rota setup` |
| License | MIT | MIT |

## Where OpenSpec is stronger

- **A durable spec of behavior.** Requirements with scenarios live in `openspec/specs/` by domain and survive every change. rota has no spec layer; its closest record is the project map and the issues themselves.
- **Delta specs for existing code.** A change states what is ADDED, MODIFIED, REMOVED or RENAMED against the current spec, which suits brownfield work. rota only borrows the vocabulary.
- **Tool reach.** It lists about 40 assistants and installs its skills for each. rota supports two harnesses plus OpenCode for workers.
- **Light entry.** `openspec init` and `/openspec-propose` are enough to start. It also installs without Node as a standalone binary. There is no terminal host, tracker or `gh` to set up.
- **Cross-repo planning.** The beta Stores and Worksets features let a team keep planning in a shared repo, with one change plan spanning several repos. rota's [umbrella mode](../usage/umbrella-mode.md) covers multi-repo work differently.

## Where rota is stronger

- **Execution at scale.** Rounds assign issues to workers in separate worktrees and open one PR each. OpenSpec documents no equivalent of workers, assignment or a merge queue. See [parallel rounds](../usage/parallel-rounds.md).
- **A hard gate.** `rota worker gate` runs the full suite on a scratch merge and merges only on a pass. `openspec-verify-change` is advisory. See [how it works](../how-it-works.md).
- **Tracker-native work.** Items are GitHub, GitLab, Jira or Linear issues, or `BACKLOG.md` entries. See [issue backend](../usage/issue-backend.md).
- **Usage-limit handling.** `round.accounts` and `rota limit watch` move work between accounts when one hits a limit.
- **Project knowledge beyond behavior.** `/rota-learn` and `/rota-decide` keep lessons and hard boundaries, not requirements. See [learning](../usage/learning.md) and [decisions](../usage/decisions.md).
- **Recorded proof.** `/rota-work` requires a red run and a green run through `rota proof record`. See [running work](../usage/running-work.md).

## Using both

They fit together at the handoff. Running the two side by side is untested. OpenSpec produces a change with a task list; rota consumes issues. One way to combine them: write the change in OpenSpec, file each task group as a rota issue, and let a round build them. Archive the OpenSpec change after the PRs merge so `specs/` stays current. The cost is two places that describe the same work.

rota took one idea from OpenSpec without taking the files. For milestones that split into three or more issues, each issue states a delta kind (ADDED, MODIFIED, REMOVED, RENAMED) on a named capability, and `rota milestone check` verifies that every milestone criterion is served. There are no spec files. See [vision and plans](../usage/vision-and-plans.md).

## Sources

- [OpenSpec README](https://github.com/Fission-AI/OpenSpec): description, install, profiles, folder layout, Stores (beta), MIT license.
- [Upgrade to 2.0](https://openspec.dev/docs/upgrade-to-2): `/opsx:*` to skill mapping, retired `delivery` setting and Amazon Q support.
- [OpenSpec concepts](https://github.com/Fission-AI/OpenSpec/blob/main/docs/concepts.md), [workflows](https://github.com/Fission-AI/OpenSpec/blob/main/docs/workflows.md), [commands](https://github.com/Fission-AI/OpenSpec/blob/main/docs/commands.md): specs, delta specs, `verify`, `archive`. On 2026-11-13 the commands page still showed the 1.x `/opsx:*` names; the upgrade guide is the newer source.
- [OpenSpec supported tools](https://github.com/Fission-AI/OpenSpec/blob/main/docs/supported-tools.md): tool list.
- [OpenSpec releases](https://github.com/Fission-AI/OpenSpec/releases): v2.0.0 and the 1.16 and 1.17 notes (Node 22.12, `openspec update`). Star count (74.8k) from the repo page on 2026-11-13.
- rota: [README](../../README.md), [how it works](../how-it-works.md), [vision and plans](../usage/vision-and-plans.md), [market review](../contributing/market-review-2026-10.md).

More comparisons: [how rota compares](README.md).
