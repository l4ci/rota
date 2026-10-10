# Plan critic

Used by `/rota-plan` (Step 3, between the self-check and the confirm question) when the planning dial (`references/planning-dial.md`) picks level 4, the adversarial pass. At levels 1-3 the step does not run and the flow is unchanged.

The critic is a fresh `standard` subagent (`references/subagent-dispatch.md`, *Subagent tiers*). It sees only the item and the proposal. It never sees the planning conversation, the user's redirects or your reasoning, so it cannot inherit your blind spots.

## Dispatch

Dispatch one read-only subagent through the current harness (`references/subagent-dispatch.md`). Pass paths and IDs, not pasted content:

- **Inputs**: the item (`rota item show <ID>`, including its `## Acceptance` and `## Out of scope`), and the proposal saved to a scratch file. Nothing else.
- **Return**: a short list of findings, at most 7, one per line, under the three headings below. Write `none` under a heading with nothing to report. Word budget 250.

## Critic brief

```
Goal: argue against this plan before the user approves it.

You get an item and a proposed plan. Read both. You have not seen how the plan was
made; judge only what is on the page. Report under these headings, one line per
finding, each naming the task or Acceptance line it concerns:

1. Missed cases: inputs, states, failure paths, re-runs or rollback the plan never
   handles, and Acceptance criteria no task covers.
2. Scope creep: any task, file or behavior that falls under the item's
   `## Out of scope`, or that the Acceptance does not ask for.
3. Unverifiable tasks: any task whose Verify cannot fail (it passes before the change,
   checks only that a file exists, or names no command), and any behavior task with no
   RED.

Do not rewrite the plan, praise it or restate it. Do not edit files. Skip style.
Prefer fewer, concrete findings to many vague ones.
```

## Folding the findings in

The planner, not the critic, decides. Before asking *Confirm this plan?*, add a `## Critic findings` section to the proposal with one line per finding:

- **Accepted**: `accepted: <finding> -> <what changed in the plan>`. Edit the plan to match.
- **Rejected**: `rejected: <finding> -- <reason>`. A rejection needs a reason the user can check (a file, a test, the Acceptance text).

A finding you cannot answer either way goes to Open questions. If the critic returns `none` everywhere, say so in one line and omit the section. Dispatch the critic once per proposal; do not loop.
