# `/rota-decide` source-prefill modes

Loaded by `/rota-decide` Step 2 when invoked with `--from-learning <topic>` or `--from-spike <name>`. Both modes pre-fill the same four-part decision draft (Rule, Why, Forbids, Permits) from a source artifact, then surface the same closing prompt asking the user for Forbids/Permits, the active commitment the source lacks. Step 3 (Compose the Four Parts) gets the same draft shape from either path.

The shared closing prompt, emitted at the end of either mode:

> *"Pre-filled rule and why from `<source>`. Now name the forbids and permits — those are what make this a decision, not a learning."*

Substitute `<source>` with `KNOWLEDGE.md <topic>` or `spike <name>` as appropriate. Block until the user replies, plug their answer into Forbids / Permits, then continue to Step 3.

## `--from-learning <topic>`

1. Run `rota knowledge query "<topic>"` to load the topic section. If the output is empty, error: *"Topic `<topic>` not found in `.rota/KNOWLEDGE.md`. Run `rota knowledge stats` to list topics."* and stop.
2. Parse the matched topic's bullets. Each is a one-line `- <text> <!-- YYYY-MM-DD -->`.
3. Pick the bullet to promote:
   - **1 bullet** — use it directly, no question.
   - **2-4 bullets** — call `AskUserQuestion` with header `"Bullet"` and question *"Which bullet from `<topic>` should become the decision?"*, one option per bullet. Truncate each option label to ≤80 chars; the option's description carries the full bullet text plus its date stamp.
   - **5+ bullets** — call `AskUserQuestion` the same way but with the 4 most-recent bullets as options. The 4-cap makes a chunked multiSelect picker overkill; a user who wants an older bullet re-runs with a more specific topic.
4. Draft the four parts from the picked bullet:
   - **Rule** = the bullet's text (the user can edit in Step 3 / Step 5).
   - **Why** = *"Promoted from KNOWLEDGE.md `<topic>` (<date>)."* plus any sub-bullet context attached to the picked bullet.
   - **Forbids** = `_(user must articulate — a bullet is passive context, a decision needs an explicit prohibit)_`
   - **Permits** = `_(user must articulate — anchors the boundary so it isn't over-applied)_`
5. Surface the shared closing prompt above.

## `--from-spike <name>`

1. Read `.rota/spikes/<name>.md`. If absent, error: *"Spike `<name>` not found at `.rota/spikes/<name>.md`. Run `rota spike list` to see open and closed spikes."* and stop. (Spike files always live at `.rota/spikes/<name>.md` even in umbrella mode — the `repo:` frontmatter only points at the branch's git history.)
2. Parse the spike file:
   - YAML frontmatter (`status`, `created`, `finished`, optional `repo`).
   - `## Question` — the original yes/no/conditional question.
   - `## Decision` — the verdict (`viable` / `not viable` / `depends-on-X` / `inconclusive`).
   - `## Recommended approach` — present iff verdict is `viable`.
3. Refuse to promote `inconclusive` spikes. Print *"Spike `<name>` is `inconclusive` — not enough evidence for a decision. Add findings on the spike branch and re-run `/rota-spike done <name>`, then come back."* and stop.
4. Draft the four parts from the spike content:
   - **Rule** is verdict-driven:
     - `viable` → *"Use `<X derived from question/recommended>` as the supported approach."*
     - `not viable` → *"Do not use `<X derived from question>`."*
     - `depends-on-X` → *"Use `<X>` only when `<the depends-on condition>`."*
   - **Why** = the original `## Question` plus a 1-2 sentence summary of the spike's `## Findings` section. Keep it tight: most-impactful bullets only, one paragraph max.
   - **Forbids** = `_(user must articulate — what specific patterns/files/approaches does this rule out?)_`
   - **Permits** = `_(user must articulate — what alternatives stay allowed?)_`
5. Surface the shared closing prompt above.
