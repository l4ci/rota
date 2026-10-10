# Authoring conventions

Rules for authoring new rota skills or new behavior in existing ones. Consult this when writing or modifying any `rota-*/SKILL.md`. New authoring rules land here, not inline. The index in `references/README.md` is the entry point.

## Contents

- Skills are self-contained, with autonomy rules inline
- Don't ask what the code can answer
- No ceremony: banners, per-site ask fallbacks
- Open each workflow with a copyable step checklist
- Stage features across slices using pass-through stubs
- Helper-centric surface extension
- Opt-in feature flags default to `false`
- Descriptions say when to use a skill, not how it works
- Manual-only skills use the flag, not the description
- Gates carry a Thought → Reality table
- References over 100 lines open with a Contents list
- Adjective thresholds in skill prose erode at the runtime model — bake the number at authoring time
- Question option lists respect the host limit
- Ask in the user's terms, and name the default
- Nudges on terminal/idle paths only
- The verb contract is the contract — SKILL.md prose paraphrasing drifts
- Inventory table beside a citation when ≥4 sibling rules extracted
- Avoid `&` in `TaskCreate`/`TodoWrite` payloads
- `/rota-x` and `$rota-x` are the same invocation
- Write instructions the model needs, nothing else

## Skills are self-contained, with autonomy rules inline

Each skill owns its rules inline. A shared contract file is a smell when every rule has a single owner: audit the cross-refs, and if each rule is already mirrored at its call site, the central file is vestigial pointer-chasing. Build one only when N≥3 callers need the same long rule verbatim.

Steps that branch on `autonomy.level` (off/auto) and dispatch the next skill by reading and following its instructions ("no prompt, no confirmation, no 'want me to' question") repeat that directive verbatim beside each skill invocation. Readers don't chase cross-refs, and the harness drifts toward asking when only the rule's name is at the dispatch site. Redundancy is cheaper than scattered authority.

## Don't ask what the code can answer

Before a skill asks a question, check whether the answer is derivable from the codebase, git history, or `.rota/` state — `grep`, file reads, `git log`, `BACKLOG.md`, `KNOWLEDGE.md`, `status.json`, helper output. If it is, derive the answer (with a one-line note inline about what was found and where) and skip the question. Questions are for genuine ambiguity (open requirements, opposing reasonable interpretations, the user's risk tolerance on a destructive op), not a forced-yes ritual confirming state the skill could discover.

## No ceremony: banners, per-site ask fallbacks

Skills print no banner and require no task-list tool: no `ToolSearch` load, no per-phase `TaskCreate` boilerplate. Subagent dispatches never create tasks; the orchestrator owns the list. The step checklist below is plain text the model copies into its reply, not a tool call.

## Open each workflow with a copyable step checklist

The skills guide ("Workflows and feedback loops") asks for a checklist the agent can copy into its response and tick off. Every skill with step headings opens its workflow with one, before the first step heading:

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

Question instructions describe the header, prompt, options and whether several selections are allowed; they are not tool arguments. Use the host's available question interface within its limits. If it is unavailable or cannot represent the question, ask in prose (in Codex this includes modes without a picker, approval questions the picker forbids, and missing multi-select or preview fields), number the options and accept the selected numbers or text; show previews above the question. A missing picker never skips a question. Skills do not carry per-site fallback lines.

**Claude Code only:** its picker is `AskUserQuestion`; map the question shape to its supported fields.

Map free-text replies to the nearest option only when the intent is clear. For non-gated choices, use the site's stated default on an ambiguous reply and say which one landed. Destructive operations and opt-in flags default to cancel or `false`. Manual gates require an explicit answer authorizing the action: wait for it or stop; silence, a timeout or an unavailable tool is never approval.

Phase outcomes, where a skill names them, stay mechanically verifiable (a file exists, a command exits 0, a commit landed, a recorded user answer), not subjective states.

## Stage features across slices using pass-through stubs

Multi-slice features ship the SHAPE early via pass-through stubs that explicitly name the future-slice wiring point (e.g. *"Layer-1 filter is a pass-through stub; the substantive helper lands in M01-S03"*). This signals what consumers should NOT rely on yet. **Companion rule:** when the milestone flips to `shipped`, sweep all `M0X-S0Y` slice references — they were placeholders and become stale after merge.

## Helper-centric surface extension

When scaling a feature surface from "single X" to "list of X" (e.g. one repo → many) across N skills, push parsing/validation/dispatch into `rota` verbs (`cmd/`, `internal/`) and confine each SKILL.md edit to a single guard paragraph: *"if the value resolves to ≥2 entries, call the verb; otherwise unchanged."* Single-X path stays byte-identical, multi-X complexity lives in code (exercised by smoke), per-skill prose stays ≤15 lines.

## Opt-in feature flags default to `false`

When adding a new boolean config flag whose purpose is to enable additional skill behavior or auto-invocation:

- **Default `false`** in the config defaults `rota init` writes (fresh and re-stamped projects alike).
- **Never silently flip to `true`** anywhere — not on first detection, not on first invocation, not via cwd-inferred heuristics.
- The owning skill flips the flag to `true` only via explicit user approval: first-run scaffold approval (the user opted in by approving), or a question on existing state with default "Leave off".
- `rota config set` edits the flag explicitly (the flag is never read-only).
- **Exempt:** standard-on settings with opt-out semantics (e.g. `ship.review: true`) — these are not opt-in flags. Mode switches inside an already-enabled feature (e.g. `docs.autoCreate: false→true`) are also exempt.

Without this rule, opt-in flags drift toward auto-flip-on-first-detect, which makes them on-by-default in practice.

## Descriptions say when to use a skill, not how it works

The `description` frontmatter is the trigger: the situations and phrases that should load the skill, nothing else. A description that summarises the workflow ("reproduce, hypothesize, fix, open a PR") gives the agent a shortcut: it follows the summary and skips the skill body. State the trigger, name the neighbouring skill when the two are easy to confuse, and leave the steps to the body.

`test/validate-skills.py` caps a description at 350 characters (`DESC_CAP`); the Agent Skills spec's 1024 is a ceiling, not a target. It also rejects "you", "your" and "I" outside quoted trigger phrases: the description lands in the system prompt, so it is written in the third person ("the user").

**Forbids.**
- Listing steps, outputs, verbs or config keys the skill uses in its description.
- Raising `DESC_CAP` to fit a longer description; cut the description instead.

**Permits.**
- Trigger phrases in quotes, a flag with its own trigger (`--undo` on "roll back the last cycle"), and one sentence routing a confusable request to the right skill.

## Manual-only skills use the flag, not the description

A skill the model must never start on its own sets `disable-model-invocation: true` in its frontmatter (Claude Code) and ships `agents/openai.yaml` with `policy:` / `allow_implicit_invocation: false` (Codex). Neither costs description characters, and prose like "manual only" stops nothing. `test/validate-skills.py` requires the two together. The set is `rota-decide` (a hard boundary is the maintainer's call) and `rota-release` (tags and publishes). `rota-ship` and `rota-orchestrate` stay model-invocable: `AGENTS.md` tells a model to invoke `rota-orchestrate` on "you are the orchestrator", and `rota-ship` is reached from `rota-work` and on "ship it". A flagged skill is also closed to model-initiated calls, so another skill cannot chain into it; it can only offer `/rota-decide` for the user to type.

**Forbids.**
- Writing "manual only" in a description in place of the flag.
- Flagging a skill that `AGENTS.md` or another skill invokes by name.

## Gates carry a Thought → Reality table

A hard gate (Iron Law, proof, review verdict, manual gate) gets a short two-column table in its skill: the rationalization an agent reaches for when the gate is in the way, and the fact that answers it. Place it just before the skill's `Key Principles`. Keep rows to the shortcuts the skill's own steps have to resist, one line each, and cite the step or exit code that enforces the gate. `/rota-debug`, `/rota-work` and `/rota-ship` carry one; add a row when a gate is skipped in practice, not in anticipation.

## References over 100 lines open with a Contents list

A reference longer than 100 lines starts with a `## Contents` section, within its first 15 lines, listing its `##` headings. A model that previews a file with a partial read still sees everything the file covers. `test/validate-skills.py` enforces it (`REF_TOC_LINES`); update the list when you add, rename or remove a section.

## Adjective thresholds in skill prose erode at the runtime model — bake the number at authoring time

Prose like "a few", "many", "high X", "ambiguous", "might/may" forces the runtime LLM to invent a threshold every invocation. Test each one: if two competent readers could read it in opposite directions, replace it with (a) a number, (b) a conditional (*"when X happens, Y"*) or (c) an assertive verb. Normative rules never hedge with "might", "may" or "could" where the runtime needs a binary answer. Descriptive prose where no rule fires off the adjective (*"a typical day"*) and genuinely open situations the rule flags as a known unknown are exempt.

## Question option lists respect the host limit

Use at most 4 options, or fewer when the host's interface requires it. Never assume the host will scroll, and never merge unrelated categories to fit. Chunk into sequential questions within the host limit, use two stages (categories, then keys), or list the numbered options in prose. For multiple selections, collect the chosen numbers when the picker cannot represent them.

## Ask in the user's terms, and name the default

Write every question for someone who does not have the file open. Name the choice in the vocabulary of what the user observes — the behavior, the artifact, the outcome — not in the vocabulary of the code that implements it, and say what the skill will do by default if the answer turns out not to matter. *"Should a finished game still show the training row, or only live ones?"* beats *"confirm expected `inGameMenuActions` behaviour for `replayClosable && mode === 'bot'`"*: same decision, but only the first can be answered without a file open.

The skill holds the context, so translating is its job. A question phrased in implementation terms hands that work to the user and usually gets a guess back, which reads like an answer and is acted on as one. Stating the default converts a question the user does not care about into one they can decline cheaply.

**Forbids.**
- Naming a symbol, file, config key, or boolean expression as the *subject* of the question when a user-observable phrasing exists. (Citing one as supporting detail after the question is fine.)
- Asking a question whose options the user cannot distinguish without reading code — if the options only differ internally, the skill should be picking, not asking (see *Don't ask what the code can answer*).
- Leaving the no-preference path unstated, so that "either is fine" produces another round-trip instead of a decision.

**Permits.**
- Several questions in one batch when the host supports it, within its question limit. In prose, number them and track which answers are still needed. `/rota-work` Step 2's 1–3 question batch stays correct.
- Implementation vocabulary in the `description` field of an option, where it disambiguates for a user who *does* have the file open.

A partial reply settles only the answered questions; never infer an approval for an unanswered one.

## Nudges on terminal/idle paths only

When a nudge or check could fire from multiple skills that converge on the same end-state (e.g., `/rota-work` → `/rota-ship` → `/rota-learn` handoffs), place the nudge on the *terminal/idle paths* — where the user is about to leave the session — NOT on dispatch paths that hand off to another skill. Several skills firing the same nudge from convergent flows drowns the signal.

**Forbids.**
- Firing the same nudge from a skill's tail when that skill auto-dispatches the next skill (the user never sees the message — it's overwritten by the dispatched skill's output).
- Firing the nudge from a dispatch path on the assumption *"users will see it eventually"* — they see the loudest, latest message; intermediate nudges are noise.

**Permits.**
- Firing the nudge from the terminal branch of a routing skill (e.g. `/rota-work` no-argument mode "Stop here" / empty-backlog) where the user is about to step away.
- Firing the nudge from the post-ship report (`/rota-ship` Step 9.5) where the cycle ended and no auto-dispatch follows.

## The verb contract is the contract — SKILL.md prose paraphrasing drifts

When a SKILL.md cites an `rota` verb, the verb's entry in `docs/contributing/contract/` (index in `README.md`) (and `rota <verb> --help`) IS the contract; prose paraphrases drift. Before authoring prose ABOUT a verb, read its entry — if the SKILL.md disagrees with the contract, the SKILL.md is wrong.

**Forbids.**
- Paraphrasing a verb's behavior in SKILL.md prose without reading its contract entry first.
- Inferring a verb's contract from how callers use it — callers can be wrong; the contract is the source of truth.

**Permits.**
- Quoting the verb's contract entry verbatim in the SKILL.md when the prose needs the exact contract.
- Updating SKILL.md prose to match a verb after its contract changes (the prose follows the code, not the other way around).

## Inventory table beside a citation when ≥4 sibling rules extracted

When a SKILL.md extracts N≥4 sibling rules to a `references/` file, leave a one-column inventory table beside the citation. Readers scanning the SKILL.md see rule names without opening the reference; readers wanting the body click through.

**Forbids.**
- Citing a reference with 4+ extracted rules without an inventory — the reader has to open the file to know whether the rule they care about is there.
- Restating the full rule body in the inventory — that defeats the extraction; the inventory is a TOC, not the content.

**Permits.**
- For ≤3 extracted rules, citing the reference inline without an inventory (the rule names fit in the citing sentence).
- Inventory tables with extra columns (audience, complexity, etc.) when those columns help readers triage.

## Avoid `&` in `TaskCreate`/`TodoWrite` payloads

Claude Code's TUI HTML-escapes task titles but never decodes them, so `&` shows as the literal `&amp;`. In any `TaskCreate`, `TaskUpdate` or `TodoWrite` payload (including examples in skill prose), use `and` or `+` instead; pre-encoding with `&amp;` double-escapes. `&` stays fine in prose, code blocks, shell commands and topic headings like `## Build & Tooling`.

## `/rota-x` and `$rota-x` are the same invocation

Codex invokes a skill as `$rota-x`; Claude Code as `/rota-x`. Skill text keeps `/rota-x` everywhere and does not branch on the harness: read `$rota-x` as the same call. A skill installed with `rota skills install` lists in Codex as `rota-x`, and `$rota-x` invokes it. Never write both spellings in one sentence, and never rewrite an existing `/rota-x` to `$rota-x`. When the harness has no skill-invocation tool, read the target skill's `SKILL.md` and follow it with the stated arguments and brief; a slash command is not a shell command.

## Write instructions the model needs, nothing else

Three rules for every sentence added to a skill.

- **Pair a ban with the target action.** "Don't summarize the diff" leaves the model guessing; "Don't summarize the diff; list each changed verb" gives it something to do.
- **Delete a sentence that changes nothing against the model default.** Strike it and ask whether the output would differ. If not, cut it.
- **Don't restate what the environment answers.** A validator, verb contract or tool schema already says it; point at it or stay silent.

**Forbids.**
- A bare prohibition with no replacement action.
- Instructions that repeat model defaults or something a validator already enforces.
