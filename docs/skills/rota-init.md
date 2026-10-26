---
skill: rota-init
lane: maintenance
order: 90
situation: Fresh project
summary: "Suggest config values from the repo and write the ones you confirm."
not_when:
  - use: rota-decide
    when: you are recording a hard boundary, not a config value
  - use: rota-docs
    when: you want the project's public docs scaffolded or updated
before: []
after: [rota-capture, rota-work]
manual_only: true
flags: []
example:
  source: null
  reviewed: false
  steps:
    - you: /rota-init
    - agent: |
        test.fast = go test ./...   (from Makefile target `test`)
        git.baseBranch = main       (from origin/HEAD)
        forge.provider = github     (from the origin remote URL)
        Set test.fast to `go test ./...`?  A) yes (recommended)  B) other
    - you: A
    - agent: |
        rota config set test.fast '["go test ./..."]'
  changes:
    - confirmed keys written to the rota config
    - nothing is written for keys you decline
---
# /rota-init

Reads the project first, then proposes config values that fit it: the test commands from the manifests, Makefile and CI config (`test.fast`, `test.full`), the base branch and forge from the `origin` remote (`git.baseBranch`, `forge.provider`, `issues.source`), the harness CLIs on `PATH` (`round.harnesses`) and the terminal host (`round.dispatch`, `work.isolation`). Every proposal names the file or command it came from; a key with no signal keeps the schema default and is reported as such. Each choice is one question with the recommended value first (`AskUserQuestion` in Claude Code, a lettered list elsewhere). Answers are written only after you confirm, through `rota setup --set` on a project without `.rota/` and `rota config set` otherwise. On an initialized project it lists only the keys whose current value differs from the suggestion. It does not change `rota init` or `rota setup` defaults and installs no harness or host. Manual only.

