# After-work QA (Step 12)

Loaded by `SKILL.md` Step 12 when `qa.afterWork` is on.

Read `rota config show qa.afterWork` (default `false`). `false` → skip silently. `true` → check the touched files against the `Watch globs` of the `.rota/qa/*.md` strategies (umbrella: `.rota/qa/<REPO>.md`); on a match, invoke `/rota-qa run` for the item just finished (umbrella: `/rota-qa run --repo $REPO`). No strategy or no match → skip silently. The verdict is advisory here; route nothing on it. Skip in a round worker (`references/worker-contract.md`): QA is the orchestrator's or `/rota-ship`'s call (`ship.qa`).
