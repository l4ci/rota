# Bounce cap

Loaded by `skills/rota-orchestrate/SKILL.md` section 7 whenever you bounce a PR by hand, and when the cap is hit.

A bounce you send by hand counts: run `rota round bounce <issue> --head <pr-head-sha>` before the relay or transfer, and send it only on exit 0. `rota round status` shows each slot's count. The gate counts its own stale and provenance bounces on the same counter. At `round.maxBounces` (default 3, 0 turns the cap off) the verb exits 4: never a further bounce. Pick one of:

- a stronger worker (the default): `rota round transfer <issue> --to <free slot> --tier <heavy|standard> --tier-reason "<why>" --body-file <gap>`. The count stays with the item, so a second failure at the higher tier goes to the human.
- the human: `rota round transfer <issue> --to human --note-file <what is left>`. The gate parks an item the same way when it hits the cap itself.

A re-review of a bounced PR is `/rota-review --since <sha of the last review>`: the fix alone, each earlier finding ADDRESSED or NOT ADDRESSED.
