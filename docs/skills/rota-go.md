---
skill: rota-go
lane: execute
order: 42
situation: Quick solo fix
summary: Implement an issue and land it with only gofmt, vet and build; no slow tests, no review.
not_when:
  - use: rota-work
    when: you want per-task verification, tests and the full review chain
  - use: rota-ship
    when: the work is already built and you want it reviewed before it lands
  - use: rota-orchestrate
    when: several issues run in parallel with workers and a merge gate
before: [rota-capture]
after: []
manual_only: false
flags: []
example:
  source: getting-started.md
  reviewed: false
  steps:
    - you: /rota-go 42
    - agent: |
        Claimed #42 on rota/42-fix-timer-badge.
        gofmt, go vet, go build: clean.
        Merged into main. Landed via /rota-go: slow tests and review skipped.
  changes:
    - the fix, merged (or a PR opened per `work.mergeStrategy`)
    - "merge commit or PR body carries the `Landed via /rota-go` line"
---
# /rota-go

Fast track for solo work: claims an existing issue, implements it with no brainstorm, plan or TDD, runs only `gofmt`, `go vet` and `go build`, then merges or opens a PR per [`work.mergeStrategy`](../usage/configuration.md#workmergestrategy-direct-or-pr). The smoke suite, `go test` and the AI review are skipped on purpose, and the merge commit or PR body says so. Cheap-check failures are fixed before anything lands.

It refuses inside a round: workers keep the [worker contract](../contributing/rounds.md) because the orchestrator relies on the gate. Use [`/rota-work`](rota-work.md) when you want tests and review.
