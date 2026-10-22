---
name: rota-bugreport
description: Use when rota itself misbehaved (a verb exited 70, a refusal was wrong, a skill step contradicted a verb, the watch reported a false state), on "report a rota bug", "file this upstream", or when `round wind-down` or the Stop hook offers recorded mistakes.
---

# rota-bugreport — File rota's own mistakes upstream

**Nothing is filed without the user's explicit yes, one report at a time.** Filing is the `bugreport-file` manual gate (`rota gate list`), in every autonomy mode. A bug in the user's own project is `/rota-capture`, not this skill.

rota records its own failures in `.rota/bugreports.jsonl` (per-developer, gitignored): a verb that fails with an internal error or panic (exit 70) writes a record by itself. Anything else you noticed, add yourself (Step 1). This skill turns records into reports on the rota repo (`l4ci/rota`, or `$ROTA_UPSTREAM_REPO`).

Copy this checklist and track your progress:
```
- [ ] Step 1 — Record (when invoked by hand)
- [ ] Step 2 — Pick the records
- [ ] Step 3 — Draft and show
- [ ] Step 4 — Check for a duplicate
- [ ] Step 5 — Ask, then file
```

## Step 1 — Record (when invoked by hand)

If the user describes a mistake that is not recorded yet, or you noticed one (a refusal that turned out wrong, a skill step that contradicts a verb, a false watch state):

```
rota bugreport add --summary "<what went wrong>" --expected "<what rota should have done>" --verb "<verb>" --harness <claude|codex|opencode|…> --model <your model>
```

Report your own harness and model: rota fills in only what the slot record knows.

## Step 2 — Pick the records

`rota bugreport list` shows the open records (`id`, source, verb, what). Offer them one by one; the user may skip any. `rota bugreport dismiss <id>` drops one for good. A record nobody answers stays open for the next offer. Never file on your own initiative.

## Step 3 — Draft and show

`rota bugreport draft <id> [--harness H --model M]` prints the report, already redacted: tokens and keys, email addresses, home-directory paths, hostnames, and the project's own repo name and remote URL are removed. **Show the user the redacted text before anything else.** The shape is [`issue-template.md`](issue-template.md): summary, what happened versus what was expected, repro, environment (rota version, OS and architecture, harness, model) and a redaction note.

Fill the gaps rota cannot: a record from an exit 70 has no `Expected` and a thin repro. Add them with a fresh `rota bugreport add` only when the user supplies them; do not invent steps. If the draft still shows something private (a customer name in an error), say so and let the user rewrite it before filing.

## Step 4 — Check for a duplicate

`rota bugreport duplicates <id>` searches the open issues on the rota repo. When it lists a likely match, show it and offer to comment on that issue instead (`--comment <number>` in Step 5). The user decides; a near-match that differs goes in as a new issue.

## Step 5 — Ask, then file

Ask once, per report, with the question showing the exact title and the redacted body: "File this on l4ci/rota?" Only on an explicit yes:

```
rota bugreport file <id> --confirm --confirm-note "<the user's answer, quoted>" [--comment <number>] [--harness H --model M]
```

Without `--confirm` the verb exits 4 and files nothing. The record is marked `filed` with the issue URL. If `gh` is missing or unauthenticated the verb prints where to file it by hand; the record stays open.

Exit 0 with `recorded:false` (or a `warning:` line) is not plain success: the issue exists, but the record could not be marked `filed` and stays open. Tell the user the issue is filed and give the URL, run `rota bugreport dismiss <id>` to close the record, and never refile. Left open, the Stop hook and wind-down re-offer it, and a later yes would file a duplicate.

## Offers

`rota round wind-down` and the Stop hook print one non-blocking line when open records exist. An orchestrator mentions it to the maintainer after the round; it never waits on an answer.
