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

Inspect the `args` value passed at invocation.

- `--strict` — run the Opus verifier (Step 7) for this run.
- `--retro` — retrospective mode. Skip Steps 2 to 8 and follow *`--retro`* in [`references/learn-rare-modes.md`](references/learn-rare-modes.md), then exit.
- `--term <name>`, `--promote <topic> "<title>"`, `--deprecate <topic> "<title>"`, `--amend <topic> "<title>"` — manual modes. Skip Steps 2 to 8 and follow the matching section of [`references/learn-rare-modes.md`](references/learn-rare-modes.md), then exit.

With none of those, run the normal flow (Step 2 onward).

## Step 2 — Scan the Session for Learnings

A learning is worth capturing if it would save a future `/rota-work` run from re-discovering it.

**Capture:**

- **Gotchas** — non-obvious failure modes, footguns (e.g., "this API returns 200 on auth failure")
- **Conventions** — project-specific patterns not obvious from the code (e.g., "all network calls go through NetworkClient")
- **Constraints** — invariants, compatibility rules (e.g., "schema migrations must be backward-compatible for 2 versions")
- **Debugging insights** — root causes for hard-won bugs
- **Decisions with rationale** — why we chose X over Y
- **Tool quirks** — build/test behavior that trips people up

**Skip:** things documented in code or README, transient session state, obvious facts, restatements of framework docs, personal preferences.

If nothing is worth capturing, say so and stop. Don't manufacture learnings.

## Step 3 — Classify by Topic

Open `.rota/KNOWLEDGE.md` first and reuse existing `## Topic` headings when they fit. Create a new topic only if nothing fits. Good topic examples: `Build & Tooling`, `Testing`, `Networking`, `Persistence`, `Auth`, `Architecture`, `Performance`, `Third-Party APIs`, `Deployment`.

Don't create a topic per learning.

## Step 4 — Auto-Write

Skip approval prompts. Proceed to Step 5 (merge into `KNOWLEDGE.md`) and Step 6 (update the topic index).

Verification is **off by default**. Run Step 7 only when `--strict` is in the args or `.rota/config.json` has `learn.verify: true`.

## Step 5 — Merge into KNOWLEDGE.md

Topics that grow past 25 bullets or 10 KB get a size nudge in Step 8 (`rota knowledge stats`-driven). It is informational only — the merge always proceeds.

`.rota/KNOWLEDGE.md` is organized as:

```markdown
# Knowledge

## <Topic>
- **<Title>** — <learning body> <!-- 2026-04-18 -->
- <older legacy learning without title>
```

Each new bullet has a short bold `**Title**` (sentence-case, identifies the rule), an em-dash separator (em-dash U+2014, not a hyphen), the body, and a trailing ISO-8601 date stamp in an HTML comment (`<!-- YYYY-MM-DD -->`). The schema is normative — `rota knowledge add` dedups by (topic, title), so calling it twice with the same title under the same topic is a silent no-op. Sharper-wording replacement requires manual `Edit` on the existing bullet; the helper refuses to overwrite a title hit. Existing bullets without a title are legacy — leave them as-is.

For each captured bullet, call:

```bash
printf '%s' "$BODY" | rota knowledge add --topic "<Topic>" --title "<Short rule title>" --body-file -
```

The verb handles insertion at the top of the topic, the date stamp, and dedup.

**Pre-step rules (handle in prose, the verb assumes them):**

- **New topics:** the verb requires `## <Topic>` to already exist. If you're introducing a new topic, append the `## <Topic>` heading to `.rota/KNOWLEDGE.md` first (alphabetical order, except `Build & Tooling` and `Architecture` may be pinned near the top), then call `rota knowledge add` to insert the first bullet.
- **Sharpened wording:** the verb dedups on a case-insensitive title match; it does NOT replace an older entry with sharper wording. If a captured learning is a sharper version of an existing bullet, use `Edit` to update the existing bullet directly, then skip the `knowledge add` call for that learning.
- **Preserve existing topics:** the verb writes only to the named topic's section. Other topics are untouched.

`rota knowledge add` exits 0 on insert OR on idempotent no-op (`changed: false`); exit 3 if the topic doesn't exist (handle topic creation first as above).

### Umbrella-mode routing

When `.rota/repos.json` registers at least one sub-repo (umbrella mode), `rota knowledge add` honors the global `--repo umbrella|<name>` flag that controls which `KNOWLEDGE.md` receives the write:

- **`--repo <name>`** — writes to `.rota/knowledge/<name>/KNOWLEDGE.md` (the sub-repo's scoped file).
- **`--repo umbrella`** — writes to `.rota/KNOWLEDGE.md` (the shared umbrella file).
- **No `--repo`** — scope auto-resolves from cwd: inside a registered sub-repo's directory the verb writes that sub-repo's scoped file; at the umbrella root it falls back to `.rota/KNOWLEDGE.md`.

**At the umbrella root**, when a learning is clearly repo-local rather than cross-repo, ask once via `AskUserQuestion` before calling `rota knowledge add`:

- Header: `"Learning scope"`
- Question: *"Capture this learning as umbrella-shared, or scoped to a specific sub-repo?"*
- Options (single-select, one per registered sub-repo plus a shared option):
  1. `"Umbrella-shared (Recommended)"` — *"Write to `.rota/KNOWLEDGE.md`; visible across all sub-repos."*
  2. `"<name>"` (one option per registered sub-repo) — *"Write to `.rota/knowledge/<name>/KNOWLEDGE.md`; scoped to that repo."*

Pass the chosen scope as `--repo <scope>` to `rota knowledge add`. `/rota-learn --term` (Glossary entries) uses the same routing — per the *"Persistence-trio scoping"* decision the Glossary topic follows KNOWLEDGE's hybrid scoping, so a `--repo`-scoped term lands in that sub-repo's `## Glossary`.

**Single-repo projects:** no `--repo` needed — scope always resolves to `"umbrella"` and the `.rota/KNOWLEDGE.md` path is used unchanged.

**New topics in a scoped file:** the "append `## <Topic>` heading first" rule applies to the *resolved* file. A fresh sub-repo `KNOWLEDGE.md` starts empty — seed the heading in that scoped file before calling `rota knowledge add`, just as you would for the umbrella file.

**DECISIONS stay umbrella-only.** Per the *"Persistence-trio scoping under umbrella mode"* decision in `.rota/DECISIONS.md`, only KNOWLEDGE is hybrid (umbrella + per-sub-repo). DECISIONS is umbrella-only — do not offer or pass a `--repo` scope when writing decisions.

## Step 6 — Update the Topic Index

```bash
rota block knowledge
```

Reads `.rota/KNOWLEDGE.md`, extracts `## Topic` headings in order, and updates the managed `<!-- rota-knowledge-start -->` block in the project instructions file: `AGENTS.md` when it exists, else `CLAUDE.md`. The verb resolves the file; never hardcode one. It creates or appends as needed and never touches other content. `/rota-work` reads this block to know when to consult `KNOWLEDGE.md`.

In umbrella mode, pass `--repo <scope>` where `<scope>` is the same scope the learning was written to: this regenerates that sub-repo's instructions file with a block listing umbrella topics first, then any topics unique to that sub-repo, while `--repo umbrella` (or omitting the flag in a single-repo project) regenerates the umbrella/project file unchanged. DECISIONS are umbrella-only and never take `--repo`.

## Step 7 — Opus Verification (opt-in)

Skip unless `--strict` was passed or `learn.verify` is `true`. Follow the brief in `rota-learn/verifier.md` — it contains the dispatch instructions, the verifier prompt, and the verdict-application rules. Apply the verdict, then continue to Step 8.

## Step 8 — Confirm

Tell the user, in one compact block, what was captured:

```
Captured 3 learnings into .rota/KNOWLEDGE.md:
  Testing (2 new)
  Networking (1 new)

Updated the topic index in <AGENTS.md|CLAUDE.md> — /rota-work will consult these on relevant tasks.
```

**Topic-size handling.** Run `rota knowledge stats --json` and check `data.topics`. If any topic has `bullets >= 25` OR `bytes >= 10240`, branch on `autonomy.level` (read `.rota/config.json`):

- `"off"` (default) — append a single nudge line per offender to the confirm output:

  ```
  Note: `<topic>` is large (<bullets> bullets, <bytes-as-KB-rounded-1dp> KB). Consider splitting it (e.g. `<topic>: <facet-A>` + `<topic>: <facet-B>`) to reduce per-query cost in /rota-work, /rota-debug, /rota-plan.
  ```

  Format KB as `{bytes/1024:.1f}` (e.g. `9.8 KB` for 9876 bytes). Splitting is editorial; the user accepts or declines.

- `"auto"` — **perform the split immediately — no prompt, no confirmation, no "want me to" question.** For each offender topic:

  1. Read the topic's bullets via `rota knowledge query "<topic>"`.
  2. Group bullets into 2 or 3 cohesive facets by semantic theme (e.g. `Helpers` / `Workers & Parallelism`, `Conventions` / `References`). Each facet must hold ≥3 bullets; `Misc` / `Other` / `Etc.` facets are forbidden — every bullet gets a substantive home. If no plausible split axis exists (bullets are byte-equivalent in theme), fall back to the `"off"` nudge for that topic and skip steps 3–7.
  3. Append `## <Topic>: <FacetA>` and `## <Topic>: <FacetB>` headings to `.rota/KNOWLEDGE.md` immediately before the old `## <Topic>` heading.
  4. For each bullet in `<Topic>`, call `rota knowledge rename-topic --from "<Topic>" --to "<Topic>: <Facet>" --title "<bullet-title>"`. The verb relocates the bullet body byte-identical AND re-keys its `.rota/knowledge-tier.json` entry from `<Topic>::<title>` to `<Topic>: <Facet>::<title>` in one atomic step — tier and hit state survive the split. Issue all calls for one offender as a single parallel batch (each invocation is atomic on a different bullet). Do NOT hand-edit bullets via `Edit` for this; it silently orphans sidecar entries.
  5. Remove the now-empty old `## <Topic>` heading.
  6. Re-run `rota block knowledge` to refresh the managed `<!-- rota-knowledge-start -->` block.
  7. Append one line to the confirm output: `Auto-split <topic> → <topic>: <FacetA> + <topic>: <FacetB> — N → A+B bullets.`

  Format KB as `{bytes/1024:.1f}` in any size figures appearing in the confirm line. Split each offender at most once per session — a topic that re-trips the threshold mid-session is a planning failure, not a re-split target.

If verification ran and passed, add a middle line: `Opus verification: PASS — all entries durable, sharp, correctly categorized.` If it returned `PASS_WITH_NOTES`, replace that line with a one-liner naming what was adjusted. If it failed, say so and stop.

**Contradictions.** Run `rota knowledge contradiction list --json`. If `data.items` is empty, skip silently; otherwise process the queue per [`references/learn-rare-modes.md`](references/learn-rare-modes.md) (*Process contradiction candidates*).

## Key Principles

- **Durable, not ephemeral.** If it only matters this week, it's a TODO. Use `/rota-capture`.
- **Sharp and short.** One sentence with a concrete claim. If you need a paragraph, link to code instead.

## References

- [`references/persistence-skills.md`](references/persistence-skills.md) — Shared spine and divergence axes for the persistence duo (`/rota-learn`, `/rota-decide`) — including `/rota-learn --term` for Glossary entries.
- [`references/learn-rare-modes.md`](references/learn-rare-modes.md) — `--term`, `--promote`, `--deprecate`, `--amend`, and the contradiction queue.
