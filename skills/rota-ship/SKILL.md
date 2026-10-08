---
name: rota-ship
description: Use on "ship it", "open the PR", "finish this branch", or when work is done and ready to integrate. Use --undo on "roll back the last cycle", "revert that merge". Use --docs on "update docs".
---

# rota-ship — Finish a Feature Branch

Copy this checklist and track your progress:

```
- [ ] Step 0 — Mode Dispatch
- [ ] Step 1 — Branch Check
- [ ] Step 2 — Scope the Work
- [ ] Step 3 — Review (opt-in)
- [ ] Step 3.5 — Second-Opinion Gate (opt-in)
- [ ] Step 3.75 — QA Gate (opt-in)
- [ ] Step 4 — Build the PR Body
- [ ] Step 5 — Pick Strategy
- [ ] Step 6a — Open a PR
- [ ] Step 6b — Direct Merge
- [ ] Step 6c — Close Upstream Issues (direct-merge path only)
- [ ] Step 7 — Update Status
- [ ] Step 8 — Mark Unfinished Items Complete
- [ ] Step 8.5 — Learn (Nudge or Auto-Invoke)
- [ ] Step 8.6 — Docs After-Work (inline)
- [ ] Step 9 — Report to User
- [ ] Step 9.5 — Release Nudge
```

## Step 0 — Mode Dispatch

Read `$ARGUMENTS`. No flag: Normal Ship Mode (Steps 1–10), continue below.

When `--undo` is present, read [`undo-mode.md`](undo-mode.md) and follow it; terminal, never falls through to Docs Mode. When `--docs` (or `--docs restructure`) is present, read [`docs-mode.md`](docs-mode.md) and follow it; nothing else in this file applies.

## Configuration

Read `.rota/config.json` (`rota config show`):

- `work.mergeStrategy`: `"pr"` or `"direct"`; unset means ask (Step 5)
- `ship.review`: review depth, `full` (default), `light` or `none`, or a policy object that picks one by diff size and label; `true` and `false` still mean `full` and `none`
- `autonomy.level`: `"off"` (default), `"auto"`: whether Step 8.5 nudges or invokes directly

`ship.secondOpinion` and `ship.qa` are read by [`opt-in-gates.md`](opt-in-gates.md); `docs.path`, `docs.afterWork` and `docs.autoCreate` by [`docs-mode.md`](docs-mode.md).

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

Run `rota review depth <branch> --json` (it reads the labels of a round branch's issue itself; add `--labels` only for labels it cannot see, such as a PR's) and print its `REVIEW-DEPTH` line (`data.depth`, `data.why`) so the user sees which depth applies and why. `none` skips this step, `light` has `/rota-review` dispatch the Standards reviewer only, `full` is the whole review; a label such as `risk:high` can force `full` over a small diff. Skipped when the depth is `none` and for a round worker's PR (branch `<agent>/<issue>-<slug>`, or the brief says it is a round slot); when either applies, read [`review-gate.md`](review-gate.md) and [`round-worker-and-issue-mode.md`](round-worker-and-issue-mode.md). Otherwise invoke `rota-review` via the `Skill` tool, then route on the recorded verdict (umbrella: add `--repo "$REPO"`):

```bash
rota verdict route <branch> --for ship-review --json
```

Exit 3: no verdict recorded; stop and rerun `/rota-review`, never read the report instead. `data.next` `continue` (PASS): go on silently. When it is `ask`, `surface` or `stop`, read `review-gate.md` (the table also serves Steps 3.5 and 3.75).

## Step 3.5 — Second-Opinion Gate (opt-in)

When `ship.secondOpinion` is `true`, read [`opt-in-gates.md`](opt-in-gates.md) (Step 3.5: skip conditions and procedure). Otherwise skip.

## Step 3.75 — QA Gate (opt-in)

When `ship.qa` is `true`, read `opt-in-gates.md` (Step 3.75: skip conditions and procedure). Otherwise skip.

## Step 4 — Build the PR Body

```bash
rota ship body <branch>
```

Capture the output (`## Summary`, `## Items resolved`, `## Evidence` when items carry proof rows) and append `## Test plan`: 2-5 checkboxes, one per meaningful area (not per file), from the touched files, each naming the most visible behavior change. No generic checks.

End the body with one line written from the diff: `Door: one-way|two-way. Blast radius: <surfaces a mistake reaches>.` One-way: a mistake outlives a revert (migration, published format, released API, deleted data); two-way: reverting the PR undoes it. Name surfaces (CLI verbs, skills, docs, file formats), not files. Run the self-audit in `references/humanizing-prose.md` silently and show the post-audit draft.

## Step 5 — Pick Strategy

When the backlog backend is issues, read [`round-worker-and-issue-mode.md`](round-worker-and-issue-mode.md): no question, go to Step 6a.

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

When the backlog backend is issues, read `round-worker-and-issue-mode.md` (`--items`, `needs-review`, skips 6b, 6c and 8).

## Step 6b — Direct Merge

When Step 5 picked "Direct merge", read [`direct-merge.md`](direct-merge.md) (`rota ship merge`, its exit-4 cases). Skip on the PR path.

## Step 6c — Close Upstream Issues (direct-merge path only)

When Step 6b ran, read `direct-merge.md` (the `issue-close` manual gate and the close flow). Skip on the PR path and the issue backend.

## Step 7 — Update Status

```bash
rota status rm [--repo "$REPO"] <branch>
```

Umbrella waves must pass `--repo`; without it the active entry leaks into the next `/rota-work`.

## Step 8 — Mark Unfinished Items Complete

When the backlog backend is issues, or you are a round worker, skip: read [`round-worker-and-issue-mode.md`](round-worker-and-issue-mode.md). File mode: the merger completes items, which outside a round is this step.

`/rota-work` completes most IDs; this catches manual commits that referenced IDs without closing them. For each ID in `referencedIds`:

```bash
rota item complete <ID> --commit <merge-or-last-commit-hash> [--reason handed-off|blocked|dropped --note <text>]
```

Already-completed IDs are a no-op; an unknown ID exits 3. When it exits 4 with `blockedBy: proof missing`, record a row with `rota proof record <ID> -- <command>` (`rota proof add` only for docs-only changes) and rerun: read [`proof-missing.md`](proof-missing.md).

## Step 8.5 — Learn (Nudge or Auto-Invoke)

Round workers skip Steps 8.5 and 8.6 (`round-worker-and-issue-mode.md`). Otherwise run `references/post-cycle-trigger-gate.md` with:

- **Nudge (`"off"`):** append to the Step 9 report *"Capture learnings before context fades? Run `/rota-learn` — this cycle has the fresh session context."*
- **Target (`"auto"`):** dispatch `rota-learn` via `Skill` immediately, no prompt.
- **Brief:** the resolved IDs and touched files.

## Step 8.6 — Docs After-Work (inline)

Skipped for round workers (see Step 8.5). Run `references/post-cycle-trigger-gate.md`, inline variant, with config flag `docs.afterWork` (default `false`; enable with `rota config set docs.afterWork true` or by running `/rota-ship --docs` once). On trigger, read [`docs-mode.md`](docs-mode.md) and run its after-work flow (Steps D-A1 to D-A6) in this session, with the resolved IDs and touched files as context. No `autonomy.level` branch: Step D-A5's approval is the checkpoint.

## Step 9 — Report to User

One compact block:

```
PR opened: https://github.com/.../pull/42
Title: fix: timer badge and quick-switch overlay
Resolved: #12 #15   (file backend: [B01] [F03])
```

or `Merged `rota/demo` into main — commit a1b2c3d` plus the `Resolved:` line. When a review exception or a proof exception applies, `review-gate.md` and `proof-missing.md` say what to append.

## Step 9.5 — Release Nudge

After a successful ship (PR or merge): `rota release pending --json`. If `shouldNudge` is false or `lastTag` is empty (the first release is the user's call), say nothing. Otherwise append `data.message` to the report as one line after `Resolved:`.

## Undo Mode (--undo)

When `--undo` is present, read `undo-mode.md` (Steps U1 to U3: preview, confirm, apply). Terminal.

## Docs Mode (--docs)

When `--docs` is present, read `docs-mode.md` (Steps D1–D6 first-run scaffold, D-A1–D-A6 after-work). Only `--docs` and Step 8.6 load it.

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

- [`undo-mode.md`](undo-mode.md) — `--undo` rollback (Steps U1 to U3).
- [`docs-mode.md`](docs-mode.md) — `--docs` scaffold and after-work flow, docs config.
- [`opt-in-gates.md`](opt-in-gates.md) — second-opinion and QA gates, their config and skip rules.
- [`review-gate.md`](review-gate.md) — verdict routing table, `REVIEW_CHOICE`, review-skipped case.
- [`round-worker-and-issue-mode.md`](round-worker-and-issue-mode.md) — round worker skips, issue backend and umbrella branches.
- [`direct-merge.md`](direct-merge.md) — Steps 6b and 6c.
- [`proof-missing.md`](proof-missing.md) — Step 8 proof-missing handling.
- [`references/authoring-conventions.md`](references/authoring-conventions.md) — skill authoring rules.
- [`references/docs-conventions.md`](references/docs-conventions.md) — docs page layout and naming.
- [`references/humanizing-prose.md`](references/humanizing-prose.md) — self-audit for the PR body.
- [`references/issue-mode.md`](references/issue-mode.md) — issue backend: claim, proof note, PR path.
- [`references/manual-gates.md`](references/manual-gates.md) — always-manual gates (`pr-open`, `issue-close`).
- [`references/post-cycle-trigger-gate.md`](references/post-cycle-trigger-gate.md) — when the Step 8.5 and 8.6 triggers fire.
- [`references/review-verdict-routing.md`](references/review-verdict-routing.md) — CONCERNS options and carrier labels.
- [`references/silent-failure-hunter.md`](references/silent-failure-hunter.md) — rubric carried by the review brief.
- [`references/three-mode-skill-shape.md`](references/three-mode-skill-shape.md) — normal, undo and docs mode layout.
- [`references/worker-contract.md`](references/worker-contract.md) — round-worker rules.
