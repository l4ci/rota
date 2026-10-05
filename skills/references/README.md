# References

Project-root `references/` holds extracted choreography that two or more skills share — UX shapes, decision tables, multi-step protocols. Skills cite these inline; this index gives a top-down view of what each reference contains and which skills consume it.

See KNOWLEDGE.md "Skill Authoring: Prose & References" for the conventions that govern when to extract (≥30 lines of self-contained choreography), how to size the reference (per cohesive scope, not per consumer), and when to keep prose inline.

## Index

| Reference | Purpose | Cited by |
|-----------|---------|----------|
| [`authoring-conventions.md`](authoring-conventions.md) | Authoring rules shared across SKILL.md files (inline autonomy directives, mirror-step threshold). | `/rota-capture`, `/rota-refactor`, `/rota-ship` |
| [`context-load-protocol.md`](context-load-protocol.md) | K+D context loading sequence shared by every cycle-starting skill. | `/rota-plan`, `/rota-vision`, `/rota-work` (including `--preview`) |
| [`design-exploration.md`](design-exploration.md) | Shared five-step spine for skills that negotiate what to build before downstream skills capture how. | `/rota-brainstorm`, `/rota-vision` |
| [`detail-files.md`](detail-files.md) | Detail-file template used when an item's input exceeds 3 sentences. | `/rota-capture` |
| [`docs-conventions.md`](docs-conventions.md) | Conventions for content under `docs/` (registration sites, audience split). | `/rota-ship` (Docs Mode) |
| [`grilling.md`](grilling.md) | Frontier-round questioning with a recommended answer per question, code before user, edge-case scenarios, glossary conflicts, explicit stop condition. | `/rota-brainstorm`, `/rota-decide`, `/rota-vision` |
| [`handoff-template.md`](handoff-template.md) | Handoff-note template written by `/rota-pause` and read by `/rota-work` (no argument). | `/rota-pause` |
| [`humanizing-prose.md`](humanizing-prose.md) | Rule sheet + silent self-audit pass applied to user-facing prose (release notes, PR body, doc-page edits) before the draft is shown to the user. | `/rota-release`, `/rota-ship` |
| [`isolation-patterns.md`](isolation-patterns.md) | Branch / worktree creation patterns per work.isolation + umbrella mode. | `/rota-work` |
| [`task-ledger.md`](task-ledger.md) | `Task:` commit trailer written per task and read on resume to skip finished tasks. | `/rota-pause`, `/rota-work` |
| [`work-preview.md`](work-preview.md) | `/rota-work --preview` procedure and peek template. | `/rota-work` |
| [`work-toolchain-siblings.md`](work-toolchain-siblings.md) | Tool-generated sibling patterns and the sweep commit. | `/rota-work` |
| [`work-wave-planning.md`](work-wave-planning.md) | File and shared-symbol collisions, brief rules, verifying a completion. | `/rota-work` |
| [`knowledge-consult.md`](knowledge-consult.md) | Canonical K+D query pattern (`rota knowledge query` + `rota decisions query`) used by every cycle-starting skill. | `/rota-debug`, `/rota-review`, `/rota-work` |
| [`learn-rare-modes.md`](learn-rare-modes.md) | `/rota-learn` manual flags (`--term`, `--promote`, `--deprecate`, `--amend`) and the contradiction queue. | `/rota-learn` |
| [`manual-gates.md`](manual-gates.md) | The manual-gate registry (`rota gate list`): gates the verbs enforce with `--confirm`, and the skill-only callouts. | `/rota-release`, `/rota-ship` |
| [`milestone-tagging.md`](milestone-tagging.md) | Milestone-tagging UX pattern used by capture/go skills. | `/rota-capture` |
| [`persistence-skills.md`](persistence-skills.md) | Shared spine and divergence axes for the persistence duo (`/rota-learn`, `/rota-decide`), plus umbrella scoping (hybrid KNOWLEDGE, umbrella-only DECISIONS). | `/rota-decide`, `/rota-learn` |
| [`post-cycle-trigger-gate.md`](post-cycle-trigger-gate.md) | Trigger condition + nudge-or-dispatch choreography for post-cycle skills. | `/rota-qa`, `/rota-ship`, `/rota-work` |
| [`refactor-design-approaches.md`](refactor-design-approaches.md) | Competing-design choreography (decisions consult, agent constraints, output shape) for `/rota-refactor --designs`. | `/rota-refactor` |
| [`review-verdict-routing.md`](review-verdict-routing.md) | Verdict semantics, `AskUserQuestion` shapes for `/rota-review` consumers. | `/rota-qa`, `/rota-review`, `/rota-ship` |
| [`silent-failure-hunter.md`](silent-failure-hunter.md) | Rubric for detecting work that reports complete but didn't move the system, used in review passes. | `/rota-review`, `/rota-ship` |
| [`source-prefill.md`](source-prefill.md) | Source-prefill / promote-between-artifacts semantics for `/rota-decide`. | `/rota-decide` |
| [`subagent-dispatch.md`](subagent-dispatch.md) | Cross-skill rulebook for when and how skills push work into subagents instead of the orchestrator thread. | `/rota-debug`, `/rota-qa`, `/rota-vision` |
| [`worker-contract.md`](worker-contract.md) | Standing worker contract and approval provenance for round workers. | `/rota-orchestrate` |
| [`herdr-dispatch.md`](herdr-dispatch.md) | What herdr changes versus tmux: tabs as slots, startup dialogs, worker-contract additions. | `/rota-orchestrate` |
| [`tmux-dispatch.md`](tmux-dispatch.md) | Judgment `rota worker` verbs do not enforce: permissions, relay provenance, merge-gate lore, failure modes (both hosts). | `/rota-orchestrate` |
| [`three-mode-skill-shape.md`](three-mode-skill-shape.md) | Three-mode skill shape (first-run / after-work / restructure) used by `/rota-ship` (Docs Mode) and `/rota-qa`. | `/rota-qa`, `/rota-ship` |
| [`umbrella-mode.md`](umbrella-mode.md) | Umbrella-mode helpers, registry shape, and `Repos:` field semantics. | `/rota-capture`, `/rota-qa`, `/rota-spike`, `/rota-work` |

## Conventions

- **Path style.** Citations from SKILL.md use the form `references/<file>.md` (relative to the installed skill's directory). `rota skills install` copies the references each skill cites into `<skill>/references/`, so links resolve wherever a skill is loaded.
- **Inline vs. extracted.** Inline prose wins when it's local to its step and under 30 lines. Extract to `references/<topic>.md` when the same choreography appears in 2+ skills OR when extraction shrinks a SKILL.md by ≥30 lines of self-contained content (per the "Single-consumer references" KNOWLEDGE entry).
- **One-line purpose.** Each row's `Purpose` column is one sentence; longer context lives inside the reference file. If the one-liner needs a clause about scope or a noteworthy exception, keep it under 25 words.
- **Cited by.** The `Cited by` column is the canonical consumer set — derived by `grep -l "references/<name>" rota-*/SKILL.md`. A reference with no consumers should not exist; if you find one while running step 2 above, flag it in your completion report.

## Maintenance

When adding a new reference file, append a row to the Index table in alphabetical order, fill in `Purpose` and `Cited by`, and add at least one inline citation in a SKILL.md (otherwise the reference shouldn't exist yet — write it from a consumer's perspective).

When the consumer set for a reference changes, re-run the grep above and update the `Cited by` column.
