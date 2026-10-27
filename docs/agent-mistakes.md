# Where coding agents go wrong, and what rota does about it

An agent can build the wrong thing, write a test that proves nothing, or keep trying the same failed fix. rota puts checks around some of these mistakes and gives the agent instructions for the rest. The distinction matters: a command refusing an action is stronger than a paragraph asking the agent to stop.

This page was checked against `main` on 2026-10-27, after [#2121](https://github.com/l4ci/rota/issues/2121) through [#2125](https://github.com/l4ci/rota/issues/2125) landed. **Enforced** below means the supported verb or gate checks it, with any configuration limits stated. **Asked for** means the skill tells the agent to do it. Neither prevents an agent from bypassing rota with raw commands. The remaining gaps are catalogued in [#2126](https://github.com/l4ci/rota/issues/2126); links to closed issues identify changes already delivered, not promised fixes.

## 1. Overengineering

The agent adds options, abstractions or adjacent cleanups that nobody asked for. Even useful code can make the requested change harder to review and maintain.

**Enforced.** On the issue backend, `rota plan check` checks tasks against acceptance criteria: a task without a criterion, an unknown criterion, a criterion with no task or a task without a Verify step fails the check (exit 1). This is a structural check on the plan, not a measure of complexity. The skill skips it for legacy plans without `Serves:` lines and for the file backend. See [plans](usage/vision-and-plans.md).

**Asked for.** The plan critic checks scope creep against `## Out of scope`. The Spec reviewer traces changes back to the item's intent; a spec's silence is not permission to expand it. Workers are told to stay within the issue's boundary.

**Still open.** A task can name a valid criterion and still overbuild it. Nothing measures complexity within scope, and the Spec rubric treats drift alone as a concern rather than a failing verdict. This remains a judgment call ([gap recorded in #2126](https://github.com/l4ci/rota/issues/2126)).

## 2. Shortcuts

The agent declares success after a weak check, or adds a test that would pass without the fix. A green result then hides missing behavior.

**Enforced.** `rota proof record` runs the command and records PASS or FAIL from its exit code. `rota item complete` refuses an item without proof unless explicitly overridden. Manual-gate verbs require `--confirm` and a confirmation note. With `test.vacuousCheck: true` in the base configuration, the merge gate and train also run selected added or changed tests with the non-test changes reverted. Tests that still pass block the merge with `vacuous-tests`. This check is off by default and skips cases such as tests-only changes or no selected runnable tests. See [test dependence checking](usage/configuration.md#checking-whether-tests-depend-on-a-change), delivered by [#2123](https://github.com/l4ci/rota/issues/2123).

**Asked for.** A behavior change starts with a RED test that fails on an assertion. Reviewers look for tautological tests, tests coupled to implementation details, silent failures and stale scaffolding. The agent must ask before using the proof override.

**Still open.** The agent still chooses the proof command. A measured exit code does not establish a useful assertion, and the dependence check accepts a nonzero counterfactual test exit without proving why it failed ([gap recorded in #2126](https://github.com/l4ci/rota/issues/2126)).

## 3. Sugar-coated results

The summary sounds complete even when the diff or test output says otherwise. A confident explanation can make a missing requirement easy to overlook.

**Enforced.** The round gate verifies a scratch merge of the candidate and its base, and proof rows record actual command results. Configured cross-harness review requires a current review record from another harness for matching issue labels (by default `size:Major` or `risk:high`), unless explicitly skipped or no alternate reviewer harness is available. An issue's trusted `## Try it` commands run on the merged tree; absent or untrusted commands do not. See [review and ship](usage/review-and-ship.md) and [the round gate](usage/parallel-rounds.md).

**Asked for.** Reviewers judge the diff, not its defence. A rationale cannot lower a finding's severity. They must list what they could not judge rather than guess it into a PASS.

**Still open.** No semantic check compares every sentence in the PR body or closing summary with the proof. A review record is also not proof that the reviewer reasoned well ([gap recorded in #2126](https://github.com/l4ci/rota/issues/2126)).

## 4. Failure circles

The agent keeps patching a failed approach, accumulating changes without revisiting its explanation of the bug.

**Enforced.** `rota debug counter` refuses further fixes after three failed fixes. For solo work, `rota debug work-fix attempt` counts per task across sessions and refuses the fourth attempt since that task's last PASS or reset; [#2121](https://github.com/l4ci/rota/issues/2121) added this cap. Reset verbs require manual confirmation. Rounds count gate, relay and manual bounces toward `round.maxBounces`; at the cap the item parks `needs-human`. The configurable item timeout provides another stopping point. See [running work](usage/running-work.md), [debugging](usage/debugging.md) and [rounds](usage/parallel-rounds.md).

**Asked for.** The agent must call the attempt counter before a fix, stop when refused and hand the problem to `/rota-debug` or a human. A multi-task plan's tracer must pass before later tasks start. A red check should be rerun alone before blaming the branch.

**Still open.** Counters bound recorded attempts; they cannot stop unrecorded edits or establish that a claimed PASS is meaningful. The solo loop is capped now, but following the handoff remains an agent obligation ([gap recorded in #2126](https://github.com/l4ci/rota/issues/2126)).

## 5. Forgetting

A new session repeats a past mistake or misses a decision because the previous conversation is gone.

**Enforced.** Knowledge, decisions, glossary and map entries have durable, tracked files behind verbs. With the Claude Code hooks installed, `rota hook guard` supplies matching path-scoped knowledge and active decisions before Edit or Write, once per entry per session ([#2124](https://github.com/l4ci/rota/issues/2124)). SessionStart can inject an orchestrator handoff; the orchestrator's Stop hook requests one at the context threshold, with a capped number of blocks. These protections depend on installed hooks and available session state. See [recall before editing](usage/learning.md#recall-before-editing) and [handoff hooks](usage/unattended-rounds.md#orchestrator-handoff).

**Asked for.** Before working, the agent queries relevant knowledge and decisions. `/rota-learn` saves discoveries, and round workers post learnings on their issue for the orchestrator to harvest.

**Still open.** Unscoped knowledge still needs a topic query. A missing or wrong path scope misses the lesson; shell writes and harnesses without the Edit/Write hook do not receive that automatic recall. Injection also cannot ensure the agent understands the entry ([gap recorded in #2126](https://github.com/l4ci/rota/issues/2126)).

## 6. Silent assumptions

The agent fills a gap in the request without saying what it chose. The user discovers the decision only after the work is built.

**Enforced.** For a remote PR whose body is available, the gate refuses missing `## Assumptions` or `## Rulings` headings with `assumptions-missing` or `rulings-missing`. The train excludes those PRs before verification. [#2122](https://github.com/l4ci/rota/issues/2122) added this check. Empty sections and `None` pass: the check requires headings, not truthful or complete disclosure.

**Asked for.** Workers escalate unsettled product choices with one decision-ready question. PR bodies distinguish assumptions taken from the code from the worker's own rulings and their cost if wrong. The Spec reviewer checks stated assumptions against the diff and intent; design interviews ask the user about intent and take discoverable facts from the code.

**Still open.** Self-reporting misses assumptions the agent never notices. Heading checks cannot find them. Solo `/rota-work` also allows a recommended default on ambiguity, so it still depends on the agent stating the choice ([gap recorded in #2126](https://github.com/l4ci/rota/issues/2126)).

## 7. Fragility without structure

A long rulebook can look reassuring while leaving the important steps dependent on the agent remembering every paragraph.

**Enforced.** Verbs return exit codes and typed refusal reasons. Locks, attempt fencing and the scratch-merge gate protect workflow state; each round worker gets a worktree. The installed Claude Code worker guard rejects a defined set of destructive git commands. Managed `rota review prepare` and `rota review probe` operations own reviewer workspace setup and temporary probe config, refusing setup failures. These are specific boundaries, not an OS sandbox. See [review workspaces](usage/review-and-ship.md#disposable-reviewer-workspaces) and [the worker guard](usage/parallel-rounds.md#worker-guard).

**Asked for.** Skills still govern semantic duties: dispute a wrong ticket, respect scope, choose useful checks, report limitations and use the managed paths. Validators and regression tests catch selected rule and code regressions; they cannot prove an agent follows every instruction.

**Still open.** Much of the rulebook remains prose. The [#2125 audit](https://github.com/l4ci/rota/issues/2125) separates hard checks from written duties and ranks observed failures. Its workspace and config follow-ups, [#2144](https://github.com/l4ci/rota/issues/2144) and [#2145](https://github.com/l4ci/rota/issues/2145), have since landed, but unsandboxed commands can still reach absolute paths outside those temporary roots. The broader enforcement gap remains ([#2126](https://github.com/l4ci/rota/issues/2126)).

## Read further

- [How it works](how-it-works.md): skills, verbs and the files they share
- [Skill-rule audit](contributing/skill-rule-audit-2026-10.md): dated inventory and evidence behind the enforcement gaps
- [rota or a bare session](usage/rota-vs-plain-session.md): the comparison with a plain agent session
