# Banner preamble

A code-fenced label every user-invocable rota-* skill prints verbatim at the top of its run, so the user can see at a glance which skill is active, what it does, and which triggers and pairs it carries. Consumed by the user-invocable SKILL.md files across the *Capture & pick*, *Plan & build*, *Review & ship*, *Persist*, *Vision & docs*, and *Maintenance* categories (see `README.md` for the current grouping). Each consumer's preamble line cites this file.

## The rule

> **Print the banner below verbatim before any other action — skip if dispatched as a subagent.** See `references/banner-preamble.md`.

The banner content itself — emoji, name, tagline, triggers, pairs — is a per-skill carrier artifact that stays inline in each SKILL.md. This reference covers the *pattern*, not the per-skill text.

## When to print

- **Always print** when invoked via the `Skill` tool from the foreground session (user invocation, slash command, or one skill invoking another via `Skill`).
- **Always skip** when dispatched as a subagent via the `Agent` tool — banner output rolls up to the orchestrator as a tool result, not to the user, so a subagent banner is noise that conflicts with the orchestrator's own banner.

The skip-condition is a per-invocation check, not a per-skill property — the same SKILL.md may print on `Skill`-dispatch and skip on `Agent`-dispatch.

## Format

````
```
════════════════════════════════════════════════════════════════════════
  <emoji>  <skill-name>  ·  <one-line tagline>
  triggers: "<phrase>", "<phrase>"  ·  pairs: <skill>, <skill>
════════════════════════════════════════════════════════════════════════
```
````

1. **Top rule** — row of U+2550 `═` (Box Drawings Double Horizontal), ~72 columns.
2. **Title line** — two-space indent, then `<emoji>  <skill-name>  ·  <tagline>` (double-space between emoji and name; ` · ` separator).
3. **Metadata line** — two-space indent, then `triggers: "<phrase>", "<phrase>"  ·  pairs: <skill>, <skill>` (triggers in double-quotes; pairs are bare skill names).
4. **Bottom rule** — same as top.

The outer triple-backtick fence ensures the LLM emits the block verbatim with no markdown interpretation.

## Why skip on subagent dispatch

A subagent's stdout is captured as a tool result by the orchestrator, not surfaced to the user. A subagent banner would add noise to the orchestrator's tool-result stream, conflict with the orchestrator's banner printed earlier in the same conversation, and mislead readers about which skill is active.

## Example

From `rota-pause/SKILL.md`:

````
```
════════════════════════════════════════════════════════════════════════
  💤  rota-pause  ·  write handoff note for clean pause
  triggers: "pause", "hand off"  ·  pairs: rota-work, rota-learn
════════════════════════════════════════════════════════════════════════
```
````

## What this reference does NOT cover

- **Per-skill banner content** (emoji, name, tagline, triggers, pairs) — stays inline in each SKILL.md as a carrier artifact.
- **Banner-data style conventions** (which emoji to pick, how to phrase triggers, length budgets) — out of scope; pick by visual cue from sibling skills.
