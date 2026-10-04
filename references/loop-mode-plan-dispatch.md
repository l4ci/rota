# Loop-mode auto-dispatch chain & rename-collision detection

Shared reference for `/rota-work` Step 4 *Plan Tasks* details that are dense and self-contained: the loop-mode auto-dispatch chain (B28 design pre-flight, F34 uncertainty pre-flight, F35 orchestrator-model contract, F32 auto-plan) plus the wave-planning *Absorb wave-internal file collisions* rule. The SKILL.md keeps the plan-as-artifact gate and the per-step decomposition list inline; this file holds the longer choreography.

## Design pre-flight (B28, loop mode only)

Before the uncertainty pre-flight, `/rota-work` checks for a design artifact at `.rota/designs/<itemId>.md`. When absent for a Major + Milestone-tagged item, dispatch `/rota-brainstorm --auto-loop <itemId>` via the `Skill` tool — no prompt, no confirmation. The dispatched skill auto-resolves design questions via the same pipeline shape `/rota-plan --auto-loop` uses (Local-first against `DECISIONS.md` / `KNOWLEDGE.md` / `CONTEXT.md` / `MILESTONES.md` → Bounded web when `loop.webResearch == true` → Placeholder fallback for the unresolved), logs fresh picks via `rota decisions auto-log`, and writes the design with `auto: true` frontmatter.

When the design already exists (auto-written or manually authored), this step is a no-op — loop calls are idempotent and never replace existing designs.

The design feeds the downstream `/rota-plan --auto-loop` directly: `/rota-plan` Step 3 already loads `.rota/designs/<itemId>.md` as soft input. The plan inherits the design's chosen approach instead of re-resolving it, and the open questions / assumptions land in the plan's frontmatter pointer (`design: .rota/designs/<itemId>.md`).

## Loop-mode auto-plan dispatch

When no plan exists AND `autonomy.level == "loop"` AND the item is **Major** AND the item is **Milestone-tagged** (a plan key exists), `/rota-work` does **not** stop the loop on the missing plan. After the design pre-flight (above) and the uncertainty pre-flight (below) complete, dispatch `/rota-plan --auto-loop <milestone>-<itemId>` via the `Skill` tool — no prompt, no confirmation, no "want me to" question. When the dispatched plan run returns, `/rota-work` re-runs the plan-as-artifact check (the file now exists) and uses the auto-written plan as the orchestrator's plan. Off and auto modes never auto-dispatch — they fall through to the manual decomposition.

## Uncertainty pre-flight (F34, loop mode only)

Before the auto-plan dispatch, `/rota-work` runs:

```bash
rota plan uncertain <itemId>
```

The verb applies a structural-triple heuristic — fires "uncertain" when the item is Major AND any of: (a) no detail file at `.rota/<bugs|features|tasks>/<itemId>.md`, (b) brief contains 2+ question marks or explicit uncertainty markers (`TBD`, `unclear`, `unsure`, `open question`, `heuristic TBD`), or (c) brief contains zero backtick-delimited code spans (no concrete identifier anchors → unknown surface). Exit 0 = uncertain (with reasons on stdout, `data.reasons` under `--json`); exit 1 = certain; exit 3 = the item or `BACKLOG.md` is missing.

When uncertain, **run the `/rota-work` Preview Mode procedure inline** with `<itemId>` as the target — no prompt, no confirmation. The peek prints to chat and lands in the orchestrator's session context, where the subsequent `/rota-plan --auto-loop` reads it. After the peek returns, proceed with the `/rota-plan --auto-loop` dispatch as normal. When certain, skip the peek and dispatch `/rota-plan --auto-loop` directly.

## Orchestrator-model contract (F35)

The two dispatched skills — `/rota-brainstorm --auto-loop` and `/rota-plan --auto-loop` — are invoked via the `Skill` tool, which loads each inline in the current session. The Preview Mode peek (when uncertain) runs inline inside `/rota-work`'s own session — no dispatch, no Skill call. Since `/rota-work` itself runs in the orchestrator session under `models.orchestrator`, both the dispatched skills AND the inline peek benefit from orchestrator-grade judgment. If a future change moves any of the dispatched skills to `Agent`-based dispatch, the call site must explicitly pass `model: orchestrator` (read from `.rota/config.json`) to preserve this guarantee.

## Absorb wave-internal file collisions

### The general rule

Before grouping tasks into waves, compute every task's modified-file set and scan for **any pair whose sets intersect**. Under `work.isolation: "branch"` all workers in a wave share one working tree, so two write-only workers editing the same file collide on disk — the second worker's `Edit` reads content the first already mutated and either clobbers it or fails with *"File has been modified since read"*. A same-file pair left intact forces the wave to serialize, defeating parallel dispatch.

The orchestrator's standing recourse is **absorption**, applied at dispatch time (Step 6), in priority order:

- **Absorb** (preferred). Fold one task's same-file edits into the *other* task's brief, so the absorbed task touches only files no other task writes. The orchestrator rewrites both Step 6 briefs: the absorbing brief gains the merged edits and a widened modified-file set; the absorbed brief drops the shared file and narrows to its disjoint remainder. Both tasks then dispatch as parallel write-only workers. The task IDs stay distinct in BACKLOG/commits — absorption changes *who writes the file*, not *what work is tracked*.
- **Split ownership cleanly.** Re-scope the pair so no file appears in both modified-file sets (the rename + link-sweep split below is the canonical shape).
- **Serialize across waves.** Task A in wave N, Task B in wave N+1, so B operates on A's settled output. Use only when the edits are genuinely dependent — absorption is cheaper when they're merely co-located.

This is a reproducible orchestrator-side technique, not per-orchestrator improvisation: every intersecting pair must be resolved by one of the three before grouping, and the choice is recorded implicitly in the rewritten briefs.

**Worked example (M02-S02).** A four-task plan had T3 adding a server-side view function and T4 adding the template + CSS + a second `server.py` edit. T3 and T4 both wrote `server.py` → same-file collision under branch isolation. Recourse: T4's `_build_employee_view` `server.py` addition was **absorbed into T3**, so T4's modified-file set narrowed to template + CSS only. Both ran as parallel write-only workers; T3 and T4 kept their own commits and BACKLOG entries.

### Canonical sub-case: rename + link-sweep

The most common intersecting pair is *Task A renames a file (`git mv old new` or equivalent), Task B edits files that link to `old`*. The collision is on **shared written files** — when the link-sweep enumerates the renamed file itself or other files the rename task already edits, both tasks race on the index even when their stated mandates appear disjoint. Resolve by the same priority order, which for this shape concretely means:

- **Merge** rename + link-sweep into one task (the absorb case when they're one logical change — one commit, atomic revert).
- **Split ownership cleanly**: rename task owns the file move plus edits to the renamed file's own content; link-sweep task owns link updates in all *other* files. No file appears in both tasks' modified-file sets.
- **Serialize across waves**: rename in wave N, link-sweep in wave N+1, so the sweep operates on settled paths.

For every rename, derive the incoming-link file set with `rota plan rename-check <old-name> [-- <scope>...]` (wraps `git grep -l`); the plan author's enumeration is a hint, the verb is ground truth. Re-run the same check at verify time (Step 7) to catch files the plan missed.

## Cited by

- `/rota-work` Step 4 — *Plan Tasks*
