# Slash commands

Quick-reference table of every `/rota-*` command.

Setup, config, update and migration are `rota` verbs, not skills: `rota init` (and `rota init umbrella`), `rota config show` / `rota config set`, `rota update`, `rota migrate hv` / `rota migrate issues`. See [config options](config-options.md).

<!-- generated:skills:table begin -->
<!-- edit docs/skills/*.md and run python3 scripts/skills-docs.py -->

| Skill | Lane | What it does |
|-------|------|--------------|
| [`/rota-capture`](../skills/rota-capture.md) | Capture | Record bugs, features and tasks in the backlog without starting work. |
| [`/rota-brainstorm`](../skills/rota-brainstorm.md) | Plan | Negotiate the design of one Major item before planning it. |
| [`/rota-spike`](../skills/rota-spike.md) | Plan | Try one risky idea on a throwaway branch and keep only the finding. |
| [`/rota-plan`](../skills/rota-plan.md) | Plan | Write a signed-off implementation plan with verifiable tasks. |
| [`/rota-work`](../skills/rota-work.md) | Execute | Build the next item in small commits, one verified task at a time. |
| [`/rota-debug`](../skills/rota-debug.md) | Execute | Find the root cause of one bug, fix it atomically and prove it. |
| [`/rota-review`](../skills/rota-review.md) | Ship | Review a branch against its spec and conventions before it leaves. |
| [`/rota-ship`](../skills/rota-ship.md) | Ship | Review, then open the PR or merge, linking items by ID. |
| [`/rota-docs`](../skills/rota-docs.md) | Ship | Scaffold, update or reorganize the public docs. |
| [`/rota-orchestrate`](../skills/rota-orchestrate.md) | Rounds | Run a parallel round: pick the slate, watch workers, merge their PRs. |
| [`/rota-answer`](../skills/rota-answer.md) | Rounds | Answer every decision a round is waiting on, one question at a time. (manual only) |
| [`/rota-learn`](../skills/rota-learn.md) | Persist | Save the session's durable lessons to KNOWLEDGE.md. |
| [`/rota-decide`](../skills/rota-decide.md) | Persist | Record a hard-boundary decision with explicit forbids and permits. (manual only) |
| [`/rota-init`](../skills/rota-init.md) | Maintenance | Suggest config values from the repo and write the ones you confirm. (manual only) |
| [`/rota-bugreport`](../skills/rota-bugreport.md) | Maintenance | File rota's own mistakes upstream, after your explicit yes. |
| [`/rota-pause`](../skills/rota-pause.md) | Persist | Stop cleanly and leave a handoff note for the next session. |
| [`/rota-vision`](../skills/rota-vision.md) | Plan | Turn an idea into milestones with dependencies and ready/blocked status. |
| [`/rota-refactor`](../skills/rota-refactor.md) | Execute | Review the architecture and file refactor issues; --map drafts the map. |
| [`/rota-qa`](../skills/rota-qa.md) | Ship | Run product-level QA against the built artifact and score it. |
| [`/rota-release`](../skills/rota-release.md) | Maintenance | Cut a release: checklist, version bump, notes, tag, publish. (manual only) |
<!-- generated:skills:table end -->

Each skill has its own page with examples, flags and the detail that used to live here: [skills](../skills/README.md).
