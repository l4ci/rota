# Skills audit against Anthropic's authoring guide

Every `skills/rota-*/SKILL.md` and `skills/references/*.md` was checked against Anthropic's [Skill authoring best practices](https://platform.claude.com/docs/en/agents-and-tools/agent-skills/best-practices), read on 2026-10-05 (#248). The audit ran after the twenty skill changes from the mattpocock/skills and obra/superpowers surveys (#228–#247) had merged, at `main` `0968561`.

Mechanical fixes landed in the #248 PR. Larger ones are filed as items. Guidelines that conflict with a rota convention are listed at the end and left alone.

## Contents

- What already passes
- Findings by skill
- Findings in references
- Conflicts left alone

## What already passes

| Guideline | Result |
|---|---|
| `name`: 64 chars max, lowercase/digits/hyphens, no reserved words | All 16 pass; `test/validate-skills.py` enforces it |
| `description`: non-empty, 1024 chars max, no XML | All pass; rota caps descriptions at 350 (`DESC_CAP`, #246) |
| SKILL.md body under 500 lines | All pass. The longest are `/rota-ship` (357) and `/rota-work` (310) |
| Forward-slash paths | No backslash paths |
| Fully qualified MCP tool names | No skill calls an MCP tool |
| Scripts solve, don't defer; utility scripts over generated code | Deterministic work lives in `rota` verbs with documented exit codes; skills run them, they do not read them |
| Feedback loops for quality-critical steps | Proof rows, the review verdict, the Iron Law and `rota verdict route` already loop on failure |

## Findings by skill

Size: **mech** is a wording or deletion fix with no behaviour change. **large** needs design or touches behaviour.

| Skill | Guideline | Where | Fix | Size | Outcome |
|---|---|---|---|---|---|
| all | Third-person description | `rota-pause`, `rota-ship`, `rota-spike` descriptions said "you" | Reworded; validator now rejects "you"/"your"/"I" outside quoted triggers | mech | Fixed |
| rota-ship | Progressive disclosure | Docs Mode, ~85 of 361 lines, loads on every ship | Move to a file next to the skill | large | #274 |
| rota-ship | Concise | Step 3.5 rationale for gate ordering | Deleted | mech | Fixed |
| rota-ship | Time-sensitive | `secondOpinionRunner: "codex"` removed-in-5.0 note | Drop or move to `rota migrate` | large | #278 |
| rota-work | Workflow steps | `rota status add` in Steps 3 and 5; Step 9.5 runs before Step 9; one-paragraph proof step | Reorder, number in run order | large | #276 |
| rota-work | Terminology | "orchestrator"/"worker" mean main session/subagent here, round roles elsewhere | Say "main session"/"subagent" | large | #277 |
| rota-work | Concise | "Use it when … 2+ independent pieces" repeats the description and contradicts single-item use | Deleted | mech | Fixed |
| rota-review | Time-sensitive | "(F03 lifecycle)", a retired item ID as a label | Deleted | mech | Fixed |
| rota-review | Concise | Backstory before the Task-N comment scan | Cut to the instruction | mech | Fixed |
| rota-review | Workflow steps | Step 7's "don't re-run on a passed branch" gate sits in Rules | Move into Step 7 | large | #276 |
| rota-review | Concise | Phase list repeats step headings; long When-to-use lists | Cut | large | #281 |
| rota-capture | Time-sensitive | "real F27 incident" narrative | Deleted | mech | Fixed |
| rota-capture | Time-sensitive | Removed import flags row; legacy `GH:`/`GL:` de-tag flow | Retire or move into the binary | large | #278 |
| rota-debug | Terminology | `light`/`standard` tiers undefined in the skill | One tier vocabulary | large | #277 |
| rota-debug | Concise | Key principles restate Steps 3–5 | Cut | large | #281 |
| rota-decide | Concise | "Codified from grill-with-docs" provenance line | Deleted | mech | Fixed |
| rota-decide | Workflow steps | Step 1 / Step 1.5 numbering | Renumber | large | #276 |
| rota-learn | Time-sensitive | F18, T5, F21, T03 history in live rules | Deleted | mech | Fixed |
| rota-learn | Concise | Dedup rule stated three times; umbrella-only decision stated twice | Keep once | large | #281 |
| rota-orchestrate | Concise / time-sensitive | "misled reads … many times"; "qualifies today" | Deleted | mech | Fixed |
| rota-orchestrate | Too many options | Stuck worker: four options, no default | Name a default | large | #276 |
| rota-orchestrate | Workflow steps | Autopilot and escalation policy in 170–200-word paragraphs | Split into bullets | large | #276 |
| rota-pause | Terminology | "handoff note", "note", "handoff" for one file | One term | large | #277 |
| rota-pause | Workflow steps | Unnumbered orchestrator section between Steps 5 and 6 | Move or number | large | #276 |
| rota-plan | Concise | Key principles repeat Step 3's verify and RED rules | Cut | large | #281 |
| rota-qa | Time-sensitive | "removed in 5.0" clause | Reworded | mech | Fixed |
| rota-qa | Concise | "Authoring tier" row, metadata with no runtime use | Deleted | mech | Fixed |
| rota-qa | Templates | `--body-file "$VERDICT"` has no shape; verdict line omits `INFRA-FAIL` | Add a template | large | #276 |
| rota-refactor | Terminology | `models.orchestrator`/`models.worker` vs `light`; "candidate" vs "finding" | One vocabulary | large | #277 |
| rota-release | Consistency | Config table said the checklist is walked in "Step 1.5"; it is Step 2 | Fixed the reference | mech | Fixed |
| rota-release | Concise | 150-word asset paragraph; edge case restates Step 11 | Cut | large | #281 |
| rota-spike | Time-sensitive | "codified in KNOWLEDGE 2026-05-02" | Deleted | mech | Fixed |
| rota-spike | Concise | One umbrella fact stated four times; duplicate principles | Keep once | large | #281 |
| rota-vision | Templates | Milestone proposal format is a run-on sentence | Add a template | large | #276 |
| rota-brainstorm | — | No finding beyond the shared items | — | — | — |
| rota-capture, rota-plan, rota-vision, rota-spike | Don't assume tools are installed | `… --json \| jq -r .data.<field>`; `rota doctor` checks `git` and `gh`, not `jq` (fixed in #282: doctor now checks `jq`) | Doctor check, or a field flag on the verbs | large | #282 |
| all | Evaluations | No skill has an evaluation; none is tested across Haiku, Sonnet and Opus | Write scenarios for the core skills | large | #279 |

## Findings in references

| Reference | Guideline | Where | Fix | Size | Outcome |
|---|---|---|---|---|---|
| 4 files over 100 lines | Contents list for long files | `authoring-conventions`, `learn-rare-modes`, `umbrella-mode`, `worker-contract` | Added `## Contents`; validator enforces it (`REF_TOC_LINES`) | mech | Fixed |
| herdr-dispatch, tmux-dispatch, work-preview, issue-mode, isolation-patterns, umbrella-mode, grilling, design-exploration, review-verdict-routing | References one level deep | Reference-to-reference chains, two cycles, triplicated branch commands | Give each fact one home | large | #275 |
| milestone-tagging, work-toolchain-siblings | Progressive disclosure | Single-consumer stubs (9 and 22 lines) | Inline | large | #275 |
| subagent-dispatch, grilling, worker-contract | Terminology | Tier names vs model names vs config keys | Define the tiers once | large | #277 |
| humanizing-prose | Concise | 55-line general AI-tells catalog | Keep project rules and a pointer | large | #281 |
| learn-rare-modes | Correctness | "Defer" keeps a candidate, then the step clears the whole queue | Clear only resolved items, or drop Defer | large | #280 (bug) |
| docs-conventions, post-cycle-trigger-gate, tmux-dispatch, README | Consistency | Stale step labels (Docs Mode D-steps, release gates, "SKILL.md Step 6"), a cite to a missing "mirror-step" rule, "five-step spine" | Fixed to match the files cited | mech | Fixed |
| three-mode-skill-shape | Consistency | Cite to a "Tier S/C" section that does not exist | Deleted | mech | Fixed |
| silent-failure-hunter | Consistency / concise | "seven recurring shapes" over ten bullets; provenance line | Fixed | mech | Fixed |
| subagent-dispatch, source-prefill, persistence-skills, knowledge-consult, review-verdict-routing, context-load-protocol, learn-rare-modes | Time-sensitive | Dated cites, retired item IDs (F03, F04, F21), V1/V2 labels, "no verb today", "a recent audit" | Deleted or restated as current fact | mech | Fixed |
| detail-files, umbrella-mode, knowledge-consult, context-load-protocol, isolation-patterns | Terminology | "TODO entry" for a backlog entry | "backlog entry". The `Related TODO entry:` template line stays: the binary writes it and golden tests pin it | mech | Fixed |
| authoring-conventions | Time-sensitive | "V2-surface" section pointing at `bin/` helpers, which no longer exist | Retitled; now says `rota` verbs | mech | Fixed |
| review-verdict-routing, rota-qa | Time-sensitive | Retired codex runner still named in routing rules | Removed from routing; the `rota-qa` shim is in #278 | mech / large | Fixed / #278 |

## Conflicts left alone

| Guideline | Rota convention | Why it stays |
|---|---|---|
| The description says what the skill does *and* when to use it | Descriptions are trigger-only (`authoring-conventions.md`, #246) | A workflow summary in the description lets an agent follow the summary instead of reading the skill. Third person is kept; the "what" half is not |
| Gerund skill names (`processing-pdfs`) | `rota-<word>` names | The names are the slash-command surface users type. Renaming them is a one-way change for no gain in discovery: the shared prefix already groups them |
| "Copy this checklist into your response" for complex workflows | No mandatory task lists ("No ceremony") | Phase lists are optional and the host's task tool is used when present. #281 cuts the lists that only repeat step headings |
| Concise: say each thing once | Autonomy directives repeat verbatim at every dispatch point | A rule named only by reference at a dispatch site drifts toward asking. The repeat is deliberate and #281 excludes it |
| Concise: drop rationale | Thought → Reality tables before Key Principles in gated skills | The table answers the shortcut an agent reaches for at a gate; it is behaviour, not background |
| Concise: drop provenance | "Codified …" notes in `authoring-conventions.md` | That file is read by skill authors, not loaded at a skill's runtime path. The history tells an author whether a rule still applies. Stale facts in it were fixed |
