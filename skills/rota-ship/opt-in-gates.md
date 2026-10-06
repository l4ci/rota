# Opt-in ship gates (Steps 3.5 and 3.75)

Config these steps read (`rota config show`):

- `ship.secondOpinion` — `false` (default); `true` runs a no-prior-context adversarial review after `/rota-review` (Step 3.5), except for round PRs, which never get one.
- `ship.qa` — `false` (default); `true` runs `/rota-qa run` after the reviews (Step 3.75).

## Step 3.5 — Second-Opinion Gate

Skipped for a round worker's PR (`round-worker-and-issue-mode.md`). Also skipped when `ship.secondOpinion` is `false`, when Step 3 was skipped and the user has not asked for a second opinion this session, or when `REVIEW_CHOICE == ship-anyway`.

A fresh subagent gets only the diff and the goal:

```bash
rota review brief [--repo "$REPO"] <branch>
```

Dispatch the brief verbatim to a fresh `standard` subagent (`Agent` with `subagent_type: "general-purpose"`, `model: "sonnet"`, `description: "Second-opinion review of <branch>"`). It returns a report ending in a fenced `json` verdict block. Save the block to a temp file, then:

```bash
rota verdict add <branch> --kind second-opinion --verdict <PASS|CONCERNS|FAIL> --body-file "$VERDICT" --json
rota verdict route <branch> --for ship-second-opinion --json
```

Exit 2 from `add` names the malformed field: ask the agent to resend; never guess a verdict. Route per the Step 3 table.

## Step 3.75 — QA Gate

Skipped when `ship.qa` is `false` or `REVIEW_CHOICE == ship-anyway`. If there is no `.rota/qa/` strategy for the scope (single repo: no `.rota/qa/*.md`; umbrella: no `.rota/qa/<REPO>.md`), say *"`ship.qa: true` but no QA strategy for `<scope>`. Run `/rota-qa first-run` to bootstrap, or set `ship.qa: false` to skip."* and continue.

Invoke `Skill(skill="rota-qa", args="run")` (umbrella: `args="run --repo $REPO"`), then:

```bash
rota verdict route <branch> --for ship-qa --json
```

Exit 3: `/rota-qa` recorded nothing; stop and rerun it. Route per the Step 3 table (`qa.gate` decides advisory versus blocking inside the verb).
