# `/rota-refactor` competing design approaches

Loaded by `/rota-refactor` Step 5 when a friction point is classified **structural** in Step 3. Simple friction points skip Step 5 and go straight to Step 6 — that decision stays in the SKILL.md.

## Consult decisions before designing

**Consult decisions before designing.** Pull relevant boundary entries:

```bash
rota decisions query <topics…>
```

Any approach that violates a decision is disqualified before the design phase. If every generated approach would violate, **stop and surface to the user** — refactors must not silently work around committed boundaries. Refactors are exactly when boundaries matter most.

## Agent dispatch

For each **structural** friction point, spawn 3+ sub-agents in parallel using the configured **orchestrator** model. Each agent gets the same technical brief (file paths, coupling details, dependency category, what's being hidden) but a different design constraint:

- **Agent 1**: "Minimize the interface — aim for 1-3 entry points max"
- **Agent 2**: "Maximize flexibility — support many use cases and extension"
- **Agent 3**: "Optimize for the most common caller — make the default case trivial"
- **Agent 4** (if a remote dependency is involved): "Design around the ports & adapters pattern"

## Per-design output

Each sub-agent outputs:

1. Interface signature (types, methods, params)
2. Usage example showing how callers use it
3. What complexity it hides internally
4. Dependency strategy (how deps are handled per the category)
5. Trade-offs

## Presentation

Present designs sequentially, then compare them in prose. Give an opinionated recommendation: which design is strongest and why. If elements from different designs combine well, propose a hybrid.

## confirmBeforeExecute gate

If `confirmBeforeExecute` is `true`, gate with `AskUserQuestion` per structural friction point (batch up to 4 in one call):

- **Header:** short name of the friction point (≤12 chars, e.g., `"Ring buffer"`)
- **Question:** *"Which design should I use for `<friction point>`?"*
- **Options** (single-select, up to 4): one per competing design. Mark your recommended design `(Recommended)`. Label each with the design's constraint (e.g., `"Minimal interface (Recommended)"`, `"Max flexibility"`, `"Caller-optimized"`, `"Ports & adapters"`).

Use the `preview` field on each option to show the interface signature + usage example — this is exactly the case that's worth a side-by-side comparison.

Plain-text fallback: *"Which approach for `<friction point>`? (design 1 / 2 / 3 / 4)"*

If `confirmBeforeExecute` is `false`: use the recommended approach and proceed.
