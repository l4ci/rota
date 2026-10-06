# Authoring conventions

Rules for authoring new rota skills or new behavior in existing ones. Consult this when writing or modifying any `rota-*/SKILL.md`. New authoring rules land here, not inline. The index in `references/README.md` is the entry point.

## Contents

- Skills are self-contained — no shared contract file
- Imperative rules in autonomy-aware steps must live inline at every dispatch point
- Don't ask what the code can answer
- No ceremony: banners, per-site ask fallbacks
- Open each workflow with a copyable step checklist
- User-volition gates enforced at exactly one point
- Stage features across slices using pass-through stubs
- Helper-centric surface extension
- Opt-in feature flags default to `false`
- Descriptions say when to use a skill, not how it works
- Gates carry a Thought → Reality table
- References over 100 lines open with a Contents list
- Dispatch heavy work to subagents
- Adjective thresholds in skill prose erode at the runtime model — bake the number at authoring time
- `AskUserQuestion` option list capped at 4
- Ask in the user's terms, and name the default
- Nudges on terminal/idle paths only
- The verb contract is the contract — SKILL.md prose paraphrasing drifts
- Inventory table beside a citation when ≥4 sibling rules extracted
- Avoid `&` in `TaskCreate`/`TodoWrite` payloads
- `/rota-x` and `$rota-x` are the same invocation

## Skills are self-contained — no shared contract file

Each skill owns its rules inline. A "shared contract" file is a smell when every rule has a single owner. Before keeping one, audit the cross-refs: if each rule is already mirrored inline at the call site (autonomy off/auto dispatch, learn trigger thresholds, etc.), the central file is vestigial pointer-chasing. Build a shared file only when N≥3 callers need the same long rule verbatim.

## Imperative rules in autonomy-aware steps must live inline at every dispatch point

Steps that branch on `autonomy.level` (off/auto) and dispatch the next skill via `Skill` ("no prompt, no confirmation, no 'want me to' question") must repeat the directive verbatim alongside each `Skill`-tool invocation. Readers don't chase cross-refs, and the harness drifts toward asking when only the rule's name is at the dispatch site. Redundancy is cheaper than scattered authority.

## Don't ask what the code can answer

Before a skill calls `AskUserQuestion`, check whether the answer is derivable from the codebase, git history, or `.rota/` state — `grep`, `Read`, `git log`, `BACKLOG.md`, `KNOWLEDGE.md`, `status.json`, helper output. If it is, derive the answer (with a one-line note inline about what was found and where) and skip the question. `AskUserQuestion` is for genuine ambiguity (open requirements, opposing reasonable interpretations, the user's risk tolerance on a destructive op), not a forced-yes ritual confirming state the skill could discover.

Codified from grill-with-docs (2026-05-10): *"If a question can be answered by exploring the codebase, explore the codebase instead."* Companion to the *AskUserQuestion option list capped at 4* rule (`KNOWLEDGE.md`, 2026-05-08) — that one constrains the option list once asking is right; this one constrains whether to ask at all.

## No ceremony: banners, per-site ask fallbacks

Skills print no banner and require no task-list tool: no `ToolSearch` load, no per-phase `TaskCreate` boilerplate. Subagent dispatches never create tasks; the orchestrator owns the list. The step checklist below is plain text the model copies into its reply, not a tool call.

## Open each workflow with a copyable step checklist

The skills guide ("Workflows and feedback loops") asks for a checklist Claude can copy into its response and tick off. Every skill with step headings opens its workflow with one, before the first step heading:

````markdown
Copy this checklist and track your progress:

```
- [ ] Step 1 — Resolve target
- [ ] Step 2 — Load context
- [ ] Step 3 — Write
```
````

- One line per step, in run order, with the step heading's text verbatim. `test/validate-skills.py` fails a line that names no step heading and a step heading missing from the checklist.
- A skill with modes gives one checklist per mode, each placed above its mode's steps. A line may add a trailing parenthetical to mark a conditional step (`- [ ] Step 2.5 — Audit Against Code State (milestone-spec capture only)`); put the marker in the heading when the step is always conditional.
- A step that runs a validator (tests, gate, `validate-skills`, a review verdict, smoke) states the loop in its body: run, fix what fails, re-run, and continue only on a pass. The checklist line stays one line; the loop lives in the step.
- Do not add a "Task list" step or a "Track these phases with the host's task tool" line; the checklist replaces both.

When a skill asks the user a question, ask in prose with the options listed and a recommended default when the host has no option picker. `AskUserQuestion` is the Claude Code example of such a picker; a skill names it only to describe the question shape. A free-text reply to a picker is mapped to the nearest option. Ask once, and on an ambiguous reply take the site's stated default and say which one landed. Destructive operations and opt-in flags default to the safe side (cancel, `false`). Skills do not carry per-site "Plain-text fallback" lines.

Phase outcomes, where a skill names them, stay mechanically verifiable (a file exists, a command exits 0, a commit landed, a recorded user answer), not subjective states.

## User-volition gates enforced at exactly one point

Manual confirmation gates (`/rota-decide`'s manual-only contract, the acceptance-of-risk gate in `/rota-ship` Step 6a, etc.) must be enforced at exactly ONE point in a skill, never propagated across orchestrator + called skill. The gate is architecture-enforced — only the owning skill can ask the question, and no other skill dispatches the gated skill via `Skill`. Putting a confirmation check in a skill that other skills can invoke breaks the contract under autonomy.

## Stage features across slices using pass-through stubs

Multi-slice features ship the SHAPE early via pass-through stubs that explicitly name the future-slice wiring point (e.g. *"Layer-1 filter is a pass-through stub; the substantive helper lands in M01-S03"*). This signals what consumers should NOT rely on yet. **Companion rule:** when the milestone flips to `shipped`, sweep all `M0X-S0Y` slice references — they were placeholders and become stale after merge.

## Helper-centric surface extension

When scaling a feature surface from "single X" to "list of X" (e.g. one repo → many) across N skills, push parsing/validation/dispatch into `rota` verbs (`cmd/`, `internal/`) and confine each SKILL.md edit to a single guard paragraph: *"if the value resolves to ≥2 entries, call the verb; otherwise unchanged."* Single-X path stays byte-identical, multi-X complexity lives in code (exercised by smoke), per-skill prose stays ≤15 lines.

## Opt-in feature flags default to `false`

When adding a new boolean config flag whose purpose is to enable additional skill behavior or auto-invocation:

- **Default `false`** in the config defaults `rota init` writes (fresh and re-stamped projects alike).
- **Never silently flip to `true`** anywhere — not on first detection, not on first invocation, not via cwd-inferred heuristics.
- The owning skill flips the flag to `true` only via explicit user approval: first-run scaffold approval (the user opted in by approving), or `AskUserQuestion` on existing state with default "Leave off".
- `rota config set` edits the flag explicitly (the flag is never read-only).
- **Exempt:** standard-on settings with opt-out semantics (e.g. `ship.review: true`) — these are not opt-in flags. Mode switches inside an already-enabled feature (e.g. `docs.autoCreate: false→true`) are also exempt.

Codified after F15 introduced `docs.afterWork`. Without this rule, opt-in flags drift toward auto-flip-on-first-detect, which makes them on-by-default in practice.

## Descriptions say when to use a skill, not how it works

The `description` frontmatter is the trigger: the situations and phrases that should load the skill, nothing else. A description that summarises the workflow ("reproduce, hypothesize, fix, open a PR") gives the agent a shortcut: it follows the summary and skips the skill body. State the trigger, name the neighbouring skill when the two are easy to confuse, and leave the steps to the body.

`test/validate-skills.py` caps a description at 350 characters (`DESC_CAP`); the Agent Skills spec's 1024 is a ceiling, not a target. It also rejects "you", "your" and "I" outside quoted trigger phrases: the description lands in the system prompt, so it is written in the third person ("the user").

**Forbids.**
- Listing steps, outputs, verbs or config keys the skill uses in its description.
- Raising `DESC_CAP` to fit a longer description; cut the description instead.

**Permits.**
- Trigger phrases in quotes, a flag with its own trigger (`--undo` on "roll back the last cycle"), and one sentence routing a confusable request to the right skill.

## Gates carry a Thought → Reality table

A hard gate (Iron Law, proof, review verdict, manual gate) gets a short two-column table in its skill: the rationalization an agent reaches for when the gate is in the way, and the fact that answers it. Place it just before the skill's `Key Principles`. Keep rows to the shortcuts the skill's own steps have to resist, one line each, and cite the step or exit code that enforces the gate. `/rota-debug`, `/rota-work` and `/rota-ship` carry one; add a row when a gate is skipped in practice, not in anticipation.

## References over 100 lines open with a Contents list

A reference longer than 100 lines starts with a `## Contents` section, within its first 15 lines, listing its `##` headings. A model that previews a file with a partial read still sees everything the file covers. `test/validate-skills.py` enforces it (`REF_TOC_LINES`); update the list when you add, rename or remove a section.

## Dispatch heavy work to subagents

Skills MUST consult `references/subagent-dispatch.md` for any step involving ≥3 file reads, repeated independent operations on N items, long tool output, or fan-out research. Orchestrator-only work (decisions, user interaction, atomic writes, verification of subagent output) is exempt — it stays on the main thread.

The reference defines the cost/benefit threshold, the small-brief template, the return-shape contract, the subagent tiers (`light` / `standard` / `heavy`) and their model mapping, the parallel fan-out pattern (single-turn dispatch, worktree-isolation cross-cite), and the orchestrator's remaining responsibilities.

**Forbids.** Dispatching for ≤2 small reads, for orchestrator-already-loaded context, for interactive steps, or when the brief would cost more tokens than the work. Cross-worker communication. Returning full transcripts instead of synthesis. Calling out to `superpowers:dispatching-parallel-agents` or other external skills — the rota dispatch discipline is self-contained.

**Permits.** Mixed tiers in a single wave (one `light` subagent alongside three `standard` ones in the same turn). Opportunistic `light` usage declared inline in the brief without a config flag. Per-skill judgment on which steps trip the threshold — the rule sets a floor, not a ceiling.

## Adjective thresholds in skill prose erode at the runtime model — bake the number at authoring time

Prose like "a few", "many", "high X", "ambiguous", "might/may" forces the runtime LLM to invent a threshold every invocation, and two competent readers can read the same adjective two ways. Before shipping, test each one: if the adjective could plausibly be read in opposite directions by two competent readers, replace it with (a) a number, (b) a conditional (*"when X happens, Y"*), or (c) an assertive verb.

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

The skill holds the context, so translating is its job. A question phrased in implementation terms hands that work to the user and usually gets a guess back, which reads like an answer and is acted on as one. Stating the default converts a question the user does not care about into one they can decline cheaply.

**Forbids.**
- Naming a symbol, file, config key, or boolean expression as the *subject* of the question when a user-observable phrasing exists. (Citing one as supporting detail after the question is fine.)
- Asking a question whose options the user cannot distinguish without reading code — if the options only differ internally, the skill should be picking, not asking (see *Don't ask what the code can answer*).
- Leaving the no-preference path unstated, so that "either is fine" produces another round-trip instead of a decision.

**Permits.**
- Several questions in one `AskUserQuestion` call — the host renders each separately and returns an answer per question, so batching does not produce the partial answers that a free-text channel would. `/rota-work` Step 2's 1–3 question batch stays correct.
- Implementation vocabulary in the `description` field of an option, where it disambiguates for a user who *does* have the file open.

Codified from a read of klufft's `swarm.md` (rota#20, 2026-07-31), whose orchestrator pays for this in tmux panes rather than pickers. Its companion rule — *ask one question at a time* — deliberately did **not** transfer: it is a property of a free-text channel where a batch gets a partial reply, and `AskUserQuestion` is not that channel.

## Nudges on terminal/idle paths only

When a nudge or check could fire from multiple skills that converge on the same end-state (e.g., `/rota-work` → `/rota-ship` → `/rota-learn` handoffs), place the nudge on the *terminal/idle paths* — where the user is about to leave the session — NOT on dispatch paths that hand off to another skill. Several skills firing the same nudge from convergent flows drowns the signal.

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
