# Milestones

rota is a Claude Code workflow that plans before coding, makes one commit per task, and keeps a project knowledge layer that survives `/clear`. Vision-track work centers on a single wedge — *"Claude that doesn't forget"* — and reducing the surface area around it until that wedge is the obvious reason to adopt. v3.x established the engine; v4.x simplifies the surface around it.

## Active milestones

_(none active — set with `/rota-vision`)_

## Milestones


### M01 — v4.0: The Loop, simplified

**Status:** shipped · **Depends:** —

Cut 8 redundant commands (28 → 20), ship migration codemod, write announcement. The cut is the marketing.

[Full plan: `.hv/milestones/M01.md`]

### M06 — v4.1: umbrella-aware persistence

**Status:** shipped · **Depends:** M01

Close the umbrella gap left open in v4.0: per-sub-repo .hv/knowledge/<repo>/KNOWLEDGE.md storage with hybrid umbrella+sub-repo routing, so umbrella users running /hv-migrate v4 no longer hit F19's explicit refusal. DECISIONS.md stays umbrella-only by architectural commitment.

[Full plan: `.hv/milestones/M06.md`]

### M07 — Issue-tracker backend

**Status:** shipped · **Depends:** —

backlog.backend: issues makes GitHub/GitLab issues + native milestones the source of truth; file mode unchanged

[Full plan: `.hv/milestones/M07.md`]
