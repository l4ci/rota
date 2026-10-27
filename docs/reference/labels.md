# Tracker labels

Generated from `internal/labels`; do not edit this page by hand. Regenerate the defaults with `go generate ./internal/labels`.

`rota labels sync` compares the effective registry and project declarations with the tracker. It previews missing definitions, wrong descriptions or colors, overlapping tracker defaults, unused claims, and unknown labels. `--apply` creates missing definitions and repairs metadata on GitHub, GitLab and Linear. It never deletes or renames a label. Jira and It's a Plan report names only; their sync makes no writes.

Open prefixes below describe existing definitions only. Sync does not create a literal prefix label. A claim used by no open issue is reported and left unchanged. Retired labels (including `site:support`) are unknown and left unchanged, as are overlapping tracker defaults (`bug`, `enhancement`, `documentation`).

`issues.labels.*` overrides the configured names and prefixes. Declare project labels in `issues.labels.extra` as a JSON object of name to description; exact registry entries retain their own metadata. Labels read through `review.crossLabels`, `review.lenses`, or `ship.review.labels` also count as known. The registry documents who applies and reads each definition and when it leaves an issue; sync never removes definitions.

For a project-specific page, run `go run ./internal/labels/genref --config .rota/config.json <output.md>`. This uses the same resolved entries as sync, including custom names and project declarations.

Colors are six-digit RGB: blue for milestones and sizes, purple for routing and review, red for defects and blocked work, orange for priorities and review changes, yellow for round decisions, green for released work, and pale blue for project labels and tasks. Existing colors change only with `--apply`.

| Name or prefix | Description | Color | Group | Config | Sets | Reads | Removed from issue |
|---|---|---|---|---|---|---|---|
| `in-progress` | Work is held by a worker. | `1d76db` | state | `issues.labels.inProgress` | item claim/state | backlog; rounds; gate | release or next state |
| `needs-review` | Work is ready for review. | `5319e7` | state | `issues.labels.needsReview` | ship; item state | rounds; review queue | review result or next state |
| `changes-requested` | Review feedback needs another pass. | `d93f0b` | state | `issues.labels.changesRequested` | review; item state | backlog; rounds | next state |
| `released` | Work shipped in a release. | `0e8a16` | state | `issues.labels.released` | release | backlog; release | retained |
| `not-planned` | Closed without a planned implementation. | `cfd3d7` | state | `issues.labels.notPlanned` | item complete; tracker close | backlog; tracker close reason | reopen or next state |
| `blocked` | Work is blocked and remains open. | `b60205` | state | `issues.labels.blocked` | item complete --reason blocked | backlog | next state |
| `needs-human` | Work is handed to the maintainer. | `b60205` | state | `issues.labels.needsHuman` | round transfer --to human | round candidates | return to worker queue |
| `milestone-tracker` | Tracking issue for a rota milestone. | `0052cc` | milestone | `issues.labels.milestoneTracker` | milestone create | milestone lookup; backlog filtering | retained |
| `type:bug` | A defect to fix. | `d73a4a` | type | `issues.labels.types.bug` | item create; migrate | backlog type | type change |
| `type:feature` | A capability to build. | `a2eeef` | type | `issues.labels.types.feature` | item create; migrate | backlog type | type change |
| `type:task` | A maintenance or supporting task. | `c5def5` | type | `issues.labels.types.task` | item create; migrate | backlog type | type change |
| `p0` | Bug priority; lower numbers are more urgent. | `d93f0b` | priority | `issues.labels.priorityPrefix` | item create; migrate | backlog ordering | priority change |
| `p1` | Bug priority; lower numbers are more urgent. | `d93f0b` | priority | `issues.labels.priorityPrefix` | item create; migrate | backlog ordering | priority change |
| `p2` | Bug priority; lower numbers are more urgent. | `d93f0b` | priority | `issues.labels.priorityPrefix` | item create; migrate | backlog ordering | priority change |
| `p3` | Bug priority; lower numbers are more urgent. | `d93f0b` | priority | `issues.labels.priorityPrefix` | item create; migrate | backlog ordering | priority change |
| `size:Major` | Feature implementation size. | `0052cc` | size | `issues.labels.sizePrefix` | item create; migrate | backlog; round tier; review depth | size change |
| `size:Minor` | Feature implementation size. | `0052cc` | size | `issues.labels.sizePrefix` | item create; migrate | backlog; round tier; review depth | size change |
| `size:Cosmetic` | Feature implementation size. | `0052cc` | size | `issues.labels.sizePrefix` | item create; migrate | backlog; round tier; review depth | size change |
| `claimed:*` | Worker and machine holding an open issue. | `1d76db` | claim |  | item claim | round claim arbitration; doctor | release, return or completed merge |
| `harness:claude` | Harness selected for this issue. | `5319e7` | routing |  | maintainer; round assign | round worker selection | explicit routing change |
| `harness:codex` | Harness selected for this issue. | `5319e7` | routing |  | maintainer; round assign | round worker selection | explicit routing change |
| `harness:opencode` | Harness selected for this issue. | `5319e7` | routing |  | maintainer; round assign | round worker selection | explicit routing change |
| `model:*` | Model selected for this issue. | `5319e7` | routing |  | maintainer | round worker selection | explicit routing change |
| `machine:*` | Machine assigned to this issue. | `5319e7` | routing |  | round support; maintainer | round candidates; dispatch | return or assignment change |
| `decision:research` | Decision workflow requested for this issue. | `fbca04` | round |  | maintainer | round worker selection | decision resolved |
| `decision:grilling` | Decision workflow requested for this issue. | `fbca04` | round |  | maintainer | round worker selection | decision resolved |
| `status:planned` | Milestone lifecycle status. | `0052cc` | milestone |  | milestone status | milestone read | next milestone status |
| `status:active` | Milestone lifecycle status. | `0052cc` | milestone |  | milestone status | milestone read | next milestone status |
| `status:shipped` | Milestone lifecycle status. | `0052cc` | milestone |  | milestone status | milestone read | next milestone status |
| `status:archived` | Milestone lifecycle status. | `0052cc` | milestone |  | milestone status | milestone read | next milestone status |
| `review:claude` | Cross-harness review routing or explicit opt-out. | `5319e7` | review |  | maintainer | review depth; merge gate | explicit routing change |
| `review:codex` | Cross-harness review routing or explicit opt-out. | `5319e7` | review |  | maintainer | review depth; merge gate | explicit routing change |
| `review:opencode` | Cross-harness review routing or explicit opt-out. | `5319e7` | review |  | maintainer | review depth; merge gate | explicit routing change |
| `review:none` | Cross-harness review routing or explicit opt-out. | `5319e7` | review |  | maintainer | review depth; merge gate | explicit routing change |
| `partial-slice` | A PR may land one slice without closing this issue. | `fbca04` | round |  | maintainer | merge gate closing check | final slice or explicit removal |
| `refactor` | Architecture review or refactoring work. | `c5def5` | round |  | round architecture; maintainer | round architecture candidates | retained |
| `needs-info` | More information is needed before dispatch. | `fbca04` | round |  | maintainer | round candidates | information supplied |
| `best-of:2` | Two independent attempts compete for one issue. | `fbca04` | round |  | maintainer | round assign; best-of merge gate | explicit removal |
| `risk:high` | Require a cross-harness review for high-risk work. | `b60205` | review |  | maintainer | review.crossLabels | explicit removal |
