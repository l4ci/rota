---
chains:
  - id: small-item
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
      - skill: rota-brainstorm
        why: Settle the design with you, section by section.
      - skill: rota-plan
        why: Turn the design into tasks with a way to verify each.
      - skill: rota-work
        why: Build the plan, one verified task at a time.
      - skill: rota-ship
        why: Review against what the item promised, then merge.
  - id: unclear-bug
    title: Chase a bug you don't understand
    when: Something is broken and the cause is not known yet.
    walkthrough: walkthroughs/greenfield-from-brief.md#step-7-a-real-bug-run-rota-debug
    steps:
      - skill: rota-debug
        why: Reproduce it, test one hypothesis at a time, fix it in one commit.
      - skill: rota-ship
        why: Review and merge the fix.
      - skill: rota-learn
        why: Keep what the bug taught you for the next session.
  - id: risky-idea
    title: Try a risky idea first
    when: You don't know yet whether an approach or a library works.
    walkthrough: walkthroughs/greenfield-from-brief.md#step-3-rota-spike-optional
    steps:
      - skill: rota-spike
        why: Try it on a throwaway branch. Only the findings come back.
      - skill: rota-plan
        why: Plan the real work with what the spike found.
      - skill: rota-work
        why: Build it.
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
      - skill: rota-capture
        why: Turn the plan into backlog items.
      - skill: rota-work
        why: Build the first item.
  - id: parallel-round
    title: Run several issues in parallel
    when: A few independent issues are ready and you can stay around for questions.
    walkthrough: walkthroughs/round-cycle.md
    steps:
      - skill: rota-plan
        why: Shape the slate so every issue has clear acceptance.
      - skill: rota-orchestrate
        why: Hand issues to workers, review their PRs, merge through the gate.
      - skill: rota-answer
        why: Answer the decisions the round put to you, one at a time.
      - skill: rota-learn
        why: At wind-down, keep what the round learned.
      - skill: rota-release
        why: Cut a release when the work adds up to one.
  - id: end-of-session
    title: Stop for today
    when: The context is filling up or you have to leave mid-item.
    walkthrough: null
    steps:
      - skill: rota-learn
        why: Save the lessons worth keeping.
      - skill: rota-pause
        why: Write a handoff so a fresh session continues.
      - skill: rota-work
        why: Next session, with no argument, it reads the handoff and picks up.
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
