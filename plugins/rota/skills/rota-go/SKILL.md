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
   - `direct`: `rota ship merge <branch> --body-file - --json` with a subject starting `merge: ` and the footer below. Read the JSON even on a nonzero exit. Exit 4 with `data.blockedBy: "branch moved"` and `data.changed: true` means the pinned commit landed and the branch was kept: save `data.sha`, continue Step 6 for the pinned work, and never retry the merge. Tell the user that `data.pinned` landed on `data.base` as `data.sha`; the branch remains at `data.tip`, and commits added after the pin need their own review and merge. Exit 4 `manual gate`: ask, rerun with `--confirm --confirm-note`. Verdict refusals and aborted conflicts stop the flow.
   - `pr` (also a tracker, which forces it): write the body to a file first: a summary, the `Closes #<ID>` line, `## Assumptions` and `## Rulings` (`None.` when empty), then the footer below. Then `rota ship pr <branch> --title "<issue title>" --body-file <f> --items <ID>`.
6. **Close out.** `rota ship finish --json <branch> [--commit <data.sha>]`; on the direct path always pass `--commit <data.sha>` from the merge result, including when the branch was kept. This scopes bookkeeping to the pinned work in `M^1..M^2`, where `M` is that merge commit, and excludes later work on the branch. Act on its JSON as `/rota-ship` Steps 8 to 9.5 do. On a tracker the PR merge closes the issue.

## Footer (merge commit or PR body)

```
Landed via /rota-go: slow tests and review skipped. Cheap checks run: gofmt, go vet, go build.
```

Say the same in your final message so the skipped checks are visible.
