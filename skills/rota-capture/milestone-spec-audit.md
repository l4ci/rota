# Step 2.5 — Audit Against Code State

Loaded by `SKILL.md` Step 2.5 when the input captures from a milestone spec (names an `M<NN>` tag or a `milestones/M<NN>.md` path).

Milestone specs drift behind code; run `rota item shipped "<title 1>" "<title 2>" …` with the parsed titles. Exit 0 means ship evidence was found (stdout lists hits per title, `--json` has `data.titles[].hits`); exit 1 means none, continue silently.

On exit 0, print the report verbatim, then ask, up to 4 flagged titles per call. Header `"Item N"`. Question: *"`<short-title>` looks shipped — `<hash>` `<subject>`. What now?"* Options:
  1. *"Skip this item (Recommended)"* — drop it from this run.
  2. *"Capture anyway"* — the user reviewed the matches and the item is genuinely distinct.
  3. *"Stop the whole capture"* — print *"Capture aborted — reconcile the milestone spec before retrying."* and write nothing.

Filtered titles never reach the backlog.
