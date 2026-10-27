---
refs:
  - internal/verdict
---

## B3: verdict refusals, the Iron Law

B3 (#56) moves two prose rules into the verbs. Orchestrator rulings (round 4, phase B, from the maintainer on 2026-10-03): ship refuses after a recorded review or second-opinion FAIL, (the advisory codex fallback of #158 is retired: a second-opinion FAIL always blocks); the Iron Law is a per-item counter, and a 4th attempt is refused until `rota debug reset <ID>`, a B1 gated verb (`--confirm` plus `--confirm-note`, audited, with a reason).

- **Ship refusal.** `ship pr` and `ship merge` read the branch's records in the B2 store (key as `verdict add` builds it). The branch is blocked when its effective review verdict is `FAIL`, or its latest `review-cross`, lens review (`review-security`, `review-perf`, `review-ux`) or `second-opinion` record is `FAIL`. A locally recorded stale FAIL still blocks: only a newer record of the same kind clears it, so a fix is reviewed again before it ships. `CONCERNS` and QA verdicts never block here; their routing stays with `verdict route`. In issue mode, valid, trusted reports at the current SHA are recovered only when the branch has no locally recorded verdict there, through the same lookup as `verdict show`. Any local record at head makes local verdicts authoritative for the whole branch. Recovered entries refresh when a report is updated at the same SHA. Cached reports stop applying after the head moves; they never become locally recorded stale FAILs. File mode stays local-only. A branch with no records ships as before. The refusal is exit 4 before anything changes, with failure data `{"blockedBy": "verdict", "kind": string, "verdict": "FAIL", "sha": string, "stale": bool, "changed": false}` (`kind` is the blocking record's: `review-spec`, `review-quality`, `review-cross`, a lens kind or `second-opinion`, checked in that order; the first failing lens in `security`, `perf`, `ux` order). The hint names `rota verdict show`. `ship pr-merge` (the `/rota-review --queue` merge path) applies the same refusal to the PR's head branch, read from the forge's open-PR listing the verb already makes; in umbrella mode the key is `<repo>:<branch>` for the `--repo` sub-repo. When the forge reports no head branch, the verb does not refuse and adds the warning `could not resolve the head branch of PR <n>; verdicts not checked`. `worker gate` and `worker train` apply the same FAIL refusal plus the bounce rule (#2073): a review record (`review-spec`, `review-quality`, `review-cross`, a lens) recorded at the head the gate merges (the PR's pushed head, not the local tip) with a blocking finding or a failed item refuses even under `CONCERNS`, with `kind` and `sha` of that record, `stale` false and the finding's title in the message. A record at another commit does not count there. `ship pr`, `ship merge` and `ship pr-merge` keep the FAIL-only rule.
- **Iron Law.** The count is per item: the item's `debug-fix` FAIL records in the store since its latest `debug-reset` record, the same number `debug verdict` reports as `failedFixes`. A new branch or a cleared session file does not reset it. At 3 or more, `debug counter init <ID>` and `debug counter record-attempt` (which reads the item from the session's `bug_id`) exit 4 with failure data `{"blockedBy": "iron law", "bugId": string, "failedFixes": number, "changed": false}` and the hint `rota debug reset <ID>`. `debug verdict` is never refused: an outcome is always recorded.

- **Work-fix cap (#2121).** `/rota-work` Step 7 counts fix dispatches per task the way the Iron Law counts debug fixes, but separately: the count is the `work-fix` FAIL records of the store key (the item ID, or `<ID>@<task>` with `--task`, one key per plan task) since its latest `work-fix` PASS or `work-fix-reset` record, so a PASS clears it and a new session or branch does not. It never feeds `failedFixes`, so the hand-off to `/rota-debug` is not halted by it.

### rota debug work-fix attempt
rota debug work-fix attempt <ID> [--task <N>]
repo: scoped
data: {"bugId": string, "attempt": number, "changed": true}
exit: 3 when no `.rota/` is found or the issue does not resolve; 4 when the item already has 3 attempts since its last PASS or reset, with `{"blockedBy": "iron law", "bugId": string, "failedAttempts": number, "next": "/rota-debug <ID>", "changed": false}` and the hint `rota debug work-fix reset <ID>`
note: appends a `work-fix` FAIL record and returns the new count; the check and the write share the store lock. A refusal writes nothing.

### rota debug work-fix pass
rota debug work-fix pass <ID> [--task <N>]
repo: scoped
data: {"bugId": string, "changed": true}
note: appends a `work-fix` PASS record, which starts the count again at 0.

### rota debug work-fix reset
rota debug work-fix reset <ID> [--task <N>] --reason <text> --confirm --confirm-note <answer>
repo: scoped
data: {"bugId": string, "cleared": number, "changed": bool}
exit: as `debug reset`; the gate is the same `debug-reset` row.
note: appends a `work-fix-reset` record. An item with no attempts since its last PASS or reset needs no approval and resets nothing.

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
