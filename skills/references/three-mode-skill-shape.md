# Three-mode skill shape

Used by `/rota-ship` (Docs Mode, accessed via `--docs`) and `/rota-qa` — two patterns that maintain a curated artifact (public user guide, per-target QA strategy) over the project's lifetime in three modes: one-time scaffold, incremental update or execution, on-demand reorganization.

The pair shares the **mode skeleton** but diverges wherever the artifact's audience and lifecycle call for different defaults. A future three-mode skill (say `/rota-architecture` or `/rota-changelog`) should match the skeleton and choose from {public/internal, gated/auto, scaffolded/always-on}, not invent a fourth mode shape.

## The skeleton

Every three-mode skill in this family has:

1. **First-run mode** — interactive scaffold of the canonical artifact. The skill detects an empty or missing target (`<docs.path>/` absent or empty; `.rota/qa/<target>.md` missing), inspects the project to form a hypothesis, proposes a structure, and writes only after explicit user approval (a question with a `(Recommended)` option). Never auto-scaffolds.
2. **After-work / run mode** — for Docs Mode, auto-invoked from `/rota-ship` post-cycle when the cycle's diff touches user-facing surface; reads what changed, maps changes to entries in the artifact, and either proposes edits behind an approval gate or writes them directly. For `/rota-qa`, `run` mode executes the strategy declared in `.rota/qa/<target>.md` and emits a verdict; it does not edit the artifact itself.
3. **Audit/restructure mode** — interactive on-demand reorganization. Surfaces staleness, duplicates, broken commands, and dead strategies; proposes merges, archives, or fixes; applies only on user confirmation.

Both regenerate a managed CLAUDE.md block via an index helper after writing, so read-side skills consult an always-on summary.

## Intentional divergences

The two implementations diverge by design on every operational axis:

| Aspect | Docs Mode (`/rota-ship --docs`) | `/rota-qa` |
|---|---|---|
| Artifact root | `<docs.path>/` — typically `docs/` at repo root | `.rota/qa/<target>.md` — per-target strategy files (umbrella: `<target>` is a registered repo name; single-repo: user-named surface like `web`, `api`, `cli`) |
| Audience | end users (humans) | AI runners + contributors triaging findings |
| Trigger gate | post-cycle trigger condition — see `references/post-cycle-trigger-gate.md` | gated by `ship.qa: true` from `/rota-ship`; also runs on demand from the user |
| First-run opt-in for downstream automation | flips `docs.afterWork: true` on scaffold approval | opt-in via `ship.qa: true` and `qa.afterWork: true` |
| Commit ownership | Docs Mode: own commit (`docs:` prefix) when run inline from `/rota-ship` Step 8.6 or manually via `/rota-ship --docs` | no commits — `/rota-qa` is read-only on the codebase |

These divergences are **not bugs to file**. Audience sets the gate strength (public docs need user approval per batch; QA strategy files face AI runners). Lifecycle decides whether mode 2 edits or executes.

## When the skeleton applies (and when it doesn't)

A skill belongs in this family when its purpose is **continuous curation of a single artifact across the project's lifetime**, not single-shot transformation. Litmus:

- The artifact has a first-time-empty state that needs interactive scaffolding (rules out skills that always have a starting corpus).
- The artifact accumulates entries over the cycle history, not from a one-time import.
- The artifact periodically needs maintenance (stale entries, duplicates, broken links) — not just "append-only growth".

Skills that import, generate or transform once (`/rota-release` cuts a tag; `/rota-spike` runs one experiment) don't fit, even if they touch persistent files.
