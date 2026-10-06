# `/rota-refactor` competing design approaches

Loaded by `/rota-refactor` when `--designs` is passed, for findings that are **structural** (they reshape an interface or move ownership of a concept). Simple findings skip it. Without `--designs` no competing designs run.

## Consult decisions before designing

Pull relevant boundary entries:

```bash
rota decisions query <topics…>
```

Any approach that violates a decision is disqualified before the design phase. If every generated approach would violate, **stop and surface to the user**: refactors must not silently work around committed boundaries, and they are when boundaries matter most.

## Agent dispatch

For each structural finding, spawn 3+ sub-agents in parallel on the main session's model (`models.orchestrator`). Each agent gets the same technical brief (file paths, coupling details, which seam, what the module would hide) but a different design constraint:

- **Agent 1**: "Minimize the interface — aim for 1-3 entry points max"
- **Agent 2**: "Maximize flexibility — support many use cases and extension"
- **Agent 3**: "Optimize for the most common caller — make the default case trivial"
- **Agent 4** (if a remote dependency is involved): "Design around the ports & adapters pattern"

## Per-design output

Each sub-agent outputs:

1. Interface signature (types, methods, params)
2. Usage example showing how callers use it
3. What complexity it hides internally
4. Adapters at the seam (one is hypothetical, two is real)
5. Trade-offs

## Presentation

Present designs sequentially, then compare them in prose. Give an opinionated recommendation: which design is strongest and why. If elements from different designs combine well, propose a hybrid.

## Where the result goes

In a findings run, put the recommended interface and the rejected alternatives in the filed issue's **Solution** section. On the `--fix` path, hand the chosen design to the `standard` subagent brief. With `--interactive` or `refactor.confirmBeforeExecute` true, gate with `AskUserQuestion` per finding (batch up to 4): one option per design, recommended first, `preview` showing the signature and usage example.
