# References

Extracted choreography that two or more skills share: UX shapes, decision tables, multi-step protocols. Skills cite these inline. Each file's opening line names its consumers; `grep -l "references/<name>" rota-*/SKILL.md` lists them.

See KNOWLEDGE.md "Skill Authoring: Prose & References" for when to extract (≥30 lines of self-contained choreography) and how to size a reference (per cohesive scope, not per consumer).

## Index

| Reference | Purpose |
|-----------|---------|
| [`authoring-conventions.md`](authoring-conventions.md) | Authoring rules shared across SKILL.md files. |
| [`context-load-protocol.md`](context-load-protocol.md) | Context reads before a cycle-starting skill proposes anything. |
| [`dependent-items.md`](dependent-items.md) | Dependency edges, `--depends-on`, creation order, expand → migrate → contract. |
| [`design-exploration.md`](design-exploration.md) | Draft, approve, write, hand-off shape for skills that negotiate what to build. |
| [`detail-files.md`](detail-files.md) | Detail-file template for items whose input exceeds 3 sentences. |
| [`docs-conventions.md`](docs-conventions.md) | Conventions for content under `docs/`. |
| [`grilling.md`](grilling.md) | Frontier-round questioning with a recommended answer per question. |
| [`handoff-template.md`](handoff-template.md) | Handoff note written by `/rota-pause`, read by `/rota-work`. |
| [`herdr-dispatch.md`](herdr-dispatch.md) | What herdr changes versus tmux. |
| [`humanizing-prose.md`](humanizing-prose.md) | Rule sheet and silent self-audit for user-facing prose. |
| [`isolation-patterns.md`](isolation-patterns.md) | Branch and worktree creation per `work.isolation` and umbrella mode. |
| [`issue-mode.md`](issue-mode.md) | The issue backend: IDs, verb map, state labels, PR flow, milestones. |
| [`knowledge-consult.md`](knowledge-consult.md) | The `rota knowledge query` + `rota decisions query` pattern. |
| [`learn-rare-modes.md`](learn-rare-modes.md) | `/rota-learn` manual flags and the contradiction queue. |
| [`manual-gates.md`](manual-gates.md) | The manual-gate registry: verb-enforced gates and skill-only callouts. |
| [`persistence-skills.md`](persistence-skills.md) | Shared spine, gate strengths and umbrella scoping for `/rota-learn` and `/rota-decide`. |
| [`planning-dial.md`](planning-dial.md) | Rubric for how much planning an item earns: pointers, spec, research round, adversarial pass. |
| [`post-cycle-trigger-gate.md`](post-cycle-trigger-gate.md) | Trigger condition and nudge-or-dispatch sequence for post-cycle steps. |
| [`refactor-design-approaches.md`](refactor-design-approaches.md) | Competing-design choreography for `/rota-refactor --designs`. |
| [`review-verdict-routing.md`](review-verdict-routing.md) | Verdict semantics and routing for review consumers. |
| [`silent-failure-hunter.md`](silent-failure-hunter.md) | Rubric for work that reports complete but didn't move the system. |
| [`source-prefill.md`](source-prefill.md) | `/rota-decide --from-learning` and `--from-spike` prefill. |
| [`subagent-dispatch.md`](subagent-dispatch.md) | When and how skills push work into subagents; the tier table. |
| [`task-ledger.md`](task-ledger.md) | `Task:` commit trailer, read on resume to skip finished tasks. |
| [`three-mode-skill-shape.md`](three-mode-skill-shape.md) | First-run / after-work / restructure shape for `/rota-ship --docs` and `/rota-qa`. |
| [`tmux-dispatch.md`](tmux-dispatch.md) | Judgment the `rota worker` verbs do not enforce; shared by both hosts. |
| [`umbrella-mode.md`](umbrella-mode.md) | Umbrella registry, `Repos:` field and resolution verbs. |
| [`work-preview.md`](work-preview.md) | `/rota-work --preview` procedure and peek template. |
| [`work-wave-planning.md`](work-wave-planning.md) | Wave collisions, brief rules, verifying a completion. |
| [`worker-contract.md`](worker-contract.md) | Standing worker contract and approval provenance. |

## Conventions

- **Path style.** SKILL.md cites `references/<file>.md`, relative to the installed skill's directory. `rota skills install` copies each cited reference into `<skill>/references/`.
- **Inline vs. extracted.** Inline prose wins when it is local to its step and under 30 lines. Extract when the same choreography appears in 2+ skills or extraction shrinks a SKILL.md by ≥30 lines.
- **New reference.** Add a row in alphabetical order and at least one inline citation in a SKILL.md; without a consumer the reference should not exist yet.
