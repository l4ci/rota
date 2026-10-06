# Step 8 — proof missing and `--no-proof`

Loaded by `SKILL.md` Step 8 on exit 4 with `blockedBy: proof missing`.

No `## Proof` row. Record one row per executed check that passed during this ship (the Step 3.75 QA run, or the project's test or smoke command), then rerun `rota item complete`:

```bash
rota proof add <ID> --check "<command that ran>" --result PASS --evidence "<summary line or log path>" --sha <merge-or-last-commit-hash>
```

A `/rota-review` or second-opinion PASS is acceptance, not proof (it runs nothing), so it never becomes a row. If no executed check exists, ask: run the project's test command now and record it (Recommended) / close with `--no-proof` (the user's call, named in the Step 9 report) / leave the item open. Never pass `--no-proof` without that answer.

Step 9 report: if Step 8 left IDs open for lack of proof, append `Unproven (still open): <ID> …`; if the user chose `--no-proof`, append `Closed without proof: <ID> …`.
