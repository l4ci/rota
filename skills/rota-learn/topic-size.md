# Topic-size handling for `/rota-learn`

Loaded by `skills/rota-learn/SKILL.md` Step 8 when a topic is large.

Run `rota knowledge stats --json`; for any topic in `data.topics` with `bullets >= 25` OR `bytes >= 10240`, branch on `autonomy.level` (`.rota/config.json`):

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
