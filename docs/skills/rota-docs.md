---
skill: rota-docs
lane: ship
order: 60
situation: Docs drifted
summary: Scaffold, update or reorganize the public docs.
not_when:
  - use: rota-learn
    when: the point is a gotcha for future sessions, not user-facing documentation
  - use: rota-release
    when: you want release notes and a tag
before: [rota-ship]
after: [rota-release]
manual_only: false
flags:
  - flag: "restructure"
    does: audit and reorganize the docs tree; follows autonomy.level
  - flag: "(no argument)"
    does: first-run scaffold if docs.path is missing or empty, else after-work proposals
example:
  source: null
  reviewed: true
  steps:
    - you: /rota-docs
    - agent: |
        After-work mode is on. Checking docs against changes since the last `docs:` commit.
        Proposed updates:
          docs/usage/running-work.md  add the new --preview note
        Apply these doc updates? Apply all / Apply selectively / Skip / Cancel
  changes:
    - doc pages under docs.path, only after you approve
    - "a docs: commit that marks the next after-work baseline"
---
# /rota-docs

The public-docs maintainer. After-work mode may run in any agent, including a round worker's PR; scaffold and restructure follow `autonomy.level` and escalate to the maintainer in a round. Scaffolds `<docs.path>/` on first run (discovery + tailored tree + interactive approval), proposes doc updates in after-work mode, or audits and reorganizes in restructure mode (`/rota-docs restructure`). Runs inline at ship time when `docs.afterWork: true` and the post-cycle trigger condition matches.

`/rota-ship --docs` still works for one release as a deprecated alias: it prints a one-line pointer to `/rota-docs` and runs it.
