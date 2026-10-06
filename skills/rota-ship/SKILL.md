---
name: rota-ship
description: Use on "ship it", "open the PR", "finish this branch", or when work is done and ready to integrate. Use --undo on "roll back the last cycle", "revert that merge". Use --docs on "update docs".
---

## Step 0 — Mode Dispatch

Read `$ARGUMENTS`. Route on the first flag present:

| Args contain | Route to |
|---|---|
| `--undo` | **Undo Mode**: guided rollback of the last cycle on the base branch. Terminal: never falls through to Docs Mode. |
| `--docs` (or `--docs restructure`) | **Docs Mode**: maintain the public user guide under `<docs.path>/`. Read [`docs-mode.md`](docs-mode.md) and follow it; nothing else in this file applies. |
| (none) | **Normal Ship Mode** (Steps 1–10). Step 8.6 inline-runs Docs Mode's after-work flow when `docs.afterWork: true` and the trigger fires. |

# rota-ship — Finish a Feature Branch

## Configuration

Read `.rota/config.json` (`rota config show`):

- `work.mergeStrategy` — `"pr"` or `"direct"`; unset means ask (Step 5)
- `ship.review` — `true` (default) runs `/rota-review` first; `false` skips it
- `ship.secondOpinion` — `false` (default); `true` runs a no-prior-context adversarial review after `/rota-review` (Step 3.5), except for round PRs, which never get one.
- `ship.qa` — `false` (default); `true` runs `/rota-qa run` after the reviews (Step 3.75)
- `autonomy.level` — `"off"` (default), `"auto"`: whether Step 8.5 nudges or invokes directly
- `docs.path` (default `"docs"`), `docs.afterWork` (default `false`), `docs.autoCreate` (default `false`)

## When to Use

- Feature branch has 1+ commits and the work is done (including after `/rota-work` with `mergeStrategy: "pr"`).
- Not mid-work (finish via `/rota-work`), not with nothing committed, not to resume a paused branch (`/rota-work` with no argument).

## Step 1 — Branch Check

```bash
rota git guard feature-branch
```

Exit 1 (`data.reason` `base` or `detached`): pass the message through and stop.

## Step 2 — Scope the Work

```bash
rota status show --json <branch>        # data.repo is $REPO, null in single-repo projects
rota review scope --json [--repo "$REPO"] <branch>
```

Keep the scope JSON (commits, `touchedFiles`, `referencedIds`, `intents`) for later steps. Exit 1: no commits beyond the base; tell the user and stop.

## Step 3 — Review (opt-in)

Skipped when `ship.review` is `false`, and for a round worker's PR (branch `<agent>/<issue>-<slug>`, or the brief says it is a round slot): review is the orchestrator's seat, so a worker never runs `/rota-review` on its own branch (`skills/references/worker-contract.md`). Skip Step 3's routing and treat `REVIEW_CHOICE` as unset. Otherwise invoke `rota-review` via the `Skill` tool. Its brief carries the silent-failure rubric (`references/silent-failure-hunter.md`); `SILENT-FAIL` flags arrive as CONCERNS. Route on the recorded verdict (umbrella: add `--repo "$REPO"`):

```bash
rota verdict route <branch> --for ship-review --json
```

Exit 3: no verdict recorded; stop and rerun `/rota-review`, never read the report instead. Act on `data.next`; the table also serves Steps 3.5 and 3.75.

| `data.next` | Meaning | Do |
|---|---|---|
| `continue` | PASS | Go on silently. |
| `ask` | CONCERNS | Surface each concern, then `AskUserQuestion` with the options in `references/review-verdict-routing.md`: Address via `/rota-work` (Recommended) / Ship anyway / Stop. |
| `surface` | Advisory gate (QA under `qa.gate: advisory`, any QA `INFRA-FAIL`, advisory second opinion) | Surface the findings, continue. A missing dev server or credentials never blocks a ship. |
| `stop` | FAIL | Stop. Surface the findings; the user fixes via `/rota-work` or `/rota-debug` and reruns `/rota-ship`. |

Label surfaced concerns by producer (carrier labels in `references/review-verdict-routing.md`): "Second-opinion concerns", "QA concerns".

Remember a CONCERNS answer as `REVIEW_CHOICE` (`address`, `ship-anyway`, `stop`); Steps 3.5, 3.75 and 9 read it. A review FAIL also makes `rota ship pr` and `rota ship merge` refuse (exit 4, `data.blockedBy: "verdict"`), but stop here.

## Step 3.5 — Second-Opinion Gate (opt-in)

Skipped for a round worker's PR (branch `<agent>/<issue>-<slug>`, or the brief says it is a round slot; the orchestrator's merge gate is the second check, `docs/contributing/rounds.md`). Also skipped when `ship.secondOpinion` is `false`, when Step 3 was skipped and the user has not asked for a second opinion this session, or when `REVIEW_CHOICE == ship-anyway`.

Otherwise run Step 3.5 in [`opt-in-gates.md`](opt-in-gates.md).

## Step 3.75 — QA Gate (opt-in)

Skipped when `ship.qa` is `false` or `REVIEW_CHOICE == ship-anyway`. If there is no `.rota/qa/` strategy for the scope (single repo: no `.rota/qa/*.md`; umbrella: no `.rota/qa/<REPO>.md`), say *"`ship.qa: true` but no QA strategy for `<scope>`. Run `/rota-qa first-run` to bootstrap, or set `ship.qa: false` to skip."* and continue.

Otherwise run Step 3.75 in `opt-in-gates.md`.

## Step 4 — Build the PR Body

```bash
rota ship body <branch>
```

Capture the output (`## Summary`, `## Items resolved`, `## Evidence` when items carry proof rows) and append `## Test plan`: 2-5 checkboxes, one per meaningful area (not per file), from the touched files, each naming the most visible behavior change. No generic checks.

End the body with one line written from the diff: `Door: one-way|two-way. Blast radius: <surfaces a mistake reaches>.` One-way: a mistake outlives a revert (migration, published format, released API, deleted data); two-way: reverting the PR undoes it. Name surfaces (CLI verbs, skills, docs, file formats), not files. Run the self-audit in `references/humanizing-prose.md` silently and show the post-audit draft.

## Step 5 — Pick Strategy

On the issue backend (`references/issue-mode.md`) there is no question: go to Step 6a, never direct-merge. If `work.mergeStrategy` is `"direct"`, say so in one line (*"`work.mergeStrategy` is `direct` but the issue backend always opens a PR; ignoring it."*) so the mismatch is visible, then go on.

If `work.mergeStrategy` is `"direct"` or `"pr"` and the user has not said otherwise this session, use it silently. If unset or the user hinted otherwise, ask (single-select, header `"Strategy"`, *"How should I integrate `<branch>`?"*):

- `"Direct merge"` — *"Merge into the base with `--no-ff` and delete the branch."*
- `"PR"` — *"Push and open a PR with the body."*

Mark the configured strategy `(Recommended)`; unset defaults to Direct merge.

## Step 6a — Open a PR

> **Manual gate — filing a public artifact (`pr-open`).** Opening a PR creates externally visible state. This step is **always manual** — never auto-invoked, regardless of `autonomy.level`. Skill-enforced only (the verb does not refuse), so never skip the Step 5 question or the user's go-ahead. See `references/manual-gates.md`.

```bash
printf '%s' "$BODY" | rota ship pr <branch> --title "<short title>" --body-file - [--repo <name>]
```

Title: strongest commit subject, 70 characters max, no `[ID]` tags. Share the PR URL. Exit 4 with `data.blockedBy: "verdict"` is a recorded FAIL: surface it and stop.

**Issue mode:** add `--items <ID1>,<ID2>` (qualified `<repo>:<ID>` in an umbrella) so the PR closes them, then `rota item state <ID> --to needs-review` per item. Do not call `rota item release`: the claim stays until the PR merges. Shipping never merges here. The merge owner is the orchestrator in a round (`rota worker gate`), otherwise whoever runs `/rota-review --queue`. Skip Steps 6b, 6c and 8.

## Step 6b — Direct Merge

```bash
printf 'merge: <summary>\n\n- item 1\n- item 2\n' | rota ship merge <branch> --body-file - [--repo <name>]
```

The subject must start `merge: ` (undo recognizes cycles by it). Share the hash from `data.sha`. Exit 4: `data.blockedBy: "verdict"` is a recorded FAIL; surface and stop. `"manual gate"` is the `merge-approval` gate (`ship.mergeApproval` requires a human; `data.paths` names the files that triggered it) and nothing changed: ask in an `AskUserQuestion`, then rerun with `--confirm --confirm-note "<their answer>"`. A merge conflict (also exit 4) is aborted by the verb; tell the user.

## Step 6c — Close Upstream Issues (direct-merge path only)

Skip on the PR path (`rota ship body` already emits `Closes #N`) and on the issue backend (the tracker issues close when the PR merges).

`rota issues imported --json --open-only`; keep `data.entries` whose `itemId` is in the shipped IDs from Step 2. None: skip silently.

> **Manual gate — closing public upstream issues (`issue-close`).** Closing posts a comment and changes issue state on the remote. This step is **always manual** — never auto-invoked, regardless of `autonomy.level`. Skill-enforced only. See `references/manual-gates.md`.

Ask (header `"Close"`, *"Close N upstream issue(s) tied to the shipped items? (`#N, …`)"*): `"Yes, close all"` / `"Pick subset"` / `"No, leave open"`. For a subset, a second multiSelect `AskUserQuestion` (header `"Pick issues"`, options `"#N (item <ID>)"`, chunk by 4). Close each selected issue in one parallel batch:

```bash
rota issues close <N> --commit <merge-sha> --item <ID> [--repo <name>]
```

On "No, leave open" print *"Skipping upstream issue close — N issue(s) left open. Run `gh issue close <N>` / `glab issue close <N>` manually if desired."*

## Step 7 — Update Status

```bash
rota status rm [--repo "$REPO"] <branch>
```

Umbrella waves must pass `--repo`; without it the active entry leaks into the next `/rota-work`.

## Step 8 — Mark Unfinished Items Complete

**Issue mode:** skip. Closing happens at the merge (`rota worker gate` in a round, `rota ship pr-merge` from the queue). Use `rota item complete` only with `--reason handed-off|blocked|dropped`.

**File mode:** the merger completes items. Outside a round that is this step; a round worker skips it (workers never edit tracked `.rota/`) and the orchestrator completes the items when it merges the PR.

`/rota-work` completes most IDs; this catches manual commits that referenced IDs without closing them. For each ID in `referencedIds`:

```bash
rota item complete <ID> --commit <merge-or-last-commit-hash> [--reason handed-off|blocked|dropped --note <text>]
```

Already-completed IDs are a no-op; an unknown ID exits 3.

Exit 4 with `blockedBy: proof missing`: no `## Proof` row. Record one row per executed check that passed during this ship (the Step 3.75 QA run, or the project's test or smoke command), then rerun:

```bash
rota proof add <ID> --check "<command that ran>" --result PASS --evidence "<summary line or log path>" --sha <merge-or-last-commit-hash>
```

A `/rota-review` or second-opinion PASS is acceptance, not proof (it runs nothing), so it never becomes a row. If no executed check exists, ask: run the project's test command now and record it (Recommended) / close with `--no-proof` (the user's call, named in the Step 9 report) / leave the item open. Never pass `--no-proof` without that answer.

## Step 8.5 — Learn (Nudge or Auto-Invoke)

**Round workers skip Steps 8.5 and 8.6** (branch `<agent>/<issue>-<slug>`, or the brief says it is a round slot): the orchestrator runs learn and docs once per round. Otherwise run `references/post-cycle-trigger-gate.md` with:

- **Nudge (`"off"`):** append to the Step 9 report *"Capture learnings before context fades? Run `/rota-learn` — this cycle has the fresh session context."*
- **Target (`"auto"`):** dispatch `rota-learn` via `Skill` immediately, no prompt.
- **Brief:** the resolved IDs and touched files.

## Step 8.6 — Docs After-Work (inline)

Skipped for round workers (see Step 8.5). Run `references/post-cycle-trigger-gate.md`, inline variant, with config flag `docs.afterWork` (default `false`; enable with `rota config set docs.afterWork true` or by running `/rota-ship --docs` once). On trigger, read `docs-mode.md` and run its after-work flow (Steps D-A1 to D-A6) in this session, with the resolved IDs and touched files as context. No `autonomy.level` branch: Step D-A5's approval is the checkpoint. A missing or empty `<docs.path>/` makes the flow skip itself.

## Step 9 — Report to User

One compact block:

```
PR opened: https://github.com/.../pull/42
Title: fix: timer badge and quick-switch overlay
Resolved: #12 #15   (file backend: [B01] [F03])
```

or `Merged `rota/demo` into main — commit a1b2c3d` plus the `Resolved:` line. If `REVIEW_CHOICE == ship-anyway`, append a one-line list of the concerns the user proceeded through. If Step 8 left IDs open for lack of proof, append `Unproven (still open): <ID> …`; if the user chose `--no-proof`, append `Closed without proof: <ID> …`.

## Step 9.5 — Release Nudge

After a successful ship (PR or merge): `rota release pending --json`. If `shouldNudge` is false or `lastTag` is empty (the first release is the user's call), say nothing. Otherwise append `data.message` to the report as one line after `Resolved:`.

## Undo Mode (--undo)

Read [`undo-mode.md`](undo-mode.md) and follow it (Steps U1 to U3: preview, confirm, apply). Terminal.

## Docs Mode (--docs)

Read `docs-mode.md` and follow it: Steps D1–D6 (first-run scaffold) and D-A1–D-A6 (after-work). Only `--docs` and Step 8.6 load it.

## Gates: Thought → Reality

| Thought | Reality |
|---|---|
| "The review report reads fine, skip the verdict." | `rota verdict route` exit 3 means none was recorded: rerun `/rota-review`. Never read the report instead. |
| "CONCERNS are minor, ship anyway." | CONCERNS routes to `ask`: surface each one, then the user picks. `FAIL` is `stop`, and `rota ship pr` and `rota ship merge` refuse it (exit 4, `blockedBy: "verdict"`). |
| "I'm a round worker, run `/rota-review` once to be safe." | Review is the orchestrator's seat. Skip Step 3 on a `<agent>/<issue>-<slug>` branch. |
| "Autonomy is on, so open the PR." | `pr-open` and `issue-close` are always manual, whatever `autonomy.level` says. Ask first. |
| "The merge touched few files, `--confirm` it." | `blockedBy: "manual gate"` is `merge-approval`: ask, then rerun with `--confirm --confirm-note "<their answer>"`. |
| "`--undo` only rewinds one cycle, no need to ask." | It runs `git reset --hard`, unrecoverable past the reflog. The confirmation is the only guard. |

## Key Principles

- **Read-only until Step 6.** Review, scoping and body generation mutate nothing.
- **One integration pass.** If review passes, ship.
- **Titles stay clean.** The body carries the linkage.

## References

`authoring-conventions.md`, `docs-mode.md`, `docs-conventions.md`, `humanizing-prose.md`, `issue-mode.md`, `manual-gates.md`, `post-cycle-trigger-gate.md`, `review-verdict-routing.md`, `silent-failure-hunter.md`, `three-mode-skill-shape.md`.
