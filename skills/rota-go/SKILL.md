---
name: rota-go
description: Use on "/rota-go 42", "just do this issue and land it", "fast track 42" for solo work that should skip slow tests and review. Never inside a round.
---

# rota-go

Fast track: implement an existing issue and land it with only the cheap checks. No brainstorm, plan, TDD, smoke, `go test`, or reviewer agent, on purpose. Solo use only.

## Refuse in a round

If `ROTA_SLOT` is set, or the brief says you are a round worker, stop: *"/rota-go is solo-only; a round worker follows `skills/references/worker-contract.md` and the orchestrator's gate."* Do nothing else.

## Flow

1. **Claim.** `rota work start --json <ID>`. Handle `items[].claim`, `ready: false` and exit 1, 3 and 4 as `/rota-work` Step 1 does. Read the issue and its thread; if it is wrong or already done, say so and stop.
2. **Implement.** Edit directly, smallest change that meets the acceptance bullets. Commit on the branch. Subagents are optional.
3. **Cheap checks.** Only `gofmt -l .` (must print nothing), `go vet ./...`, `go build ./...` (the project's equivalents elsewhere). Never `go test`, `bash test/smoke.sh`, `rota test run`, or `/rota-review`.
4. **On failure,** fix and rerun all three. Never land with a failing cheap check. If you cannot fix it, stop and report.
5. **Land.** `rota ship strategy --json`, then:
   - `direct`: `rota ship merge <branch> --body-file -` with a subject starting `merge: ` and the footer below. Exit 4 `manual gate`: ask, rerun with `--confirm --confirm-note`.
   - `pr` (also a tracker, which forces it): `rota ship pr <branch> --items <ID> --body-file <f>` with the footer below.
6. **Close out.** `rota ship finish --json <branch> [--commit <data.sha>]`; act on its JSON as `/rota-ship` Steps 8 to 9.5 do. On a tracker the PR merge closes the issue.

## Footer (merge commit or PR body)

```
Landed via /rota-go: slow tests and review skipped. Cheap checks run: gofmt, go vet, go build.
```

Say the same in your final message so the skipped checks are visible.
