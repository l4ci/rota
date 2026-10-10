# Architecture review

Loaded by `skills/rota-orchestrate/SKILL.md` section 3 when an `architecture` line shows a review is due.

`rota round status`, `candidates` and `start` carry an `architecture` line: `architecture review in N issues`. After every `wait`, run `rota round architecture`. It does nothing until a review is due: `round.architectureEvery` closed non-refactor items (default 20, `0` is off), or an idle slot with no ready candidate. When due it mints one `arch(<area>): architecture review` item per area (`round.architectureAreas`, else the subsystem map, else the whole repo), assigns them to idle slots and restarts the count; don't ask first. Leftover review items are ordinary candidates in every scope. Each runs `/rota-refactor <area>` in findings-only mode and files `refactor`-labelled issues, which don't count toward the next review. `rota round architecture --check` only reads. Under solo it returns each `brief` and `worktree` to launch like an `assign`.
