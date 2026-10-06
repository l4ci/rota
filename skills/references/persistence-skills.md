# Persistence skills

Used by `/rota-learn` and `/rota-decide` — the duo that writes durable project state into `.rota/<FILE>.md` and re-renders a managed block in `CLAUDE.md` so read-side skills (`/rota-work`, `/rota-debug`, `/rota-plan`, `/rota-refactor`, `/rota-review`, `/rota-vision`) can consult it.

`/rota-learn` carries two modes — passive learnings written as bullets under topic headings in `.rota/KNOWLEDGE.md`, and term entries written as nested-bullets under the pinned `## Glossary` topic of the same file (via the `--term <name>` flag). Both modes share the same writer-skill surface.

The two skills share one **contract** but different **gate strengths**. New persistence skills should match the contract; their gate strength is a design pick, not a free-form decision.

## The contract

Every persistence skill (and `/rota-learn`'s `--term` mode) follows:

1. **Opens with a step checklist** in the format of `authoring-conventions.md`, one line per step heading. Step count and names are skill-specific.
2. **Identifies a candidate** (learning / term / decision) from arguments, conversation context, or a source artifact. The shape of this step is intentionally skill-local.
3. **Classifies into a section heading** — by topic for both `/rota-learn` topic bullets and `/rota-decide`; by term name for `/rota-learn --term` (the term entry lands under the fixed `## Glossary` topic). Topic-keyed branches share the alphabetical-with-pinning rule below.
4. **Merges via a writer verb** that owns insertion, deduplication, and the date stamp:
   - `/rota-learn` (topic bullets) → `rota knowledge add`
   - `/rota-learn --term` → `rota glossary write`
   - `/rota-decide` → `Edit` directly on `.rota/DECISIONS.md` (no writer verb)
5. **Regenerates the managed index block** in the instructions file (`AGENTS.md` when it exists, else `CLAUDE.md`; the verb resolves it) via `rota block`. The block is the always-on signal to read-side skills:
   - `/rota-learn` (both modes) → `rota block knowledge` (`--term` runs it internally via `rota glossary write`; Glossary surfaces as a topic name in the Knowledge index automatically)
   - `/rota-decide` → `rota block decisions`
6. **Confirms via a compact block** — *"Captured `<artifact>` into `.rota/<FILE>.md`… Updated CLAUDE.md `<block>` block."* Match the shape; don't recap the plan.

The duo does **not** commit. `.rota/KNOWLEDGE.md`, `.rota/DECISIONS.md`, and `CLAUDE.md` are all tracked under the partial-ignore model, so the duo leaves three working-tree diffs and lets the caller (the user, or a parent `/rota-work` cycle) commit them as one summary. Aligning here matters — the duo is dispatched in sequence under `autonomy.level: auto`, so a per-skill commit would fragment what should be one summary commit.

## Topic-classification rule (rota-learn topic bullets ↔ rota-decide)

Both topic-keyed branches share:

- **Reuse existing `## Topic` headings** when they fit. Create a new topic only if nothing fits.
- **Reuse topic names across the two files** where overlap exists (`Architecture`, `Testing`, `Build & Tooling`, etc.) so a single topic name maps to both `KNOWLEDGE.md` and `DECISIONS.md`.
- **Order new topics alphabetically**, with the exception that `Architecture`, `Build & Tooling`, and `Glossary` may be pinned near the top of `KNOWLEDGE.md`.
- **Coarser is better than per-entry.** Don't mint a topic per bullet (`/rota-learn`) or per rule (`/rota-decide`).

`/rota-learn --term` is term-keyed and always writes to the fixed `## Glossary` topic — the topic-classification rule does not apply.

## Intentional divergences

The gate strengths are by design. The active/passive distinction lives here:

| Aspect | `/rota-learn --term` | `/rota-learn` (topic bullet) | `/rota-decide` |
|---|---|---|---|
| Capture trigger | explicit `--term <name>` (or auto from definitional signals like *"by X I mean..."*) | auto in `auto`, nudge in `off` | always manual |
| Confirmation gate | conditional (only on existing-term conflict — alias collision is the gate; same-name updates are silent) | **none** — Step 4 explicitly auto-writes | **manual gate**, always |
| Verifier | none | Opus opt-in (`--strict` or `learn.verify`) | none |
| Source-prefill flags | `--def`, `--alias`, `--not`, `--touch` | none | `--from-learning`, `--from-spike` |
| Active vs passive | vocabulary (low-risk additive) | passive ("remember if relevant") | active commitment (forbids + permits) |

A future skill author looking at this table should read it as: **these are not bugs to file**. The gate-strength column encodes the project's policy on what costs the user *must* approve. `/rota-decide` always asks because writing a forbids/permits constrains future work; `/rota-learn` topic bullets never ask because passive content is cheap to amend; `/rota-learn --term` only asks on alias collision because adding a fresh term is additive.

If a new persistence skill needs a different gate, choose deliberately from {none, conditional, manual}; don't invent a fourth shape.

## What this reference does NOT cover

- **The user-facing distinction.** `docs/usage/learning.md` and `docs/usage/decisions.md` explain the duo to users — terminology + gotchas vs. boundaries. This reference is for skill authors.
- **Manual gates inventory.** The list of always-manual sites across all skills (not just this duo) lives in `references/manual-gates.md`.
- **Knowledge & decisions consult.** The read-side pattern (verbs, carrier semantics, parallelism) lives in `references/knowledge-consult.md`.
- **Umbrella-mode scoping.** The model is in *Umbrella scoping* below.

## Umbrella scoping

How KNOWLEDGE.md, DECISIONS.md and the Glossary behave in an umbrella project (a root repo with registered sub-repos). The hard boundary is `.rota/DECISIONS.md` *"Persistence-trio scoping under umbrella mode"* (Architecture); this section describes the model and does not re-decide it. Changing the model means revisiting that decision first.

**KNOWLEDGE.md is hybrid.** `.rota/KNOWLEDGE.md` (always present) holds cross-repo learnings and umbrella Glossary terms. `.rota/knowledge/<name>/KNOWLEDGE.md` (created on first write or by `rota init` umbrella setup) holds that sub-repo's learnings and Glossary terms. Learnings that apply across repos go in the umbrella file; one-repo learnings (*"`web`'s Postgres pool config differs from `api`'s"*) go in that repo's file.

**DECISIONS.md is umbrella-only.** One `.rota/DECISIONS.md` at the umbrella root, never split per sub-repo: hard boundaries are cross-repo. A truly repo-local "decision" is a learning; use `/rota-learn`.

**Glossary follows KNOWLEDGE's scoping.** The `## Glossary` topic in each file holds that scope's terms. Glossary skips the tier lifecycle: terms are canonical when written, not probationary.

**Scope resolution**, highest priority first:

1. `--repo umbrella|<name>` always wins.
2. cwd inside a registered sub-repo selects that sub-repo. At the umbrella root, skills ask once via `AskUserQuestion` (umbrella-shared vs a specific sub-repo).
3. Single-repo projects always resolve to `umbrella`.

The scoped `rota knowledge` and `rota glossary` verbs resolve the target file and tier sidecar from the global `--repo` flag or the cwd.

**Readers are hybrid** when the scope is a sub-repo: `rota knowledge query` and `rota glossary read` read both the umbrella and the sub-repo file, with a `> from: <path>` provenance line before each block. At umbrella scope (root or `--repo umbrella`) only the umbrella file is read. `rota knowledge amend` refuses an ambiguous `(topic, fragment)` that matches entries in both files; pass `--repo` to disambiguate.

**Tier sidecars** are per file: `.rota/knowledge-tier.json` (umbrella) and `.rota/knowledge/<name>/knowledge-tier.json`, each tracking only its own bullets. Glossary is exempt in both.

**CLAUDE.md managed block.** A sub-repo's CLAUDE.md (or AGENTS.md when present) gets `rota block knowledge --repo <name>`, listing umbrella topics plus that sub-repo's own, so a reader in the sub-repo sees the full topic index. The umbrella-root file lists umbrella topics only. Single-repo projects are unchanged.

