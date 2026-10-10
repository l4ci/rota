# Step 3 — Duplicate, Shipped and Decision Check

Loaded by `SKILL.md` Step 3, once per item before `rota item create`. Read-only: it creates nothing.

Pick 2 to 4 distinctive terms from the item (command, skill, symbol, concept), then search:

1. **Items, open and archived.** `rota backlog list --grep "<term>"` for open items. Closed and archived: issue backend `gh issue list --state all --search "<term>"` (`glab issue list --all --search` on GitLab); file backend grep `.rota/ARCHIVE.md`.
2. **Shipped work.** `rota item shipped "<title>"` (exit 0 lists commit hits), plus a grep of the code for the concept. A hit is a location that already does what the item asks.
3. **Decisions.** `rota decisions query <topic>` for the item's topic. A hit is a boundary that forbids or already settled the item.

No hit anywhere: say nothing, ask nothing, continue to the questions. Never spend one of the 2 to 4 questions here; a hit is a finding, not a question.

On a hit, show it with its link (`#N` URL, `[B07]`, commit hash, `path:line`, decision heading), then ask one question per hit, up to 4 per call, option 1 marked `(Recommended)`:

| Hit | Options |
|-----|---------|
| Duplicate item (open or archived) | 1. *"Comment on `<ID>` instead"* (`rota item comment add <ID> --body-file …`, creates nothing) · 2. *"Create and link"* (`--related <ID>`) · 3. *"Create anyway"* |
| Already implemented | 1. *"Skip: it exists at `<location>`"* · 2. *"Create anyway"* (the user says what is missing; put it in `--desc`) |
| Prior decision | 1. *"Skip: `<decision>` settles it"* · 2. *"Create anyway"* |

Create nothing for an item until its hit is answered. A skipped item never reaches Step 6. Hits are heuristic: show them, never decide silently.
