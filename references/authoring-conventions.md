# Authoring conventions

These conventions constrain how new rota skills (or new behavior in existing skills) are authored. Skill authors consult this reference when writing or modifying any `rota-*/SKILL.md` file. New authoring rules land here, not inline. The index in `references/README.md` is the entry point.

## Skills are self-contained — no shared contract file

Each skill owns its rules inline. A "shared contract" reference file (an old `GUIDE.md` was one) is a smell when every rule has a single owner. Audit the cross-refs before retaining a shared file: if each rule is already mirrored inline at the call site (autonomy off/auto/loop dispatch, learn trigger thresholds, etc.), the central file is vestigial pointer-chasing. Build a shared file only when N≥3 callers need the same long rule verbatim.

## Imperative rules in autonomy-aware steps must live inline at every dispatch point

Steps that branch on `autonomy.level` (off/auto/loop) and dispatch the next skill via `Skill` ("no prompt, no confirmation, no 'want me to' question") must repeat the directive verbatim alongside each `Skill`-tool invocation. Readers don't chase cross-refs to a single source of truth, and the harness drifts toward asking when only the rule's name is at the dispatch site. Redundancy is cheaper than scattered authority.

## Don't ask what the code can answer

Before a skill calls `AskUserQuestion`, check whether the answer is derivable from the codebase, git history, or `.rota/` state — `grep`, `Read`, `git log`, `BACKLOG.md`, `KNOWLEDGE.md`, `status.json`, helper output. If it is, derive the answer (with a one-line note inline about what was found and where) and skip the question. `AskUserQuestion` is for genuine ambiguity — open requirements, opposing reasonable interpretations, the user's risk tolerance on a destructive op — not a forced-yes ritual confirming state the skill could discover.

Codified from grill-with-docs (2026-05-10): *"If a question can be answered by exploring the codebase, explore the codebase instead."* Companion to the *AskUserQuestion option list capped at 4* rule (`KNOWLEDGE.md`, 2026-05-08) — that one constrains the option list when asking is the right move; this one constrains whether to ask at all.

## No ceremony: banners, mandatory task lists, per-site ask fallbacks

Skills print no banner, and none requires a task-list tool. A multi-phase skill may list its phases and say *"Track these phases with the host's task tool if it has one."* That is the whole rule: no `ToolSearch` load, no per-phase `TaskCreate` boilerplate. Subagent dispatches never create tasks; the orchestrator owns the list.

When a skill asks the user a question, ask in prose with the options listed and a recommended default when the host has no option picker. `AskUserQuestion` is the Claude Code example of such a picker; a skill names it only to describe the question shape. A free-text reply to a picker is mapped to the nearest option. Ask once, and on an ambiguous reply take the site's stated default and say which one landed. Destructive operations and opt-in flags default to the safe side (cancel, `false`). Skills do not carry per-site "Plain-text fallback" lines.

Phase outcomes, where a skill names them, stay mechanically verifiable (a file exists, a command exits 0, a commit landed, a recorded user answer), not subjective states.

## Routine routing/tagging auto-picks Recommended in loop mode

When `autonomy.level == "loop"`, AskUserQuestion calls that present a single clear `(Recommended)` option for **routine routing or tagging** must silently auto-pick the Recommended option without invoking AskUserQuestion. The host's question UI never fires; the skill proceeds as if the user picked the Recommended answer.

This is what makes loop mode actually loop — a single "Tag with M01?" or "Resume vs ship?" prompt mid-queue stalls every subsequent item until the user types an answer. Loop mode's contract is "drain the queue until empty / guard / interrupt"; intermediate routine prompts violate it.

Routine = the kind of question where the Recommended option is the obvious right answer, not a design pick. Examples: milestone tagging (`/rota-capture` Step 4.5), sub-repo tagging (`/rota-capture` Step 4.6), reconcile resolution (`/rota-work` no-argument mode, step 1 — resume / ship / leave), CONCERNS routing (`/rota-ship` Step 3 — "Address via /rota-work"), refactor scope and candidate gates.

**Forbids.** Auto-picking on:
- **Design decisions with open questions** — competing approaches, version-bump escalation, novel pattern choice. These belong to F32 (loop-mode auto-planning, with `[Auto:Loop]` decision logging). A `(Recommended)` flag on a design pick is a *suggestion*, not a routine answer; the loop must surface them.
- **Manual gates that are never auto-invoked regardless of autonomy** — `/rota-decide` approvals, `/rota-learn` Step 8.5 issue filing, `/rota-learn` Step 9 runlog filing, `/rota-ship` Step 5 PR strategy, `/rota-release` push/publish gates. These have explicit `**Manual gate — ...**` callouts in their SKILL.md. Loop mode honors the gate — it does not auto-pick.
- **Config-flip questions** — `/rota-ship --docs` after-work-mode opt-in. These flip user-preference flags; the opt-in-defaults-to-`false` rule (below) requires explicit user approval, not loop-mode synthesis.

**Permits.**
- Routine routing/tagging with one clear Recommended option (the use cases listed above and any future analogue).
- Sites that already implement the pattern explicitly (`/rota-work` no-argument mode, step 4 confirm-the-suggested-item; `/rota-capture` Step 8 work-it-now hand-off) — same shape, already inline; new sites follow their lead.
- Per-site phrasing variations — each site's loop branch states the auto-pick locally because the autonomy-rule-must-live-inline convention (above) forbids cross-refs to a single source of truth.

The dispatch site should add a short loop branch alongside the existing `"off"` AskUserQuestion arm. Pattern (adapt phrasing per site):

> **Loop mode:** when `autonomy.level == "loop"`, silently auto-pick the Recommended option without invoking AskUserQuestion — `<one-line summary of what gets dispatched>`.

Codified after F33 caught loop-mode discontinuity from `/rota-capture` milestone tagging and `/rota-work` no-argument reconcile gates breaking the `/rota-work` → `/rota-learn` → `/rota-work` chain.

## User-volition gates enforced at exactly one point

Manual confirmation gates (`/rota-decide`'s manual-only contract, the public-artifact gate in `/rota-learn` Step 8.5, etc.) must be enforced at exactly ONE point in a skill, never propagated across orchestrator + called skill. The gate is architecture-enforced — only the owning skill can ask the question, and no other skill dispatches the gated skill via `Skill`. Putting a confirmation check in a skill that other skills can invoke breaks the contract under autonomy.

## Stage features across slices using pass-through stubs

Multi-slice features ship the SHAPE early via pass-through stubs that explicitly name the future-slice wiring point (e.g. *"Layer-1 filter is a pass-through stub; the substantive helper lands in M01-S03"*). This signals what consumers should NOT rely on yet. **Companion rule:** when the milestone flips to `shipped`, sweep all `M0X-S0Y` slice references — they were placeholders and become stale after merge.

## Helper-centric V2-surface extension

When scaling a feature surface from "single X" to "list of X" (e.g. one repo → many) across N skills, push parsing/validation/dispatch into `bin/` helpers and confine each SKILL.md edit to a single guard paragraph: *"if the value resolves to ≥2 entries, call helper-X; otherwise unchanged."* Single-X path stays byte-identical, multi-X complexity lives in code (exercised by smoke), per-skill prose stays ≤15 lines.

## Opt-in feature flags default to `false`

When adding a new boolean config flag whose purpose is to enable additional skill behavior or auto-invocation:

- **Default `false`** in the config defaults `rota init` writes (fresh and re-stamped projects alike).
- **Never silently flip to `true`** anywhere — not on first detection, not on first invocation, not via cwd-inferred heuristics.
- The owning skill flips the flag to `true` only via explicit user approval: first-run scaffold approval (the user opted in by approving), or `AskUserQuestion` on existing state with default "Leave off".
- `rota config set` edits the flag explicitly (the flag is never read-only).
- **Exempt:** standard-on settings with opt-out semantics (e.g. `learn.verify: true`, `ship.review: true`) — these are not opt-in flags. Mode switches inside an already-enabled feature (e.g. `docs.autoCreate: false→true`) are also exempt.

Codified after F15 introduced `docs.afterWork`. Without this rule, opt-in flags drift toward auto-flip-on-first-detect, which makes them on-by-default in practice — defeating the opt-in semantics.

## Dispatch heavy work to subagents

Skills MUST consult `references/subagent-dispatch.md` for any step involving ≥3 file reads, repeated independent operations on N items, long tool output, or fan-out research. Orchestrator-only work (decisions, user interaction, atomic writes, verification of subagent output) is exempt — it stays on the main thread.

The reference defines the cost/benefit threshold, the small-brief template, the return-shape contract, the model-tier mapping (haiku / sonnet / opus), the parallel fan-out pattern (single-turn dispatch, worktree-isolation cross-cite), and the orchestrator's remaining responsibilities.

Two retrofitted skills illustrate compliance:

- `rota-vision` — Step 2 bundles all context reads into one haiku worker that returns a compact snapshot; Step 4 fans out N parallel research workers (one per angle) instead of serial `WebSearch` calls on the orchestrator.
- `rota-debug` — Step 5 dispatches reproduction to a sonnet worker when the repro is heavy (multi-MB output, multi-step manual setup, writing a failing test from scratch); Step 7 dispatches verification to a worker (model tier depends on whether the verdict requires judgment or pattern-matching). Cheap repros and single-line verifications stay inline.

A skill author asking "what does compliance look like?" can read either retrofit and find a concrete answer for every rule in the reference. New skills follow the same pattern.

**Forbids.** Dispatching for ≤2 small reads, for orchestrator-already-loaded context, for interactive steps, or when the brief would cost more tokens than the work. Cross-worker communication. Returning full transcripts instead of synthesis. Calling out to `superpowers:dispatching-parallel-agents` or other external skills — the rota dispatch discipline is self-contained.

**Permits.** Mixed tiers in a single wave (one haiku worker alongside three sonnet workers in the same turn). Opportunistic haiku usage declared inline in the brief without a config flag. Per-skill judgment on which steps trip the threshold — the rule sets a floor, not a ceiling.

## Adjective thresholds in skill prose erode at the runtime model — bake the number at authoring time

Prose like "a few", "many", "high X", "ambiguous", "might/may" forces the runtime LLM to invent a threshold every invocation. Two thoughtful readers can interpret the same adjective two ways. Before shipping, test each one: if the adjective could plausibly be read in opposite directions by two competent readers, replace it with (a) a number, (b) a conditional (*"when X happens, Y"*), or (c) an assertive verb.

**Forbids.**
- Shipping prose with vague quantity adjectives (*"a few"*, *"many"*, *"several"*) when a number or conditional would lock the threshold.
- Shipping prose with vague intensity adjectives (*"high X"*, *"low X"*, *"common"*, *"rare"*) when the threshold matters for the rule's correctness.
- Hedging verbs (*"might"*, *"may"*, *"could"*) in normative rules where the runtime needs a binary answer.

**Permits.**
- Adjectives in descriptive prose where no threshold is implied (*"a typical day"*, *"common workflow"*) — flavor doesn't trip the runtime if no rule fires off it.
- Hedging in genuinely open situations that the rule explicitly flags as a known unknown.

Codified during the T52 sweep across `rota-debug`, `rota-release`, `rota-review`, `rota-spike`, and `references/post-cycle-trigger-gate.md` (six phrases replaced with concrete thresholds).

## `AskUserQuestion` option list capped at 4

`AskUserQuestion`'s option list is hard-capped at 4. Any SKILL.md picklist with N>4 silently degrades to prose (the user has to type names back), defeating the native UX promised in the skill description.

**Forbids.**
- Designing a question with 5+ options on the assumption the host will scroll — the host won't; the array is rejected and the skill falls back to free text.
- Compressing categories to fit 4 by merging unrelated answers — the merger destroys the picklist's semantic clarity.

**Permits.**
- Chunking into multiple sequential `AskUserQuestion` calls with ≤4 options each, `multiSelect: true` so the user picks across batches.
- Two-stage flow: pick categories first (single multiSelect, ≤4), then drill into the keys within each chosen category in a second call.

Codified during the B11 fix where a 13-key config picklist silently fell back to free text; resolved with category-then-keys staging.

## Ask in the user's terms, and name the default

Write every `AskUserQuestion` for someone who does not have the file open. Name the choice in the vocabulary of what the user observes — the behavior, the artifact, the outcome — not in the vocabulary of the code that implements it, and say what the skill will do by default if the answer turns out not to matter. *"Should a finished game still show the training row, or only live ones?"* beats *"confirm expected `inGameMenuActions` behaviour for `replayClosable && mode === 'bot'`"*: same decision, but only the first can be answered without a file open.

The skill is the side of the exchange holding the context, so translating is its job. A question phrased in implementation terms transfers that work to the user and usually gets a guess back — which reads exactly like an answer and is acted on as one. Stating the default converts a question the user does not care about into one they can decline cheaply.

**Forbids.**
- Naming a symbol, file, config key, or boolean expression as the *subject* of the question when a user-observable phrasing exists. (Citing one as supporting detail after the question is fine.)
- Asking a question whose options the user cannot distinguish without reading code — if the options only differ internally, the skill should be picking, not asking (see *Don't ask what the code can answer*).
- Leaving the no-preference path unstated, so that "either is fine" produces another round-trip instead of a decision.

**Permits.**
- Several questions in one `AskUserQuestion` call — the host renders each separately and returns an answer per question, so batching does not produce the partial answers that a free-text channel would. `/rota-work` Step 2's 1–3 question batch stays correct.
- Implementation vocabulary in the `description` field of an option, where it disambiguates for a user who *does* have the file open.

Codified from a read of klufft's `swarm.md` (rota#20, 2026-07-31), whose orchestrator pays for this in tmux panes rather than pickers. Its companion rule — *ask one question at a time* — deliberately did **not** transfer: it is a property of a free-text channel where a batch gets a partial reply, and `AskUserQuestion` is not that channel.

## Nudges on terminal/idle paths only

When a nudge or check could fire from multiple skills that converge on the same end-state (e.g., `/rota-work` → `/rota-ship` → `/rota-work` via loop continuation), place the nudge on the *terminal/idle paths* — where the user is about to leave the session — NOT on dispatch paths that hand off to another skill. Multiple skills firing the same nudge from convergent flows drowns the signal.

**Forbids.**
- Firing the same nudge from a skill's tail when that skill auto-dispatches the next skill (the user never sees the message — it's overwritten by the dispatched skill's output).
- Firing the nudge from a dispatch path on the assumption *"users will see it eventually"* — they see the loudest, latest message; intermediate nudges are noise.

**Permits.**
- Firing the nudge from the terminal branch of a routing skill (e.g. `/rota-work` no-argument mode "Stop here" / empty-backlog) where the user is about to step away.
- Firing the nudge from the post-ship report (`/rota-ship` Step 9.5) where the cycle ended and no auto-dispatch follows.

Codified after F19's release-pending nudge: fires from `/rota-work` no-argument mode only on the "Stop here" / empty-backlog branch and from `/rota-ship`'s post-ship report, never from inside `/rota-work`'s tail (the most-frequent path, but always followed by a dispatch).

## The verb contract is the contract — SKILL.md prose paraphrasing drifts

When a SKILL.md cites an `rota` verb, the verb's entry in `docs/design/contract/` (index in `README.md`) (and `rota <verb> --help`) IS the contract; prose paraphrases drift. Before authoring prose ABOUT a verb, read its entry — if the SKILL.md disagrees with the contract, the SKILL.md is wrong.

**Forbids.**
- Paraphrasing a verb's behavior in SKILL.md prose without reading its contract entry first.
- Inferring a verb's contract from how callers use it — callers can be wrong; the contract is the source of truth.

**Permits.**
- Quoting the verb's contract entry verbatim in the SKILL.md when the prose needs the exact contract.
- Updating SKILL.md prose to match a verb after its contract changes (the prose follows the code, not the other way around).

Codified on T28: `rota-work/SKILL.md` Step 4.5 gated umbrella mode on `umbrella.enabled`, but the umbrella-on helper's header pinned the contract to `.rota/repos.json` presence (today: `rota repo umbrella`). The header was authoritative; the SKILL.md was wrong.

## Inventory table beside a citation when ≥4 sibling rules extracted

When a SKILL.md extracts N≥4 sibling rules to a `references/` file, leave a one-column inventory table beside the citation. Readers scanning the SKILL.md see rule names without opening the reference; readers wanting the body click through.

**Forbids.**
- Citing a reference with 4+ extracted rules without an inventory — the reader has to open the file to know whether the rule they care about is there.
- Restating the full rule body in the inventory — that defeats the extraction; the inventory is a TOC, not the content.

**Permits.**
- For ≤3 extracted rules, citing the reference inline without an inventory (the rule names fit in the citing sentence).
- Inventory tables with extra columns (audience, complexity, etc.) when those columns help readers triage.

Codified on T39: a SKILL.md grew a 9-row inventory table beside its `references/authoring-conventions.md` citation; the inventory itself is what triggered this rule's codification.

## Avoid `&` in `TaskCreate`/`TodoWrite` payloads

Claude Code's TUI HTML-escapes task titles for rendering but never decodes — strings containing `&` show up as the literal entity `&amp;` in the task list view. Workaround until the upstream renderer is fixed: in any `TaskCreate(subject=…)`, `TaskCreate(activeForm=…)`, `TaskUpdate(...)`, or `TodoWrite(...)` payload (in examples in skill prose or in actual calls), use `and` or `+` instead of `&`. The substitution is purely cosmetic; both renderings parse identically.

**Forbids.**
- Ampersand in any `subject`, `description`, or `activeForm` string in `TaskCreate`/`TodoWrite`/`TaskUpdate` payloads — including example strings embedded in skill prose.
- Workarounds using `&amp;` or `&` in payloads to "pre-encode" — the bug isn't in encoding; the renderer escapes whatever it sees, so pre-encoded forms double-escape.

**Permits.**
- `&` elsewhere in prose, code blocks, or shell commands — the bug is scoped to task-list payloads, not all skill content.
- Topic headings like `## Build & Tooling` in `KNOWLEDGE.md` — those aren't TaskCreate payloads.
- `+` as the connector where it reads naturally (e.g. *"Commit + TODO + smoke"*) — already used elsewhere; renders correctly.

Codified on T01: a `/rota-work` session surfaced `Dispatch &amp; verify wave` and `Merge &amp; report` rendered with literal `&amp;` in the TUI task list. Four example payloads were swept in `rota-ship`, `rota-review`, `rota-work` SKILL.md; the F06 SKILL-format validator can grow a rule for this once the upstream Claude Code fix lands and we want to track removal.

## `/rota-x` and `$rota-x` are the same invocation

Codex invokes a skill as `$rota-x`; Claude Code as `/rota-x`. Skill text keeps `/rota-x` everywhere and does not branch on the harness: read `$rota-x` as the same call. A skill installed with `rota skills install` lists in Codex as `rota-x`, and `$rota-x` invokes it. Never write both spellings in one sentence, and never rewrite an existing `/rota-x` to `$rota-x`.
