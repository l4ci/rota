# Grilling

A grilling pass settles every open decision of a design before anything is drafted, so nothing is silently assumed. Used by `/rota-brainstorm` (item scope), `/rota-vision` (Step 5, project scope) and `/rota-decide` (forbids/permits). `/rota-capture` never grills: it is intake.

## Rounds over the frontier

Treat the design as a tree of decisions. The **frontier** is every open decision whose prerequisites are already settled. Each round:

1. List the frontier. Decisions that depend on an unsettled one wait for a later round.
2. Answer what you can without the user (next section). Settled facts leave the frontier with a one-line note on what was found and where.
3. Ask the rest as one batch, numbered `Q1..Qn`. Each question carries a **recommended answer** and a one-line reason; the user confirms or redirects. Cap a round at 4 questions, fewer when the picker requires it (one question per decision); in plain prose, number them and say which you recommend.
4. Fold the answers in. New decisions they unlock join the next frontier.

Don't ask a question whose answer changes nothing downstream.

## Code before user

Facts are not the user's to supply. Before a question reaches the user, try the code (`grep`, file reads), `rota map query`, git history, `.rota/` state and the item's thread. For a fact that needs broad reading, send a `light` subagent (see also `references/subagent-dispatch.md`). Ask the user only for intent, preference, priority and risk tolerance. If the answer needs a running experiment, say it warrants `/rota-spike` instead of guessing.

## Edge-case scenarios

When a decision looks settled, test it with one concrete scenario that stresses its boundary (*"a worker's PR is open and its slot is reassigned: which rule wins?"*). If the answer surprises, the decision was not settled; put it back on the frontier. Offer scenarios, not abstractions.

## Terms

- **Conflict.** If the user's wording collides with a glossary entry (`rota glossary read <term>`), name it: *"Glossary has Worker as X; you mean Y. Which?"*. Don't carry both meanings forward.
- **Sharpening.** A vague or overloaded term gets pinned to one meaning in the user's words. When the user defines or confirms one (*"by X I mean…"*), write it inline, without a separate step: `rota glossary write "<name>" --def "<text>" [--alias "a,b"] [--not "x,y"]` (exit 4 on an alias collision: surface it, don't retry).

## Stop condition

Stop when a round's frontier is empty: every decision is answered, derived from code or marked a deferred open question with an owner. Say so in one line (*"Frontier empty; nothing left assumed."*) and move on. Don't run an extra round to be thorough, and don't stop while a decision is unanswered; an assumption you keep goes in the artifact's Assumptions, named.
