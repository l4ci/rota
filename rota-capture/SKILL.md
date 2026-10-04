---
name: rota-capture
description: >-
  Capture bugs, features, and tasks into BACKLOG.md without executing them. Classifies each item, assigns priority/size, mints zero-padded IDs ([B01], [F01], [T01]). Also supports `--remove <ID>[,<ID>...]` to delete captured items and clean up cross-references (dry-run + confirmation gate), and `--from-github` / `--from-gitlab` to pull open upstream issues into the backlog with `GH: #N` / `GL: #N` cross-refs and round-trip closing via `/rota-ship`. Use when the user brain-dumps work, says "capture", "add to backlog", "note this bug", "/rota-capture", "remove [B07]", "delete this entry", "drop this item", "import issues", "pull open issues from GitHub", "list issues", or describes a problem without asking for an immediate fix. Records, then offers to work a single captured item now via /rota-work; items already in BACKLOG go straight to /rota-work.
---

**Print the banner below verbatim before any other action — skip if dispatched as a subagent.** See `references/banner-preamble.md`.

```
════════════════════════════════════════════════════════════════════════
  📥  rota-capture  ·  capture work items into .rota/BACKLOG.md
  triggers: "capture", "log bug"  ·  pairs: rota-work, rota-brainstorm
════════════════════════════════════════════════════════════════════════
```

# rota-capture — Capture & Manage Work Items

Quick-capture bugs, features, and tasks into `.rota/BACKLOG.md` with just enough context to act on them later. Handles multiple items and mixed types in one pass. `--remove <ID>` strips an item (the local inverse of capture); `--from-github` / `--from-gitlab` pull open upstream issues in with `GH: #N` / `GL: #N` cross-references (closing happens via `/rota-ship`).

## Step 1 — Task list

**Initialize task list.** Follow `references/task-list-init.md` — load `TaskCreate(…)` via `ToolSearch select:TaskCreate,TaskUpdate` if needed, then create one task per phase of the mode below.

- **Capture:** mode / dispatch, audit code state (milestone specs only), classify, dedupe, append, report, offer to work it.
- **Remove (`--remove`):** resolve IDs, preview, de-tag upstream issues, confirm, apply.
- **Import (`--from-github` / `--from-gitlab`):** resolve repos, discover candidates, pick, capture, label upstream, report.

## Step 1.5 — Mode Dispatch

| First arg | Mode |
|-----------|------|
| `--remove <ID>[,<ID>...]` | [Remove Mode](#remove-mode) |
| `--from-github` / `--from-gitlab` | [Import Mode](#import-mode); the flag fixes the provider, in umbrella mode the resolved sub-repo set decides which repos are scanned |
| anything else / nothing | Step 2 onward |

Skip Steps 2 to 8 in the other two modes.

## Step 2 — Parse & Classify

The user gives a keyword, phrase or longer description, possibly several issues of mixed types. **Split it into distinct items**, each a separate concern that would get its own ID. Clues: separate sentences about unrelated problems, "also…", "plus…", a list, mixed bug/feature/chore language.

| Goes to | When the item describes… |
|---------|--------------------------|
| `## Bugs` | Broken behavior: worked and stopped, or doesn't work as expected |
| `## Features` | New or enhanced behavior that doesn't exist yet |
| `## Tasks` | Chores and maintenance: refactoring, dependency updates, docs, CI, cleanup |

Before any write, run `rota git guard clean --context "/rota-capture"` once and remember whether it exited 0. Step 8 needs the pre-capture answer, because the capture itself dirties `.rota/BACKLOG.md`.

## Step 2.5 — Audit Against Code State (milestone-spec capture only)

Fires only when the input captures *from a milestone spec*: it names an `M<NN>` tag or a `milestones/M<NN>.md` path. Otherwise skip.

Milestone specs drift behind code. Capturing criteria that already shipped under other IDs creates duplicate work and, under `autonomy.level: loop`, dispatches `/rota-work` on finished work (real F27 incident: 11 captures, 10 already shipped). Run `rota item shipped "<title 1>" "<title 2>" …` with the parsed titles. Exit 0 means ship evidence was found (stdout lists hits per title, `--json` has `data.titles[].hits`); exit 1 means none, continue silently.

On exit 0, print the report verbatim, then by `autonomy.level`:

- **`loop`:** auto-skip every flagged title, one line each: *"Skipped `<title>` — ship evidence in `<top-match-hash>`."* Silently capturing already-shipped work is what loop must not do.
- **`off` / `auto`:** `AskUserQuestion`, up to 4 flagged titles per call. Header `"Item N"`. Question: *"`<short-title>` looks shipped — `<hash>` `<subject>`. What now?"* Options:
  1. *"Skip this item (Recommended)"* — drop it from this run.
  2. *"Capture anyway"* — the user reviewed the matches and the item is genuinely distinct.
  3. *"Stop the whole capture"* — print *"Capture aborted — reconcile the milestone spec before retrying."* and write nothing.

Plain-text fallback: *"Skip, capture anyway, or stop?"* Filtered titles never reach the backlog.

## Step 3 — Gather Context

Gather **just enough context** to make each item actionable later. Ask 2 to 4 quick questions total across all items, not per item. Skip anything the user already answered; a detailed input may need none. Pick from:

- **Bugs:** expected vs. actual, trigger steps, every time or intermittent, which view/component, error output.
- **Features:** user-facing behavior, which part of the app, existing workaround, what triggers the need.
- **Tasks:** goal, area of the codebase, deadline or dependency, relevant context (error output, PR link).

## Step 4 — Assign Priority / Size

| Bugs | Meaning |
|------|---------|
| `[P0]` | Blocks usage: crash, data loss, can't complete core workflow, security issue |
| `[P1]` | Degrades experience: wrong behavior, broken feature, workaround exists |
| `[P2]` | Minor annoyance: cosmetic glitch, edge case, user unlikely to notice |

| Features | Meaning |
|----------|---------|
| `[Major]` | New screens, significant rework, breaks existing patterns, multi-day |
| `[Minor]` | Contained change: new option, small UI addition, 1 to 3 files, hours |
| `[Cosmetic]` | Visual polish: spacing, color, label tweak, minutes |

Tasks get no priority or size tag.

## Step 4.5 — Tag Active Milestone (when applicable)

Follow `references/milestone-tagging.md`: the `rota milestone active` gate, the question shapes, loop-mode auto-pick, plain-text fallback. Carry the chosen milestone as `--milestone` in Step 6. Omit it if the user left the item untagged.

## Step 4.6 — Tag Sub-Repo (when umbrella mode is on)

Gate and registry semantics: `references/umbrella-mode.md`. Ask only when `rota repo umbrella` exits 0 and `.rota/repos.json` registers at least one sub-repo (the registry is the truth, not the config flag). Otherwise skip silently.

**Issue mode** (`backlog.backend: "issues"`; `references/issue-mode.md`): an item lives on one sub-repo's tracker, so pick exactly one repo (single-select, no multi-repo option) and pass `--repos <name>`. `rota item create` refuses multi-repo items: for work spanning repos, capture one item per repo and link them with `Related:`.

Otherwise ask:

- **Header:** `"Repos"`; **Question:** *"Which sub-repo(s) does this item belong to?"* (with the item's short title)
- **multiSelect:** true
- **Options:** one per `name` in `.rota/repos.json` (mark the likely match `(Recommended)` when the item text names a repo), then *"None / unsure — leave untagged"* last.

Two or more repos make a multi-repo item that `/rota-work` branches in each repo. If *"None / unsure"* comes with concrete names, the names win. Plain-text fallback: ask once; an ambiguous reply leaves the item untagged, and `/rota-work` will then refuse it and point back here.

**Loop mode:** auto-pick the `(Recommended)` repo. With none flagged (item is ambiguous), still ask: this is the ambiguity that should surface (`references/authoring-conventions.md` rule #5).

Carry the picks as a comma-separated list of registered sub-repos into `--repos`. Omit the flag if untagged.

## Step 5 — Handle Large Input

When an item's input would bloat the entry beyond about 3 sentences (stack traces, logs, specs, long repro), use `references/detail-files.md` and pass the file as `--body-file`. Skip this for items that fit in 1 to 3 sentences.

## Step 6 — Write All Entries

**Consult the Glossary.** Scan the `## Glossary` topic of `.rota/KNOWLEDGE.md` (`rota glossary read <term>`). If the user's phrasing maps to a canonical term or alias, use the canonical name. If the capture introduces a new domain concept the user names, suggest `/rota-learn --term <name>` afterwards; never auto-invoke.

Create each item in one command; it prints the new ID:

```bash
ID=$(rota item create --json --kind bugs --title "Short title" --tag P1 --desc "Description." --related "[F02]" | jq -r .data.id)
```

Flags: `--kind bugs|features|tasks`, `--tag` (`P0`-`P3` for bugs, `Major`/`Minor`/`Cosmetic` for features, none for tasks), `--desc`, `--related`, `--milestone`, `--repos`, `--subsystem`, `--body-file`. See `rota item create --help` and `docs/design/contract/backlog.md` (*rota item create*) for ID minting, field order, the `Since:` stamp, detail-file placement and the issue-backend mapping; none of that is the skill's job.

Judgment the skill does own:

- **`--related`:** link only items that clearly relate. Scan `## Bugs`, `## Features`, `## Tasks` and `.rota/ARCHIVE.md` for connections; items in the same batch can reference each other. Don't force links.
- **`--subsystem`:** match filenames and skill names in the user's text against `.rota/map/` (or the `## Project Map` block in CLAUDE.md), e.g. `rota-work` or `rota init`. Pass `Subsystem: <name>` only on a confident match; never block or delay capture for it.
- **`--desc`:** what happens, when, what should happen instead (bugs); what it does, where, why it matters (features); what and why (tasks). One to three sentences.

## Step 7 — Brainstorm Nudge

Fires when the batch includes a `[Major]` feature or a `[P0]` bug; skip otherwise. In every autonomy mode, add one line per qualifying ID to the report, after other post-capture nudges and a blank line:

> *"Run `/rota-brainstorm [ID]` before `/rota-plan` to negotiate the design."*

**Never invoke `/rota-brainstorm` from here.** Capture is pure intake. Advancement without asking lives in `/rota-work`: with no argument it reconciles and suggests the next item, and in `loop` mode it auto-dispatches `/rota-brainstorm --auto-loop` for Major, milestone-tagged items without a design.

Confirm what you wrote: show every added entry grouped by section.

## Step 8 — Work It Now? (optional)

Runs at the end of a normal capture only, never after `--remove` or import.

- **Several items captured, or `autonomy.level` is `loop`:** skip silently. Loop already chains into `/rota-work`; this step adds no new loop path.
- **Step 2's guard exited non-zero:** ask nothing. Tell the user the item is captured and the working tree needs cleaning (commit or stash) before `/rota-work` can start.
- **Otherwise** (one item, clean tree): `AskUserQuestion`, header `"Work it now?"`, question *"Work `[ID] <title>` now?"*. Options:
  1. *"Work it now"*, marked `(Recommended)` only when the item is neither a `[Major]` feature nor a `[P0]` bug without a design (those want `/rota-brainstorm` first, per Step 7).
  2. *"Not now"*.

On yes, invoke `/rota-work` through the Skill tool with a brief: the captured ID, title, short description and detail-file path (if any). Plain-text fallback: *"Work it now? (yes/no)"*; anything but yes is no.

---

## Remove Mode

The inverse of capture. `rota item rm` owns the mechanics: BACKLOG entry, `Related:` cross-references, the detail file and any plan keyed to the item. It previews by default and only `--apply` writes. ARCHIVE entries stay unless `--scrub-archive`, the only audit trail a removed item has left. Counters never decrement. Contract: `docs/design/contract/backlog.md` (*rota item rm*).

### Step R1 — Resolve IDs

Split the argument on commas and pass the IDs as positionals. Exit 3 means an ID is unknown: show stderr and stop.

### Step R2 — Dry-Run Preview

Run `rota item rm <IDS>` and show stdout verbatim. If an item has `activeBranch` (`--json`: `data.items[].activeBranch`), tell the user: *"The item is active on `<branch>`. Apply refuses it until the stream is dropped with `rota status rm <branch>`."* and continue.

### Step R3 — De-tag Upstream Issues (manual gate)

> **Manual gate — removing the upstream label.** Removing the `in-progress` label upstream is externally visible: collaborators see the issue no longer claimed. This step is **always manual** — never auto-invoked, regardless of `autonomy.level`. The item delete proceeds either way; this decides only whether the label is cleaned up too. See `references/manual-gates.md`.

Find upstream links: `rota issues imported --json`, keep `data.entries` whose `itemId` is in the removal set. Read the label from `rota config show --json issues.label` (default `in-progress`). No matches: skip to Step R4.

Otherwise ask, and never auto-pick in loop mode:

- **Header:** `"De-tag"`; **Question:** *"Remove the `<label>` label on <N> upstream issue(s)? <list of #N>."*
- **Options:**
  1. *"Yes, remove the label upstream"* — `rota issues label <issue> --remove "<label>"` per entry, adding `--repo <repo>` when the entry's `repo` is non-null. Propagate a failure.
  2. *"No, just delete the item"* — print: *"Note: upstream issues still carry the `<label>` label. Remove via `gh issue edit <N> --remove-label <label>` or `glab issue update <N> --unlabel <label>` if desired."*

### Step R4 — Confirmation Gate

Show the preview, then one `AskUserQuestion`. Header `"Apply"`, question *"Apply this removal plan for <IDS>?"*:

1. *"Apply (Recommended)"* — `rota item rm --apply <IDS>`; ARCHIVE entries stay as the historical record.
2. *"Apply + scrub ARCHIVE"* — `rota item rm --apply --scrub-archive <IDS>`; also removes the ARCHIVE entry and its cross-references.
3. *"Cancel"* — print *"No changes."* and stop.

Plain-text fallback: *"Apply changes? (yes/no/scrub-archive)"*; anything else cancels. This is a destructive gate: it always asks, and loop mode does not accelerate it (`references/authoring-conventions.md`).

### Step R5 — Apply

Run the chosen command and pass its per-ID output through verbatim. On exit 4 (`data.blockedBy: "active"`), tell the user to run `rota status rm <branch>` first and stop. Don't nudge any other skill.

**When to use `--remove`:** duplicate of an existing item, wrong premise, made obsolete by other work, captured against the wrong project or milestone, ruled out by a spike or decision, or the user changed their mind. It does not close upstream issues; that is always manual.

---

## Import Mode

Under `backlog.backend: "issues"` the open issues already are the backlog: skip this mode and tell the user. The rest applies to the file backend.

Fetch open issues from upstream, subtract those already in the backlog, let the user pick, capture the picks, and label them upstream behind a manual gate. The provider is fixed by the flag. The verbs live under `rota issues` (`docs/design/contract/backlog.md`, *rota issues*).

### Step I1 — Resolve Target Repo Set

If `rota config show issues.providers.<github|gitlab>` resolves to `false` for the flag's provider, stop: *"Provider disabled: set `issues.providers.<github|gitlab>` to `true` in `.rota/config.json` to enable."* (No verb checks this flag; the skill does.)

**Single-repo mode** (`rota repo umbrella` exits 1 or the registry has no sub-repos): target is cwd's repo. If `rota issues provider` names the other provider, stop: *"Provider mismatch: --from-<flag> requires a <flag> remote; cwd resolves to <other>."*

**Umbrella mode:** from `.rota/repos.json`, keep the repos whose `rota issues provider --repo <name>` matches the flag (drop the rest silently). Ask which to scan: `AskUserQuestion`, multiSelect, header `"Repos"`, question *"Which sub-repos to pull issues from?"*, chunks of at most 4 options: *"All repos"* first and `(Recommended)`, one option per repo, *"None / cancel"* last. **Loop mode:** auto-pick all repos. Plain-text fallback: *"Which repos? (all / <name> / none)"*; ambiguous means all. If no repos resolve, fall back to single-repo mode.

### Step I2 — Discover Candidates

Per target repo, run `rota issues list --json [--repo <name>] [--mine]` and `rota issues imported --json [--for-repo <name>]`. Pass `--mine` when `issues.filterMineOnly` is true. Never pass the `issues.label` value as a filter: it is the label applied in Step I6, not a source filter. A repo whose `issues list` fails (CLI missing or unauthenticated, no provider) is skipped with a `skipped: <repo> — <error>` line; one repo's failure never fails the step.

### Step I3 — Subtract Already-Imported Issues

An issue is imported when its `(provider, repo, number)` matches an `imported` entry; in single-repo mode `repo` is null, so match `(provider, number)`. Report *"Repo `<name>`: N candidates, K already imported — showing M."* If nothing remains, print *"Nothing to capture — all open issues are already in BACKLOG.md or ARCHIVE.md."* and stop.

### Step I4 — Pick

`AskUserQuestion`, multiSelect, header `"Issues"`, chunks of at most 4 candidates, question *"Which issues to capture? (<range> of <total>)"* (drop the range for one chunk). Option label `"#<N>: <title, 60 chars>"`, description `"<labels> · by @<author> · <url>"`. Plain-text fallback: comma-separated numbers or `none`. **Loop mode:** auto-pick every remaining candidate; Step I6 still asks.

### Step I5 — Classify and Capture Each Pick

**Classify from remote labels:** `bug`/`kind/bug` to Bugs, `enhancement`/`feature`/`kind/feature` to Features, anything else to Tasks. Classify silently.

**Priority / size:** bugs default `[P1]`, features default `[Minor]`, silently. Ask only when the body says otherwise: crash, data loss or outage wording suggests `[P0]`; cosmetic or wording suggests `[P2]`; new screens, significant rework or multi-repo scope suggests `[Major]`. One `AskUserQuestion` per such pick, header `"Classify #<N>"`, question *"Priority / size for '<title>'?"*, up to 4 options for the section with the default marked `(Recommended)` (bugs: `"[P0] — crash / data loss"`, `"[P1] — broken feature (Recommended)"`, `"[P2] — cosmetic / edge case"`, `"Leave default"`). **Loop mode:** never ask; use the defaults.

**Create:** write the issue body to a scratch file as markdown, followed by `---`, `**Upstream:** <url>`, `**Captured from:** <provider> #<N>`. Then:

```bash
ID=$(rota item create --json --kind <bugs|features|tasks> --title "<Title>" --tag <Tag> --desc "<first sentence or two>. <GH: #N|GL: #N>" --body-file <scratch-file> --repos <name> | jq -r .data.id)
```

Keep the `GH: #N` / `GL: #N` tag exactly: it is the signal `rota ship body` uses to emit `Closes #N`, and the key `rota issues imported` indexes. Drop `--repos` in single-repo mode and `--tag` for tasks. Process picks serially; parallel minting risks counter collisions.

### Step I6 — Apply the `in-progress` Label Upstream

> **Manual gate — labeling upstream issues.** Applying the `in-progress` label (or the configured `issues.label`) upstream is externally visible: collaborators see the issues marked as claimed. This step is **always manual** — never auto-invoked, regardless of `autonomy.level`. The orchestrator may stage which issues to label, but the user confirms before any label is written. See `references/manual-gates.md`.

No verb enforces this gate (registry: `issue-label`, skill only), so this paragraph is the only guard. Ask with `AskUserQuestion`, header `"Label upstream"`, question *"Apply `<label>` to these <N> issues upstream?"* plus a list of picked titles and numbers:

1. *"Yes — apply `<label>` to all (Recommended)"* — `rota issues label <N> --add <label> [--repo <name>]` per issue, in parallel. A failure is printed inline; continue with the rest.
2. *"No — skip labeling"* — print *"Labeling skipped. Issues are captured in BACKLOG.md but not marked upstream."*

Plain-text fallback: *"Apply `<label>` to these issues upstream? (yes/no)"*; default is skip, since silence is not consent. **Loop mode:** auto-picking Yes is forbidden. Surface the question and pause the loop until the user answers, as `/rota-ship` Step 6a does for acceptance-of-risk answers.

### Step I7 — Compact Report

```
Captured <N> issues:
- [ID1] Title 1 (GH #42 → in-progress)
- [ID2] Title 2 (GL #7 → in-progress)
Skipped <K> issues (already imported).
```

Use `→ not labeled` when labeling was skipped. Append a `Skipped repos:` list with reasons for repos skipped in Step I2. Import writes `.rota/BACKLOG.md` and makes no commit; `/rota-work` bundles the backlog update into its close-the-loop commit.

---

## Rules

- Capture appends only: never remove or reorder existing entries, never investigate now.
- Show the user every entry written, grouped by section.
- Remove: preview is the default; the de-tag gate (Step R3) and apply gate (Step R4) always ask.
- Import: the label gate (Step I6) is never bypassed by any `autonomy.level`; loop mode auto-picks routing answers (which repos, which issues) only.

## References

| Reference | Purpose |
|-----------|---------|
| [`authoring-conventions.md`](references/authoring-conventions.md) | Loop-mode auto-picks, destructive and manual gates. |
| [`banner-preamble.md`](references/banner-preamble.md) | Banner-print rule shared by every skill. |
| [`detail-files.md`](references/detail-files.md) | Detail-file template for bulky input. |
| [`issue-mode.md`](references/issue-mode.md) | Issue-backend umbrella rules (Step 4.6). |
| [`manual-gates.md`](references/manual-gates.md) | Manual-gate callout shape (Step R3 de-tag, Step I6 label upstream). |
| [`milestone-tagging.md`](references/milestone-tagging.md) | Milestone-tagging question shapes (Step 4.5). |
| [`task-list-init.md`](references/task-list-init.md) | Task-list init pattern (Step 1). |
| [`umbrella-mode.md`](references/umbrella-mode.md) | Umbrella-mode verbs, registry shape, `Repos:` semantics. |
