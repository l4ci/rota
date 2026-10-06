# Opt-in ship gates (Steps 3.5 and 3.75)

## Step 3.5 — Second-Opinion Gate

The `/rota-review` reviewer shares context with the work it produced and normalizes its blind spots. This gate gives a fresh subagent only the diff and the goal:

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

Review and second opinion judge the diff; QA runs the product. Invoke `Skill(skill="rota-qa", args="run")` (umbrella: `args="run --repo $REPO"`), then:

```bash
rota verdict route <branch> --for ship-qa --json
```

Exit 3: `/rota-qa` recorded nothing; stop and rerun it. Route per the Step 3 table (`qa.gate` decides advisory versus blocking inside the verb).
