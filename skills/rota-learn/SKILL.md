---
name: rota-learn
description: Use at the end of a session that surfaced reusable knowledge, after a correction-rich debugging arc, or on "save what we learned", "capture this learning", "/rota-learn".
---

# rota-learn — Capture Session Learnings

Copy this checklist and track your progress:
```
- [ ] Step 1 — Parse Args
- [ ] Step 2 — Scan the Session for Learnings
- [ ] Step 3 — Classify by Topic
- [ ] Step 4 — Auto-Write
- [ ] Step 5 — Merge into KNOWLEDGE.md
- [ ] Step 6 — Update the Topic Index
- [ ] Step 7 — Opus Verification (opt-in)
- [ ] Step 8 — Confirm
```

## Step 1 — Parse Args

- `--strict`: run the Opus verifier (Step 7) for this run.
- `--retro [<session-id|path>]`: retrospective mode. Skip Steps 2 to 8 and follow *`--retro`* in [`references/learn-rare-modes.md`](references/learn-rare-modes.md), then exit.
- `--term <name>`, `--promote <topic> "<title>"`, `--deprecate <topic> "<title>"`, `--amend <topic> "<title>"`: manual modes. Skip Steps 2 to 8 and follow the matching section of [`references/learn-rare-modes.md`](references/learn-rare-modes.md), then exit.

With none of those, run the normal flow (Step 2 onward).

## Step 2 — Scan the Session for Learnings

Capture what would save a future `/rota-work` run from re-discovering it: gotchas, project conventions not obvious from code, constraints and invariants, root causes of hard-won bugs, decisions with rationale, tool quirks.

**Skip:** anything documented in code or README, transient session state, obvious facts, framework-doc restatements, personal preferences.

If nothing qualifies, say so and stop. Don't manufacture learnings.

## Step 3 — Classify by Topic

Reuse existing `## Topic` headings in `.rota/KNOWLEDGE.md`; new topic only if nothing fits, never one per learning.

## Step 4 — Auto-Write

Skip approval prompts. Proceed to Step 5 (merge into `KNOWLEDGE.md`) and Step 6 (update the topic index).

Verification is **off by default**. Run Step 7 only when `--strict` is in the args or `.rota/config.json` has `learn.verify: true`.

## Step 5 — Merge into KNOWLEDGE.md

Format of `.rota/KNOWLEDGE.md` (size nudges for big topics come in Step 8; the merge always proceeds):

```markdown
# Knowledge

## <Topic>
- **<Title>** — <learning body> <!-- 2026-04-18 -->
- <older legacy learning without title>
```

Each new bullet: short sentence-case `**Title**`, an em-dash (U+2014, not a hyphen), the body, and a trailing `<!-- YYYY-MM-DD -->` stamp. `rota knowledge add` dedups by (topic, title, case-insensitive): a repeat is a silent no-op and never overwrites. Leave untitled legacy bullets as-is.

For each captured bullet, call:

```bash
printf '%s' "$BODY" | rota knowledge add --topic "<Topic>" --title "<Short rule title>" --body-file -
```

The verb inserts at the top of the topic and stamps the date.

**Pre-step rules:**

- **New topics:** `## <Topic>` must already exist. Append the heading to `.rota/KNOWLEDGE.md` first (alphabetical, except `Build & Tooling` and `Architecture` may be pinned near the top), then call the verb.
- **Sharpened wording:** the verb never replaces an older entry. For a sharper version of an existing bullet, edit that bullet and skip `knowledge add`.

Exit 0 on insert or idempotent no-op (`changed: false`); exit 3 if the topic doesn't exist.

### Umbrella-mode routing

When `.rota/repos.json` registers at least one sub-repo, read [`umbrella-routing.md`](umbrella-routing.md) before calling `rota knowledge add`: it covers the `--repo` scope flag and the scope question. Step 6 and `/rota-learn --term` follow the same scope.

Single-repo projects: no `--repo` needed.

## Step 6 — Update the Topic Index

```bash
rota block knowledge
```

Updates the managed `<!-- rota-knowledge-start -->` block in the project instructions file (`AGENTS.md` if present, else `CLAUDE.md`; the verb resolves it, never hardcode). `/rota-work` reads this block to know when to consult `KNOWLEDGE.md`.

When `.rota/repos.json` registers a sub-repo, read [`umbrella-routing.md`](umbrella-routing.md) (*Step 6*) for the `--repo` flag.

## Step 7 — Opus Verification (opt-in)

Skip unless `--strict` was passed or `learn.verify` is `true`. Read [`verifier.md`](verifier.md) (dispatch, prompt, verdict rules) and follow it, then continue to Step 8.

## Step 8 — Confirm

Report in one compact block:

```
Captured 3 learnings into .rota/KNOWLEDGE.md:
  Testing (2 new)
  Networking (1 new)

Updated the topic index in <AGENTS.md|CLAUDE.md> — /rota-work will consult these on relevant tasks.
```

**Topic-size handling.** Run `rota knowledge stats --json`. When any topic in `data.topics` has `bullets >= 25` OR `bytes >= 10240`, read [`topic-size.md`](topic-size.md) (nudge or auto-split by `autonomy.level`).

If verification ran and passed, add a middle line: `Opus verification: PASS — all entries durable, sharp, correctly categorized.` If it returned `PASS_WITH_NOTES`, replace that line with a one-liner naming what was adjusted. If it failed, say so and stop.

**Contradictions.** Run `rota knowledge contradiction list --json`. If `data.items` is empty, skip silently; otherwise process the queue per [`references/learn-rare-modes.md`](references/learn-rare-modes.md) (*Process contradiction candidates*).

## Key Principles

- **Durable, not ephemeral.** If it only matters this week, use `/rota-capture`.
- **Sharp and short.** One sentence, concrete claim; otherwise link to code.

## References

- [`references/persistence-skills.md`](references/persistence-skills.md): shared spine and divergence axes for `/rota-learn` and `/rota-decide`, including `--term` for Glossary entries.
- [`references/learn-rare-modes.md`](references/learn-rare-modes.md): `--term`, `--promote`, `--deprecate`, `--amend`, and the contradiction queue.
- [`umbrella-routing.md`](umbrella-routing.md): Step 5 / 6 `--repo` scope flag and scope question in umbrella mode.
- [`topic-size.md`](topic-size.md): Step 8 nudge or auto-split for large topics.
- [`verifier.md`](verifier.md): Step 7 Opus verifier dispatch, prompt and verdict rules.
