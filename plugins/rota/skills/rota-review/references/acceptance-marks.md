# Acceptance marks (issue backend)

The one owner of the rule for marking `AC-n` criteria before shipping or reporting done. Read by `/rota-work` Step 7, `/rota-ship` Step 4 and the round worker (`worker-contract.md`).

After recording PASS proof, read each held item's criteria (`rota item show <ID>` and `rota proof show <ID>`). For each met `AC-n`, run `rota plan pass <ID> AC-n --proof '<sha>:<check>'` (use `'#2150'`, for example, as the issue key), using that PASS row's sha and check. Repeat per resolved item, including every open bundle member, before closing, shipping or reporting done. Proof rows alone do not mark criteria; a changed criterion or withdrawn proof needs a new valid mark. Leave an unmet criterion unmarked and state its id and reason (in the PR when shipping).

A plain done or `--outcome done` refuses (exit 4) while an explicit criterion on a tracker item the PR closes is unmet. A non-closing `Refs` slice leaves the parent open and does not require its later criteria. Non-closing `Refs` slices and file-mode items keep their existing done flow.

A round worker who cannot meet a criterion reports `rota worker done <slot> --outcome partial --summary-file <f>`, with each unmet id and reason on the summary's third line (`failed` if nothing can be delivered). Never mark a criterion met to clear the refusal.

For an intentional close with unmet criteria, the merger uses `rota item complete <ID> --commit <sha> --unmet-reason <text>` to record the gaps and reason; the gate skips acceptance for the closed issue. Round workers never close items to clear the refusal. Items without explicit `AC-n` ids keep their existing flow and need no marks.
