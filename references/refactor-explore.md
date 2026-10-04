# `/rota-refactor` exploration agent

Loaded by `/rota-refactor` Step 2 when single-repo mode is in effect (umbrella fanout exits before reaching Step 2 — see `references/refactor-umbrella-fanout.md`).

Dispatch an exploration agent using the configured **orchestrator** model. Pass it the full context of what was already fixed in prior rounds (if any — check recent commits). The agent walks the codebase with an explicit prioritization rule and stop condition, reads files in full, and reports every friction point with file name, line numbers, and why it matters.

## Prompt template

```
Explore [PROJECT] at [PATH]. Walk the codebase with this strategy:

1. PRIORITIZATION — rank files by (inbound-import count desc, lines of
   code desc, mtime desc). Read the top 20% in full; sample roughly one
   in five from the bottom 80%. The interfaces that hurt most when
   wrong are usually the most-imported.
2. NEIGHBORHOOD EXPANSION — for each priority-read file, follow one hop
   of imports and callers and read those too. Do NOT chase 2+ hops;
   stop expanding when adjacency stops surfacing new friction
   categories.

Look for:
- Shallow modules where the interface is nearly as complex as the impl
- Concepts co-owned across multiple files that should live in one place
- Silent failures — errors logged but not propagated or surfaced
- State split across types in ways hard to reason about
- Implicit assumptions baked into data transformations
- Anything requiring 3+ files to understand one concept
- Tightly-coupled modules with integration risk at their seams

[If prior rounds exist]: Do NOT re-surface already-fixed issues: [list them].

For every friction point report: file, approximate lines, the friction,
why it matters, and the dependency category (in-process,
local-substitutable, ports-and-adapters, or true external).

STOP CONDITION — stop searching when either (a) 8–12 friction points
have been surfaced across the categories above (the sweet spot for one
/rota-refactor cycle), OR (b) 30+ files have been read with no new
category in the last 5 reads, whichever fires first. Quality of the
smallest fix list beats raw count — do not pad past 12.
```
