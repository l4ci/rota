## B2: verdicts

B2 (#55) replaces "parse the last line" routing in `/rota-review`, `/rota-qa`, `/rota-ship` and `/rota-debug` with typed verdicts: the skill records each verdict through a verb, which validates it, and the routing from verdict to next step is a tested function in code. B3 (#56) builds on the store: `ship pr` and `ship merge` refuse after a recorded FAIL, and the debug Iron Law counts failed fixes per item.

- **Verdict store.** `.rota/verdicts.json`, at the project root (the umbrella root in umbrella mode), gitignored per developer like `status.json` (`rota init` adds the line to its managed `.gitignore` block). Shape: `{"branches": {"<key>": [record, …]}, "items": {"<id>": [record, …]}}`. A branch key is the branch name, or `<repo>:<branch>` for a sub-repo branch (git forbids `:` in ref names, so keys never collide). An item key is the ID as given to the verb. Records are appended oldest first, and each list keeps its latest 20. The file follows the conventions' write rules (atomic, locked read-modify-write).
- **record.** `{"kind": string, "verdict": string, "combined"?: string, "branch"?: string, "repo"?: string, "sha": string, "recordedAt": string, "summary"?: string, "findings": [finding], "items"?: [{"id": string, "verdict": string}]}`. `sha` is the short tip of the branch (HEAD for debug) when the verdict was recorded; `recordedAt` is UTC ISO 8601. Item records carry no `branch` or `repo`. `combined` is stored on `review-quality` records only (see `verdict add`).
- **kinds and their verdicts.** `review-spec`, `review-quality` and `second-opinion` take `PASS`, `CONCERNS` or `FAIL`; `qa` adds `INFRA-FAIL`; `debug-fix` takes `PASS` or `FAIL`. Worst-of order is `PASS` < `CONCERNS` < `FAIL` < `INFRA-FAIL`.
- **body.** `--body-file` is optional and holds a JSON object `{"verdict"?: string, "summary"?: string, "findings"?: [finding], "items"?: [{"id": string, "verdict": string}]}`. A `finding` is `{"severity": "blocker"|"major"|"minor"|"info", "title": string, "file"?: string, "line"?: number, "detail"?: string}`. Validation is strict: an unknown key, a wrong type, an empty `title`, a `line` below 1, a `severity` or `items[].verdict` outside its set, or text that is not one JSON object is exit 2, and the message names the field. `--verdict` is always required; a `verdict` in the body must equal it (exit 2 otherwise), so a reviewer's JSON block can be passed through whole.
- **effective review verdict** of a branch. The latest `review-quality` record's `combined`, unless a `review-spec` record is newer, in which case that record's `verdict` (a spec-only review, or a spec FAIL that skipped Stage 2). `verdict route --for ship-review` and B3's ship refusal read it.
- **stale.** A record is stale when its `sha` differs from the branch's current tip. Routing reports it and does not change `next` because of it.

### rota verdict add
rota verdict add [<branch>] --kind <review-spec|review-quality|second-opinion|qa> --verdict <verdict> [--body-file <path|->]
repo: scoped
data: record, plus {"next": string, "combined"?: string, "changed": true}
exit: 2 when --kind or --verdict is missing, the verdict is not one the kind takes, the body is invalid (see body), or at an umbrella root without --repo; 3 when the branch does not exist, HEAD is detached and no branch is named, or no base branch resolves; 5 when git fails
old: none (new in B2)
note: `<branch>` defaults to the current branch. The verb works on the base branch too, since `/rota-qa` may run there.
note: `combined` is set on `review-quality` only: the worst of this verdict and the branch's previous record, when that record is a `review-spec` at the same `sha`; otherwise it equals `verdict` (a quality-only review).
note: `next` is the producer's next step. `review-spec`: `quality` on PASS or CONCERNS, `report` on FAIL (Stage 2 is skipped). Every other kind: `report`.

### rota verdict show
rota verdict show [<branch>]
repo: scoped
data: {"branch": string, "repo"?: string, "head": string, "records": [record + {"stale": bool}]}
exit: 2 at an umbrella root without --repo; 3 when the branch does not exist, HEAD is detached and no branch is named, or no base branch resolves; 5 when git fails
old: none (new in B2)
note: `records` holds the latest record of each kind for the branch, in kind order (`review-spec`, `review-quality`, `second-opinion`, `qa`). A branch with none gives `records: []` and exit 0. `head` is the branch's current short tip.

### rota verdict route
rota verdict route [<branch>] --for <ship-review|ship-second-opinion|ship-qa|queue>
repo: scoped
data: {"for": string, "kind": string, "verdict": string, "sha": string, "stale": bool, "advisory": bool, "next": string}
exit: 2 when --for is missing or unknown, or at an umbrella root without --repo; 3 when no verdict of the kind --for reads is recorded for the branch (hint: rota verdict add), the branch does not exist, HEAD is detached and no branch is named, or no base branch resolves; 5 when git fails
old: none (new in B2)
note: a classifier (rule 7): exit 0 whatever the verdict. `ship-review` and `queue` read the effective review verdict, `ship-second-opinion` the latest `second-opinion` record, `ship-qa` the latest `qa` record.
note: `next` is the consumer's next step, from the verdict, `autonomy.level`, `qa.gate` and `ship.secondOpinionRunner`. `ask` is the Address / Ship anyway / Stop question (off and auto); `address` sends the findings to `/rota-work` and reruns the caller (loop); `surface` shows the findings and continues; `stop` halts and stops a loop. For `queue`, `ask` is the merge question.

| --for | PASS | CONCERNS | FAIL | INFRA-FAIL (qa only) |
|---|---|---|---|---|
| `ship-review` | `continue` | `ask`, or `address` in loop | `stop` | |
| `ship-second-opinion` | `continue` | `ask`, or `address` in loop | `stop` | |
| `ship-second-opinion`, runner `codex` (advisory) | `continue` | `surface` | `surface` | |
| `ship-qa`, `qa.gate` `advisory` | `continue` | `surface` | `surface` | `surface` |
| `ship-qa`, `qa.gate` `blocking` | `continue` | `ask`, or `address` in loop | `stop` | `surface` |
| `queue` | `ask`, or `merge` in loop | `request-changes` | `request-changes` | |

note: `INFRA-FAIL` surfaces under either gate, as `/rota-ship` already treated it: QA could not run, so the product was not judged and a dev server that happened to be down does not block a ship. `advisory` is true in the two advisory rows. A leftover `ship.secondOpinionRunner: "codex"` runs the subagent in advisory mode (maintainer ruling, #158); `qa.gate` defaults to `advisory`.

### rota debug verdict
rota debug verdict <bugId> --verdict <PASS|FAIL> [--body-file <path|->]
repo: none
data: {"bugId": string, "kind": "debug-fix", "verdict": string, "sha": string, "recordedAt": string, "summary"?: string, "findings": [finding], "failedFixes": number, "next": string, "attempt"?: number, "changed": true}
exit: 2 when `<bugId>` is empty, --verdict is not PASS or FAIL, or the body is invalid; 5 when not in a git repository
old: none (new in B2)
note: records whether a committed fix held, under the item key `<bugId>`. `failedFixes` counts the item's `FAIL` records (B3 adds a human reset that starts the count again). `next` is `complete` on PASS, `hypothesize` on FAIL below 3 failed fixes and `halt` (the Iron Law) at 3 or more.
note: when the branch's `debug counter` session has a pending last attempt, the verb closes it as passed or failed, the same as `debug counter pass` or `fail`, and reports its number in `attempt`, so `debug counter summary` keeps working. No session file or no pending attempt leaves the counter alone.
