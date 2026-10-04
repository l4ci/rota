---
name: rota-refactor
description: Run a full architectural refactor cycle — explores the codebase for friction, categorizes dependencies, designs competing approaches for structural changes, then fixes everything with parallel subagents. Use when you want to find and fix architectural issues.
---

# rota-refactor

## Configuration

Read `.rota/config.json`:

- `models.orchestrator` — exploration, design, and verification (default `opus`)
- `models.worker` — implementation subagents (default `sonnet`)
- `refactor.confirmBeforeExecute` — pause for user approval before executing fixes (default `true`; `false` for full autonomy)
- `refactor.verifyCommands` — array of shell commands to run as CI-shape gates during Step 7 verification (default `[]` — read-only verification when empty)

## Args

- `--here` — skip Step 1.5 (umbrella scope detection); run the single-repo flow in cwd. Used internally by Step 1.5's fanout dispatch — each sub-agent invokes `/rota-refactor --here` after `cd`-ing into its target. Single-repo projects (umbrella.enabled false or unset) ignore the flag — the flow is identical with or without it.

## Flow

Explore → Triage → Present *(checkpoint)* → Design competing approaches *(checkpoint)* → Fix in parallel → Verify → Re-fix on failures → Commit → Report

## Dependency Categories

Every friction point gets one category — it drives the fix strategy:

1. **In-process** — pure computation, in-memory state, no I/O. Merge modules, test directly.
2. **Local-substitutable** — has a local test stand-in (PGLite for Postgres, in-memory FS). Test with the stand-in in the suite.
3. **Ports & Adapters** — your services across a network boundary (microservices, internal APIs). Define a port at the module boundary; deep module owns logic, transport is injected. Tests use an in-memory adapter; production uses the real one.
4. **True external** — third-party services (Stripe, Twilio) you don't control. Mock at the boundary; tests provide the mock, production uses the real implementation.

## Step 1 — Task List

The clean-tree guard (`rota git guard clean`) lives in Step 1.5's branches, not here — fanout sub-agents run their own guards against their target trees, so the umbrella-level guard isn't needed (and would falsely fail when the umbrella is not a git repo).

Track these phases with the host's task tool if it has one.

Phases:

1. *Explore* — fanout sub-agents surface friction signals (Steps 1.5–2)
2. *Categorize* — signals grouped by structural axis (Step 3)
3. *Design approaches* — competing approaches drafted per category (Step 4)
4. *User confirm* — approach selection gate (Step 5)
5. *Execute* — parallel sub-agents apply the chosen design (Step 6)
6. *Verify & merge* — diffs verified, branch merged or PR opened (Steps 7–8)

## Step 1.5 — Umbrella Scope (skip if `--here` was passed)

Skip this step entirely if EITHER:
- `--here` was passed to this invocation
- `umbrella.enabled` is false in `.rota/config.json`

In that case, run the clean-tree guard and proceed to Step 2 (single-repo flow):

```bash
rota git guard clean --context "/rota-refactor"
```

Otherwise, list refactor targets:

```bash
rota refactor targets --json
```

`data` holds:

```json
{
  "umbrella": {"hasCode": true|false},
  "subRepos": [{"name": "<name>", "path": "<abs-path>"}, ...]
}
```

If `subRepos` is empty (umbrella mode is on but no sub-repos are registered), proceed to Step 2 with the single-repo flow — the umbrella's tree is the only target.

Otherwise, ask the user which scope to refactor via `AskUserQuestion`:

- **Header:** `"Scope"`
- **Question:** *"Umbrella mode is on with N sub-repo(s): `<comma-separated names>`. Where should this refactor cycle run?"*
- **Options** (single-select), conditional on `umbrella.hasCode`:
  - When `hasCode == true`:
    1. *"All sub-repos + umbrella (Recommended)"* — *"N+1 parallel cycles, one per sub-repo plus one for the umbrella's own tree."*
    2. *"All sub-repos only"* — *"N parallel cycles. Skip the umbrella's own tree."*
    3. *"Umbrella only"* — *"Refactor the umbrella's tree only; skip sub-repos."*
    4. *"Pick a subset"* — *"Multi-select which sub-repos to include."*
  - When `hasCode == false`:
    1. *"All sub-repos (Recommended)"* — *"N parallel cycles, one per sub-repo."*
    2. *"Pick a subset"* — *"Multi-select which sub-repos to include."*
    3. *"Umbrella only"* — *"Refactor the umbrella's tree only (no code detected — likely no findings)."*

With no answer, take the Recommended option for the relevant `hasCode` state.

**Loop mode:** if `autonomy.level == "loop"`, auto-pick the Recommended scope option without invoking AskUserQuestion. With `hasCode == true`, that's *"All sub-repos + umbrella (Recommended)"* — fan out to N+1 parallel cycles. With `hasCode == false`, that's *"All sub-repos (Recommended)"* — fan out to N parallel cycles. The "Pick a subset" follow-up never fires under loop. Per the authoring convention "routine routing/tagging auto-picks Recommended in loop mode" (see `references/authoring-conventions.md` rule #5).

If the user picks **"Pick a subset"**, follow up with a multiSelect:

- **Header:** `"Sub-repos"`, `multiSelect: true`
- **Question:** *"Which sub-repos to refactor?"*
- **Options:** up to 4 sub-repos by name (alphabetical); if more than 4, list top 4 alphabetically and ask the user to name the rest in free text.

### Branch — "Umbrella only"

Run `rota git guard clean --context "/rota-refactor"` against the umbrella's tree if you skipped it earlier. Then proceed to Step 2 (single-repo flow). The umbrella's `.git/` (if it has one) receives the commit; if the umbrella isn't a git repo, the guard exits 3 and this step stops.

### Branch — fanout (any other scope: "All sub-repos", "All + umbrella", "Pick a subset")

The full fanout choreography — context collection, the per-target sub-agent prompt template, the parallel dispatch, the after-returns aggregation + counter reset + report — lives in `references/refactor-umbrella-fanout.md`. Run that flow end-to-end, then EXIT this skill (skip Steps 2–10).

The reference's sub-agent prompt embeds `<orchestrator>` and `<worker>` placeholders — substitute from the umbrella's `.rota/config.json` `models.orchestrator` / `models.worker` (defaults `opus` / `sonnet`) before dispatch.

## Step 2 — Explore with Orchestrator

Dispatch an exploration agent using the configured **orchestrator** model, with a prioritization rule (inbound-import count + LoC + mtime, top 20% read in full), a neighborhood-expansion rule (one hop), and a stop condition (8–12 friction points across the categories, or 30+ files read with no new category in the last 5). Full prompt template + categories list lives in `references/refactor-explore.md`.

## Step 3 — Triage, Categorize & Classify

After the exploration agent returns, process each friction point:

1. **Assign a dependency category** (in-process / local-substitutable / ports & adapters / true external)
2. **Classify as simple or structural:**
   - **Simple** — the fix is obvious and self-contained (surface an error, propagate a value, consolidate duplicated logic, add a missing return). No design choices involved.
   - **Structural** — the fix reshapes a module boundary, merges tightly-coupled modules, changes ownership of a concept, or introduces a new interface. Multiple valid approaches exist.
3. **Group into independent fix batches:**
   - **Independent files** → parallel agents
   - **Same file** → single agent handles all changes to that file
   - **Sequential dependency** (fix A must land before fix B can reference it) → note the order, run sequentially after the first batch

## Step 4 — Present Candidates

Present a numbered list of all friction points. For each, show:

- **Cluster**: which files/modules are involved
- **Classification**: simple or structural
- **Dependency category**: which of the 4 categories applies
- **Why it matters**: the concrete risk or cost of leaving it

If `confirmBeforeExecute` is `true`, gate with `AskUserQuestion`:

- **Header:** `"Candidates"`
- **Question:** *"Found N friction points. Which should I fix?"*
- **Options** (single-select):
  1. "Fix all N (Recommended)" — *"Proceed with every candidate in parallel."*
  2. "Pick a subset" — *"Choose which items to include; the rest are skipped."*
  3. "Stop" — *"No changes; surface the list and exit."*

If the user picks "Pick a subset", follow up with a second `AskUserQuestion`:

- **Header:** `"Subset"`, `multiSelect: true`
- **Question:** *"Select the items to fix."*
- **Options:** up to 4 candidates by label (e.g., `"SessionOrchestrator error propagation"`). If more than 4, list top 4 by impact and ask the user to name the rest in free text.

**Loop mode:** if `autonomy.level == "loop"` AND `confirmBeforeExecute == true`, auto-pick *"Fix all N (Recommended)"* without invoking AskUserQuestion — proceed with every candidate in parallel. The "Pick a subset" follow-up never fires under loop. Per the authoring convention "routine routing/tagging auto-picks Recommended in loop mode" (see `references/authoring-conventions.md` rule #5). When `confirmBeforeExecute == false` the gate is already skipped (existing behavior in the next paragraph), so this auto-pick is conditional on the gate firing in the first place.

If `confirmBeforeExecute` is `false`: present the list for visibility, then proceed immediately with all items.

## Step 5 — Design Competing Approaches (Structural Only)

For each **structural** friction point, spawn 3+ sub-agents in parallel using the configured **orchestrator** model. Each gets the same technical brief but a different design constraint (minimal interface, max flexibility, caller-optimized, ports & adapters when remote deps apply). Each returns interface signature, usage example, hidden complexity, dependency strategy, and trade-offs. Orchestrator picks or hybridizes; when `confirmBeforeExecute` is `true`, gate with `AskUserQuestion` (one option per design, side-by-side `preview`). Decisions consult, agent dispatch shape, and gate UX in `references/refactor-design-approaches.md`.

**Simple** friction points skip this step entirely — they go straight to Step 6.

## Step 6 — Fix with Parallel Worker Agents

Dispatch all independent fixes in parallel using the configured **worker** model. Each agent gets:
- Exact files to read and modify
- Precise description of the friction and the chosen approach
- For structural changes: the selected interface design from Step 5
- Dependency category and how deps should be handled
- Constraint: read the file first, minimal diff, no unrelated changes
- Return: short summary of what changed

For each agent brief:
- Include the relevant code snippet showing the problem
- Include the replacement code or a precise description of it
- Name every line number so the agent doesn't have to hunt

Don't announce the dispatch — just do it. After parallel batch completes, dispatch any sequential agents that depended on the first batch.

## Step 7 — Verify with Orchestrator

Read `refactor.verifyCommands` from `.rota/config.json`:

```bash
python3 -c "import json,sys; print(json.dumps(json.load(open('.rota/config.json')).get('refactor',{}).get('verifyCommands',[])))"
```

Dispatch a single verification agent using the configured **orchestrator** model. For each fix, it reads the modified file and reports PASS / FAIL / CONCERN as before.

**When `verifyCommands` is non-empty**, the agent's brief MUST include the command list and require it to execute every command verbatim, capturing exit code and stderr. Any non-zero exit MUST be reported as a FAIL with the failing command and its stderr inline. The agent reports PASS only when (a) all file reads check out AND (b) every gate command exited zero.

**When `verifyCommands` is empty** (default), Step 7 runs file-read verification only — behavior unchanged from prior versions. No spurious "no commands configured" messages; silent pass-through is the common case.

The verification agent must read actual file content (not just trust fix summaries) AND actually run the gate commands (not just report what it would run). Don't relay individual PASS verdicts to the user — only surface FAILs and CONCERNs.

## Step 8 — Handle Failures

If any fix got **FAIL**:
- Read the verification finding
- Dispatch a new worker agent with the corrected brief
- Re-verify with orchestrator
- Only mention persistent failures to the user (ones that don't resolve after retry)

If any fix got **CONCERN**:
- Assess whether it blocks commit
- Fix if blocking, note if informational — surface informational concerns briefly at the end, not inline during verification

## Step 9 — Commit

After all fixes pass verification, commit everything:

```bash
# Stage all modified files explicitly (never git add -A)
git add [file1] [file2] ...

# Commit with a message that lists all fixes
git commit -m "refactor: [N] architectural improvements

[one line per fix, e.g.:]
- SessionOrchestrator: surface autosave errors to lastError
- RingBuffer: write() returns overflow count (@discardableResult)
- SpeakerReconciler: returned embeddings use EMA values
..."
```

If the project uses a build tool to regenerate project files (e.g. `xcodegen generate` for XcodeGen projects), run it before committing if any files were added or deleted.

After the commit lands, zero the refactor-pressure counter so the next cycle starts fresh:

```bash
rota refactor reset
```

## Step 10 — Report to User

After commit, give one compact summary. Example:

```
Refactored 4 items — commit d7e8f9a

- SessionOrchestrator: surface autosave errors to lastError
- RingBuffer: write() returns overflow count
- SpeakerReconciler: returned embeddings use EMA values
- TimerManager: consolidated 3 timer sources into one
```

If any CONCERNs surfaced during verification, append them briefly:

```
Note: RingBuffer overflow count changes the return type — callers using `_ = write()` are fine, but check any that inspect the return.
```

Don't recap the exploration findings, the design alternatives, or the verification pass/fail log. The user can read the diff.

## Key Principles

- **No noise.** Don't narrate steps that produced no output or found nothing. Don't echo back what you're about to do before doing it. Report results, not process.
- **Orchestrator for judgment, worker for execution.** Models are configured in `.rota/config.json` (default: opus/sonnet). Exploration, design, and verification require deep reasoning; implementation is precise execution of a known fix.
- **Categorize before fixing.** Every friction point gets a dependency category and a simple/structural classification. This prevents over-engineering simple fixes and under-designing structural ones.
- **Compete on structural changes.** When multiple valid approaches exist, design them in parallel and pick the strongest. Don't commit to the first idea.
- **Parallel by default.** Independent fixes always run in parallel. Sequential only when there's a real dependency.
- **Minimal diffs.** Each fix touches only what's necessary. No reformatting, no unrelated cleanup.
- **Read before edit.** Every agent reads the target file before making changes.
- **Verify before commit.** Never commit without orchestrator sign-off.
- **Commit once** per run (not per fix) unless fixes are truly independent milestones.

## References

- [`references/authoring-conventions.md`](references/authoring-conventions.md) — Authoring rules shared across SKILL.md files (loop-mode auto-picks, mirror-step threshold).
- [`references/refactor-explore.md`](references/refactor-explore.md) — Exploration-agent prompt + categories + stop condition used by `/rota-refactor` single-repo mode.
- [`references/refactor-design-approaches.md`](references/refactor-design-approaches.md) — Competing-design choreography (decisions consult, agent constraints, output shape, `confirmBeforeExecute` gate) used by `/rota-refactor` Step 5.
- [`references/refactor-umbrella-fanout.md`](references/refactor-umbrella-fanout.md) — Per-repo fan-out logic for `/rota-refactor` in umbrella mode.
