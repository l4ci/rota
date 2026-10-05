# Knowledge & decisions consult

Used by `/rota-debug` Step 3+3.5, `/rota-refactor` (Orient step), `/rota-review` Step 3, and indirectly by `references/context-load-protocol.md` (which composes this pattern into a wider load list for `/rota-plan`, `/rota-vision`, `/rota-work` — including `/rota-work --preview`).

The pattern is one pair of `rota` calls plus carrier semantics. Skills point here so the call-site logic lives in one place.

## Verbs

```bash
rota knowledge query "Topic A" "Topic B"
rota decisions query "Topic A" "Topic B"
```

Topic selection: read the `rota-knowledge` and `rota-decisions` managed blocks in `CLAUDE.md`, infer which topics plausibly touch the work (e.g., a backend refactor might pull `Architecture`), and pass those names as arguments. Pass several when in doubt — both verbs filter, so extras cost nothing.

Both verbs are lookups: they exit 0 with empty `text` when no topic matches (`knowledge query` also lists the unmatched names in `data.missing`). Do not wrap them with `2>/dev/null` or treat empty output as failure — that hides real errors and contradicts the lookup-vs-resolve contract in `.rota/DECISIONS.md`.

## Parallelism

The two queries are independent and disk-bound. When the calling skill issues both (almost always together), dispatch them as parallel tool calls in the same response.

## Carrier semantics

The two verbs return different *kinds* of content and the calling skill must handle them differently.

**KNOWLEDGE bullets are gotchas / hints.** Surface relevant ones into whatever brief the calling skill is building — debug hypothesis, reviewer brief, refactor sub-agent prompt, worker brief. The label varies per skill; the role is the same: prior-art that informs but does not constrain.

**DECISIONS entries are hard boundaries.** Pass the FULL entry to the consumer — rule, *Why*, **Forbids**, **Permits** — not just the title. The calling skill MUST treat them as constraints, not suggestions. If the planned action would violate a decision, stop and surface to the user before proceeding. Do not paper over a violation by rewording or scoping it away.

## Hit-register after consumption

**This step is mandatory whenever the calling skill carries bullets into a downstream brief.** Skip only when `rota knowledge query` returned nothing or all returned bullets were pruned before the brief was written.

After building the brief, identify which KNOWLEDGE bullets actually landed in it. For each, call:

```bash
rota knowledge hit --topic "<T>" --title "<first-line-of-bullet>"
```

Where `<T>` is the exact `## Topic` heading from `rota knowledge query`'s output and `<title>` is the bold **title** text on that bullet (the text between `**` and `** —`).

**Worked example.** Suppose `rota knowledge query "Architecture"` returned:

```
## Architecture

- **Always route network calls through NetworkClient** — raw URLSession is forbidden outside the client layer. <!-- 2024-01-10 -->
- **Avoid force-unwrap in production paths** — use guard/let or explicit error propagation. (provisional) <!-- 2024-03-05 -->
```

And both bullets survived into the brief's `**Known gotchas:**` section. Register both in one parallel batch:

```
Bash: rota knowledge hit --topic "Architecture" --title "Always route network calls through NetworkClient"
Bash: rota knowledge hit --topic "Architecture" --title "Avoid force-unwrap in production paths"
```

Issue all calls **in a single parallel tool-call batch** — each verb serializes its sidecar write behind a per-file lock, so concurrent calls don't lose hits. Silent on success. Provisional bullets auto-promote to confirmed once `hits >= learn.promoteThreshold` (default 3) without a pending contradiction.

**Only register bullets that survived into the brief.** Bullets returned by the query but pruned before they reached a `**Known gotchas:**` / `**Relevant project conventions:**` section don't earn credit — unused queries shouldn't drive promotion.

## Skip silently on empty

Either verb can legitimately return no rows. That is not an error; it means no topic in the registry matched the arguments. Carry only what matched into the downstream brief — do not insert empty headings, placeholder "(none)" lines, or apology prose.

A caller MAY add carrier-specific failure semantics on top — for example, "if my brief explicitly asserts that topic X is registered and the lookup returns empty, surface to the user." That logic belongs in the calling skill, not in this reference and not in the verbs.

## What this reference does NOT cover

- **Full pre-planning context load.** This is the K+D *query* pattern only. The composed load (backlog entry, plan, milestone, git history, plus K+D) lives in `references/context-load-protocol.md`, which cites this file for the K+D subset.
- **`.rota/KNOWLEDGE.md` `## Glossary` vocabulary lookup.** That's `rota glossary read`, a term-keyed reader against the Glossary topic. Glossary entries are stored alongside other topics in KNOWLEDGE.md but the reader returns nested-bullet entries (term + definition + aliases + not) rather than topic bodies.
