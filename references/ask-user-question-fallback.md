# AskUserQuestion plain-text fallback

When the host doesn't surface `AskUserQuestion`'s option picker — plain-text terminal, stripped harness, web client without component support — or the user replies with free text to an options-based prompt, the agent must ask the same intent in prose and parse the reply against the original option set. Roughly 17 SKILL.md files carry per-step "Plain-text fallback:" lines today; this reference covers the canonical mechanics so the per-site lines stay site-local carrier (the exact question text and option-to-outcome mapping) instead of restating the rule.

## When it fires

- The host doesn't render `AskUserQuestion` UI at all (plain-text harness, web client without component support, scripted automation piping through stdin).
- The user replies with free text instead of selecting an option — always, even on hosts that DO support `AskUserQuestion`. The picker is a hint, not a contract.
- A scripted/automated harness skips the picker entirely; the next user-shaped input is whatever lands on stdin.

## The mechanic

- **Ask once in prose.** Restate the intent of the original `AskUserQuestion` question with the option labels listed inline (e.g. *"Apply, Apply + scrub ARCHIVE, or Cancel?"*).
- **Map the free-text reply to the original options.** A clear synonym or substring match counts — *"ship it"* maps to a `"Ship"` option, *"yeah"* to a yes; *"scrub-archive"* to the `Apply + scrub ARCHIVE` arm. An ambiguous reply triggers the site's default rule (below).
- **Do not re-ask in a loop.** One round-trip max. A second ambiguity means commit to the documented default and proceed, naming the choice in the next message.

## Default rules (three patterns)

| Pattern | When to use | On ambiguity |
|---------|-------------|--------------|
| **Honor yes/no** | Binary confirmation (proceed / cancel, ship / hold, write / skip). The site's prose lists the two outcomes. | Treat the reply as `no` — the binary cases are usually destructive or commit-producing, so silence defaults to the safe side. |
| **Default to Recommended** | Multi-option picks where one option is `(Recommended)` and the others are equally valid alternatives. Used for the general routing cases — picking among reasonable plan shapes, mapping to a default merge strategy, etc. | Pick the `(Recommended)` option; state it explicitly in the next message so the user sees what landed. |
| **Default to opt-in-off / cancel** | Destructive operations (archive scrubs, branch deletes, rm-style cleanups) and opt-in feature toggles (new config flags whose purpose is to enable behavior). The safe default is "don't do the thing." | Default to the safe option (cancel for destructive; `false` for opt-in flags). The *Opt-in feature flags default to `false`* convention is the canonical authority for the flag case; this rule extends the same disposition to destructive operations. |

## Example mappings

The three rules in the wild today — read these as illustration, not as authoritative wording (the prose is site-local):

- **Honor yes/no** — `/rota-learn` issue-file gate (*"File a rota issue?"*), `/rota-spike` promote-to-decision gate (*"Promote to a decision?"*), `/rota-pause` uncommitted-work stance (*"Wrap them in a `wip:` commit, stash them, or leave them in place?"*).
- **Default to Recommended** — `/rota-work` plan-shape ambiguity (one Recommended interpretation among several equally-valid plans), `/rota-ship --docs` route picks (first-run / after-work / restructure), `/rota-vision` brainstorm-vs-edit picks.
- **Default to opt-in-off / cancel** — `/rota-capture --remove` apply gate (anything other than `yes` / `scrub-archive` is Cancel), `/rota-decide` write gate (only `yes` / `write` commits the decision), `/rota-ship --docs` after-work mode opt-in (default **Leave off**).

## Why three rules and not one

A single "default to Recommended" would be wrong for the destructive and opt-in sites — *"silence flips the config flag because Enable was marked Recommended"* is exactly the auto-flip-on-first-detect drift that the *Opt-in feature flags default to `false`* convention exists to prevent. A single "default to no / cancel" would be wrong for the routine routing sites — `/rota-work` stalling on every ambiguous plan-shape reply makes loop mode unusable and forces re-prompts the user already declined.

The three rules carve up by *consequence*, not by question shape: binary gates honor explicit yes/no because the cases are commit-producing; routing picks default Recommended because one option is the de facto good answer; destructive and opt-in default off because silence is never user approval for either. The site picks the rule when authoring the SKILL.md prose — the mechanic does not infer it from the option list.

## Per-skill carrier — what stays inline

- **The exact prose question text.** Each skill's UX wording, examples, and option summary belong to that SKILL.md — *"Apply changes? (yes/no/scrub-archive)"* is not the same shape as *"Author a runlog entry?"*.
- **The mapping from specific free-text replies to specific outcomes.** Each skill's option set differs; `yes` / `write` / `scrub-archive` / `ship` / `leave off` all live in their owning sites.
- **Which of the three default rules applies.** Declared at each site — the mechanic doesn't decide for you. A binary `/rota-learn` issue-file gate honors yes/no; `/rota-work`'s plan-shape ambiguity defaults to Recommended.

## See also

- `references/authoring-conventions.md` — option-count cap (`AskUserQuestion`'s option list is capped at 4) and other authoring rules. The cap is a different concern (option-list ergonomics, mitigated by category-then-keys staging), not restated here.
- `references/authoring-conventions.md` *"Opt-in feature flags default to `false`"* — the canonical home for the safety-default-on-flags rule; this reference extends the same disposition to destructive operations under the *opt-in-off / cancel* bucket.
