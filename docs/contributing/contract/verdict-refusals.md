---
verified-sha: c535c4cf9bd5b3b92d47e1d1df0c0014b4bfe750
refs:
  - internal/verdict
---

## B3: verdict refusals, the Iron Law

B3 (#56) moves two prose rules into the verbs. Orchestrator rulings (round 4, phase B, from the maintainer on 2026-10-03): ship refuses after a recorded review or second-opinion FAIL, except under the advisory codex fallback (#158); the Iron Law is a per-item counter, and a 4th attempt is refused until `rota debug reset <ID>`, a B1 gated verb (`--confirm` plus `--confirm-note`, audited, with a reason).

- **Ship refusal.** `ship pr` and `ship merge` read the branch's records in the B2 store (key as `verdict add` builds it). The branch is blocked when its effective review verdict is `FAIL`, or its latest `second-opinion` record is `FAIL` and `ship.secondOpinionRunner` is not `codex`. A stale FAIL still blocks: only a newer record of the same kind clears it, so a fix is reviewed again before it ships. `CONCERNS` and QA verdicts never block here; their routing stays with `verdict route`. A branch with no records ships as before. The refusal is exit 4 before anything changes, with failure data `{"blockedBy": "verdict", "kind": string, "verdict": "FAIL", "sha": string, "stale": bool, "changed": false}` (`kind` is `review-spec`, `review-quality` or `second-opinion`; review is checked first). The hint names `rota verdict show`. `ship pr-merge` (the `/rota-review --queue` merge path) applies the same refusal to the PR's head branch, read from the forge's open-PR listing the verb already makes; in umbrella mode the key is `<repo>:<branch>` for the `--repo` sub-repo. When the forge reports no head branch, the verb does not refuse and adds the warning `could not resolve the head branch of PR <n>; verdicts not checked`. `worker gate` is not covered.
- **Iron Law.** The count is per item: the item's `debug-fix` FAIL records in the store since its latest `debug-reset` record, the same number `debug verdict` reports as `failedFixes`. A new branch or a cleared session file does not reset it. At 3 or more, `debug counter init <ID>` and `debug counter record-attempt` (which reads the item from the session's `bug_id`) exit 4 with failure data `{"blockedBy": "iron law", "bugId": string, "failedFixes": number, "changed": false}` and the hint `rota debug reset <ID>`. `debug verdict` is never refused: an outcome is always recorded.

- **Canonical debug identity (#348).** Counter initialization, attempt checks, verdicts and resets use the item backend’s canonical identity (B2). New sessions store it in `bug_id`; existing sessions resolve their stored reference before checking. File-mode IDs stay literal.
- **Legacy item keys.** Before issue-mode debug operations, a locked migration consolidates old spellings, retaining every record. A legacy reset clears only its original spelling: cleared prefixes precede the still-active tails, so failed counts add across aliases. Unqualified legacy umbrella keys have no repository provenance; their history is copied once to each registered repository’s matching number. This can conservatively halt an uninvolved repository until its own approved reset. Qualified histories and all subsequent resets stay independent. Unparseable legacy keys remain untouched. Migration is idempotent and can run even when the requested attempt or reset is refused.

### rota debug reset
rota debug reset <bugId> --reason <text> --confirm --confirm-note <answer>
repo: scoped
data: {"bugId": string, "failedFixes": number, "cleared": number, "changed": bool}
exit: 2 when `<bugId>` is empty, --reason is missing or blank, or a confirmation flag is given without the other; 3 when no `.rota/` is found or the issue does not resolve; 2 for an ambiguous umbrella reference; tracker; 4 when the `debug-reset` gate is not cleared (B1 manual-gate shape)
old: none (new in B3)
note: appends `{"kind": "debug-reset", "verdict": "RESET", "sha": <HEAD or "">, "recordedAt", "summary": <reason>, "findings": []}` to the item's list, so `failedFixes` starts again at 0 and the earlier attempts stay in the store. `cleared` is the count before the reset. The gate check runs after the arguments; an item with no failed fixes since its last reset needs no approval and resets nothing (exit 0, `changed: false`, no audit line).
note: the gate is B1's `debug-reset` row (registry and audit writer from B1, #54); the audit `target` is the bug ID. The reason lives in the store record; the audit line carries the human's answer.
note: the branch's `debug counter` session file is left alone; `debug counter clear` removes it.
