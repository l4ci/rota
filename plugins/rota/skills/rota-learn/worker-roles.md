# Support and round-worker roles for `/rota-learn`

Loaded by `skills/rota-learn/SKILL.md` Step 1 role check when `round.role` is `support` or `ROTA_SLOT` is set.

- **`support`** (a support machine): never write `.rota/KNOWLEDGE.md`, never run `rota knowledge add` or `tier fold` (both exit 4 `blockedBy: role`). Post each learning on the issue it came from: `rota item comment add <ID> --kind learning --body-file <f>`. A learning with no issue goes upstream, not into a file: a rota defect to `/rota-bugreport`, anything else to `/rota-capture`. Stop after posting; main harvests.
- **Round worker** (`ROTA_SLOT` is set, any role): run only the learning pass of Step 2, once, before `rota worker done`. Post each learning on the issue you worked, as for `support` above (rules and the `Learnings: none` / `Learnings: <n> posted` PR marker: `skills/references/worker-contract.md`). The orchestrator's wind-down run harvests them.
