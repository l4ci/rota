---
chains:
  - id: small-item
    featured: true
    title: Fix a small bug or add a small feature
    when: One clear item, small enough to build in one sitting.
    walkthrough: walkthroughs/quickstart-manual-mode.md
    steps:
      - skill: rota-capture
        why: Write the item down. Capture records it and stops.
      - skill: rota-work
        why: Build it in small commits. The proof gate needs a red run, then a green one.
      - skill: rota-ship
        why: Review the branch, then open the PR or merge.
  - id: big-feature
    title: Build a bigger feature
    when: A Major item where the design or the approach needs sign-off before code lands.
    walkthrough: walkthroughs/round-cycle.md#a-major-item-brainstorm-then-plan
    steps:
      - skill: rota-capture
        why: Record the feature as an item.
        example:
          source: usage/brainstorm.md#one-example-end-to-end
          reviewed: false
          steps:
            - you: /rota-capture "an /rota-archive command that ages out resolved items older than 90 days"
            - agent: |
                [F12] /rota-archive command for old resolved items   Feature, Major
                Run /rota-brainstorm F12 before /rota-plan to negotiate the design.
          changes:
            - a new row under `## Features` in `.rota/BACKLOG.md`
            - nothing else; capture records and stops, it never starts the brainstorm
      - skill: rota-brainstorm
        why: Settle the design with you, section by section.
        example:
          source: usage/brainstorm.md#one-example-end-to-end
          reviewed: false
          steps:
            - you: /rota-brainstorm F12
            - agent: |
                Read F12, KNOWLEDGE.md and DECISIONS.md.
                Question: what signals "resolved"? ## Completed only, or also ARCHIVE.md?
            - you: Completed only.
            - agent: |
                Design drafted: 3 approaches with tradeoffs, recommended the append-only journal.
                Approve and hand off to /rota-plan, approve with no plan yet, or revise?
          changes:
            - the approved design stored at `.rota/designs/F12.md`
            - nothing in code
      - skill: rota-plan
        why: Turn the design into tasks with a way to verify each.
        example:
          source: usage/brainstorm.md#how-it-feeds-rota-plan
          reviewed: false
          steps:
            - you: /rota-plan F12
            - agent: |
                Planning dial: 1 (pointers) - the design is signed off and revert is clean. Override: say "level 1" or "level 4".
                Goal: /rota-archive moves ## Completed items older than 90 days into ARCHIVE.md.
                T1. Append archived items to ARCHIVE.md in internal/backlog/archive.go.
                    Verify: go test ./internal/backlog -run TestArchiveAppend
                T2. Wire the command and the 90-day cutoff.
                    Verify: go test ./cmd -run TestArchiveCutoff
                Confirm this plan? (yes / changes)
            - you: "yes"
          changes:
            - the plan written to `.rota/plans/F12.md`, with a `design:` pointer to `.rota/designs/F12.md`
      - skill: rota-work
        why: Build the plan, one verified task at a time.
        example:
          source: null
          reviewed: false
          steps:
            - you: /rota-work F12
            - agent: |
                Tree clean. Branch rota/F12-rota-archive created. Plan .rota/plans/F12.md: 2 tasks.
                T1  append archived items to ARCHIVE.md
                    rota proof record F12 -- go test ./internal/backlog -run TestArchiveAppend   FAIL (red run)
                    rota proof record F12 -- go test ./internal/backlog -run TestArchiveAppend   PASS
                    c4d5e6f feat: append archived items to ARCHIVE.md [F12]
                T2  wire the command and the 90-day cutoff   PASS
                    d7e8f90 feat: add /rota-archive with a 90-day cutoff [F12]
          changes:
            - a feature branch with one commit per task
            - a proof row per check
            - F12 marked in progress in .rota/status.json until it lands
      - skill: rota-ship
        why: Review against what the item promised, then merge.
        example:
          source: null
          reviewed: false
          steps:
            - you: /rota-ship
            - agent: |
                /rota-review: PASS (checked against the F12 design and its acceptance)
                PR body built from commit subjects and resolved IDs: F12
                Merged into main with --no-ff; branch deleted.
          changes:
            - "F12 under ## Completed in BACKLOG.md, stamped with the merge commit's hash"
            - main carries the feature as one revertable unit
            - status.json entry cleared
  - id: unclear-bug
    featured: true
    title: Chase a bug you don't understand
    when: Something is broken and the cause is not known yet.
    walkthrough: walkthroughs/greenfield-from-brief.md#step-7-a-real-bug-run-rota-debug
    steps:
      - skill: rota-debug
        why: Reproduce it, test one hypothesis at a time, fix it in one commit.
        example:
          source: walkthroughs/greenfield-from-brief.md#step-7-a-real-bug-run-rota-debug
          reviewed: false
          steps:
            - you: /rota-debug F04
            - agent: |
                Reproducer: TestAmountRoundTrip FAILED (0.10 -> 9, want 10)
                H1 (likely): the minor-unit helper multiplies a float64 by 100 and truncates.
                Verified H1: the intermediate float prints 9.999999999999998.
                Fix: decimal Shift(2) instead of Float64() * 100. Reproducer passes.
                fix(money): use decimal.Shift instead of Float64 for minor-unit conversion
                Root cause was non-obvious. Consider running /rota-learn to capture this.
          changes:
            - one atomic fix commit on a `rota/fix-*` branch
            - a proof row for the reproducer
      - skill: rota-ship
        why: Review and merge the fix.
        example:
          source: null
          reviewed: false
          steps:
            - you: /rota-ship
            - agent: |
                /rota-review: PASS
                PR body built from commit subjects and resolved IDs: F04
                Merged into main with --no-ff; branch deleted.
          changes:
            - the fix on main as one revertable unit
            - status.json entry cleared
      - skill: rota-learn
        why: Keep what the bug taught you for the next session.
        example:
          source: walkthroughs/greenfield-from-brief.md#step-9-rota-learn
          reviewed: false
          steps:
            - you: /rota-learn
            - agent: |
                Proposed bullet under ## Money & Decimals:
                Never multiply a decimal.Decimal by a float to convert to minor units. Float64() * 100 gives 9.999... for 0.10, and truncation gives 9. Use decimal.NewFromString(...).Shift(2).IntPart() end to end.
                Written to .rota/KNOWLEDGE.md.
          changes:
            - one new bullet under its topic in .rota/KNOWLEDGE.md, date-stamped
            - the topic listed in the `## Project Knowledge` block in AGENTS.md
  - id: risky-idea
    title: Try a risky idea first
    when: You don't know yet whether an approach or a library works.
    walkthrough: walkthroughs/greenfield-from-brief.md#step-3-rota-spike-optional
    steps:
      - skill: rota-spike
        why: Try it on a throwaway branch. Only the findings come back.
      - skill: rota-plan
        why: Plan the real work with what the spike found.
        example:
          source: walkthroughs/greenfield-from-brief.md#step-4-rota-plan-m01-s01
          reviewed: false
          steps:
            - you: /rota-plan M01-S01
            - agent: |
                Planning dial: 3 (research round) - a schema migration does not revert cleanly; the spike already answered the open question. Override: say "level 1" or "level 4".
                Read .rota/spikes/sqlite-schema.md: ship single-currency in M01, no currency column yet.
                Goal: stand up the SQLite schema and tally add end to end.
                T1. Skeleton CLI in cmd/tally.
                    Verify: go test ./... && tally --version | grep v0.0.0
                T2. Migrations engine in internal/store/migrate.go.
                    Verify: run tally init twice; the second run is a no-op
                ...
                Confirm this plan? (yes / changes)
            - you: "yes"
          changes:
            - the plan written to `.rota/plans/M01-S01.md`
            - the spike's decision carried into the plan's assumptions
      - skill: rota-work
        why: Build it.
        example:
          source: walkthroughs/greenfield-from-brief.md#step-6-first-cycle-rota-work--rota-ship--rota-learn
          reviewed: false
          steps:
            - you: /rota-work M01-S01
            - agent: |
                Plan .rota/plans/M01-S01.md read: 5 tasks, none done. Branch rota/M01-S01 created.
                T1  skeleton CLI in cmd/tally
                    go test ./...                       PASS
                    tally --version | grep v0.0.0       PASS
                    feat(cli): scaffold tally root command
                Next: T2 migrations engine.
          changes:
            - a feature branch with one commit per task
            - a proof row per check
  - id: new-project
    title: Start a project from a brief
    when: An empty repo and an idea of where it should go.
    walkthrough: walkthroughs/greenfield-from-brief.md
    steps:
      - skill: rota-init
        why: Suggest config values from the repo, confirmed one by one.
      - skill: rota-vision
        why: Turn the brief into milestones.
      - skill: rota-plan
        why: Plan the first milestone slice.
        example:
          source: walkthroughs/greenfield-from-brief.md#step-4-rota-plan-m01-s01
          reviewed: false
          steps:
            - you: /rota-plan M01-S01
            - agent: |
                Planning dial: 2 (spec) - first slice of a new schema; a migration does not revert cleanly. Override: say "level 1" or "level 4".
                Goal: stand up the SQLite schema and tally add end to end.
                T1. Skeleton CLI in cmd/tally.
                    Verify: go test ./... && tally --version | grep v0.0.0
                T2. Migrations engine in internal/store/migrate.go.
                    Verify: run tally init twice; the second run is a no-op
                ...
                Confirm this plan? (yes / changes)
            - you: "yes"
          changes:
            - the plan written to `.rota/plans/M01-S01.md`
      - skill: rota-capture
        why: Turn the plan into backlog items.
        example:
          source: walkthroughs/greenfield-from-brief.md#step-5-rota-capture
          reviewed: false
          steps:
            - you: /rota-capture "tally CLI skeleton, sqlite migrations engine, transactions schema with int64 minor units, tally add command, tally list command"
            - agent: |
                [F01] tally CLI skeleton                          Feature, Minor, Milestone: M01
                [F02] sqlite migrations engine                    Feature, Minor, Milestone: M01
                [F03] transactions schema with int64 minor units  Feature, Minor, Milestone: M01
                [F04] tally add command                           Feature, Minor, Milestone: M01
                [F05] tally list command                          Feature, Minor, Milestone: M01
          changes:
            - "five new rows under `## Features` in `.rota/BACKLOG.md`, tagged `Milestone: M01`"
            - nothing else; capture records and stops
      - skill: rota-work
        why: Build the first item.
        example:
          source: walkthroughs/greenfield-from-brief.md#step-6-first-cycle-rota-work--rota-ship--rota-learn
          reviewed: false
          steps:
            - you: /rota-work
            - agent: |
                Backlog (M01 active): Features F01, F02, F03, F04, F05 (all Minor)
                Suggested next: F01 (head of M01 dependency chain)
                Run /rota-work F01? [y/N]
            - you: "y"
            - agent: |
                Branch rota/F01-tally-cli-skeleton. Plan task 1: skeleton CLI.
                verify: go test ./...                      PASS
                verify: tally --version | grep v0.0.0      PASS
                feat(cli): scaffold tally root command [F01]
                Merged into main with --no-ff; branch deleted.
          changes:
            - a feature branch with one commit per task
            - F01 marked complete in BACKLOG.md
  - id: parallel-round
    featured: true
    title: Run several issues in parallel
    when: A few independent issues are ready and you can stay around for questions.
    walkthrough: walkthroughs/round-cycle.md
    steps:
      - skill: rota-plan
        why: Shape the slate so every issue has clear acceptance.
        example:
          source: walkthroughs/round-cycle.md#a-major-item-brainstorm-then-plan
          reviewed: false
          steps:
            - you: /rota-plan 42
            - agent: |
                Planning dial: 2 (spec) - new flag on a published command; revert is clean. Override: say "level 1" or "level 4".
                Goal: export --since DATE keeps only rows on or after DATE.
                T1. Parse and validate --since in cmd/export.go (AC-1).
                    Verify: go test ./cmd -run TestExportSince
                T2. Document the flag in docs/usage/export.md (AC-2).
                    Verify: grep -n -e --since docs/usage/export.md
                Confirm this plan? (yes / changes)
            - you: "yes"
          changes:
            - "the plan stored as a note on issue #42, each task tied to an acceptance criterion"
            - "`rota plan check '#42'` then exits 1 if a criterion has no task or a task has no verify step"
      - skill: rota-orchestrate
        why: Hand issues to workers, review their PRs, merge through the gate.
      - skill: rota-answer
        why: Answer the decisions the round put to you, one at a time.
      - skill: rota-learn
        why: At wind-down, keep what the round learned.
        example:
          source: walkthroughs/round-cycle.md#3-wind-down
          reviewed: false
          steps:
            - you: /rota-learn
            - agent: |
                Read the round summary: #58 bounced twice on the same criterion.
                Proposed bullet under ## Rounds:
                A criterion with two readings produces the same wrong PR on every bounce. Audit the acceptance criteria before the third relay.
                Written to .rota/KNOWLEDGE.md.
          changes:
            - one new bullet under its topic in .rota/KNOWLEDGE.md, committed straight to the base branch
            - run once for the whole round, since workers skip it per PR
      - skill: rota-release
        why: Cut a release when the work adds up to one.
  - id: end-of-session
    title: Stop for today
    when: The context is filling up or you have to leave mid-item.
    walkthrough: null
    steps:
      - skill: rota-learn
        why: Save the lessons worth keeping.
        example:
          source: null
          reviewed: false
          steps:
            - you: /rota-learn
            - agent: |
                Proposed bullet under ## Timer:
                The badge reads its count from the store on mount, so a stale subscription shows the previous value until the first tick. Re-subscribe in the quick-switch handler.
                Written to .rota/KNOWLEDGE.md.
          changes:
            - one new bullet under its topic in .rota/KNOWLEDGE.md, date-stamped
            - gotchas belong here, not in the handoff note
      - skill: rota-pause
        why: Write a handoff so a fresh session continues.
      - skill: rota-work
        why: Next session, with no argument, it reads the handoff and picks up.
        example:
          source: usage/pausing-and-resuming.md#recovering-after-clear
          reviewed: false
          steps:
            - you: /rota-work
            - agent: |
                Active streams
                  rota/fix-B07-timer-badge  (1 wip commit)  mid-implementation

                Handoff note found:
                  Next planned step: re-run the badge test after the selector change

                Resume with /rota-work, or leave the handoff for later?
            - you: resume
            - agent: |
                Resuming /rota-work with handoff brief.
          changes:
            - the handoff note deleted once you confirm; "leave for later" keeps it
            - work continues on the same branch, from the wip commit
---
# Skills

One page per `/rota-*` skill: when to use it, when another skill fits better, what usually comes before and after, and a short example. This index, the README skills table and the chains in [how it works](../how-it-works.md) are generated from these pages by `scripts/skills-docs.py`, so edit the pages, not those lists.

<!-- generated:skills:index begin -->
<!-- edit docs/skills/*.md and run python3 scripts/skills-docs.py -->

## Capture

- [`/rota-capture`](rota-capture.md): Record bugs, features and tasks in the backlog without starting work. Flags: `--remove <ID>`, `--triage <N>`.

## Plan

- [`/rota-brainstorm`](rota-brainstorm.md): Negotiate the design of one Major item before planning it. Flags: `--grill`.
- [`/rota-spike`](rota-spike.md): Try one risky idea on a throwaway branch and keep only the finding. Flags: `done <name>`.
- [`/rota-plan`](rota-plan.md): Write a signed-off implementation plan with verifiable tasks.
- [`/rota-vision`](rota-vision.md): Turn an idea into milestones with dependencies and ready/blocked status.

## Execute

- [`/rota-work`](rota-work.md): Build the next item in small commits, one verified task at a time. Flags: `--preview <ID>`.
- [`/rota-debug`](rota-debug.md): Find the root cause of one bug, fix it atomically and prove it.
- [`/rota-refactor`](rota-refactor.md): Review the architecture and file refactor issues; --map drafts the map. Flags: `--fix`, `--designs`, `--interactive`, `--map`.

## Ship

- [`/rota-review`](rota-review.md): Review a branch against its spec and conventions before it leaves. Flags: `--since <sha>`, `--queue`.
- [`/rota-ship`](rota-ship.md): Review, then open the PR or merge, linking items by ID. Flags: `--undo`, `--docs`.
- [`/rota-docs`](rota-docs.md): Scaffold, update or reorganize the public docs. Flags: `restructure`.
- [`/rota-qa`](rota-qa.md): Run product-level QA against the built artifact and score it. Flags: `first-run`, `run [<target>]`, `restructure`, `uat <ID>`.

## Persist

- [`/rota-learn`](rota-learn.md): Save the session's durable lessons to KNOWLEDGE.md. Flags: `--strict`, `--retro [<session-id|path>]`, `--term <name>`, `--promote | --deprecate | --amend <topic> "<title>"`.
- [`/rota-decide`](rota-decide.md): Record a hard-boundary decision with explicit forbids and permits. Manual only. Flags: `--from-learning <topic>`, `--from-spike <name>`, `--supersede <topic> "<title>"`, `--retire <topic> "<title>"`.
- [`/rota-pause`](rota-pause.md): Stop cleanly and leave a handoff note for the next session. Flags: `--report`.

## Rounds

- [`/rota-orchestrate`](rota-orchestrate.md): Run a parallel round: pick the slate, watch workers, merge their PRs.
- [`/rota-answer`](rota-answer.md): Answer every decision a round is waiting on, one question at a time. Manual only.

## Maintenance

- [`/rota-init`](rota-init.md): Suggest config values from the repo and write the ones you confirm. Manual only.
- [`/rota-bugreport`](rota-bugreport.md): File rota's own mistakes upstream, after your explicit yes.
- [`/rota-release`](rota-release.md): Cut a release: checklist, version bump, notes, tag, publish. Manual only. Flags: `major | minor | patch | <semver>`, `--dry-run`.
<!-- generated:skills:index end -->
