# Bounce cap

Loaded by `skills/rota-orchestrate/SKILL.md` section 7 whenever you bounce a PR by hand, and when the cap is hit.

A bounce you send by hand counts: run `rota round bounce <issue> --head <pr-head-sha> --slot <slot>` before the relay or transfer, and send it only on exit 0. `rota round status` shows each slot's count. `--slot` names the slot holding the bounced PR; it matters on a best-of:2 pair, where the ledger entry otherwise lands on the first slot holding the issue. The gate counts its own stale and provenance bounces, and `rota round review-relay` its review relays, on the same counter. The verb refuses with exit 4 (`blockedBy: no blocking findings`) when the review verdicts recorded at the PR's current head hold no blocking finding, so a nit never costs a bounce; `--force` counts it anyway. At `round.maxBounces` (default 3, 0 turns the cap off) the verb exits 4: never a further bounce. Three misses on one issue more often mean the criteria are ambiguous than that the model is weak, so audit before you pay for a stronger tier.

1. **Audit the criteria.** Read the issue's `## Acceptance` from the tracker (`gh issue view`), never from memory. For each criterion write at most three lines: is it checkable as written, does it have one reading, and (if not) the second reading or the missing check. Write the audit to a file.
2. **Any criterion ambiguous or uncheckable**: hand the item to the human with the audit as the note: `rota round transfer <issue> --to human --cap-reason ambiguous-criteria --note-file <audit>`. A stronger tier would build the same ambiguity a fourth time.
3. **Every criterion checkable with one reading**: a stronger worker: `rota round transfer <issue> --to <free slot> --tier <heavy|standard> --tier-reason "<why>" --cap-reason tier --body-file <gap>`. The count stays with the item, so a second failure at the higher tier goes to the human.

`--cap-reason` lands in the ledger row of the transfer, and `rota round summary` counts both reasons. The gate parks an item on the human the same way when it hits the cap itself, without an audit. Do the audit by hand; no model runs it.

A re-review of a bounced PR is `/rota-review --since <sha of the last review>`: the fix alone, each earlier finding ADDRESSED or NOT ADDRESSED.
