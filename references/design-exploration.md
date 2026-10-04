# Design exploration — shared shape

Used by `/rota-vision` (project / milestone scope) and `/rota-brainstorm` (single-item scope) — the two skills that negotiate *what to build and why* before downstream skills capture *how to build it*. Both follow the same five-step spine; they diverge on scope, artifact path, discovery cadence, proposal shape, and two project-specific phases (web research, deliberate challenge) that earn their place at milestone scale but add noise at item scale.

The pair shares the **spine** but diverges on every axis where scope, blast radius, and downstream handoff dictate different defaults. Future design-exploration skills should match the spine and choose deliberately from the divergence axes below — not invent a sixth step or skip one of the five.

## Shared spine

1. **Socratic discovery** — multi-choice clarifying questions via `AskUserQuestion` (≤ 4 options per the picker cap) with plain-text fallback per `references/ask-user-question-fallback.md`. Cadence (one-per-round vs batched) and round budget are scope-dependent — see the divergences table.
2. **Propose before disk write** — present the proposal inline as plain markdown (not yet committed). At milestone scope, the proposal is the milestone list; at item scope, the proposal is 2 or 3 candidate approaches with Shape / Pros / Cons / Why-this-might-or-might-not-be-the-right-answer plus an *"Ask more questions first"* escape.
3. **Iterate before commit** — the user redlines; the skill restates; loop until the user explicitly confirms. The gate shape (free-form redline vs structured per-section approval) is scope-dependent — see the divergences table.
4. **Write artifact via verb** — call the canonical writer verb, then `Edit` placeholder bodies. Frontmatter stays intact. At milestone scope: `rota milestone add` (and a final `rota milestone index` to regenerate the managed CLAUDE.md block). At item scope: `rota design add` (the index is term/topic-keyed and refreshed by separate persistence verbs — not in this skill).
5. **User-review gate** — present the artifact (or invoke a `* show` verb) and ask approve / revise / stop. Plain-text fallback: `approve` / `revise` / `stop`. Default rule: honor yes/no — silence does not approve.

## Per-axis divergences

| Axis | `/rota-vision` (project) | `/rota-brainstorm` (item) |
|---|---|---|
| Scope | Project / milestone-level — multiple milestones can be active at once | Single backlog item (`[B##]` / `[F##]` / `[T##]`) |
| Artifact | `MILESTONES.md` (overview) + `.rota/milestones/<MNN>.md` (per-milestone detail) | `.rota/designs/<ID>.md` (single file per item) |
| Discovery cadence | Batched — single `AskUserQuestion` call with 2-3 questions in Create mode, single question in Edit mode; no hard round cap | One question per round; 5-round cap; plain-text fallback after the fifth; section gates and the final review excluded from the budget |
| Proposal shape | Single milestone list (M01 … MNN) with goal / acceptance / rationale / risks per milestone; no fixed milestone count | 2 or 3 candidate approaches with Shape / Pros / Cons / Why; single-select pick with *"Ask more questions first"* escape |
| Iterate gate | Free-form redline pass (combine, cut, retire, add, re-prioritize); explicit user confirmation before disk write | Sectioned design (Goal → Design → Approaches → Open questions → Assumptions) with per-section approval — `yes` / `changes` / `approve all remaining` |
| Web research step | Yes — dedicated phase between discovery and proposal; gathers external context (prior art, pitfalls, patterns) via parallel `WebSearch` / `WebFetch` | No — local-context only; `/rota-spike` handoff when a question needs code-touching evidence |
| Challenge step | Yes — deliberate counter-position before committing (scope check, risk frontloading, overlap detection, cut tradeoff, dependency surfacing, assumption naming) | No — challenge happens implicitly through approach-pick tradeoffs (Pros / Cons / Why) |
| Self-review pass | No dedicated phase — surfaces inline during Challenge (Step 5) and the iterate loop (Step 7) | Dedicated Step 8 — placeholders, internal contradictions, scope creep, ambiguous adjectives |
| Handoff | `/rota-work` for milestone-driven backlog work, or `/rota-capture` / `/rota-plan` to seed a newly active milestone | `/rota-plan` for per-item implementation plan (soft input — never required) |
| Trigger | *"let's plan"*, *"create a roadmap"*, *"brainstorm milestones"*, *"what's the bigger picture"* | *"brainstorm F03"*, *"design B07"*; auto-invoked for `[Major]` features and `[P0]` bugs under `autonomy.level: auto` |

## Why the divergences stay

**Web research and challenge belong at milestone scope, not item scope.** Project-scope brainstorming has a broad search space — competitor patterns, industry shifts, and architectural prior art are all reachable from a one-paragraph vision, and a wrong call has multi-quarter blast radius. Web research grounds the conversation in outside context; the challenge step deliberately stress-tests the framing before milestones land on disk. Per-item brainstorming is bounded by an already-captured item — the search space is already narrowed by `/rota-capture`'s classification and the item's detail file, so external research adds noise. For genuinely uncertain code-touching questions (*"does library X support feature Y?"*), `/rota-brainstorm` hands off to `/rota-spike` rather than improvising a research phase.

**Cadence and gate-shape track the artifact's structure.** Vision's milestone list isn't section-shaped — milestones are peers, not parts — so a per-section gate would be the wrong primitive; free-form redlining matches the artifact. Brainstorm's design artifact has five fixed sections (Goal / Design / Approaches / Open questions / Assumptions), so per-section approval gives the user precise control over each. Vision's batched discovery exploits Create-mode's known three-axis question shape (Scope / Audience / Constraint); brainstorm's clarifying space is open-ended, so one-per-round avoids overwhelming the user with a wide multi-question call.

**Implicit challenge is sufficient at item scope.** The 2-3-approaches step surfaces tradeoffs in the Pros / Cons / Why-this-might-or-might-not-be-the-right-answer fields; a separate Challenge phase would duplicate that effort. At milestone scope, the broader implication set (which milestones come first, what's parallel, what gets cut) is not visible in any single milestone proposal — Challenge exists to surface it.

## When the spine applies (and when it doesn't)

A new skill belongs in this family when its purpose is **interactive design negotiation before a downstream skill captures the result**. Litmus:

- The skill writes a design / plan / vision artifact the user must approve, not a side-effect-free output.
- The artifact's correctness depends on the user's intent, not just on disk state — Socratic clarifying earns its place.
- The proposal is contestable; picking one shape over another is a real decision, not a default.

Skills that import / generate / one-shot transform (e.g., `/rota-spike` runs a single experiment to answer a feasibility question; `/rota-capture` records what the user dictates) don't fit even if they touch persistent files. `/rota-plan` is a sibling, not a member — by the time `/rota-plan` runs, the design is assumed settled; its job is task decomposition, not negotiation.

## See also

- `references/ask-user-question-fallback.md` — plain-text fallback shape used by every `AskUserQuestion` call in the spine.
- `references/banner-preamble.md` — banner-print rule shared by every skill.
- `references/context-load-protocol.md` — K+D+C parallel-load sequence both skills run before discovery.
- `references/manual-gates.md` — inventory of always-manual sites; both skills' user-review gate appears there.
