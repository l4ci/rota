---
skill: rota-learn
lane: persist
order: 80
situation: Worth remembering
summary: Save the session's durable lessons to KNOWLEDGE.md.
not_when:
  - use: rota-decide
    when: the point is a commitment with explicit forbids and permits, not a gotcha
  - use: rota-pause
    when: you only need to hand off where you stopped, not record lasting knowledge
before: [rota-debug, rota-work]
after: []
manual_only: false
flags:
  - flag: "--strict"
    does: run the Opus verifier over the new bullets
  - flag: "--retro [<session-id|path>]"
    does: turn the session's mistakes into guardrails and standards
  - flag: "--external-review"
    does: propose splits for oversized external topics and fold stray files, asking before each split
  - flag: "--term <name>"
    does: write a Glossary entry
  - flag: "--promote | --deprecate | --amend <topic> \"<title>\""
    does: edit one existing bullet's tier or body without the capture flow
example:
  source: walkthroughs/brownfield-existing-project.md#step-8-a-real-debug-cycle
  reviewed: false
  steps:
    - you: /rota-learn
    - agent: |
        Proposed bullet under ## Alerts:
        Severity is case-sensitive in the dedup key path. Normalize at the boundary, not at storage time.
        Written to .rota/KNOWLEDGE.md.
  changes:
    - one new bullet under its topic in .rota/KNOWLEDGE.md, date-stamped
    - the rota-knowledge topic index in AGENTS.md refreshed
---
# /rota-learn

Writes durable knowledge from the current session into `.rota/KNOWLEDGE.md`, grouped by topic. It captures gotchas, project conventions, constraints, debugging insights and decisions with rationale, and skips anything obvious from reading the code. `--strict` adds a verifier pass over the new bullets (off by default). `--retro [<session-id|path>]` reads the newest session transcript (Claude Code or Codex), or the one you name, and cites it as evidence, then classifies the session's mistakes into guardrails (filed as items), standards, navigation pointers, tool-economy fixes and information-access gaps (filed as items), flags no-op bullets for removal without deleting them, audits steering files (`AGENTS.md`, `CLAUDE.md`, global instructions) for restating or dead lines without editing them, and reports a repo whose check command runs in no CI workflow or hook as a no-guardrail finding. In umbrella mode the write (and `--term` Glossary entries) routes to the scope that the cwd or `--repo` resolves to: repo-local or the umbrella-shared `.rota/KNOWLEDGE.md`. Three manual flags edit one existing bullet and skip the capture flow: `--promote <topic> "<title>"` sets its tier to `confirmed` without waiting for the hit threshold, `--deprecate <topic> "<title>"` sets it to `deprecated`, and `--amend <topic> "<title>"` appends text to its body (tier and hits stay). `--external-review` is the maintenance pass over the external knowledge base: it measures the topics, drafts a split plan (3 to 7 subtopics with hooks) for each oversized one, offers to fold stray files an older rota left behind, and always asks you before it runs `rota knowledge split`, whatever `autonomy.level` says. See [learning](../usage/learning.md), [external knowledge](../usage/external-knowledge.md) and [umbrella mode](../usage/umbrella-mode.md) for the full flow.
