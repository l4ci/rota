# Decisions

Hard boundaries for this project. Each entry is a commitment, not a preference — re-read before proposing changes that touch its area.

Use `/hv-decide` to capture a new decision. `/hv-work`, `/hv-debug`, `/hv-plan`, `/hv-refactor`, `/hv-review`, and `/hv-vision` consult this file when its topics are relevant.

## Architecture

### Persistence-trio scoping under umbrella mode

KNOWLEDGE.md uses a hybrid umbrella+sub-repo model; DECISIONS.md stays permanently umbrella-only; CONTEXT.md is folded into KNOWLEDGE.md as a `## Glossary` topic that follows KNOWLEDGE's hybrid scoping.

*Why.* The original architecture in `references/context-umbrella-scoping.md` framed KNOWLEDGE.md AND DECISIONS.md as always-umbrella-shared, with only glossaries splitting per-sub-repo. The F18 fold (`/hv-learn --term` → `/hv-learn --term`) and the F21 brainstorm surfaced that learnings have BOTH cross-repo and repo-local flavors — forcing them all umbrella-shared drowns repo-specific context (e.g., *"the web sub-repo's Postgres pooling differs from api's"*) in noise that doesn't apply across the umbrella. Decisions are different: hard boundaries are inherently cross-repo by nature; a forbid like "no submodules in umbrella tree" applies everywhere. Repo-local commitments are better captured as learnings. The hybrid model for KNOWLEDGE preserves both invariants — learnings cross repos when they should, stay scoped when they shouldn't. DECISIONS stays umbrella-only by architectural commitment, with the rule that repo-local "decisions" redirect to `/hv-learn`.

**Forbids.**
- Forcing all KNOWLEDGE content into the umbrella file when scope is repo-local — `/hv-learn` and `hv-glossary-write` must offer both target scopes.
- Splitting DECISIONS.md per-sub-repo. If a "decision" is repo-local, it's actually a learning — redirect to `/hv-learn`.
- Re-creating a standalone `.hv/CONTEXT.md` after F18 ships. Terms live in KNOWLEDGE.md's Glossary topic going forward.
- Treating the Glossary topic as a normal tier-eligible KNOWLEDGE topic. Glossary is special-cased to skip the F03 tier lifecycle (terms are canonical, not probationary).

**Permits.**
- Per-sub-repo `.hv/knowledge/<repo>/KNOWLEDGE.md` for repo-local learnings and per-sub-repo Glossary terms (F21).
- Umbrella-shared `.hv/KNOWLEDGE.md` for cross-repo learnings and umbrella Glossary terms.
- All 5 `hv-knowledge-*` helpers (merge, query, index, tier, amend) gaining `--repo umbrella|<name>` handling with auto-resolve fallback (F21).
- DECISIONS.md remaining at the umbrella root in its current single-file form, regardless of how many sub-repos exist.
- Future learnings captured at either scope based on relevance (auto-resolve from cwd inside a sub-repo; ask at umbrella root).

<!-- 2026-05-16 -->
