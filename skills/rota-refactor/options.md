# Optional paths

## Umbrella projects

`rota refactor targets --json` lists the sub-repos. Run once per sub-repo with the global `--repo <name>` so each finding lands on the owning tracker. Do not fan out sub-agents from here; a round's orchestrator assigns one area per worker.

## Rejections

If the user rejects a finding for a reason a later review must respect, offer `/rota-decide`. Skip ephemeral ("not now") and self-evident reasons.

## Interactive filing

Under `--interactive`, show the ranked list before filing and let the user drop or reorder; design discussion of a chosen finding belongs in `/rota-brainstorm`.
