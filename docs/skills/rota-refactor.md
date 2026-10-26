---
skill: rota-refactor
lane: execute
order: 115
situation: Bigger picture
summary: "Review the architecture and file refactor issues; --map drafts the map."
not_when:
  - use: rota-work
    when: the refactor is already an issue and ready to build
  - use: rota-vision
    when: you are planning product milestones, not code structure
before: []
after: [rota-work]
manual_only: false
flags:
  - flag: "--fix"
    does: implement the candidates you pick (parallel workers, verification, one commit)
  - flag: "--designs"
    does: draft competing interfaces for structural candidates
  - flag: "--interactive"
    does: choose which candidates to file
  - flag: "--map"
    does: write the `.rota/map/` subsystem files, then run `rota map index`
example:
  source: walkthroughs/brownfield-existing-project.md
  reviewed: false
  steps:
    - you: /rota-refactor internal/cli
    - agent: |
        Strong: split cmd dispatch from flag parsing (deletion test: parsing has one caller).
        Worth exploring: second adapter for the config store.
        Filed #71 and #72 as `refactor` issues with `## Acceptance`; skipped 1 duplicate.
        No code changed.
  changes:
    - one `refactor`-labelled issue per candidate
    - no code changes unless you pass --fix
---
# /rota-refactor

Architecture review. Explores the codebase (or one area, `/rota-refactor internal/cli`) for friction using one vocabulary (module, interface, depth, seam, adapter, leverage, locality) and a few heuristics (the deletion test, the interface as the test surface, one adapter is a hypothetical seam). It reads the glossary, knowledge and decisions first, ranks candidates Strong / Worth exploring / Speculative, and files each as a `refactor`-labelled issue with a `## Acceptance` section, deduped against open and closed issues. It changes no code by default.

Flags: `--fix` implements the candidates you pick (parallel workers, verification, one commit; `refactor.confirmBeforeExecute` gates it), `--designs` drafts competing interfaces for structural candidates, `--interactive` lets you choose which to file. Several workers can each review one area in a round.

