# Plan-as-artifact check (Step 4)

Loaded by `SKILL.md` Step 4 when the item is tagged to a milestone or slice.

For an item tagged to a milestone (`Milestone: M01` on `B07` → key `M01-B07`) or a slice (`M01-S01`), run `rota plan show <milestone>-<unit> 2>/dev/null`. If a plan exists, use its decomposition, files, verify steps and assumptions as the dispatch briefs; restate user redlines. If the conversation contradicts the plan, ask whether to update it first (`/rota-plan`) or proceed and ignore it.
