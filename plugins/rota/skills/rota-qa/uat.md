# Mode: uat

Loaded by `SKILL.md` on `/rota-qa uat <ID>`, and by `/rota-ship` Step 3.8 when `ship.uat` is `true`. Walks one item's acceptance criteria with the user. Tests already prove most of them; only the rest reach a human. Read-only: it edits no code, stages nothing and records no `rota verdict`.

Invoking it directly always works, whatever `ship.uat` says. The key only decides whether `/rota-ship` calls it.

## Steps

1. **Read the criteria.** `rota item show <ID>`; take the lines under `## Acceptance`. A line is a criterion with its `AC-n` id (unnumbered lines get the next free id, as `rota plan check` reads them). No `## Acceptance` section, or no checkbox lines: say so and stop.
2. **Read the proof.** `rota proof show <ID> --json`; `git rev-parse HEAD`.
3. **Route each criterion.**
   - Its text carries the token `(human)` (any case): it goes to the human.
   - Otherwise it auto-passes when a PASS row at the current HEAD sha has a command as its check (a row from `rota proof record`, never a docs-only `no test seam` row) and that row names the criterion: its id, or the test that covers it. A PASS row that covers nothing in particular does not prove one criterion.
   - A criterion with no such row, a row it cannot read, or an ambiguous match goes to the human. Never guess a pass.
4. **Ask about the human ones, one at a time.** Quote the criterion and ask for `pass`, `fail` or `skip`. Never batch them. `skip` leaves the criterion unjudged and does not fail the run.
5. **Capture each failure.** For a `fail`, ask for one line on what went wrong, then `/rota-capture` a new item: the body quotes the criterion verbatim under its `AC-n` id and that line, and carries `Related: <ID>`. The original item stays open; do not edit its checkboxes or proof.
6. **Report.**

```
UAT <ID>
  auto-passed: AC-1, AC-3   (proof: <check> @ <sha>)
  passed:      AC-2
  failed:      AC-4 -> captured as <new ID>
  skipped:     AC-5
```

## Routing from `/rota-ship`

`ship.uat` is advisory for the PR: a failed criterion is captured, reported in the PR body's Notes and does not block the merge or PR. The user chooses to continue or fix first. `rota worker gate` and the round merge path never run this mode; round PRs stay unattended.

## Out of scope

Auto-diagnosing a failure into a fix plan, keeping a UAT file across `/clear`, and any change to `ship.qa` or the QA strategies.
