# Plan critic

Used by `/rota-plan` (Step 3, between the self-check and the confirm question) when the planning dial (`references/planning-dial.md`) picks level 4, the adversarial pass, or level 3 when the item has at least one `decision` comment. At levels 1-2, and at level 3 without a decision comment, the step does not run and the flow is unchanged.

The critic is a fresh `standard` subagent (`references/subagent-dispatch.md`, *Subagent tiers*), or, at level 4 with `plan.criticHarness` set, a read-only run on another harness (*Another harness* below). It sees only the item and the proposal. It never sees the planning conversation, the user's redirects or your reasoning, so it cannot inherit your blind spots.

## Dispatch

Dispatch one read-only subagent through the current harness (`references/subagent-dispatch.md`). Pass paths and IDs, not pasted content:

- **Inputs**: the item (`rota item show <ID>`, including its `## Acceptance` and `## Out of scope`), and the proposal saved to a scratch file. Nothing else.
- **Return**: a short list of findings, at most 7, one per line, under the four headings below. Write `none` under a heading with nothing to report. Word budget 250.

## Another harness

Only level 4 reads `plan.criticHarness` (`rota config show plan.criticHarness`). Level 3 with a `decision` comment always takes the same-harness subagent above, whatever the key says. Unset, or equal to the harness you run in, changes nothing: use the subagent.

With `claude`, `codex` or `opencode` set (a different harness than yours), save the critic brief below to a scratch file and run it headless and read-only from the project root, as [`cross-harness-run.md`](cross-harness-run.md) says (command, account, token line, reading the output). The inputs and the return shape are the ones above: the item, written with `rota item show <ID> > <scratch-item-file>` and passed by path, and the proposal file, by path, nothing else. The token goes on the first line of the proposal file.

Trim the reply to the return shape (at most 7 lines, four headings).

**Fallback.** When the run is unavailable (`cross-harness-run.md`, *Read the output*) or `<out>` does not hold the four headings, run the same-harness subagent instead and print one line: `plan critic: <harness> unavailable (<reason>), ran on the planning harness`. Never skip the critic silently. Do not retry the other harness.

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
4. Weakened outcomes: any task whose Observable behavior is weaker than the acceptance
   it serves (a narrower input, a softer guarantee, a case dropped to make it fit).

Do not rewrite the plan, praise it or restate it. Do not edit files. Skip style.
Prefer fewer, concrete findings to many vague ones.
```

## Folding the findings in

The planner, not the critic, decides. Before asking *Confirm this plan?*, add a `## Critic findings` section to the proposal. Open the section with `Critic harness: <harness>`, the harness that produced the findings: `claude`, `codex` or `opencode` for a run on another harness, or the planning harness's own name for the subagent (including after a fallback). Then one line per finding:

- **Accepted**: `accepted: <finding> -> <what changed in the plan>`. Edit the plan to match.
- **Rejected**: `rejected: <finding> -- <reason>`. A rejection needs a reason the user can check (a file, a test, the Acceptance text).

A finding you cannot answer either way goes to Open questions. If the critic returns `none` everywhere, say so in one line naming the harness and omit the section. Dispatch the critic once per proposal; do not loop.
