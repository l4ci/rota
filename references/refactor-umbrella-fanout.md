# `/rota-refactor` umbrella fanout

Loaded by `/rota-refactor` Step 2 when the user picks any "fanout" scope — *"All sub-repos"*, *"All + umbrella"*, or *"Pick a subset"*. The umbrella's `.git/` (if any) is handled by the *"Umbrella only"* branch, which stays in the SKILL.md.

This reference covers the dispatch choreography end-to-end: collect umbrella context, build the per-target sub-agent prompt, launch all agents in parallel, aggregate the per-repo summaries, run a single counter reset, and exit before the single-repo Steps 2–10 would run. The sub-agent prompt template embedded below is the one variant fanout uses — single-repo `/rota-refactor` does not consult it.

## Dispatch

Build the target list. For each target, you'll dispatch one sub-agent via `Agent` IN A SINGLE MESSAGE (parallel).

**Collect umbrella context for the sub-agents.** KNOWLEDGE.md and DECISIONS.md live only at the umbrella; sub-agents need their content embedded so they respect cross-repo conventions and decisions. Apply the canonical K+D query pattern (`references/knowledge-consult.md`) against the topics that may apply across the targets. If unsure which topics apply, pass every topic from each file (read the `## Topic` headings and pass all of them) — umbrella fanout has broader topic relevance than a per-step query. Capture the output as `KNOWLEDGE_BLOB` and `DECISIONS_BLOB` for embedding into each sub-agent prompt below.

The umbrella concept — registry, resolution helpers, sub-repo `.git/` distinction — lives in `references/umbrella-mode.md`. This step's per-target dispatch carries those mechanics into each sub-agent's brief.

**Dispatch sub-agents.** For each target (each chosen sub-repo, plus optionally the umbrella when "All + umbrella" was picked), build a single Agent call with this prompt template:

```
You are a /rota-refactor sub-agent operating on a single repo within an umbrella project. Run a focused refactor cycle for THIS REPO ONLY.

Repo: <name>
Path: <abs-path>
Umbrella: <umbrella-abs-path>

## Your steps

1. cd <abs-path>

2. Run `rota git guard clean --context "/rota-refactor" --repo <name>` (`rota` finds the umbrella's `.rota/` by walking up from here). If it exits non-zero (not a git repo, or uncommitted changes), stop and report "no changes" back.

3. Run a focused refactor cycle equivalent to /rota-refactor's Steps 2-9 on THIS REPO:
   a. Dispatch an exploration agent (orchestrator model: <orchestrator from config>) to explore THIS REPO ONLY for friction. Do not walk into the umbrella or other sub-repos.
   b. Triage findings; classify simple vs structural; categorize dependencies (in-process / local-substitutable / ports-and-adapters / true-external).
   c. For structural items, design competing approaches if the umbrella's `.rota/config.json` has `refactor.confirmBeforeExecute: true`; otherwise pick the recommended approach and proceed (the config file lives at the umbrella, not here — read `<umbrella-abs-path>/.rota/config.json`).
   d. Dispatch parallel worker agents (worker model: <worker from config>) for the fixes. File-disjoint fixes run in parallel; same-file fixes go to one worker; sequential dependencies serialize.
   e. Verify with a single verification agent (orchestrator model). Re-fix any FAIL verdicts.
   f. Commit in this repo's `.git/`. One commit for the cycle. Stage modified files explicitly (no `git add -A`).

4. Do NOT:
   - Run `rota refactor reset` — the umbrella orchestrator does that once at the end.
   - Modify the umbrella's `.rota/`, `.claude*/`, or any other sub-repo.
   - Push or create PRs.

## Context for THIS sub-cycle

### Umbrella KNOWLEDGE.md (relevant topics)

<KNOWLEDGE_BLOB>

### Umbrella DECISIONS.md (full)

<DECISIONS_BLOB>

## Return

A compact summary in EXACTLY this format:

repo: <name>
commit: <short hash, or "no changes" if the cycle found nothing to commit>
items: <N>
- <one-line item description 1>
- <one-line item description 2>
...
```

The orchestrator and worker model names embedded in the prompt come from the umbrella's `.rota/config.json` `models.orchestrator` / `models.worker` (defaults `opus` / `sonnet`).

**Wait for all sub-agents to complete**, then aggregate.

## After fanout returns

1. Aggregate the per-repo summaries into a single umbrella-level report.
2. Run `rota refactor reset` ONCE.
3. Print the aggregated report. Format:

```
Umbrella refactor cycle — N targets

repo: web (commit a1b2c3d, 4 items)
  - <item 1>
  - <item 2>
  ...

repo: api (commit e4f5g6h, 2 items)
  - <item 1>
  - <item 2>

repo: <umbrella-name> (no changes)

(Counter reset.)
```

For sub-repos that returned "no changes", skip the bullet list — just note the result.

**EXIT** — skip Steps 2-10 in the SKILL.md (those are the single-repo flow that umbrella fanout has already delegated to sub-agents).
