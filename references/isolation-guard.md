# Isolation guard — parallel waves require worktree isolation

Shared reference for `/rota-work` Step 5's *Isolation guard*: when the guard fires, why it exists, and what it forbids / permits. The SKILL.md keeps a one-paragraph summary and a pointer here; the longer rationale lives in this file.

> **Boundary note.** The *Why this guard exists* + **Forbids** / **Permits** block below is decision-record material. A future `/rota-decide` cycle should promote it to a dedicated `.rota/DECISIONS.md` entry titled *"Parallel waves require worktree isolation"* (Skill Authoring topic). Until then, this reference doubles as the canonical home; SKILL.md cites here.

## When the guard fires

Under the default introduced in F11, workers write files only and the orchestrator commits per task in Step 7.5 — the index race the guard exists to prevent cannot occur because workers never touch `.git/`. The guard remains as defense-in-depth: it fires only when a brief explicitly asks workers to commit (the legacy / opt-in pattern documented in `/rota-work` Step 6 *Alternative: legacy worker-commits*) and ≥2 such workers run in parallel under branch isolation.

Before any worker is dispatched, if the planned wave has **≥2 commit-producing parallel workers** AND `work.isolation == "branch"`, **abort fatally**:

> Error: this wave plans to dispatch <N> parallel workers (<task IDs>) under branch isolation. The 2026-05-02 isolation decision in `.rota/DECISIONS.md` requires `work.isolation: "worktree"` for ≥2 parallel commit-producing workers in a single wave — branch isolation forces all workers to share `.git/index`, which races even on disjoint files.
>
> Resolve by either:
> - Re-plan the wave to a single worker (sequential commits within one task).
> - Run `rota config set work.isolation worktree`.

This guard is **fatal**, not warn-and-proceed. It fires regardless of umbrella mode.

A wave is "commit-producing" by default; "read-only" workers (research, lint-only verifications, smoke validators that don't commit) are exempt — count only workers whose brief instructs them to stage and commit.

## Why this guard exists

Caught on M02-S01 Wave 1: four parallel workers running under branch isolation produced two failure modes against the shared `.git/index` — `index.lock` collisions (poll-and-retry survived these), and an undetectable index-sweep where Worker A's staged file landed in Worker B's commit. T1's new umbrella-resolver script was orphaned that way; T4's worker had to `git reset --soft` and re-stage, the orchestrator re-committed T1 standalone, and the implementation history is now obscured by recovery commits. The fix is structural: each worker on its own worktree → its own index → no race.

## Forbids

- `/rota-work` dispatching ≥2 parallel workers in the same wave when `work.isolation == "branch"`.
- Worker briefs that ask multiple agents to run `git add && git commit` against the same `.git/` concurrently.
- Plan-as-artifact wave layouts that put 2+ commit-producing tasks in a single wave under branch isolation.
- Suppressing the guard via env-var overrides or "just this once" exceptions.

## Permits

- Parallel `/rota-work` waves under `work.isolation == "worktree"` (each worker has its own worktree, its own index).
- Serial waves of size 1 under either isolation mode (no concurrent index access).
- Multi-task waves where only ONE worker commits and the others are read-only (research, smoke-validate, lint-only verifications).
- The file-disjointness rule from `KNOWLEDGE.md` — this guard is **additive** to that one, not a replacement.

## Cited by

- `/rota-work` Step 5 — *Create Branch or Worktree*
