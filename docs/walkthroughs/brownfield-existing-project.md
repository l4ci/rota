# Brownfield: dropping rota into an existing project

You have a codebase. You've been maintaining it for months or years. You're tracking a list of bugs informally, some open GitHub issues you haven't gotten to, and a vague sense that the same gotchas keep recurring. By the end of this walkthrough rota is wired into the project, your bugs are captured, one is shipped, and a first lesson is in `KNOWLEDGE.md`.

The example project is **Pinpoint**, an internal incident dashboard. Node and React, deployed as a single container. It's been in production for 18 months. 14 open GitHub issues, 30-odd `TODO` comments scattered through the source, and three bugs you keep meaning to fix.

## The shape of the walkthrough

```mermaid
flowchart LR
  REPO[(existing repo<br/>+ open GH issues)] --> INIT["rota init"]
  INIT --> SUBS[(.rota/map/<br/>6 subsystem files,<br/>hand-authored)]
  INIT --> CAP["/rota-capture"]
  CAP --> BACKLOG
  BACKLOG --> NEXT["/rota-work"]
  NEXT --> WORK["/rota-work B05<br/>P0 secrets-in-URL"]
  NEXT --> DEBUGCYCLE["/rota-debug B01<br/>severity casing"]
  WORK --> SHIP["/rota-ship<br/>Closes #41"]
  DEBUGCYCLE --> LEARN["/rota-learn"]
  LEARN --> KNOW[(KNOWLEDGE.md<br/>Alerts)]
  SUBS -.consults.-> WORK
  SUBS -.consults.-> DEBUGCYCLE
  KNOW -.consults.-> WORK
```

Ten steps follow, in execution order.

## Step 1: rota init

```bash
$ rota init
```

Same defaults as a greenfield setup. For an existing repo I usually flip two with `rota config set`: `worktree` isolation so `main` stays untouched while agents run (useful when you also need to deploy from `main` mid-cycle), and `pr` merge strategy if your team requires GitHub review. For a solo maintenance pass, the defaults are fine.

`rota init` writes `.rota/` and the managed blocks in `AGENTS.md` (`CLAUDE.md` imports it). It doesn't read your code. That happens next.

## Step 2: Scaffold the project map by hand

`/rota-work` and `/rota-debug` need to know what subsystems your project has so they don't burn context re-exploring the same directories on every cycle. The project map lives in `.rota/map/<name>.md`: one Markdown file per coherent area of the codebase, hand-authored. `rota init` already created the empty `.rota/map/` directory in Step 1; you fill it in now.

For Pinpoint you spend ten minutes sketching out:

```
.rota/map/
  api.md           server/api/*           (Express routes, request validation)
  dashboard.md     web/src/dashboard/*    (React dashboard pages)
  alerts.md        server/alerts/*        (rule engine, dedup, escalation)
  integrations.md  server/integrations/*  (Datadog, PagerDuty, Slack)
  storage.md       server/storage/*       (Postgres pool, migrations)
  shared.md        shared/*               (cross-cutting types, utils)
```

Each file is short. A typical one:

```markdown
---
subsystem: alerts
summary: Rule engine that ingests events, dedups, and escalates to integrations.
touched: 2026-05-16
created: 2026-05-16
---

## Purpose
Rule-based alerting. Reads events, evaluates rules, dedups within a window, and escalates via integrations.

## Entry points
- server/alerts/engine.js:14 — main rule loop
- server/alerts/dedup.js:8 — dedup key + window logic

## Key files / dirs
- server/alerts/

## Conventions specific here
- Severity is normalized at the integration boundary, not at storage time.

## Notes / gotchas
- Dedup window is hardcoded; per-rule windows are on the backlog.
```

Run `rota map index` once after writing the files; it pulls each file's `summary:` into the always-on `## Project Map` block in `AGENTS.md`. The map isn't exhaustive; just enough for the orchestrator to know where to look. After every `/rota-work` cycle, touched subsystems get their `touched:` date bumped automatically and the always-on block is regenerated. When subsystems drift or duplicate later, edit or retire the relevant `.rota/map/<name>.md` files by hand.

## Step 3: open issues (optional)

If your project has open GitHub or GitLab issues, set `backlog.backend` to `"issues"`: the issues already are the backlog, so there is nothing to import. (`/rota-capture --from-github` / `--from-gitlab` was removed.) On the file backend, create an item by hand with `rota item create`, adding a `GH: #N` tag so `/rota-ship` emits `Closes #N`. Round-trip closing runs through `/rota-ship`: the PR body gets `Closes #N` lines, or the direct-push path offers a manual-gated `rota issues close` prompt.

If your project has no remote tracker, skip this step entirely.

## Step 4: /rota-capture for the mental backlog

The remaining items, the ones you've been tracking informally, go in via `/rota-capture`. Brain-dump in one go; the model splits, classifies, and assigns IDs. The IDs and files below are the file backend's; on the issues backend each item is an issue and its ID is `#N` ([issue backend](../usage/issue-backend.md)).

```bash
$ /rota-capture "alert rule editor crashes on empty title; we should add a /health endpoint for k8s; dedup window is hardcoded at 5 min, should be per-rule; DST off-by-one on dashboard 24h filter"
```

Output:

```
[B06] alert rule editor crash on empty title  Bug, P1, Major
[F01] /health endpoint for k8s probes         Feature, Minor
[F02] per-rule dedup window                   Feature, Major
[B07] DST off-by-one on dashboard 24h filter  Bug, P1, Major
```

The detail files at `.rota/bugs/B06.md` etc. capture the long-form description for items that overflow the one-line BACKLOG row.

F02 is size-Major. `/rota-capture` nudges you:

> *F02 is Major and touches multiple subsystems. Consider /rota-brainstorm F02 before /rota-plan, for design negotiation, not implementation.*

You take the nudge. `/rota-brainstorm F02` runs a focused design session: Socratic discovery, two competing approaches with tradeoffs, sectioned design with per-section approval. The output lands at `.rota/designs/F02.md`. When `/rota-plan F02` runs later, it reads that file as soft input rather than re-deriving the design.

## Step 5: /rota-work surveys everything

```bash
$ /rota-work
```

It reconciles `status.json` against git (nothing active yet, clean state), archives completions older than five days (none yet), and shows you the full sorted picture:

```
In Progress: (none)

Bugs (sorted P0 → P2):
  [B05] integration config UI accepts secrets in URL   P0 Major  GH: #41
  [B01] alert dedup fires twice on rapid escalation    P1 Major  GH: #14
  [B02] dashboard date filter ignores timezone         P1 Major  GH: #21
  [B03] Datadog drops events under rate-limit          P1 Major  GH: #28
  [B06] alert rule editor crash on empty title        P1 Major
  [B07] DST off-by-one on dashboard 24h filter         P1 Major
  [B04] PagerDuty incident link uses old API           P2 Minor  GH: #33

Features (sorted Major → Cosmetic):
  [F02] per-rule dedup window                          Major     design: .rota/designs/F02.md
  [F01] /health endpoint for k8s probes                Minor

Suggested next: [B05] (P0 first)
Run /rota-work B05? [y/N]
```

P0 always wins. You confirm.

## Step 6: a hot-path fix that skips the queue

Before you commit to B05, you spot a typo in the contributing guide while scanning another file. Too small to queue.

```bash
$ /rota-capture "fix typo in CONTRIBUTING.md line 23, 'depencency' → 'dependency'"
[T01]
$ /rota-work T01
```

`/rota-capture` mints `[T01]` and stops; `/rota-work T01` which dispatches a worker, lands one commit on a feature branch, merges back. About thirty seconds. Capture and execute in one pass for things too small to queue.

## Step 7: the P0 cycle

Back to B05. The integration config UI accepts secrets in a URL query string field instead of routing them through a password input. They end up in browser history and access logs.

```bash
$ /rota-work B05
```

The orchestrator reads B05's detail file, queries `KNOWLEDGE.md` for the `Security` topic (empty so far), reads `.rota/map/integrations.md` for entry points, and decomposes:

```
Task 1. Render integration-config secrets via <input type="password">.
        Verify: snapshot test on the form asserts type="password" on every field marked secret.
Task 2. Server-side validation rejects secrets in URL query for the integration POST endpoint.
        Verify: integration test posts a URL-form-encoded secret, asserts 400 with the right error.
Task 3. Migration to scrub existing access logs for known secret patterns.
        Verify: dry-run against a fixture log, redacted-line count matches expected.
Task 4. Update integration docs to describe the new form.
        Verify: docs build passes.
```

Four tasks, four commits, all on `rota/B05-secrets-in-url-form`. Worktree isolation keeps `main` clean. The branch merges back via `--no-ff`, so the cycle is one revertable unit if anything goes sideways downstream.

## Step 8: a real debug cycle

Two days later you tackle B01, the dedup-fires-twice issue. You run `/rota-work B01`. The orchestrator dispatches, the worker writes what looks like a fix, and the verify step fails:

```
[worker] verify: test/alerts/dedup_test.js (FAIL — 2 events still delivered)
[worker] verify failed — stopping. Recommend /rota-debug B01.
```

You take the nudge.

```bash
$ /rota-debug B01
```

The cycle:

1. **Reproduce.** Run the failing test, confirm two events. Add console logs at the dedup gate.
2. **Hypothesize.** `dedupKey` is built from `rule_id + entity_id + severity`. The logs show the second event arrives with severity `"high"`, while the first was `"HIGH"`. The two integrations send different casing.
3. **Verify the hypothesis.** Add an assertion: `dedupKey` should match across the two events when severity is lowercased. The assertion passes. Hypothesis confirmed.
4. **Fix.** Lowercase `severity` in the dedup-key computation. One-line change. Re-run the failing test; it passes. Run the full alerts suite; green.
5. **Commit.** `fix(alerts): normalize severity casing in dedup key [B01]`.
6. **Nudge /rota-learn.**

Run it.

```bash
$ /rota-learn
```

The bullet that lands:

```markdown
## Alerts

- **Severity is case-sensitive in the dedup key path.** External integrations send inconsistent casing for severity (Datadog sends `high`, PagerDuty sends `HIGH`). Normalize at the boundary, not at storage time. The dedup gate runs before normalization. Fix once at the integration layer, or accept that the bug-class returns.
```

This bullet now travels with every future `/rota-work` and `/rota-debug` cycle in the `alerts` or `integrations` subsystems. Next time you or a teammate touches dedup logic, the orchestrator pulls this paragraph into its planning context automatically. The class of bug doesn't recur.

## Step 9: /rota-ship

When the branch is ready:

```bash
$ /rota-ship
```

`/rota-review` reads commits, resolved IDs, and any `KNOWLEDGE.md` topics matching touched files. For the B01 fix it returns `PASS`. `/rota-ship` builds a PR body from the commit subjects and the `GH: #14` cross-reference on B01, opens the PR via `gh`, and prints the URL. On merge, GitHub auto-closes #14 because the body includes `Closes #14`. `BACKLOG.md` moves B01 to `## Completed` and stamps it with the merge commit SHA.

If you'd configured `work.mergeStrategy = direct` instead, `/rota-ship` would have merged into `main` directly and prompted the optional `rota issues close` step to close #14 upstream with a tracking comment naming the commit.

## Step 10: over the next week

After a week of dropping rota into Pinpoint:

- `BACKLOG.md` has 11 items from the issue-linked items and the brain-dump; six are shipped, two in flight, the rest queued
- `KNOWLEDGE.md` has four to six bullets across `Alerts`, `Storage`, `Integrations`, and `Security`. Surprises worth keeping, not a fix log
- `.rota/map/` has six subsystems, three of them `touched: 2026-05-19` (this week) and three older
- `DECISIONS.md` has one hard boundary you committed to mid-cycle: *"Secrets are never accepted via URL query params on any integration endpoint. Forbids: query-string POST bodies. Permits: form bodies with `type=password` inputs."* Future workers must respect it; the orchestrator surfaces it during planning if a touched file is in scope.
- One milestone if you decided to add one, e.g. `M01 — security hardening`. Or none, if you've been working straight off the backlog. Either is fine; brownfield doesn't require a milestone to be useful.

What you notice over time is that the same class of gotcha stops recurring. Three months from now, when a teammate ships a new integration and trips the severity-casing bug, the next `/rota-work` cycle pulls the `## Alerts` knowledge bullet into context and the orchestrator flags it during planning, before any code lands.

## What changes structurally

You don't have to refactor anything to adopt rota. The only structural addition is `.rota/` (tracked by default, with a few machine-specific paths gitignored) and a managed block in `AGENTS.md`. Your existing build, tests, deploy pipeline, and code layout stay the same. The map and the knowledge accumulate from how you already work (debug, fix, ship), except now the loop leaves a trace that future cycles consult automatically.

## Scale to a round

Once the backlog holds several independent, well-specified items, you don't need to drive each `/rota-work` cycle yourself. A [parallel round](../usage/parallel-rounds.md) has an orchestrator hand issues to workers in separate worktrees and merge what passes the gate. Use it when you have a queue of issues that don't touch the same files; keep `/rota-work` for the handful you are watching.
