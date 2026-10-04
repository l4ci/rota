---
name: rota-brainstorm
description: Per-item design exploration before /rota-plan — Socratic discovery, 2-3 approaches with tradeoffs, sectioned design with per-section approval, writes .rota/designs/<ID>.md, hands off to /rota-plan. Use when a Major feature or P0 bug needs design negotiation before implementation planning.
---

# rota-brainstorm — Per-item Design Exploration

`/rota-brainstorm` fills the gap between `/rota-capture` (records what to build) and `/rota-plan` (decomposes how to build it) by negotiating *whether this is the right thing and what its shape should be*. Scope is a single backlog item (`[B##]` or `[F##]` or `[T##]`); project-level exploration stays with `/rota-vision`. The artifact lands at `.rota/designs/<ID>.md` and feeds `/rota-plan` as soft input — never required.

## Step 1 — Setup

**Autonomy gate.** Read `autonomy.level` and parse the `--auto-loop` flag:

```bash
LEVEL=$(jq -r '.autonomy.level // "off"' .rota/config.json)
```

Also parse `AUTO_LOOP`: scan `$ARGUMENTS` (the skill `args` value) for the literal string `--auto-loop`; set `AUTO_LOOP=true` if present, `AUTO_LOOP=false` otherwise. Then branch:

- **`LEVEL == "loop"` AND `AUTO_LOOP=false`** — print *"Note: /rota-brainstorm is skipped under loop autonomy (throughput mode). Re-run after `rota config set` to off or auto if you want to brainstorm."* and exit 0. Per the 2026-05-09 KNOWLEDGE inline-autonomy-directives convention, the check lives at every dispatch point including this one.
- **`LEVEL == "loop"` AND `AUTO_LOOP=true`** — enter auto-loop mode: proceed to Step 2 without exiting. All `AskUserQuestion` calls are suppressed for the rest of the run; the auto-resolution pipeline (see `## Auto-loop mode`) drives every pick.
- **`LEVEL != "loop"` (off/auto) AND `AUTO_LOOP=true`** — stop: `--auto-loop` is loop-mode only (`rota design add --auto-loop` refuses it with exit 2 anyway; this check just stops before the run).
- **`LEVEL != "loop"` AND `AUTO_LOOP=false`** — normal interactive flow (today's path); proceed to Step 2.

Track these phases with the host's task tool if it has one.

Phases:

1. *Resolve target* — item ID parsed, existence verified, re-run mode picked (Step 2)
2. *Load context* — TODO entry, detail file, K+D+C queries gathered in parallel (Step 3)
3. *Discover* — Socratic clarifying rounds, capped at 5 (Step 4)
4. *Propose approaches* — 2-3 candidates with tradeoffs, user picks one (Step 5)
5. *Section drafts* — Goal → Design → Approaches → Open questions → Assumptions with per-section approval (Step 6)
6. *Write* — artifact persisted to `.rota/designs/<ID>.md` (Step 7)
7. *Self-review* — placeholder/contradiction/scope/ambiguity scan (Step 8)
8. *User review* — final approval gate (Step 9)

## Step 2 — Resolve Target

Parse the item ID from the invocation. It must match `[BFT]\d{2,}`. Reject milestone IDs (`M01`) and slice IDs (`S01`) with: *"Error: /rota-brainstorm operates on a single backlog item. For project-level exploration use /rota-vision; for slice planning use /rota-plan."*

Verify the item exists in `.rota/BACKLOG.md`:

```bash
rota item field get <ID> --name title
```

Exit 3 means the ID is not in the backlog. Refuse with: *"Error: [<ID>] not found in BACKLOG.md. Run /rota-capture first to add it."*

**Re-run check.** Under `--auto-loop`, if `.rota/designs/<ID>.md` already exists, exit silently with a one-line note **`Design already exists — no auto-action.`** Loop calls are idempotent; replacing a design requires manual `/rota-brainstorm <ID>` invocation.

If `.rota/designs/<ID>.md` already exists (interactive mode), ask via `AskUserQuestion` (single-select, 3 options):

- **View** — print the existing design and exit
- **Edit** — enter brainstorm with existing design loaded as starting context
- **Replace** — discard the existing design and re-run discovery from scratch

Default: opt-in-off / cancel (replace is destructive). Routing:

- **View** → invoke `rota design show <ID>` and exit 0.
- **Edit** → load the existing design content; the first Step 4 clarifying question is *"What would you change about the existing design?"*
- **Replace** → run `rota design rm <ID>` then proceed to Step 3.

## Step 3 — Load Context Silently

Pull the picture in parallel — these reads are independent and latency-bound:

- `rota item field list --json <ID>` — `data.fields` with `title`, `milestone`, `related`, `detail`, `repos`, `subsystem`, `since` (single corpus load; avoids re-parsing BACKLOG.md per field)
- Detail file: `.rota/bugs/<ID>.md`, `.rota/features/<ID>.md`, or `.rota/tasks/<ID>.md` (read whichever exists)
- `rota knowledge query <topic>` for topics inferred from the TODO entry and detail file
- `rota decisions query <topic>` for the same topics — committed boundaries the design must respect
- `rota glossary read <term>` for any domain terms the item references

**Issue all of these as parallel tool calls in a single response.** Don't narrate the loading. Form a picture; carry findings forward into Steps 4 and 5.

DECISIONS matches are hard boundaries. If the brainstorm would violate any, surface the conflict before proposing approaches and ask whether to update the decision first.

## Step 4 — Frame & Discover

**Skipped under `--auto-loop`** — the auto-resolution pipeline runs in lieu of clarifying rounds. See `## Auto-loop mode` below.

Socratic clarifying questions via `AskUserQuestion`, one per round, multi-choice preferred (≤ 4 options per the picker cap). **Cap: 5 clarifying rounds**; after the fifth, switch to plain-text prose. Section gates (Step 6) and the final review (Step 9) are check-ins, not exploratory questions, and do **not** count against the budget.

`/rota-vision` is the project-scope sibling and uses batched discovery — see `references/design-exploration.md` for the family spine and divergence rationale.

Good clarifying-question shapes for an item:

- **Scope** — *"Does this need to handle umbrella sub-repos in V1, or is single-repo fine?"*
- **Interaction** — *"Should this run automatically after /rota-capture, or only on explicit invocation?"*
- **Gotcha boundaries** — *"What existing skill must this NOT touch?"*
- **Failure modes** — *"What happens when <key precondition> is false?"*

**Spike handoff.** When a clarifying question or candidate approach needs code-touching evidence to answer (feasibility, library support, performance characteristics), surface:

> *"This warrants a spike. Run `/rota-spike <name>` first to gather evidence, then re-invoke `/rota-brainstorm <ID>` to finish the design."*

Don't guess at the answer to keep the brainstorm moving.

## Step 5 — Propose 2-3 Approaches

**Under `--auto-loop`**, the orchestrator picks the most consistent approach via the auto-resolution pipeline (no `AskUserQuestion`). Filters: any candidate that violates a DECISIONS entry is auto-rejected; remaining candidates ranked by KNOWLEDGE pattern match and architectural consistency with adjacent skills. The pick is logged via `rota decisions auto-log` under the rule title `Brainstorm approach pick for <ID>`.

Present 2 or 3 candidate approaches inline as plain markdown — not yet committed to disk. Each approach gets:

- **Shape** — one paragraph naming the moving parts and where they live
- **Pros** — 2-4 bullets on what this approach wins
- **Cons** — 2-4 bullets on what it costs
- **Why this might or might not be the right answer** — one sentence on the deciding factor

Frame each candidate around the item's detail file and the K+D+C findings from Step 3 — *"approach A reuses the helper pattern flagged in KNOWLEDGE"*, *"approach B violates the DECISION on X"*. Then ask the user to pick via `AskUserQuestion` (single-select, ≤ 4 options: one per approach plus *"Ask more questions first"* as an escape back to Step 4). Silence means more discussion, not a pick.

The picked approach is the design. Carry it into Step 6.

## Step 6 — Sectioned Design with Per-Section Approval

**Under `--auto-loop`**, write each section directly from the Step 4/5 auto-resolution outputs — no per-section `AskUserQuestion` approval gates.

Draft the design one section at a time in this order: Goal → Design → Approaches considered → Open questions → Assumptions. See `references/design-exploration.md` for the shared approval shape (yes / changes / approve all remaining).

Section content rules:

- **Goal** — one sentence on what shipping this design means
- **Design** — 3-8 sentences on the chosen shape, moving parts, where they live
- **Approaches considered** — the 2-3 candidates from Step 5 with Pros/Cons/Why summarized
- **Open questions** — questions that need answering before or during `/rota-plan`; mark spike candidates explicitly
- **Assumptions** — implicit constraints made explicit (*"assumes single-repo"*, *"assumes K+D queries return ≤ 20 hits"*)

## Step 7 — Write Artifact

Mint the design stub:

```bash
rota design add <ID> --title "<title>"
```

The verb creates `.rota/designs/<ID>.md` with frontmatter (`id`, `title`, `status: draft`, `created`) and the five placeholder section headers. Use the `Edit` tool to overwrite each placeholder section body with the approved content from Step 6. Keep the frontmatter intact.

**Issue mode** (`backlog.backend: "issues"`; `references/issue-mode.md`): the design is a note on the item's issue, not a file. `rota design add` still creates it; draft the approved sections in a scratch file (not under `.rota/designs/`) and publish with `rota design put <ID> --body-file <scratch-file>` instead of `Edit`. Read it back with `rota design show <ID>`. Post each answer that changed the design's direction with `rota item comment add <ID> --kind decision --body-file -`.

Under `--auto-loop`, mint the stub with `rota design add <ID> --title "<title>" --auto-loop`: the verb writes `auto: true` into the frontmatter, marking the artifact as auto-written.

## Step 8 — Self-Review

Scan per the shared shape — see `references/design-exploration.md` (placeholders, internal contradictions, scope creep, ambiguous adjectives). Item-specific check: every claim ties back to the item ID; nothing leaks into a sibling item or future milestone. Internal-contradiction check: Step 5's chosen approach matches the Design section; Open questions don't conflict with Assumptions.

## Step 9 — User Review Gate

**Skipped under `--auto-loop`** — the design is final on write; users review via terminal-path surfacing (`/rota-work` empty-backlog, `/rota-work` guard-fail, `/rota-pause`) where `rota decisions auto-since` prints the logged decisions.

Print the final artifact (or invoke `rota design show <ID>`) and ask via `AskUserQuestion` per the shared review-gate shape (see `references/design-exploration.md`). Item-specific routes:

- **Approve and hand off to `/rota-plan`**
- **Revise** — return to Step 6 with the user's redlines
- **Stop here** — design is enough for now; no plan handoff

## Step 10 — Report

One compact summary:

```
Design written: F58 — <title>
  Artifact: .rota/designs/F58.md
  Approaches considered: 3
  Open questions: 2
  Status: draft
```

If the user approved-and-hand-off:

- Milestone-tagged item → *"Run `/rota-plan <milestone>-<ID>` next."*
- Untagged item → *"Run `/rota-plan` next (it will prompt for milestone)."*

If the user picked *Stop here* → exit without a `/rota-plan` nudge.

## Auto-loop mode

Activated by the `--auto-loop` flag. Invoked exclusively by `/rota-work` Step 4 in loop mode when no design exists for a Major + Milestone-tagged item — see `/rota-work`'s Step 4 dispatch directive for the trigger conditions and the inline `Skill`-tool dispatch language. This section describes the run shape once the flag is set; the dispatch decision lives at `/rota-work`'s call site (per the authoring-conventions rule "Imperative rules in autonomy-aware steps must live inline at every dispatch point" convention).

**Orchestrator-model contract.** `--auto-loop` makes design picks autonomously (no `AskUserQuestion`), so it depends on orchestrator-grade design judgment. The contract: this skill is invoked via the `Skill` tool from `/rota-work` Step 4, which loads it inline in `/rota-work`'s session. Since `/rota-work` runs under `models.orchestrator` (per `.rota/config.json`, default `opus`), `--auto-loop` inherits that model. If a future change moves the dispatch to the `Agent` tool, the call site MUST explicitly pass `model: orchestrator` (resolved from `.rota/config.json`) — running `--auto-loop` under the worker model would push design picks onto an execution-tuned model and degrade design quality. The interactive (default) mode has no such constraint; it can run under any model since the user redlines via `AskUserQuestion`.

### Pipeline

For each clarifying question that Step 4 would normally surface to the user, and for each Step 5 approach pick, run three steps in order:

1. **Local-first.** Grep `DECISIONS.md` / `MILESTONES.md` / `KNOWLEDGE.md` via `rota decisions query` / `rota knowledge query`, and `rota glossary read` for any domain terms, on the question's topic keywords. If a matching commitment exists, the answer is "honor the existing commitment" — do **not** log a new `[Auto:Loop]` entry; the existing commitment IS the record.
2. **Bounded web (opt-in).** If unmatched AND the question references an external library, API, or protocol (anything outside the F14 rota surface scan: `/rota-(\w+)`, `rota <verb>` calls, `.rota/*` artifacts), AND `loop.webResearch == true` in `.rota/config.json` (default `false`), call `WebSearch` with a budget of **2 queries per question, 6 queries per design**. Block on results; no async fetch.
3. **Placeholder fallback.** If still unresolved, retain the question literally in the written design's "Open questions" section with `_(Unresolved — surfaced for review)_` after the question text. The auto-write proceeds — never stop the loop.

### Logging

Each fresh pick from step 1 (when no existing commitment matched and you made a new pick) and step 2 produces an `[Auto:Loop]` entry via:

```bash
rota decisions auto-log --topic "<topic>" --title "<rule-title>" --why "<why-text>" --plan-key "<design-key>" --date "$(date +%Y-%m-%d)"
```

Where `<design-key>` is `<milestone>-<itemId>` when the item has a `Milestone:` tag, else just `<itemId>`. The entry follows the standard `DECISIONS.md` template, but **only the rule and `*Why.*` are auto-filled**; `**Forbids.**` and `**Permits.**` stay as `_(Unresolved — user must articulate)_` placeholders. A footer comment encodes provenance: `<!-- [Auto:Loop] <design-key> <date> — review and articulate Forbids/Permits -->`. The verb is idempotent on `(topic, rule-title)`.

After all questions and the approach pick are resolved, write the design via `rota design add --auto-loop` + `Edit` (per Step 7). The design's "Open questions" section lists every step-3 placeholder verbatim.

### Surfacing

`/rota-brainstorm --auto-loop` itself does not surface auto-decisions to the user — surfacing fires only on terminal paths (`/rota-work` empty-backlog branch, `/rota-work` guard-fail branch, `/rota-pause`) via `rota decisions auto-since`. The user sees the running summary at session end, articulates `Forbids/Permits` in `DECISIONS.md`, and removes the `<!-- [Auto:Loop] -->` footer.

## Anti-pattern guard

> *"This item is too simple to need a design"* is rationalization. Every brainstormed item gets a design pass, but the design can be short — a few sentences in Goal and Design with empty Approaches-considered is a valid output for a truly simple item. The artifact captures the negotiated outcome, not a word count.

## Key Principles

- **No noise.** Don't narrate context loads; don't recap discovery rounds; don't pad the artifact with adjectives.
- **Design is negotiable; the artifact captures the negotiated outcome.** Step 9 is where alignment lands on disk.
- **Soft input to `/rota-plan` — never required.** Plans without a design stay valid; the `design:` pointer is additive.
- **Scope is single-item.** Project-level exploration is `/rota-vision`; slice-level is `/rota-plan` slice mode.

## References

- [`references/design-exploration.md`](references/design-exploration.md) — Shared spine (Socratic discovery, 2-3 approaches, sectioned design with per-section approval, self-review, user-review gate) used by `/rota-vision` and `/rota-brainstorm`.
