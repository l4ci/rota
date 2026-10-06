---
name: rota-learn
description: Use at the end of a session that surfaced reusable knowledge, after a correction-rich debugging arc, or on "save what we learned", "capture this learning", "/rota-learn".
---

# rota-learn — Capture Session Learnings

**Task list.** Track these phases with the host's task tool if it has one:

1. *Scan session* — transcript + recent commits sifted for durable gotchas (Step 2)
2. *Classify topic* — each candidate matched to a `KNOWLEDGE.md` topic (Step 3)
3. *Merge into KNOWLEDGE.md* — entries appended under topic headings (Step 5)
4. *Update topic index* — `rota block knowledge` regenerates the managed block in the instructions file (Step 6)
5. *Verify (Opus)* — only under `--strict` or `learn.verify: true` (Step 7)
6. *Confirm* — compact summary and size nudges (Step 8)

## Step 1 — Parse Args

- `--strict` — run the Opus verifier (Step 7) for this run.
- `--retro` — retrospective mode. Skip Steps 2 to 8 and follow *`--retro`* in [`references/learn-rare-modes.md`](references/learn-rare-modes.md), then exit.
- `--term <name>`, `--promote <topic> "<title>"`, `--deprecate <topic> "<title>"`, `--amend <topic> "<title>"` — manual modes. Skip Steps 2 to 8 and follow the matching section of [`references/learn-rare-modes.md`](references/learn-rare-modes.md), then exit.

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

Each new bullet: short sentence-case `**Title**`, an em-dash (U+2014, not a hyphen), the body, and a trailing `<!-- YYYY-MM-DD -->` stamp. `rota knowledge add` dedups by (topic, title): a repeat is a silent no-op and never overwrites. Leave untitled legacy bullets as-is.

For each captured bullet, call:

```bash
printf '%s' "$BODY" | rota knowledge add --topic "<Topic>" --title "<Short rule title>" --body-file -
```

The verb inserts at the top of the topic, stamps the date and dedups (case-insensitive title).

**Pre-step rules:**

- **New topics:** `## <Topic>` must already exist. Append the heading to `.rota/KNOWLEDGE.md` first (alphabetical, except `Build & Tooling` and `Architecture` may be pinned near the top), then call the verb.
- **Sharpened wording:** the verb never replaces an older entry. For a sharper version of an existing bullet, `Edit` that bullet and skip `knowledge add`.

Exit 0 on insert or idempotent no-op (`changed: false`); exit 3 if the topic doesn't exist.

### Umbrella-mode routing

When `.rota/repos.json` registers at least one sub-repo, read [`umbrella-routing.md`](umbrella-routing.md) before calling `rota knowledge add`: it covers the `--repo` scope flag and the scope question. Step 6 and `/rota-learn --term` follow the same scope.

Single-repo projects: no `--repo` needed.

## Step 6 — Update the Topic Index

```bash
rota block knowledge
```

Updates the managed `<!-- rota-knowledge-start -->` block in the project instructions file (`AGENTS.md` if present, else `CLAUDE.md`; the verb resolves it, never hardcode). `/rota-work` reads this block to know when to consult `KNOWLEDGE.md`.

In umbrella mode, pass `--repo <scope>`, the same scope the learning was written to: it regenerates that sub-repo's instructions file (umbrella topics first, then sub-repo-only topics); `--repo umbrella` or no flag regenerates the umbrella/project file. DECISIONS are umbrella-only and never take `--repo`.

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

**Topic-size handling.** Run `rota knowledge stats --json`; for any topic in `data.topics` with `bullets >= 25` OR `bytes >= 10240`, branch on `autonomy.level` (`.rota/config.json`):

- `"off"` (default) — append a single nudge line per offender to the confirm output:

  ```
  Note: `<topic>` is large (<bullets> bullets, <bytes-as-KB-rounded-1dp> KB). Consider splitting it (e.g. `<topic>: <facet-A>` + `<topic>: <facet-B>`) to reduce per-query cost in /rota-work, /rota-debug, /rota-plan.
  ```

  KB = `{bytes/1024:.1f}`. The user accepts or declines the split.

- `"auto"` — **perform the split immediately — no prompt, no confirmation, no "want me to" question.** For each offender topic:

  1. Read the topic's bullets via `rota knowledge query "<topic>"`.
  2. Group bullets into 2 or 3 cohesive facets by theme. Each facet holds ≥3 bullets; `Misc` / `Other` / `Etc.` facets are forbidden. If no plausible split axis exists, fall back to the `"off"` nudge for that topic and skip steps 3–7.
  3. Append `## <Topic>: <FacetA>` and `## <Topic>: <FacetB>` headings to `.rota/KNOWLEDGE.md` immediately before the old `## <Topic>` heading.
  4. For each bullet in `<Topic>`, call `rota knowledge rename-topic --from "<Topic>" --to "<Topic>: <Facet>" --title "<bullet-title>"`. The verb moves the bullet byte-identical and re-keys its `.rota/knowledge-tier.json` entry atomically. Issue all calls for one offender as one parallel batch. Do NOT hand-edit bullets via `Edit`; it orphans sidecar entries.
  5. Remove the now-empty old `## <Topic>` heading.
  6. Re-run `rota block knowledge` to refresh the managed `<!-- rota-knowledge-start -->` block.
  7. Append one line to the confirm output: `Auto-split <topic> → <topic>: <FacetA> + <topic>: <FacetB> — N → A+B bullets.`

  Split each offender at most once per session.

If verification ran and passed, add a middle line: `Opus verification: PASS — all entries durable, sharp, correctly categorized.` If it returned `PASS_WITH_NOTES`, replace that line with a one-liner naming what was adjusted. If it failed, say so and stop.

**Contradictions.** Run `rota knowledge contradiction list --json`. If `data.items` is empty, skip silently; otherwise process the queue per [`references/learn-rare-modes.md`](references/learn-rare-modes.md) (*Process contradiction candidates*).

## Key Principles

- **Durable, not ephemeral.** If it only matters this week, use `/rota-capture`.
- **Sharp and short.** One sentence, concrete claim; otherwise link to code.

## References

- [`references/persistence-skills.md`](references/persistence-skills.md) — Shared spine and divergence axes for the persistence duo (`/rota-learn`, `/rota-decide`) — including `/rota-learn --term` for Glossary entries.
- [`references/learn-rare-modes.md`](references/learn-rare-modes.md) — `--term`, `--promote`, `--deprecate`, `--amend`, and the contradiction queue.
