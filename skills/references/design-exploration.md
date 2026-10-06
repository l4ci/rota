# Design exploration — shared shape

Used by `/rota-vision` (project scope) and `/rota-brainstorm` (single item). Both negotiate *what to build and why* before a downstream skill captures *how*. Spine: draft before any disk write, one explicit approval (silence is not approval), write via the canonical verb (`rota design put` / `rota milestone add`), then hand off.

| Axis | `/rota-vision` | `/rota-brainstorm` |
|---|---|---|
| Scope | Project; the milestone list mirrors the tracker's milestones | One backlog item |
| Artifact | `MILESTONES.md` + `.rota/milestones/<MNN>.md` (issue backend: milestone tracking issue) | The item's design (issue note, or `.rota/designs/<ID>.md`) |
| Discovery | One batched `AskUserQuestion` (2-3 questions) | None unless real ambiguity or a decision conflict; Major / P0 get the grilling pass |
| Research | Opt-in web research | None; `/rota-spike` for code-touching questions |
| Challenge | Yes, a deliberate counter-position before committing (`references/grilling.md`) | A grilling pass first for Major / P0 / `--grill` (`references/grilling.md`); pros/cons carry the rest |
| Approval | One pass on the whole list | One draft, one approval |
| Handoff | `/rota-capture` / `/rota-plan` / `/rota-work` | `/rota-plan` (soft input, never required) |

Challenge belongs at project scope: the wrong call there has multi-quarter blast radius and no single milestone shows which come first, what runs in parallel or what gets cut. At item scope the item's detail already narrows the space.

Skills that import, generate or one-shot transform (`/rota-spike`, `/rota-capture`) are not in this family, and neither is `/rota-plan`: by then the design is settled and its job is task decomposition.
