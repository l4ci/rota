# Behavioural evals for skills

`test/validate-skills.py` and `test/doclint.sh` check skill *text*. Nothing there shows
that a model still *does* the right thing after a text edit. These evals run a model
against the skills. Origin: #279, from the #248 audit against Anthropic's skill authoring
guide ("build evaluations first": three per skill, tested on Haiku, Sonnet and Opus).

They are **not** in the merge gate: every live run costs model calls (#279 out of scope).
The offline case check (`--check`) and runner regressions (fake subprocess responses)
run in `test/doclint.sh`. Run the regressions alone with
`python3 -m unittest discover -s test/evals -p 'test_*.py'`.

```bash
python3 test/evals/run.py --check                  # offline: case files are well-formed
python3 test/evals/run.py triggers  --model haiku  # 32 trigger cases, about $1 on haiku
python3 test/evals/run.py scenarios --model opus   # 12 scenarios, under $1.50
python3 test/evals/run.py scenarios --model sonnet --only work-no-red rota-ship --out /tmp/r.json
```

Needs the `claude` CLI, logged in. `--model` takes `haiku`, `sonnet`, `opus`.

Failed calls and invalid JSON responses fail the affected case; the remaining cases
still run. Trigger replies must be a single `SKILL: <installed-name>` or `SKILL: none`
line. Empty replies, extra text and unknown skills fail validation before load/skip
scoring. Failures appear in the console and in `--out` results, and make the run exit nonzero.

## Cases

**`triggers.json`**: one request that should load each of the 16 skills (`load`) and one that
should not (`skip`, with the skill that fits better in `better`). The model sees every skill's
`description` and names one skill or `none`. Run it after editing any description.

**`scenarios/<skill>.json`**: for `/rota-work`, `/rota-ship` and `/rota-capture`, in the
guide's shape: `query` (what the user typed), `situation` (the repo state and simulated command
results, standing in for the guide's "files"), `expected` (the behaviour in words) and the
regex checks that score it:

| Field | Meaning |
|---|---|
| `must` | pattern must appear; `{"re": ..., "min": N}` for a count |
| `mustNot` | pattern must not appear; `{"re": ..., "max": N}` allows up to N |
| `order` | patterns must appear in this order |

The model gets the `SKILL.md` as its system prompt, no tools, and lists its next actions as
`CMD:`, `ASK:` and `SAY:` lines. Forbidden commands are anchored to `^CMD:` so an explanation
that names a command does not trip them.

## Limits

- Simulated: nothing executes, so a scenario tests the decision (run the guard, ask before
  `--apply`, skip review on a worker branch), not the `rota` verbs. `test/sections/` covers those.
- Only `SKILL.md` is loaded. Anything a skill delegates to `skills/references/` is out of reach.
- Regex scoring is blunt. A fail needs a read of the output (`--out`) before it counts as a
  regression; an `expected` line says what a human should look for.
- One run per cell, and model output varies: rerun a failing case before acting on it.
- Scenarios that reach a gate say so ("stop at the confirmation question"), because the harness
  tells the model to carry on as if the Recommended option was picked.

## Baseline (2026-10-05, one run each, `claude -p`, default effort)

| | Haiku | Sonnet | Opus |
|---|---|---|---|
| Trigger cases (32) | 32 | 32 | 32 |
| Scenarios (12) | 10 | 12 | 12 |

Raw outputs: `baseline/<model>-{triggers,scenarios}.json`. Misses on Haiku:

- `work-dirty-tree`: states the dirty-tree stop but never prints `rota git guard clean`, so it
  reports a guard result it did not run. Weak signal: the situation hands it the result.
- `capture-major-nudge`: creates the Major feature without the `## Out of scope` body section.
  A real miss against Step 6.

The first scenario drafts failed 5 to 7 of 12 on every model. Every one of those was the case
or harness (a situation that gave away the guard result, a gate the model was told to pass,
forbidden patterns matching prose), not the skill. Expect the same on the next case you write:
read the output before changing a skill.

## Automate?

Not in the gate. A rolling recommendation:

- **Keep `--check` in doclint** (done): free, stops the case files rotting when a skill or its
  description is renamed.
- **Triggers are saturated** (32/32 on all three models), so they catch only a gross
  description regression. Run them on Haiku (about $1) when a description changes.
- **Scenarios discriminate** (Haiku misses two) and are the useful half. Run them by hand on
  Sonnet and Haiku before merging a change to the `SKILL.md` of `work`, `ship` or `capture`, and
  note the result in the PR.
- If skill-text regressions keep escaping, add a manual `workflow_dispatch` job that runs both
  modes on Haiku and Sonnet (about $4). Revisit once there are 30 or more scenarios, or one
  flaky-gate escape that an eval would have caught.
