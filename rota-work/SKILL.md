---
name: rota-work
description: Orchestrator-driven parallel implementation — plans tasks, dispatches workers, verifies, commits atomically per task. Workers run as in-process subagents (default) or, under work.dispatch=tmux or herdr, as separate Claude Code sessions in their own worktrees that open PRs behind a merge gate. Supports branch or worktree isolation and direct merge or PR. Use when items already exist in BACKLOG.md and need implementation ("implement [B07]", "build these"); with no argument it reconciles active work and suggests the next item; for an item not yet captured use /rota-capture, which can hand off here.
---

**Print the banner below verbatim before any other action — skip if dispatched as a subagent.** See `references/banner-preamble.md`.

```
════════════════════════════════════════════════════════════════════════
  🔨  rota-work  ·  orchestrator-driven parallel implementation
  triggers: "implement", "build these"  ·  pairs: rota-ship, rota-review
════════════════════════════════════════════════════════════════════════
```

# rota-work

Orchestrator-driven parallel implementation with per-task verification and commits.

## Configuration

Read `.rota/config.json`:

- `models.orchestrator` — model for planning and verification (default `opus`)
- `models.worker` — model for implementation subagents (default `sonnet`)
- `work.isolation` — `"branch"` (default) or `"worktree"`. Ignored when `work.dispatch` is `"tmux"` or `"herdr"` (every slot owns a worktree by construction).
- `work.mergeStrategy` — `"direct"` (default) or `"pr"`
- `work.dispatch` — `"subagent"` (default), `"tmux"` or `"herdr"`. Selects the worker backend. `"subagent"` dispatches in-process `Agent` workers that write files while the orchestrator commits. `"tmux"` runs each worker as its own Claude Code session in its own worktree, committing and opening a PR against the cycle branch. See [`references/tmux-dispatch.md`](references/tmux-dispatch.md). `"herdr"` runs the same workers as herdr tabs in the orchestrator's workspace, with herdr's native agent state; see [`references/herdr-dispatch.md`](references/herdr-dispatch.md).
- `work.workerSlots` — integer, default `3`. Size of the tmux worker pool; ignored under `"subagent"`.
- `work.workerCommand` — string, default `""`. Launch command for a tmux worker session (must not resume a conversation: no `-c`, `-r`, `--continue`, `--resume`); empty builds `claude --model <models.worker> --dangerously-skip-permissions` (workers commit, open PRs and run tests unattended).
- `work.accounts` — array of `{name, configDir}`, default `[]`. Maps tmux slots to independent `CLAUDE_CONFIG_DIR`s so each authenticates as its own account. Empty means every slot inherits the ambient config dir.
- `work.operatorCommand` — string, default `""`. Used to relaunch the orchestrator inside tmux when the cycle starts outside one; empty builds `claude --continue --model <models.orchestrator> --permission-mode auto`.
- `autonomy.level` — `"off"` (default), `"auto"`, or `"loop"`. Controls whether Step 13 (Learn), Step 14 (Refactor), and Step 15 (Loop continuation) nudge or invoke the next skill directly.

## When to Use

- User describes a task, feature, or list of improvements
- Conversation has enough spec to act on
- Work is decomposable into 2+ independent pieces

## Flow

```
Guard → Clarify (if needed) → Status → Plan → Isolate → Dispatch → Verify → Commit → TODO → Merge/PR → Status
```

## No-Argument Mode (reconcile, suggest, then work)

`/rota-work` with no item, ID or brief reconciles what is in flight, shows the backlog, suggests one item and then continues into Step 1 with it. Rounds do not use this path; the orchestrator runs `/rota-orchestrate`.

**1. Reconcile active streams.** `rota status show` lists them. Git is the source of truth over `status.json`. For each stream: a branch that no longer exists is dropped with `rota status rm <branch> [--repo <repo>]`; otherwise note whether it has commits past the base (`rota git base`) and whether `rota status handoff <branch> [--repo <repo>]` returns a `/rota-pause` note (read its Stage, Next planned step and Current hypothesis). Resolve each stream with `AskUserQuestion` (plain-text fallback), Recommended first:

- handoff present: resume with the note as the brief (Recommended); leave it for later; abandon.
- commits, no handoff: ship via `/rota-ship` (Recommended); resume; leave as-is.
- no commits, no handoff: resume (Recommended); abandon; leave as-is.

Resume continues on the existing branch. Abandon is `git branch -D <branch>` plus `rota status rm`, and removes that stream's handoff note. A handoff note is deleted only when its stream is resumed or abandoned. Under `autonomy.level == "loop"`, auto-pick Recommended; the downstream skills keep their own manual gates.

**2. Orient.** Run `rota backlog archive --days 5` (silent), `rota milestone active` and one `rota backlog ids --milestone <MID>` per active milestone, then `rota backlog list`. Print the list in full, every row and section: the table is the point of the mode, and is exempt from length limits. Prefix it with `Active milestones: <ids and titles>` when there are any. Advisories, never blocking: for each ID in `rota backlog drift --json` print `[ID] looks shipped on <hash> but still open`, and suggest `rota proof add` then `rota item complete <ID> --commit <hash>` (or `--no-proof`), never auto-complete. Print `stale: map=N, knowledge=M, todo=K` from `rota backlog stale` (zero kinds dropped) and `empty-active: <MID>` for an active milestone with no open items. `rota backlog drift` refuses in issue mode; skip it there.

**3. Suggest one item.** Order: P0 bugs; clusters holding a blocking bug; quick wins (Cosmetic, P2); the highest-impact P1; blocking tasks (`Related:`); Minor features; Major features only when nothing else is pending or the user asks. Milestone bias at every level except P0: items tagged to an active milestone first, then untagged, then non-active milestones. In issue mode, items labelled `changes-requested` rank right after P0. Skip items already active. An active milestone with no items: say so and point at `/rota-capture`. Print `Suggested next: [ID] Title (tag)` and one sentence why.

Brainstorm nudge, only for a `[Major]` feature or `[P0]` bug with no `.rota/designs/<ID>.md`: `off` prints *"consider `/rota-brainstorm [ID]` before this"*; `auto` dispatches `rota-brainstorm` with the ID, then re-suggests; `loop` skips it (Step 4 handles design under loop).

**4. Confirm.** Under `loop`: run `rota status loop start`, print `Loop: starting [ID] Title.` and go straight to Step 1. If nothing is suggestable, print `Loop: backlog empty — stopping.`, surface any `[Auto:Loop]` decisions per `references/terminal-loop-surface.md`, and stop. Otherwise `AskUserQuestion`: Start (Recommended); Peek approach first (`--preview`, offered for Major, P0/P1 or a batch); Write a plan first (`/rota-plan`, offered for a Major item tagged to a milestone with no plan at `.rota/plans/`); Pick different items; Stop here. "Other" text is the item spec.

On a terminal path (Stop here, or an empty backlog) run `rota release pending --json` and, when `shouldNudge` is true, print its `message` as one line. Skip it when work continues, and when there is no tag yet. Pass the item's BACKLOG text into Step 1 so it is not re-read.

## Preview Mode (`--preview`)

When invoked as `/rota-work --preview <target>` (or with `--preview` anywhere in the args), the skill enters **read-only preview mode** — it produces an approach peek, then stops. No writes, no commits, no `rota` calls beyond reads. Steps 1–15 are bypassed.

The target may be a backlog item (`B07`, `F03`, `T11`), a plan key (`M01-S01`, `M01-B07`), or a milestone (`M01`). Ambiguous → ask once; do not auto-pick.

**Procedure:**

1. **Load context silently** per [`references/context-load-protocol.md`](references/context-load-protocol.md). Issue all reads in parallel. For backlog-item targets under umbrella mode: parse the entry's `Repos:` field (`rota item field get <ID> --name repos`); when umbrella mode is on (`rota repo umbrella` exits 0) and the item carries a `Repos:` value, resolve to absolute sub-repo path(s) via `rota repo resolve <name>… --json`. Multi-repo items resolve to a list — keep all entries for the render. Skip repo resolution for slice / milestone targets (umbrella-flat per M02 acceptance). If `rota decisions query` returns matches, surface them in the peek's "Hard boundaries to respect" section (between "Files I'd create" and "Tests I'd add") — one line each: `- <decision title> — <one-line summary>`. The user's job during review is to spot conflicts before code lands. No guard, no status registration.
2. **Produce the peek.** Print this structure to chat. **Nothing else** — no preamble, no recap of what context you read:

   ```
   Peek for <target>:

   Approach
     <one paragraph — the shape of what I'd do and why this over alternatives>

   Repo
     <name> (<absolute-sub-repo-path>)        # omit when single-repo or no Repos: tag
     <name2> (<absolute-sub-repo-path-2>)     # one line per repo for multi-repo items

   Files I'd touch
     - <path>  — <reason>
     - <path>  — <reason>

   Files I'd create
     - <path>  — <reason>

   Hard boundaries to respect
     - <decision title>  — <one-line summary>
     - <decision title>  — <one-line summary>

   Tests I'd add
     - <test name or location>  — <what it verifies>

   Assumptions I'm making
     - <named assumption that, if wrong, changes the approach>
     - <named assumption that, if wrong, changes the approach>

   Known unknowns
     - <thing I'd resolve mid-flight>  (will pause if unresolvable)
     - <thing I'd resolve mid-flight>  (will pause if unresolvable)

   If any of this is wrong, push back before /rota-work runs.
   ```

   Omit `Hard boundaries to respect` if no DECISIONS topics matched. Omit `Repo` entirely when umbrella mode is off, the target is a slice / milestone, or the item has no `Repos:` tag. When present, render as `<name> (<absolute-path>)` resolved via `.rota/repos.json`. Multi-repo items render one indented line per resolved sub-repo — so the user can verify every dispatch target before `/rota-work` runs.

   Be specific. *"I'd touch the auth code"* is useless — cite paths. If you don't know the path well enough to cite it, say so under Known unknowns.

3. **Stop.** Do **not** auto-invoke `/rota-work` (without `--preview`), write a plan, or take any action. The user reviews and either:

   - Says *"go"* — they invoke `/rota-work <target>` themselves (without `--preview`).
   - Pushes back — they redirect, you restate the peek with corrections.
   - Asks for a written plan — offer `/rota-plan <target>`.

**Key principles for preview mode:**

- **Pure read.** No writes, no commits, no `rota` calls beyond reads.
- **Be specific.** Generic peeks are useless. Cite paths, test names, function names.
- **Name assumptions.** The whole mode's value is making implicit choices visible.
- **Stop after the peek.** No auto-continuation; the user's pushback is the point.
- **Plan beats peek for high-stakes work.** Offer `/rota-plan` if the user wants something durable rather than ephemeral.

**Orchestrator-model contract (F35, loop mode).** When `/rota-work` Step 4's F34 uncertainty pre-flight needs a peek, it runs this Preview Mode procedure **inline** (not via recursive `Skill` dispatch) — the peek inherits the orchestrator model since the cycle is already running under it. The Step 4 chain reads the peek output from chat context and proceeds to `/rota-plan --auto-loop`. Manual invocations from the no-argument mode or the user's prompt (`/rota-work --preview <ID>`) are unconstrained — the user is in the loop and can correct any peek that under-performs.

## Step 1 — Guard

```bash
rota git guard clean --context "/rota-work"
```

Exit 0 = clean, continue. Exit 3 = not a repo, surface and stop.

**Exit 1 (dirty tree) — auto-sweep known tool siblings first.** Some toolchains generate sibling files *after* a previous `/rota-work` wave finished (Godot `.gd.uid`, Xcode `.xcworkspace/contents.xcworkspacedata`, SwiftPM `Package.resolved`, Tuist-regenerated `.xcodeproj`, `.DS_Store`). If these are all that's dirty, they belong in a `chore:` commit, not a refusal.

```bash
git status --porcelain
```

Classify every line:

- **Sibling artifact** — path matches a sibling of a tracked file (e.g. `Foo.gd.uid` next to tracked `Foo.gd`), or matches one of these patterns: `*.gd.uid`, `*.xcworkspace/contents.xcworkspacedata`, `Package.resolved`, `*.xcodeproj/project.pbxproj` regenerated without meaningful diff, `.DS_Store`.
- **User change** — anything else.

If **every** dirty path is a sibling artifact, sweep them into a single commit and continue:

```bash
git add -A -- <matching paths>
git commit -m "chore: sweep tool-generated siblings before rota-work"
```

If **any** path is a user change, stop with the original guard message — the user decides whether to stash, commit, or discard.

**Greenfield variant.** On a fresh `git init`'d repo (no commits yet), `rota git guard clean` emits a tailored message pointing at the `chore: import initial files` baseline commit. Run it and re-invoke — the guard then sees a clean tree.

Don't narrate the sweep unless it happened; silent pass-through is the common case.

**On any Step 1 guard failure that stops `/rota-work` (exit 3 not-a-repo, or exit 1 user-change dirty tree)** — this is a terminal path; the user is about to step away from the loop to resolve. Per the F19 terminal-path-only convention (mirrored in the no-argument empty-backlog path and `/rota-pause`), surface any `[Auto:Loop]` decisions logged during this loop session before printing the guard message:

Surface any `[Auto:Loop]` decisions per `references/terminal-loop-surface.md` (silent when empty). Print the surface verbatim above the guard message.

After surfacing, clear the loop timestamp so the next loop session starts fresh:

```bash
rota status loop clear   # no-op when loopStartedAt is already unset
```

**Initialize task list.** Follow the canonical pattern in `references/task-list-init.md` — load `TaskCreate(…)` via `ToolSearch select:TaskCreate,TaskUpdate` if needed, then create one task per phase below.

Phases:

1. *Guard* — clean tree + repo confirmed (Step 1)
2. *Register status* — branch named, `rota status add` recorded (Steps 3, 5)
3. *Plan tasks* — wave layout + briefs ready (Step 4)
4. *Branch / worktree* — isolation set up per `work.isolation` (Step 5)
5. *Dispatch & verify per wave* — workers run, orchestrator verifies each completion (Steps 6–8)
6. *Commit + TODO + sweep* — per-task commits, TODO entries marked complete, item plans tombstoned, tool siblings swept (Steps 7.5, 8.5, 9, 9.5)
7. *Merge/PR & report* — integration + status removal + summary + post-cycle nudges (Steps 10–15)
8. *Knowledge lifecycle* — hit-tracking and contradiction logging (Steps 2.5, 4)

## Step 2 — Clarify Ambiguous Briefs (only when needed)

If — and only if — the current brief is too thin to plan concrete tasks (missing scope, conflicting requirements, or two equally plausible interpretations), use the `AskUserQuestion` tool to resolve the ambiguity before touching any code. Otherwise skip this step entirely — the default is to proceed.

Good reasons to ask:

- The scope hits 2+ incompatible files or areas, and picking one vs. both changes the plan materially.
- A requirement is vague in a way that yields opposite reasonable implementations (e.g., *"add sorting"* — ascending or descending, stable or not, which columns).
- Multiple captured items imply different orderings, and the user didn't say which to tackle first.

Bad reasons to ask (don't):

- To confirm you understand — just act.
- For preferences you can infer from `KNOWLEDGE.md` or the existing codebase.
- Style choices inside an agreed scope — that's implementation.

When asking, use a single `AskUserQuestion` call with 1-3 questions. Each question:

- Short `header` (e.g., `"Scope"`, `"Target"`, `"Order"`).
- Options map to concrete plans. Mark the most likely intent `(Recommended)`.
- For conflicting items, use `multiSelect: true` and ask which subset to include in this run.

Plain-text fallback: ask once; on ambiguity, default to Recommended and state it explicitly in the dispatch brief. See `references/ask-user-question-fallback.md`.

**Loop mode exception:** if `autonomy.level == "loop"` and the brief is genuinely ambiguous (you'd otherwise ask Step 2), the routing depends on the item's shape:

- **Major + Milestone-tagged item** — defer to Step 4's auto-dispatch chain. The chain auto-resolves design via `/rota-brainstorm --auto-loop` (writes a design artifact with `[Auto:Loop]` decisions for fresh picks), then runs the uncertainty pre-flight + plan dispatch (`/rota-plan --auto-loop`). Step 2 does not stop in this case — the chain owns design resolution under loop.
- **Non-Major or untagged item** — **stop the loop** and surface the question for the user to resolve. Do not silently pick a default — invisible decisions across N looped items defeat the point of the loop. The user resolves and re-invokes `/rota-work` to continue the queue.

## Step 2.5 — Detect Knowledge-vs-Correction Contradictions (F03 lifecycle)

**Detect knowledge-vs-correction contradictions (F03 lifecycle).** When Step 2's `AskUserQuestion` produces a user response that contradicts the cycle's framing (the user picks "Pick different items", supplies an "Other" free-text correction that pushes back on a stated assumption, or otherwise redlines the planned direction), check whether the user's correction text overlaps with any bullet returned by Step 4's eventual K+D query.

V1 overlap heuristic — simple case-insensitive substring match: for each candidate bullet from the K+D query, extract its body text; if a meaningful fragment (≥4 contiguous lowercase words) from the bullet appears in the user's correction text, the bullet is a contradiction candidate. Log via:

```bash
rota knowledge contradiction add --topic <T> --title <S> --text "<first 200 chars of correction>"
```

Process candidates in parallel — the verb serializes its sidecar writes behind a per-file lock, so concurrent calls don't lose updates. `/rota-learn` Step 9 surfaces these candidates at session end and asks per-bullet whether to demote.

Edge case: this step only fires when Step 2 actually surfaced an AskUserQuestion AND the user provided a non-Recommended answer. The Step 4 query happens later — so this detection ALSO fires later, comparing the now-loaded bullets against the earlier correction. Practical sequencing: cache the correction text in the cycle's working memory at Step 2, then run the overlap check after Step 4's K+D returns matches.

Skip silently when Step 2 was skipped (no clarification needed).

## Step 3 — Register in Status

After picking the branch name:

**Single-repo:**

```bash
rota status add <branch> --items <ID1>,<ID2>[,...] [--worktree <path>]
```

**Umbrella mode** (when `umbrella.enabled` is true and items carry `Repos:`): parse the `Repos:` field from each item's TODO entry. The value is a comma-separated CSV — single-repo items have one name (`Repos: web`), multi-repo items have two or more (`Repos: web, api`). All items in a wave must share the *same* set of repos.

Single-repo wave: pass the one name via `--repo`:

```bash
rota status add <branch> --items <ID1>,<ID2>[,...] [--worktree <path>] --repo <repo-name>
```

Multi-repo wave: register one entry per `(branch, repo)` pair with `--repos`:

```bash
rota status add <branch> --items <ID1>,<ID2>[,...] --repos <repos-csv> [--worktrees <csv>]
```

Parse the `Repos:` field for each item:

```bash
rota item field get <ID> --name repos
```

The field parser keeps multi-repo CSVs (`web, api`) intact and handles the `Detail:`/`Related:`/`Milestone:` boundaries. Hand the names to `rota repo resolve <name>…` for validation; if it exits 3, surface the missing names (from its message) and stop.

Idempotent on `(branch, repo)` — call again with the worktree path(s) once Step 5 creates them.

## Step 4 — Plan Tasks

**Plan-as-artifact check (first).** If the work has a milestone-and-unit key — an item tagged to a milestone (`Milestone: M01` on `B07` → key `M01-B07`) or a slice (`M01-S01`) — check for an existing plan:

```bash
rota plan show <milestone>-<unit> 2>/dev/null
```

If a plan exists, **use it as the orchestrator's plan** — its task decomposition, files, verify steps, and assumptions become the dispatch briefs in Step 6 instead of decomposing ad-hoc. Restate any user redlines from the conversation, but don't silently re-derive what the user already signed off on. If the conversation contradicts the plan, ask the user whether to update the plan first (`/rota-plan` again) or proceed and ignore it.

**Loop-mode auto-dispatch chain (B28 / F32 / F34 / F35).** In loop mode with a Major + Milestone-tagged item but no plan, `/rota-work` runs the full research → plan chain in three steps, all via the `Skill` tool so the dispatched skills inherit the orchestrator model:

1. **Design pre-flight (B28).** If `.rota/designs/<itemId>.md` is absent, dispatch `/rota-brainstorm --auto-loop <itemId>`. The dispatched skill auto-resolves design questions (Local-first → Bounded web → Placeholder), logs `[Auto:Loop]` decisions for fresh picks, and writes `.rota/designs/<itemId>.md` with `auto: true` frontmatter. When a design already exists, this step is a no-op.
2. **Uncertainty pre-flight (F34).** Run `rota plan uncertain <itemId>`. Exit 0 (uncertain, reasons on stdout) → run the **Preview Mode** procedure (above) inline with `<itemId>` as the target; the peek prints to chat and lands in the orchestrator's session context. Exit 1 (certain) → skip the peek.
3. **Plan dispatch (F32 / F35).** Dispatch `/rota-plan --auto-loop <milestone>-<itemId>`. `/rota-plan` Step 3 reads the design artifact as soft input (already wired), and the auto-resolution pipeline writes the plan with all picks honored.

After the chain returns, re-run the plan-as-artifact check at the top of this step — the plan now exists; use it as the orchestrator's plan. Off and auto modes skip this chain entirely and fall through to manual decomposition. See [`references/loop-mode-plan-dispatch.md`](references/loop-mode-plan-dispatch.md) for the full choreography.

If no plan exists and the loop-mode dispatch above did not fire (off/auto, or Minor/untagged item), proceed with the steps below.

From the conversation context:

1. **Consult knowledge + decisions.** Apply the canonical K+D query pattern (`references/knowledge-consult.md`) with topics inferred from the planned work areas. Also run `rota glossary read <terms appearing in the TODO entry or task plan>…` for any domain term used in the TODO entry (terms live in `.rota/KNOWLEDGE.md`'s `## Glossary` topic), and surface inline conflict-call-outs (synonym or drift) when the user's wording deviates from the canonical term during the cycle. Carry matches into Step 6 briefs as `**Known gotchas:**` (relevant knowledge bullets only) and `**Hard boundaries:**` (full decision entries — rule + *Why* + **Forbids** + **Permits**). Workers must treat boundaries as constraints, not hints. If a planned task would violate a decision, **stop and surface to the user** before dispatching.

   - **Soft-cap check.** Run `rota map stats --cap` — prints a one-line nudge (a warning on stderr) when the subsystem count is at or above the configured soft cap, and nothing below it. Never blocks.

   > **REQUIRED — Register hits on consumed bullets (F03 lifecycle).** After writing the Step 6 briefs, apply the hit-register pattern from `references/knowledge-consult.md` *Hit-register after consumption*: for each bullet that landed in a brief's `**Known gotchas:**` section, call `rota knowledge hit --topic "<T>" --title "<first-line-of-bullet>"` once, issuing all calls as a single parallel batch — the verb serializes its sidecar writes behind a per-file lock, so concurrent calls don't lose hits. Bullets returned but pruned before the briefs don't earn credit. Silent on success. Provisional bullets auto-promote to confirmed once `hits >= learn.promoteThreshold` (default 3).

2. Identify discrete tasks — files to create/modify, what changes, acceptance criteria.
3. **Absorb wave-internal file collisions.** Before grouping into waves, scan task pairs for **any two tasks whose modified-file sets intersect** — not just rename / link-sweep. Under `work.isolation: "branch"` two write-only workers editing the same file race on disk (the second worker's `Edit` reads sibling-mutated content), so a same-file pair would otherwise force serialization across waves. The orchestrator's standing recourse is **absorption**: fold one task's same-file portion into the other task's Step 6 brief at dispatch time, leaving the absorbed task touching only files no other task writes. Both then run as parallel write-only workers. This is a reproducible orchestrator-side technique, not per-orchestrator improvisation — resolve every intersecting pair by absorption (preferred), clean split-ownership, or serialize-across-waves before grouping. Rename + link-sweep is the canonical instance; use `rota plan rename-check <old-name> [-- <scope>...]` as ground truth for it (re-run at Step 7 to catch enumeration gaps). See [`references/loop-mode-plan-dispatch.md`](references/loop-mode-plan-dispatch.md) *Absorb wave-internal file collisions* for the absorption choreography, the M02-S02 worked example, and the rename + link-sweep resolution table.

   **Disjoint file sets are not proof of independence — scan for shared-symbol collisions too.** Two tasks can hold non-intersecting file sets and still break the merged tree, because the break exists in neither worker's diff. Two shapes: a task that **widens, narrows, or re-types a shared symbol's signature** while a sibling adds a fresh call to it, and a task that **stops emitting a constant, key, or output field** while a sibling starts depending on it. Each worker's own output is internally consistent and Step 7's per-task diff review passes both — the defect is only visible in the union, and only to something that resolves symbols across the whole tree. Rename is the one instance of this class the rules above already catch (`rota plan rename-check`); signature changes and dropped emissions are not renames and no grep for the old name finds them. When a task's brief changes a symbol's *shape* rather than its *name*, either serialize it ahead of every task that references the symbol, or absorb the call-site updates into it. When the project has a whole-tree resolution check available (typecheck, compile, `rota-qa` executable check), run it once after Step 7.5 commits the wave rather than per task — per-task verification structurally cannot catch this class.
4. Group into dependency waves:
   - **Wave 1:** independent files → parallel
   - **Wave 2+:** depend on wave 1 outputs → sequential or next parallel batch

## Step 4.5 — Umbrella Pre-Flight (when umbrella mode is on)

Umbrella mode is in effect when `.rota/repos.json` registers ≥1 sub-repo (the data is truth; `umbrella.enabled` is informational). Detect with `rota repo umbrella` — see `references/umbrella-mode.md` for the registry shape, resolution verbs, and `Repos:` field semantics.

If `rota repo umbrella` exits 1 (`umbrella: false`), skip this step entirely (single-repo path).

**Issue mode** (`backlog.backend: "issues"`; `references/issue-mode.md`, *Umbrella*): items are single-repo and their IDs are qualified (`<repo>:<ID>`). Take the repo from the item's `Repos:` field (`rota item field get <ID> --name repos`); a bare ID that exists in several sub-repos exits 2 as ambiguous, so use the qualified form. Pass `--repo <repo>` to `rota ship pr` (Step 10).

If it exits 0:

1. **Every item must carry `Repos:`.** Parse via `rota item field get <ID> --name repos`. If any item lacks a tag, stop with: *"Error: `[<ID>]` lacks a `Repos:` tag. Re-run `/rota-capture` to add it. Cannot route to a sub-repo."*

2. **All items in a wave must resolve to the same repo set** (as a set, order-independent). Single-repo and multi-repo items can't mix in one wave; two multi-repo items must list the same names. On divergence, stop with: *"Error: items in this wave target different sub-repo sets: `<set-a>` vs `<set-b>`. Split into separate `/rota-work` runs."*

3. **Validate every name in the resolved set** via `rota repo resolve <name>…`, one positional per name with the CSV's spaces dropped (exits 3 and names every missing one). On failure, stop with: *"Error: `Repos: <name>` not registered in `.rota/repos.json`. Run `rota init umbrella` from the umbrella root to register sub-repos."*

4. **Walk-up convenience (single-repo only).** If `/rota-work` was invoked from a cwd that `rota repo which` resolves to a registered sub-repo, default that sub-repo as the wave's scope when items lack an explicit `Repos:` tag. Multi-repo items always come from the captured `Repos:` field — no cwd default.

When the gate passes, carry the resolved sub-repo set forward to Step 5 (branch / worktree creation) and Step 10 (merge / PR).

## Step 5 — Create Branch or Worktree

Choose a descriptive name (e.g., `rota/quick-switch`, `rota/fix-timer-badge`).

### Backend branch — `work.dispatch` is `"tmux"` or `"herdr"`

When `work.dispatch` is `"tmux"` or `"herdr"`, skip the isolation guard and the branch/worktree patterns below entirely — they describe the `subagent` backend. The two hosts share every step here; host-specific lines are marked.

**First, confirm this session is inside the host. It is a precondition, not a nicety.**

```bash
rota worker session check     # exit 0 = inside, exit 1 = outside
```

The verb reads `work.dispatch` itself: under tmux it keys on `$TMUX`, under herdr on `HERDR_ENV=1`.

The backend is worth its cost for exactly one reason: a worker that needs a decision can idle and a human can answer *in that worker's pane*. Launched from a terminal that isn't already inside tmux, the worker windows land in a **detached session nobody is looking at** — every escalation goes unanswered and the backend silently degrades into a worse subagent mode. Don't proceed on the assumption someone will attach later.

**herdr, exit 1 — stop.** There is no handoff under herdr: `ensure` exits 4 because there is no workspace to open an operator tab in from outside herdr. Tell the user to start Claude Code in a herdr pane at the repo root and re-run `/rota-work` there, then end the run. Surface any `[Auto:Loop]` decisions per `references/terminal-loop-surface.md` first.

**tmux, exit 1 (outside tmux) — hand the cycle over and stop.** Write a short instruction file telling the operator what it is resuming (the cycle target, the wave layout so far, and that it should continue from Step 5), then:

```bash
rota worker session ensure --body-file <path>
```

That creates the session, spawns an `operator` window running `claude --continue` (which resumes *this* conversation, so the plan and briefs survive), pastes the instruction, and prints the attach command.

**Then stop this cycle immediately.** Print the verb's attach block verbatim and end the run. Do **not** continue to the pool, do not dispatch, do not "keep going in case the handoff failed" — two orchestrators driving one pool dispatch the same task twice and race on the same slots. The handoff either worked (the operator is running it) or the verb exited non-zero (report that and let the user attach by hand). This is a terminal path, so surface any `[Auto:Loop]` decisions per `references/terminal-loop-surface.md` before printing the block.

**Exit 0 (inside the host) — continue.** After creating the cycle branch, stand up the worker pool:

```bash
git checkout -b <cycle-branch>
rota status add <cycle-branch> --items <ID>[,<ID>...]
rota worker pool init --slots <work.workerSlots> --base <cycle-branch>
```

`rota worker pool init` is idempotent — it creates only the slots that are missing and rebuilds any whose worktree went away. Slots live in `<project>/.worktrees/<slot>` (gitignored by `rota init`; a slot registered at the older `.claude/worktrees/rota-worker/<slot>` keeps that path until it is moved). Slots persist across cycles by design; the tmux *windows* or herdr *tabs* are what get recreated per dispatch.

`work.isolation` does not apply on this path: each slot has its own worktree and therefore its own `.git/index`, which is the precondition the isolation guard exists to enforce. Don't also evaluate the guard — it would be checking a condition that cannot occur.

Preconditions worth failing fast on: `tmux` (or `herdr`) on `PATH`, and a `claude` binary (or a `work.workerCommand` that resolves; under herdr it must launch `claude` itself, since `herdr agent start` runs the binary). If either is missing, stop and tell the user to either install it or set `work.dispatch=subagent` — do not silently fall back to the subagent backend, because the user chose this path deliberately and a silent downgrade hides that it didn't happen.

Everything below this point in Step 5 applies to `work.dispatch == "subagent"` only.

### Isolation guard (fires before any worker dispatch)

Before any worker is dispatched, abort fatally if the planned wave has **≥2 commit-producing parallel workers** AND `work.isolation == "branch"` — branch isolation forces concurrent commit-producing workers to share `.git/index`, which races even on disjoint files. Read-only workers (research, lint-only verifications, smoke validators that don't commit) are exempt; under the default write-only pattern the guard rarely fires because the orchestrator owns commits in Step 7.5. See [`references/isolation-guard.md`](references/isolation-guard.md) for the abort message, the M02-S01 incident that motivated the rule, and the full **Forbids / Permits** block.

### Branch / worktree creation

Pick the pattern from `references/isolation-patterns.md` based on `work.isolation` (`"branch"` or `"worktree"` from `.rota/config.json`) and whether umbrella mode is on (Step 4.5 resolved the sub-repo set; carry it forward). The reference's decision table covers all five combinations: single-repo branch, single-repo worktree, umbrella sub-repo branch, umbrella sub-repo worktree (Layout B), umbrella multi-repo branch (via `rota git branch <name> --repos <csv>`, which runs an atomic precheck across every named repo before creating any branches).

The most common case — single-repo, branch isolation — is just:

```bash
git checkout -b <branch>
rota status add <branch> --items <ID>[,<ID>...]
```

For umbrella-mode branch creation (single sub-repo, multi-repo, Layout B worktree), see `references/umbrella-mode.md` *Branch creation* — that reference owns the canonical umbrella ceremony.

**Issue mode** (`backlog.backend: "issues"`; see `references/issue-mode.md`). Once the branch exists, per item: run `rota item ready <ID>`. Exit 1 prints what is missing: warn the user interactively; under `autonomy.level: "loop"` refuse the item. Then claim it with `rota item claim <ID> --as <branch>`. Exit 4 means another worker holds it: drop that item from the wave and continue with the rest (or stop when none remain). Exit 3 or 5: stop and report. Load each item's context as the reference's "Resuming an item" describes (start with `rota item show <ID>`) before planning tasks.

Orchestrator stays at the repo root (or umbrella root in umbrella mode); workers `cd` into their assigned directory before any file operation, and use absolute paths in their briefs.

## Step 6 — Dispatch Worker Agents

**Dispatch vs orchestrator-direct.** When the wave is N near-identical mechanical inserts on disjoint files (e.g. an 18-site SKILL.md sweep), prefer orchestrator-direct parallel `Edit` calls over dispatching N write-only subagents — dispatch overhead exceeds the benefit when the orchestrator already has per-site content. Litmus: *"is this task one `Edit` against a uniquely-anchored `old_string`?"* — yes → inline; no → dispatch.

For each independent task, dispatch a subagent with the **worker** model:

```
You are implementing Task N of [total].
[UMBRELLA: "Sub-repo: <name>. Run all git operations from <absolute-sub-repo-path>; the umbrella's `.git/` is shared coordinator state, NOT your target."]
[WORKTREE: "Working directory: <absolute-worktree-path>. cd there before any file operations."]

**Goal:** [one sentence]

**Files:**
- Create: [paths]
- Modify: [paths with line references]

**What to do:**
[Precise instructions — what to read, what to change, exact code where possible]

**Known gotchas:**
[Relevant bullets from rota knowledge query output]

**Hard boundaries:**
[Relevant entries from rota decisions query — full rule + forbids/permits, not just the rule. Workers MUST respect these; the orchestrator's verification step (Step 7) checks the diff for violations.]

**Canonical terms:**
[Relevant terms from rota glossary read — definition + aliases. Workers MUST use these canonical names in code/comments/commit messages where they apply; aliases are listed so divergent user phrasing in the TODO entry maps back to the right term.]

**Critical constraints:**
[Behavior preservation, patterns to follow, things NOT to touch]

**Claims to verify before building on them:**
[Every factual claim this brief rests on — a line number, a call-site count, "function X already returns Y", "this is the only adopter". Check each one first. If a claim is false, STOP and report which claim and what's actually there — do not implement around it. A wrong claim usually means the task is wrong, and the orchestrator needs to know that more than it needs the code.]

**Do NOT run `git add` or `git commit`.** Write changes to files only. The orchestrator owns the commit phase (Step 7.5) — your job is to leave a clean working-tree diff matching the brief.

**Suggested commit message:** [exact commit message — orchestrator uses this in Step 7.5]

**On completion:** report the list of files you modified, plus any tool-generated siblings the toolchain produced, and confirm you did not stage or commit. Name any brief claim that turned out false, even if you worked around it.
```

**Umbrella mode notes:** when umbrella mode is on, the `[UMBRELLA: ...]` line replaces the WORKTREE line if the wave uses branch isolation; both lines appear together if the wave uses Layout B worktrees. The sub-repo path is the absolute path resolved via `.rota/repos.json` (single-repo callers ignore both lines). Workers MUST `cd` to the named directory before any `git` command — the orchestrator stays at the umbrella for `.rota/` access, so worker commands run in the umbrella's cwd by default and would target the wrong `.git/`.

**Multi-repo dispatch:** for a wave with `<N>` sub-repos in its resolved set, dispatch one worker per sub-repo, each with the sub-repo's name and absolute path in its `[UMBRELLA: ...]` line. Workers run in parallel — each repo has its own `.git/index`, so cross-repo parallelism doesn't trip the parallel-waves-require-worktree-isolation guard (which fires only when ≥2 workers share one `.git/`). Each worker's brief lists only the files in its own sub-repo; the orchestrator verifies each repo's commit independently in Step 7.

Rules for briefs: exact paths + line numbers; show the pattern to follow; name the suggested commit message; read-first, minimal-diff, no unrelated changes; workers do NOT stage or commit.

**Enumerate the brief's falsifiable claims — precision is not correctness.** A brief specific enough to execute (exact line numbers, exact call-site counts, "the only adopter is X") is also specific enough to be *wrong*, and a worker handed a precise wrong instruction implements it faithfully. The claims worth listing are the ones the task's shape depends on: if the claim is false, the task is the wrong task, not just a task with a bad line number. Stale plans are the usual source — see `KNOWLEDGE.md`'s *Re-grep the actual surface before executing a stored plan*, which is the orchestrator-side half of the same failure. Leave the section out when the brief carries no such claim (a from-scratch file creation, a mechanical sweep with a grep-derived list); an empty ritual section trains workers to skip it.

**Cross-referencing parallel artifacts — pre-bake citations.** When two parallel workers author artifacts that cite each other, pre-specify the citation language in each brief; neither worker should need to read the other's output. Works for structural cross-references (cite by path + named role); serialize when citation must quote or restate.

**Doc-writer + helper-writers in the same wave drifts.** Workers authoring docs that show helper output or signatures while sibling workers are writing those helpers will paraphrase the brief and drift from the real code. Pin exact signatures verbatim into the doc-brief, or serialize the doc writer after the helper commit so it reads the actual implementation.

**Rename-task addendum.** Briefs for `git mv old new` (or equivalent rename) tasks MUST carry the Step 4 grep step — instruct the worker to run `git grep -l "<old-name>" -- <scope>` before reporting and extend coverage to every match. Step 7 re-runs the same grep; both layers catch enumerate gaps.

Launch all independent agents in one message (parallel tool calls) — write-only workers don't race on `.git/index`, so this is safe under any isolation mode. Don't announce — just do it.

### Backend branch — `work.dispatch` is `"tmux"` or `"herdr"`

Same brief, different transport. Write each task's brief to a file and dispatch it to a slot:

```bash
rota worker dispatch <wN> --body-file <path> --task <ID>
```

`--task` records the task in `.rota/workers.json` beside the slot's handle and `state: busy`.

A task dispatch runs the slot **reset guard** first (`rota worker reset`): it refuses (exit 4) when the slot's worktree has uncommitted changes or commits that never reached the cycle branch, and otherwise cuts a fresh `rota-worker/<slot>-<task>` branch from the cycle branch tip and records it in `.rota/workers.json`, which `rota worker gate` reads. A refused slot needs its work gated and merged, or `rota worker pool reap <wN>`; do not dispatch around it. Re-dispatching the task the slot already holds (same `--task`, still on that task's branch) is a retry: it keeps the branch's commits and edits instead of refusing. Dispatch also exits 5 when the previous tab or window (and its process tree) cannot be confirmed closed, and exits 4 when `work.workerCommand` carries `-c`, `-r`, `--continue` or `--resume`, which would reopen the old conversation in the fresh session (quoted arguments such as `sh -c "claude -c"` are checked too; an unparseable command exits 2).

Two differences from the subagent path, and only two:

1. **Prepend the standing worker contract** from [`references/worker-contract.md`](references/worker-contract.md) *The standing contract* (under herdr, plus the line in [`references/herdr-dispatch.md`](references/herdr-dispatch.md) *Worker contract additions*). A tmux worker boots with none of this session's context — no conversation, no loaded KNOWLEDGE, no plan — so the rules the subagent path gets implicitly (stay in your tree, stage explicit paths, escalate rather than guess, cite the channel an approval came through in an `## Approvals` PR section) must be in the brief text. `rota worker dispatch` signs the brief `--- ORCHESTRATOR (round N) ---`; pass `--round <N>` on the first dispatch of a round. The contract also defines the two sentinels the worker prints, `ROTA-BLOCKED` and `ROTA-DONE`, which Step 7 routes on.
2. **The brief tells the worker to commit and open a PR** against the cycle branch, replacing the *"Do NOT run `git add` or `git commit`"* line. Keep the `**Suggested commit message:**` line — the worker uses it directly rather than the orchestrator.

The brief **body** — Goal, Files, What to do, Known gotchas, Hard boundaries, Canonical terms, Critical constraints, Claims to verify — is byte-identical to the subagent path. Don't fork the template; a second copy drifts.

Dispatch all slots for a wave in sequence (each call returns once pickup is confirmed), then move to Step 7's poll loop. `rota worker dispatch` exits 6 if a brief never submitted — treat that as a failed dispatch and retry that slot once before reassigning the task. Under herdr, read the tab first (`herdr agent read rota-<slot>-<handle>`): a stall does not prove the text was lost. Exit 5 (herdr only) means a dialog was already open and nothing was sent; inspect the tab and treat it like `needs-permission`.

**Edit-tool race in parallel same-file workers.** When parallel workers edit the same file at different ranges, an `Edit` call may report *"File has been modified since read"* after a sibling worker's edit invalidates the cached state. Mitigation: re-`Read` the file, re-run the same `Edit` with byte-identical `old_string` — do NOT regenerate `old_string` from scratch (risks sibling-edited content).

### Alternative: legacy worker-commits (opt-in)

When a wave touches files that overlap and write-only would racing on disk (rare — usually a planning failure that should be re-decomposed), or when an out-of-band tool requires a commit between worker steps, fall back to the legacy pattern: workers stage and commit themselves. Under this pattern:

- **Single worker per wave under `work.isolation: "branch"`.** Multiple workers committing to the same `.git/index` is forbidden by the [decisions / Skill Authoring / Parallel waves require worktree isolation] rule. Either re-plan as N sequential single-worker waves on a shared branch (the M02 multi-feature pattern), or flip `work.isolation` to `"worktree"` so each worker has its own index.
- **Brief includes `**Commit with message:** [exact text]`** — workers stage their own files and commit, one commit per task. Skip Step 7.5 (orchestrator-side commit) on this path.

This path is documented for completeness; the default write-only pattern above is preferred for new work.

## Step 7 — Verify Each Completion

Orchestrator verifies internally (don't narrate):

Trust the diff, not the worker's narrative — when a worker re-enters files in a later wave, it can mis-attribute its own writes to an earlier wave even when the on-disk output is correct. Verification reads `git diff` and the resulting files; the worker's completion report is supplementary.

1. Inspect pending changes: `git status --porcelain` then `git diff` for the files the worker reported. (Legacy path: `git log --oneline -1` if the worker committed.)
2. Read modified files — changes match the brief.
3. Structural checks: grep for expected patterns, no regressions.
4. **Rename validation (Step 4 rename + link-sweep rule).** Re-run `git grep -l "<old-name>" -- <scope>`; files outside the worker's modified-file set → dispatch a fix-up to extend coverage before staging.
5. **Claim-weight check on gap-fills.** When a worker fills a gap left by extraction (sparse carrier prose, missing rationale), expect plausible-sounding editorial that wasn't in the source. Verify the *claim weight* of any sentence the worker authored, not just structural shape — plausible ≠ sourced.
6. **A worker that disputes its brief is a PASS on the worker and a FAIL on the plan.** When a completion report names a false claim from the `**Claims to verify**` section, do not re-dispatch the same brief with a patched line number — confirm the worker's finding against the code yourself, then re-derive the task from what's actually there. If the correction changes what the task should accomplish (not just where), take it back to Step 4 and re-plan; if it invalidates a stored plan's premise, say so in the commit message rather than editing the plan back. A worker that returns exactly what the brief asked for on a task with falsifiable claims and reports no friction is not evidence the brief was right — spot-check one claim before accepting.

**When the wave produced multiple completions, verify them in parallel** — issue all the `git log`, `Read`, and grep calls for independent tasks in a single tool-call batch, not one task at a time.

**PASS** → move on silently. **FAIL** → dispatch a fix agent, re-verify. Surface failures only if they persist.

**Record proof (subagent path).** For each task that PASSes, append one row per item it resolves: `rota proof add <ID> --check "<verify command or grep>" --result PASS --evidence "<output line or path>" [--sha <task-commit>]`. A FAIL that persists is recorded with `--result FAIL`. Proof rows are facts about what ran, not acceptance: `rota item complete` (Step 9) is the acceptance write and exits 4 when an item has no proof. Loop mode never passes `--no-proof` on its own; an unproven item stays open and is surfaced. In issue mode `rota proof add` works unchanged: the rows go into the item's proof note on the issue.

### Backend branch — `work.dispatch` is `"tmux"` or `"herdr"`

Workers run in their own sessions, so this step gains a poll loop before the review, and a merge gate after it. Full protocol in [`references/tmux-dispatch.md`](references/tmux-dispatch.md) (herdr's host differences in [`references/herdr-dispatch.md`](references/herdr-dispatch.md)); the routing is:

```bash
rota worker poll --json            # data.slots: [{name, state, evidence}, ...]
```

Loop until no slot is `busy`, routing each state as it appears. Each poll also writes the slot's state into `.rota/workers.json`, and the PR URL from `ROTA-DONE` into `slot.pr`, which is what lets the gate merge through the PR:

- **`blocked`** — the worker asked a question and idled. Surface it with `AskUserQuestion`, **in the worker's own words** (it already phrased it for someone without the file open; don't re-encode it into implementation terms). Relay the answer back:

  ```bash
  rota worker dispatch <wN> --body-file <answer-file> --relay
  ```

  **Always pass `--relay` when forwarding a user's answer.** It signs the text as the orchestrator's and logs it in the slot's `relays[]`, which `rota worker gate` checks the PR's `## Approvals` against (verdict `provenance-fail`, exit 1). Without it the worker cites your relay in its PR body as a maintainer sign-off it never received — the relay arrives through the same channel a human answer would, the worker genuinely cannot tell, and once merged it is permanent.

- **`dead`** — the session died (a bare `API Error` on a static pane is a headstone, not a pulse). Re-dispatch the same brief once. If it dies a second time the fault is that session, not the API — hand the task to a different slot rather than trying a third time.

- **`needs-permission`** — the worker stopped at a permission prompt and is waiting on a human. It is **not** idle and it will never self-resolve. Surface it to the user with the slot name and tell them to approve in that pane (tmux: `tmux attach -t <session>`, switch to the slot's window; herdr: the slot's tab, labelled `<wN>`, which also raised a notification). If it recurs across slots, the permission mode is too narrow for what the briefs ask workers to do — the default `acceptEdits` auto-approves file edits but still prompts for `git`, `gh`, and test commands, so a worker briefed to commit and open a PR will stall on its first Bash call. Report that pattern rather than re-dispatching into it; widening it is the user's call via `work.workerCommand`.

- **`limited`** — the session hit its usage window. Re-dispatching onto the same account just hits the same wall, so move the slot instead:

  ```bash
  rota worker account pick --exclude <current-account>   # exit 1 = all cooling
  rota worker account assign <wN> --account <picked>
  rota worker dispatch <wN> --body-file <same-brief>
  ```

  Limits are **per-session rolling windows with staggered resets**, so read each account independently — one slot hard-stopping says nothing about its siblings, and declaring a blanket stall wastes accounts that still have headroom. When `pick` exits 1 every configured account is cooling: report the earliest `resetsAt` from `rota worker account list` and stop the wave rather than spinning.

  **If the evidence mentions `Add funds`, do not answer the prompt.** That option spends real money and is never the orchestrator's to pick — surface it to the user and wait. Local shell work (gating, merging, verification) does not consume the LLM window, so the orchestrator can keep integrating finished slots while one is limited.

  With `work.accounts` unset this state still fires but has nowhere to move the slot to; treat it as a hard stop and tell the user which window is spent.

- **`unknown`** (herdr only) — herdr sees an agent but cannot classify its screen. It is **not** done. Look at the tab and route on what is actually there; never send it to the gate on this state alone.

- **`done`** — the worker opened a PR. Review its diff against the brief using the same rubric as the subagent path above (items 1–6), then gate it:

  ```bash
  rota worker gate <wN> --base <cycle-branch> --json
  ```

  Exit 0 (`data.verdict: pass`) means merged and the merged tree verified (or skipped, `data.verifySkipped: true`): continue. Exit 4 (`data.verdict: approval-required`) is the `merge-approval` manual gate: nothing merged; ask the user (never auto-picked; name `data.paths`), then re-gate with `--confirm --confirm-note "<their answer>"`. Unattended (loop mode, or herdr workers), gate with `--escalate` instead, keep working other slots, and re-gate with `--approval <data.escalation.id>` once `rota round escalate check` reports it answered (`references/manual-gates.md`, "Merge approval in an unattended round"). Every other verdict exits 1; route on `data.verdict`. Exit 3 means the pool, slot, base or worker branch is missing, or the base branch is not checked out.

  | `data.verdict` | Meaning | Action |
  |---|---|---|
  | `stale` | the slot branched before sibling work landed | bounce to the slot to `git merge <cycle-branch>` and re-verify, then re-gate. Bounce **once**; if it goes stale again while re-syncing, resolve it yourself in the worker's worktree and document that on the PR |
  | `merge-failed` | merge conflicted | route the resolution to the slot that owns the branch context, with a summary of what landed. Never resolve a cross-worker semantic conflict blind |
  | `pr-mismatch` | PR is not the verified branch (head SHA moved, wrong head or base, not open) | re-poll the slot; a stacked PR needs its base retargeted |
  | `provenance-fail` | the PR's `## Approvals` cites a relay the slot never received | treat the approval as unverified; ask the user before re-gating |
  | `verify-failed` | the merged tree is broken (`data.changed: true`: the merge landed) | fix forward on the cycle branch; the owning slot has usually moved on |
  | `not-merged` / `not-on-base` | the tracker reported a merge that is not on the base branch | treat as unmerged; check for a scheduled auto-merge or a stacked base, then re-gate |
  | `check-broke` | the gate could not decide (bad ref, failed fetch, unreadable PR, a recorded PR but no `origin` remote) | fix the environment and re-run; do not read it as `stale`. A PR is never merged locally |
  | `merged-remotely` | the PR is on `origin/<base>` but the local base could not fast-forward | do **not** re-merge; reconcile the local base by hand, then re-verify |

  `verify-failed` is the case this gate exists for: two workers with disjoint file sets, each honestly green, merging cleanly into a broken tree. Per-task verification cannot see it — the conflicting change was never in either worker's tree. Do not skip the gate because both diffs looked fine; that is exactly the condition under which it fires.

`data.verifySkipped: true` means `refactor.verifyCommands` is empty and the merged tree was **not** gated by any command. Report that honestly in Step 12 rather than describing the cycle as verified.

## Step 7.5 — Commit per Task (orchestrator)

Under the default write-only pattern, the orchestrator commits each verified task. One commit per task, sequential, in the order the tasks were dispatched.

```bash
git add <task-N-files>
git commit -m "<suggested-message-from-task-N-brief>"
```

Rules:

- **Stage exactly the files named in that task's brief.** No `git add -A`, no `git add .` — sweeping in another worker's changes breaks atomicity.
- **One commit per task.** Even when two tasks share a wave, they get separate commits.
- **Same-file parallel carve-out.** When two parallel write-only workers edit DIFFERENT non-overlapping ranges of the SAME file, strict per-task commits would need fragile `git add -p`. Pragmatic carve-out: combine into ONE commit covering both task IDs in the message; revert granularity is preserved by the message, not the commit boundary.
- **Suggested commit message is the brief's `**Suggested commit message:**` line verbatim.** If verification surfaced a meaningful adjustment (e.g., a fix-up after a FAIL→re-dispatch loop), edit the message to reflect what landed.
- **Worktree isolation.** Run from the worktree path — the orchestrator's cwd is the umbrella, but the commit must happen against the worktree's index. Use `git -C <worktree-path>` or change directory before staging.
- **Umbrella / multi-repo.** Run each task's commit inside its target sub-repo (`git -C <umbrella>/<repo>` or `cd <repo>`). The orchestrator stays at the umbrella; each commit lands in the right `.git/`.

**Skip this step entirely** when the wave used the legacy worker-commits path (Step 6 alternative) — workers already committed.

**Skip this step entirely under `work.dispatch` `"tmux"` or `"herdr"`.** Each slot committed on its own branch and Step 7's gate already merged it into the cycle branch. There is no pending working-tree diff for the orchestrator to stage; running `git add` here would sweep in unrelated state.

## Step 8 — Sequential Waves

For dependent tasks: wait for wave 1 to complete and verify, then dispatch wave 2 with updated context. Same verification. Wave 2 dispatches see a clean working tree because Step 7.5 already committed wave 1's tasks.

## Step 8.5 — Sweep Tool-Generated Siblings

> Run AFTER Step 7.5 has committed each task — siblings get a `chore:` commit of their own, separate from the task commits.

Workers create source files without triggering the toolchain, so sibling artifacts (Step 1 patterns) end up untracked. Sweep them now or the next `/rota-work` guard will refuse on a dirty tree:

```bash
git status --porcelain
git add -A -- <matching sibling paths>
git commit -m "chore: track tool-generated siblings"
```

Non-sibling dirt → surface it; a worker produced unexpected changes and the orchestrator should investigate before merging.

If a tool regenerates siblings only when the editor loads (e.g., Godot `class_name` → `.gd.uid`), force generation once in headless mode before the sweep (e.g., `godot --headless --editor --quit`). Capture project-specific commands in `KNOWLEDGE.md`.

## Step 9 — Update BACKLOG.md

**Issue mode:** skip this step and Step 9.5. Do not call `rota item complete`: the issue closes when its PR merges (`Closes #<n>`, Step 10).

```bash
rota item complete <ID> --commit <commit-hash>
```

Run per resolved item. Match by keyword overlap between task description and TODO entry title. If unsure whether an item was addressed, leave it — don't move items you didn't work on.

## Step 9.5 — Tombstone Consumed Item Plans

For each item ID that `rota item complete` just resolved, remove its corresponding item plan if one was written:

```bash
# For each <ID> the cycle resolved (B07/F03/T11/…):
MILESTONE=$(rota item field get <ID> --name milestone)
if [ -n "$MILESTONE" ] && [ -f ".rota/plans/${MILESTONE}-<ID>.md" ]; then
  rota plan rm "${MILESTONE}-<ID>"
fi
```

Item plans (`.rota/plans/M01-B07.md`) describe how to ship one specific item. Once the cycle ships that item, the plan's task decomposition and assumptions are stale — truth now lives in code + commits. Leaving the file means a future cycle on the same key (e.g., re-opened work) re-reads pre-execution intent that no longer matches the implementation.

Skip silently when:

- The item carries no `Milestone:` tag — no plan key exists for it.
- No plan file is at the resolved key — the `[ -f … ]` guard handles this (untagged items, items that one-shot through `/rota-work` without a written plan).

**Slice plans (`M01-S01.md`) stay.** A slice covers multiple items; completing one item does not consume the slice plan. Slice cleanup is currently manual via `rota plan rm <key>` once the user is done with the slice.

**Commit the close-the-loop changes before merge/PR.** `.rota/BACKLOG.md` (updated by `rota item complete` in Step 9) and `.rota/plans/<key>.md` removals (above) are tracked under the partial-ignore model, so they leave a dirty tree. Step 10's merge/PR refuses on a dirty tree (or silently loses the diffs across the checkout), so stage and commit them here as one "close the loop" commit:

```bash
if ! git diff --quiet -- .rota/ 2>/dev/null || [ -n "$(git ls-files --others --exclude-standard .rota/)" ]; then
  git add .rota/
  git commit -m "chore: close <IDs> — backlog + plan tombstones"
fi
```

Single commit per cycle keeps the loop atomic: the implementation commits ship the code; this final commit closes the backlog and removes consumed plans in one diff that mirrors the cycle's scope.

## Step 10 — Merge or PR

Use `work.mergeStrategy` from `.rota/config.json` to pick `rota ship merge` (direct) or `rota ship pr`. Invocations (umbrella `--repo`, exit-4 verdict and merge-approval handling) are in `rota-ship` Steps 6a/6b. Opening a PR is a manual gate.

When `work.mergeStrategy == "direct"` (or unset — the default), use `rota ship merge`. When `work.mergeStrategy == "pr"`, use `rota ship pr`. The orchestrator never asks at this point in the cycle — the user set the policy via `rota config set`; respect it silently.

**Issue mode forces the PR path**, whatever `work.mergeStrategy` says, and never merges:

```bash
printf '%s' "$BODY" | rota ship pr <branch> --title "<short title>" --body-file - --items <ID1>,<ID2>
rota item state <ID> --to needs-review    # once per item
```

Do not call `rota item release`: the claim persists until the PR merges. Merging is `/rota-review --queue`'s job. Details in `references/issue-mode.md`.

## Step 11 — Update Status

**Single-repo:**

```bash
rota status rm <branch>
```

**Umbrella mode** (when the wave's resolved sub-repo from Step 4.5 is `<repo>`):

```bash
rota status rm <branch> --repo <repo>
```

Without `--repo`, the verb preserves umbrella-tagged entries (only legacy `repo: null` rows are removed) — so umbrella waves MUST pass `--repo` here or the active entry leaks into the next no-argument `/rota-work`.

## Step 12 — Report to User

One compact summary:

```
Done — merged `rota/fix-timer-badge` into main.

- [B01] Timer badge shows stale duration — fixed invalidation in MenuBarManager
- [F03] Quick-switch projects — added Cmd+Tab overlay to project picker

Commit: a1b2c3d
```

Don't recap the plan, list verification results, or describe intermediate steps.

## Step 13 — Learn (Nudge or Auto-Invoke)

Run the post-cycle choreography in `references/post-cycle-trigger-gate.md` with these parameters:

- **Nudge (`"off"`):** *"Capture learnings from this session? Run `/rota-learn` to save durable knowledge before context fades."*
- **Target (`"auto"`/`"loop"`):** **dispatch `rota-learn` via `Skill` immediately — no prompt, no confirmation, no "want me to" question.**
- **Brief:** the cycle's resolved IDs and touched files, so the verifier (if `learn.verify: true`) has the right context.

## Step 13.5 — Decide (Nudge Only)

Trigger: same gating as Step 13, OR the orchestrator noticed a non-obvious pick during verification (e.g., chose SQLite over Postgres, locked a pattern not dictated by existing code). Skip trivial fixes. Don't repeat in the same session.

**Always nudge — never auto-invoke**, regardless of `autonomy.level`. The active/passive split (decisions vs learnings) requires the human pressing the button.

> *"Did this cycle codify any boundaries (e.g., 'X always goes through Y', 'never use Z here')? Run `/rota-decide` to lock them in."*

## Step 13.6 — Docs After-Work (Nudge or Auto-Invoke)

Run the post-cycle choreography in `references/post-cycle-trigger-gate.md` with these parameters:

- **Config flag:** `docs.afterWork` (default `false`). Users opt in via `rota config set docs.afterWork true` or by running `/rota-ship --docs` manually once.
- **Nudge (`"off"`):** *"User-facing changes shipped. Run `/rota-ship --docs` to review and update public docs (after-work mode)."*
- **Target (`"auto"`/`"loop"`):** **dispatch `rota-ship --docs` via `Skill` immediately — no prompt, no confirmation, no "want me to" question.** (Skill dispatch, not inline — the inline variant belongs to `/rota-ship` Step 8.6.)
- **Brief:** the cycle's resolved IDs and touched files, so the after-work flow has the right context.

If `<docs.path>/` doesn't exist or is empty, `/rota-ship`'s Docs Mode after-work flow self-skips (printing a one-line "not yet initialized" notice) — no extra check needed here.

## Step 13.7 — Map After-Work

- **Update project map.** For any `.rota/map/<name>.md` whose `Key files / dirs` or `Entry points` overlap files touched in this cycle, bump `touched:` to today in the frontmatter; refresh `summary:` if the cycle's intent changed it; add new entry points where helpful. Don't rewrite untouched sections. After editing, run `rota map index` to regenerate the `## Project Map` block in `CLAUDE.md`. Stage the updates as part of the cycle's final commit — no separate commit. Skip silently when no map entry matches.

## Step 14 — Refactor (Nudge or Auto-Invoke)

```bash
rota refactor age --json
```

`data` is `{"features": N, "bugs": M}` — counts since the last `refactor:` commit.

Run the post-cycle choreography in `references/post-cycle-trigger-gate.md` with these parameters:

- **Trigger override:** `features >= 5` OR `bugs >= 10` (replaces the gate's default condition; don't-repeat still applies).
- **Nudge (`"off"`):** *"You've shipped [N] features / [M] bug fixes since the last refactor. Might be a good time to run `/rota-refactor` to clean up accumulated friction."*
- **Target (`"auto"`/`"loop"`):** **dispatch `rota-refactor` via `Skill` immediately — no prompt, no confirmation.** No brief needed; `refactor.confirmBeforeExecute` still governs the internal checkpoints.

## Step 15 — Loop Continuation

Only when `autonomy.level == "loop"`. **Re-enter `/rota-work` with no argument immediately — no prompt, no confirmation.** No-Argument Mode reads autonomy, auto-picks and starts the next item, sustaining the loop.

Loop stops naturally when:
- No-Argument Mode reports an empty backlog (or the active milestone has no items and the general backlog is also empty)
- A guard fails downstream (dirty tree, `/rota-review` FAIL, ambiguous brief in Step 2)
- The user interrupts

## Key Principles

- **No noise.** Report results, not process. Don't narrate steps that produced nothing.
- **Orchestrator plans and verifies; worker executes.** Never dispatch without a clear brief. Never trust completion without reading the result.
- **Orchestrator owns `.rota/` state.** Only the orchestrator touches `status.json` and `BACKLOG.md`. Workers focus on implementation.
- **Isolation protects main.** Branch or worktree — never work directly on main.
- **One commit per task, owned by the orchestrator.** Workers write files; the orchestrator commits per task. Clean history, easy revert granularity, no `.git/index` races.

## References

| Reference | Purpose |
|-----------|---------|
| [`ask-user-question-fallback.md`](references/ask-user-question-fallback.md) | Plain-text fallback shape for AskUserQuestion-less hosts. |
| [`banner-preamble.md`](references/banner-preamble.md) | Banner-print rule shared by every skill. |
| [`issue-mode.md`](references/issue-mode.md) | Issue-mode helper map, state labels, PR flow, resuming an item, exit codes (`backlog.backend: "issues"`). |
| [`isolation-patterns.md`](references/isolation-patterns.md) | Branch / worktree creation patterns per work.isolation + umbrella mode. |
| [`knowledge-consult.md`](references/knowledge-consult.md) | Canonical K+D query pattern (`rota knowledge query` + `rota decisions query`) used by every cycle-starting skill. |
| [`post-cycle-trigger-gate.md`](references/post-cycle-trigger-gate.md) | Trigger condition + nudge-or-dispatch choreography for post-cycle steps (13, 13.6, 14). |
| [`worker-contract.md`](references/worker-contract.md) | Standing worker contract and approval provenance for `work.dispatch: "tmux"` / `"herdr"`. |
| [`tmux-dispatch.md`](references/tmux-dispatch.md) | Judgment `rota worker` verbs do not enforce for `work.dispatch: "tmux"` (shared by `"herdr"`): permissions, relay provenance, merge-gate lore, failure modes. |
| [`herdr-dispatch.md`](references/herdr-dispatch.md) | herdr host for worker dispatch: tabs as slots, startup dialogs, worker-contract additions, `work.dispatch: "herdr"`. |
| [`umbrella-mode.md`](references/umbrella-mode.md) | Umbrella-mode verbs, registry shape, and `Repos:` field semantics. |
