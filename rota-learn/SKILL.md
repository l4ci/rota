---
name: rota-learn
description: Extract durable session learnings (gotchas, conventions, constraints) into .rota/KNOWLEDGE.md grouped by topic, and update the CLAUDE.md topic index. Use at end of a session that surfaced reusable knowledge, after a correction-rich debugging arc, or on "save what we learned", "capture this learning", "/rota-learn". Opus verification is on by default via learn.verify in config.json; set to false for fast/cheap mode.
---

**Print the banner below verbatim before any other action — skip if dispatched as a subagent.** See `references/banner-preamble.md`.

```
════════════════════════════════════════════════════════════════════════
  🧠  rota-learn  ·  extract session learnings to KNOWLEDGE.md
  triggers: "learn this", "save gotcha"  ·  pairs: rota-debug, rota-pause
════════════════════════════════════════════════════════════════════════
```

# rota-learn — Capture Session Learnings

## Step 1 — Task list

**Initialize task list.** Follow the canonical pattern in `references/task-list-init.md` — load `TaskCreate(…)` via `ToolSearch select:TaskCreate,TaskUpdate` if needed, then create one task per phase below.

Phases:

1. *Scan session* — transcript + recent commits sifted for durable gotchas (Step 2)
2. *Classify topic* — each candidate matched to a `KNOWLEDGE.md` topic (Steps 3–4)
3. *Merge into KNOWLEDGE.md* — entries appended under topic headings (Step 5)
4. *Update CLAUDE.md index* — `rota block knowledge` regenerates the managed block (Step 6)
5. *Verify (Opus)* — optional cold pass when `learn.verify: true` (Steps 7–8)
6. *Contradictions* — pending demotion candidates surfaced per-bullet at session end (Step 9)

**Args parsing.** Before running the phases above, inspect the `args` value passed at invocation. If `args` contains any of the following flags, skip Steps 2–8 and jump directly to Step 1.5:

- `--term <name>` — capture a domain term into the `## Glossary` topic of `.rota/KNOWLEDGE.md`; requires `--def`, accepts `--alias`, `--not`, `--touch`
- `--promote <topic> "<title>"` — promote one bullet to `confirmed`, bypassing discovery
- `--deprecate <topic> "<title>"` — demote one bullet to `deprecated`, bypassing discovery
- `--amend <topic> "<title>"` — rewrite the body of one bullet, preserving tier + hits

If none of those flags are present, proceed with the normal discovery flow (Steps 2 onward).

## Step 1.5 — Manual Override

This step fires only when a manual flag (`--term`, `--promote`, `--deprecate`, or `--amend`) was detected in the args.

### `--term <name>`

Captures a domain term into the pinned `## Glossary` topic of `.rota/KNOWLEDGE.md`.

**Required:** `--def "<text>"` — one-paragraph canonical definition (single paragraph, no nested headings).
**Optional:** `--alias "a,b,c"` (comma-separated synonyms), `--not "x,y"` (near-miss disambiguators), `--touch` (force-bump the date stamp on an existing-term update).

Shell command shape:
```bash
rota glossary write "<name>" --def "<text>" [--alias "a,b"] [--not "x,y"] [--touch]
```

Reads the existing Glossary topic, performs cross-term alias-collision uniqueness check, inserts (alphabetically) or updates the entry, regenerates the CLAUDE.md `<!-- rota-knowledge-start -->` block. Exit 4 on alias collision (`blockedBy: alias-collision`: an alias matches one already attached to a different term in Glossary); on collision, surface the error and stop without writing.

Definitional-signal autowrite — when the user phrases something like *"by X I mean Y"*, *"let's call this X"*, or *"X means Y"* during a normal session (not via the explicit `--term` flag), the orchestrator may run this same verb inline without going through `/rota-learn`. The flag form is the user-facing entry point; the inline form keeps the trio's old conversational-write behavior alive.

Report one line:

```
Captured term: <name> in .rota/KNOWLEDGE.md ## Glossary
```

Then exit (skip remaining steps).

### `--promote <topic> "<title>"`

Sets the bullet's tier to `confirmed`, bypassing the hit-threshold path.

Shell command shape:
```bash
rota knowledge tier set --topic "<topic>" --title "<title>" --tier confirmed
```

`tier set` always writes the new tier (idempotent — promoting an already-confirmed bullet is a no-op in effect). Report one line:

```
Promoted: <topic> :: <title> → confirmed
```

Then exit (skip remaining steps).

### `--deprecate <topic> "<title>"`

Sets the bullet's tier to `deprecated`.

Shell command shape:
```bash
rota knowledge tier set --topic "<topic>" --title "<title>" --tier deprecated
```

Report one line:

```
Deprecated: <topic> :: <title> → deprecated
```

**Important:** manual deprecations do NOT touch the contradictions queue. Do NOT call `rota knowledge contradiction clear` here — the queue is for heuristic candidates only, not for manually declared deprecations.

Then exit (skip remaining steps).

### `--amend <topic> "<title>"`

Rewrites the body of one bullet while preserving its tier and hits in the sidecar.

`rota knowledge amend` only APPENDS to the bullet body. To correct stale wording in place, use `rota knowledge replace --topic "<topic>" --old "<text>" --new "<text>"`: it swaps an exact substring inside the one bullet that contains it, refuses (exit 4) when the text sits in several bullets, and re-keys the tier entry if the bold title changes. This flow stays append-only.

Flow:
1. Prompt the user via `AskUserQuestion` for the new body suffix:
   - Header: `"Amend bullet"`
   - Question: *"Enter the text to append to `<topic> :: <title>` (V1: appends to existing body):"*
   - Free-text field (single-line or multi-line).
   - In loop-mode (`autonomy.level: loop`), this is an error — `--amend` requires explicit body input from the user; print `"Error: --amend requires user-provided body — cannot auto-pick in loop mode."` and exit 1.
2. Call:
   ```bash
   printf '%s' "<new body suffix>" | rota knowledge amend --topic "<topic>" --fragment "<unique fragment from existing title>" --mode append --body-file -
   ```
   The `--fragment` can be the title text itself (it is unique by (topic, title)).
3. The sidecar entry is left untouched — tier and hits are preserved.
4. Read back the current tier and hits via `rota knowledge tier get --topic "<topic>" --title "<title>"` and report:

```
Amended: <topic> :: <title> (tier=<tier>, hits=<hits> preserved)
```

Then exit (skip remaining steps).

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

Skip approval prompts. Proceed to Step 5 (merge into `KNOWLEDGE.md`) and Step 6 (update `CLAUDE.md`).

Verification is **on by default**. Read `.rota/config.json` — if `learn.verify` is `true` (default) or unset, run Step 7. Set `learn.verify: false` to skip it.

## Step 5 — Merge into KNOWLEDGE.md

Topics that grow past 25 bullets or 10 KB get a one-line size-nudge in Step 8 (`rota knowledge stats`-driven). It is informational only — the merge always proceeds.

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

The verb handles insertion at the top of the topic, the date stamp, and atomic dedup by (topic, title) — calling it twice with the same title under the same topic is a silent no-op.

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

Pass the chosen scope as `--repo <scope>` to `rota knowledge add`. `/rota-learn --term` (F18 Glossary entries) uses the same routing — per the *"Persistence-trio scoping"* decision the Glossary topic follows KNOWLEDGE's hybrid scoping, so a `--repo`-scoped term lands in that sub-repo's `## Glossary` (wired in T5).

**Single-repo projects:** no `--repo` needed — scope always resolves to `"umbrella"` and the `.rota/KNOWLEDGE.md` path is used unchanged; behavior is byte-identical to pre-F21.

**New topics in a scoped file:** the "append `## <Topic>` heading first" rule applies to the *resolved* file. A fresh sub-repo `KNOWLEDGE.md` starts empty — seed the heading in that scoped file before calling `rota knowledge add`, just as you would for the umbrella file.

**DECISIONS stay umbrella-only.** Per the *"Persistence-trio scoping under umbrella mode"* decision in `.rota/DECISIONS.md`, only KNOWLEDGE is hybrid (umbrella + per-sub-repo). DECISIONS is umbrella-only — do not offer or pass a `--repo` scope when writing decisions.

## Step 6 — Update CLAUDE.md Topic Index

```bash
rota block knowledge
```

Reads `.rota/KNOWLEDGE.md`, extracts `## Topic` headings in order, and updates the managed `<!-- rota-knowledge-start -->` block in `CLAUDE.md` (or `AGENTS.md` when present). Creates or appends as needed; never touches other content. `/rota-work` reads this block to know when to consult `KNOWLEDGE.md`.

In umbrella mode, pass `--repo <scope>` where `<scope>` is the same scope the learning was written to: this regenerates that sub-repo's `CLAUDE.md` with a block listing umbrella topics first, then any topics unique to that sub-repo, while `--repo umbrella` (or omitting the flag in a single-repo project) regenerates the umbrella/project `CLAUDE.md` unchanged. DECISIONS are umbrella-only and never take `--repo`.

## Step 7 — Opus Verification (default)

Run unless `learn.verify` is explicitly `false`. Follow the brief in `rota-learn/verifier.md` — it contains the dispatch instructions, the verifier prompt, and the verdict-application rules. Apply the verdict, then continue to Step 8.

## Step 8 — Confirm

Tell the user, in one compact block, what was captured:

```
Captured 3 learnings into .rota/KNOWLEDGE.md:
  Testing (2 new)
  Networking (1 new)

Updated CLAUDE.md topic index — /rota-work will consult these on relevant tasks.
```

**Topic-size handling.** Run `rota knowledge stats --json` and check `data.topics`. If any topic has `bullets >= 25` OR `bytes >= 10240`, branch on `autonomy.level` (read `.rota/config.json`):

- `"off"` (default) — append a single nudge line per offender to the confirm output:

  ```
  Note: `<topic>` is large (<bullets> bullets, <bytes-as-KB-rounded-1dp> KB). Consider splitting it (e.g. `<topic>: <facet-A>` + `<topic>: <facet-B>`) to reduce per-query cost in /rota-work, /rota-debug, /rota-plan.
  ```

  Format KB as `{bytes/1024:.1f}` (e.g. `9.8 KB` for 9876 bytes). Splitting is editorial; the user accepts or declines.

- `"auto"` or `"loop"` — **perform the split immediately — no prompt, no confirmation, no "want me to" question.** Per the `references/authoring-conventions.md` convention for loop-mode routine auto-picks. For each offender topic:

  1. Read the topic's bullets via `rota knowledge query "<topic>"`.
  2. Group bullets into 2 or 3 cohesive facets by semantic theme (e.g. `Helpers` / `Workers & Parallelism`, `Conventions` / `References`). Each facet must hold ≥3 bullets; `Misc` / `Other` / `Etc.` facets are forbidden — every bullet gets a substantive home. If no plausible split axis exists (bullets are byte-equivalent in theme), fall back to the `"off"` nudge for that topic and skip steps 3–7.
  3. Append `## <Topic>: <FacetA>` and `## <Topic>: <FacetB>` headings to `.rota/KNOWLEDGE.md` immediately before the old `## <Topic>` heading.
  4. For each bullet in `<Topic>`, call `rota knowledge rename-topic --from "<Topic>" --to "<Topic>: <Facet>" --title "<bullet-title>"`. The verb relocates the bullet body byte-identical AND re-keys its `.rota/knowledge-tier.json` entry from `<Topic>::<title>` to `<Topic>: <Facet>::<title>` in one atomic step — tier and hit state survive the split. Issue all calls for one offender as a single parallel batch (each invocation is atomic on a different bullet). Do NOT hand-edit bullets via `Edit` for this — that path silently orphans sidecar entries (the T03 / rota#13 regression this auto-split was fixed to prevent).
  5. Remove the now-empty old `## <Topic>` heading.
  6. Re-run `rota block knowledge` to refresh the managed `<!-- rota-knowledge-start -->` block in `CLAUDE.md`.
  7. Append one line to the confirm output: `Auto-split <topic> → <topic>: <FacetA> + <topic>: <FacetB> — N → A+B bullets.`

  Format KB as `{bytes/1024:.1f}` in any size figures appearing in the confirm line. Split each offender at most once per session — a topic that re-trips the threshold mid-session is a planning failure, not a re-split target.

If verification ran and passed, add a middle line: `Opus verification: PASS — all entries durable, sharp, correctly categorized.` If it returned `PASS_WITH_NOTES`, replace that line with a one-liner naming what was adjusted. If it failed, say so and stop.

## Step 8.5 — Suggest rota issue (when applicable)

`rota tracker suggest-upstream` enforces the `public-filing` manual gate (`rota gate list`): it exits 4 without `--confirm`, at every autonomy level. The question below is that confirmation, so never auto-pick it.

**Trigger heuristic.** Scan the just-captured bullets for any of:

- A skill slash-command name: `/rota-capture`, `/rota-vision`, `/rota-pause`, `/rota-plan`, `/rota-spike`, `/rota-work`, `/rota-debug`, `/rota-decide`, `/rota-review`, `/rota-ship`, `/rota-learn`, `/rota-refactor`, `/rota-release`.
- An `rota` verb invocation (regex `\bhv [a-z]+( [a-z-]+)?`), e.g. `rota knowledge add`.
- An `.rota/` artifact path: `.rota/BACKLOG.md`, `.rota/KNOWLEDGE.md`, `.rota/DECISIONS.md`, `.rota/MILESTONES.md`, `.rota/status.json`, `.rota/config.json`, `.rota/handoff/`, `.rota/plans/`, `.rota/spikes/`, `.rota/bugs/`, `.rota/features/`, `.rota/tasks/`, `.rota/milestones/`.

If no bullet matches any of those, skip the step silently.

**Ask before filing.** When at least one bullet matches, use `AskUserQuestion`:

- Header: `"Upstream"`
- Question: *"This learning touches rota behavior. File an issue on the rota repo?"*
- Options (single-select):
  1. `"File a rota issue (Recommended)"` — *"Pre-fill title + body and run `rota tracker suggest-upstream` to open the issue."*
  2. `"Skip"` — *"No upstream issue; the local KNOWLEDGE bullet stands on its own."*

Plain-text fallback: *"File a rota issue?"* — honor yes/no.

**File the issue.** When the user picks "File":

1. Compose title from the matching bullet's first sentence (truncate at the first period or 80 chars).
2. Compose body — use this template, substituting in real values:
   ```
   ## What happened
   <bullet text, verbatim>

   ## Expected
   <one-sentence inversion of the gotcha — what should have happened>

   ## Context
   - rota version: <output of `rota version`>
   - Captured topic: <KNOWLEDGE.md topic name>
   - Date: <today, YYYY-MM-DD>
   ```
3. Run the verb:
   ```bash
   printf '%s' "$BODY" | rota tracker suggest-upstream --json --title "$TITLE" --body-file - \
     --confirm --confirm-note "<the user's answer, verbatim>"
   ```
   - On exit 0 (gh available, issue filed): read `url` and `number` from `data`.
   - On exit 5 (`gh` missing or not authenticated): show the error hint (the manual issue URL) to the user, then prompt once: *"Paste the issue number when you've filed it manually (or 'skip' to skip):"* Read the user's reply; if a number, use it; if "skip" or empty, abandon the tracking step.

4. **Append the upstream marker to the bullet** in `.rota/KNOWLEDGE.md`. Call:
   ```bash
   printf '%s' "Upstream: rota#<N>" | rota knowledge amend --topic "<Topic>" --fragment "<unique body fragment>" --mode append --body-file -
   ```
   The fragment can be any case-sensitive substring of the bullet that uniquely identifies it within the topic — typically a distinctive word or phrase from the body. The verb appends ` Upstream: rota#<N>` after the bullet's trailing `<!-- date -->` comment, leaving the rest of the file byte-identical.

5. Add a final line to the Step 8 confirm output:
   ```
   Filed rota#<N> for the <topic> bullet — https://github.com/l4ci/rota/issues/<N>
   ```

## Step 8.6 — Suggest runlog entry (when applicable)

This step is **always manual** — never auto-invoked, regardless of `autonomy.level`. Filing to a public registry is high-stakes; the user presses the button. Mirrors Step 8.5's shape but for the *inverse* signal: external dependencies (third-party APIs, libraries, protocols, OSS quirks), not rota internals. See `references/manual-gates.md`.

**Trigger heuristic.** Scan the just-captured bullets for ANY of (literal union, not all):

- The bullet's topic heading begins with one of these external-prone prefixes (case-insensitive): `Third-Party`, `Networking`, `Auth`, `Persistence`, `Deployment`.
- The bullet body matches a protocol/transport token (case-insensitive, word-bounded): `OAuth`, `OIDC`, `JWT`, `SAML`, `WebSocket`, `SSE`, `gRPC`, `GraphQL`, `REST`, `HTTP/[12]`, `TLS`, `DNS`, `IMAP`, `SMTP`, `WebRTC`, `MQTT`, `AMQP`, `S3`.
- The bullet body contains a surfaced external HTTP status code (word-bounded): `401`, `403`, `429`, `500`, `502`, `503`, `504`.
- The bullet body names a third-party brand or library (case-insensitive, word-bounded): `anthropic`, `openai`, `claude`, `gpt`, `redis`, `postgres(?:ql)?`, `mysql`, `mongodb`, `elasticsearch`, `kafka`, `rabbitmq`, `stripe`, `twilio`, `sendgrid`, `cloudflare`, `aws`, `gcp`, `azure`, `terraform`, `kubernetes`, `docker`, `nginx`, `apache`, `envoy`.

If no bullet matches any signal, skip the step silently. Match the union, not the intersection — one signal is enough to surface the prompt.

**Mutual exclusivity with Step 8.5.** Step 8.5 (rota issue) and Step 8.6 (runlog) are independent — a bullet can match neither, one, or both. When a bullet matches both, run Step 8.5 first and let Step 8.6 ask afterward; they route to different upstreams and shouldn't bundle.

**Ask before dispatching.** When at least one bullet matches, use `AskUserQuestion`:

- Header: `"Runlog"`
- Question: *"This learning is about an external dependency. Contribute it to runlog.org via `/runlog-author`?"*
- Options (single-select):
  1. `"Run /runlog-author (Recommended)"` — *"Hand the matching bullet(s) to the runlog skill — drives the local Ed25519 verifier loop, then `runlog_submit`."*
  2. `"Skip"` — *"No upstream contribution; the local KNOWLEDGE bullet stands on its own."*

Plain-text fallback: *"Author a runlog entry?"* — honor yes/no.

**Route the answer.**

- **Run /runlog-author** — invoke the `runlog:runlog-author` skill via the `Skill` tool, naming the matching bullet(s) and the topic(s) in the brief so runlog-author has the right context. If the `Skill` tool errors that the skill is unknown (the runlog plugin isn't installed), surface one line — *"`/runlog-author` is not installed; install the runlog plugin to contribute back."* — and continue. Don't block /rota-learn on a missing peer skill.
- **Skip** — print one line — *"Run `/runlog-author` later if you change your mind."* — and continue.

When the dispatch ran, append one line to the Step 8 confirm output:

```
Ran /runlog-author for the <topic> bullet.
```

## Step 9 — Process Contradiction Candidates

Read pending contradictions:

```bash
rota knowledge contradiction list --json
```

Read `data.items`. If empty, skip this step silently.

For each candidate `{topic, title, correctionText, loggedAt}`, surface via `AskUserQuestion`:

- **Header:** `"Demote?"`
- **Question:** *"This learning was implicated by user feedback during the session: `<correctionText>`. Demote `<topic> :: <title>` to `deprecated`?"*
- **Options** (single-select):
  1. *"Demote (Recommended)"* — call `rota knowledge tier set --topic <T> --title <S> --tier deprecated`
  2. *"Keep — false positive"* — leave tier unchanged
  3. *"Defer to next session"* — keep candidate in the queue

**Loop-mode auto-pick:** *"Defer to next session"* — per the manual-gate rule that demotions need user confirmation (same principle as Step 8.5).

**V1 simplification:** after processing ALL candidates (regardless of per-candidate choice), call:

```bash
rota knowledge contradiction clear
```

This clears the entire queue. Fine-grained deferral (keeping only deferred items) is a V2 polish.

Track results in the Step 8 confirm output as:

```
Cleared N contradictions: <demoted-count> demoted, <skipped-count> skipped
```

(Where "skipped" covers both "Keep — false positive" and "Defer to next session" choices.)

## Key Principles

- **Durable, not ephemeral.** If it only matters this week, it's a TODO. Use `/rota-capture`.
- **Preserve existing structure.** Edit surgically; never regenerate the whole file.
- **Sharp and short.** One sentence with a concrete claim. If you need a paragraph, link to code instead.
- **Today's date.** Always stamp with the absolute current date.
- **Sibling persistence skills.** `/rota-learn` (with `--term <name>` for Glossary entries) and `/rota-decide` share one contract (persist + index `CLAUDE.md` + confirm) and intentionally diverge on gate strength — see `references/persistence-skills.md`.

## References

- [`references/banner-preamble.md`](references/banner-preamble.md) — Banner-print rule shared by every skill.
- [`references/manual-gates.md`](references/manual-gates.md) — The manual-gate registry (`rota gate list`): gates the verbs enforce with `--confirm`, and the skill-only callouts.
- [`references/persistence-skills.md`](references/persistence-skills.md) — Shared spine and divergence axes for the persistence duo (`/rota-learn`, `/rota-decide`) — including `/rota-learn --term` for Glossary entries.
