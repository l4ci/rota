---
refs:
  - internal/verdict
---

## B2: verdicts

B2 (#55) replaces "parse the last line" routing in `/rota-review`, `/rota-qa`, `/rota-ship` and `/rota-debug` with typed verdicts: the skill records each verdict through a verb, which validates it, and the routing from verdict to next step is a tested function in code. B3 (#56) builds on the store: `ship pr` and `ship merge` refuse after a recorded FAIL, and the debug Iron Law counts failed fixes per item.

- **Verdict store.** `.rota/verdicts.json`, at the project root (the umbrella root in umbrella mode), gitignored per developer like `status.json` (`rota init` adds the line to its managed `.gitignore` block). Shape: `{"branches": {"<key>": [record, …]}, "items": {"<id>": [record, …]}}`. A branch key is the branch name, or `<repo>:<branch>` for a sub-repo branch (git forbids `:` in ref names, so keys never collide). An item key is the canonical item ID: the literal ID in file mode, the issue number in single-repo issue mode, or `<repo>:<number>` in umbrella issue mode. New records append to the list. Branch lists keep their latest 20; item lists also retain older failures since the last reset so consolidated legacy histories cannot lose failed attempts. The file follows the conventions' write rules (atomic, locked read-modify-write).
- **record.** `{"kind": string, "verdict": string, "combined"?: string, "branch"?: string, "repo"?: string, "sha": string, "recordedAt": string, "summary"?: string, "findings": [finding], "items"?: [{"id": string, "verdict": string}], "declined"?: [declined], "transcript"?: string}`. `sha` is the short tip of the branch (HEAD for debug) when the verdict was recorded; `recordedAt` is UTC ISO 8601. Item records carry no `branch` or `repo`. `combined` is stored on `review-quality` and `review-spec` records (see `verdict add`).
- **kinds and their verdicts.** `review-spec`, `review-quality` and `second-opinion` take `PASS`, `CONCERNS` or `FAIL`; `qa` adds `INFRA-FAIL`; `debug-fix` takes `PASS` or `FAIL`; `behaviour` takes `PASS` or `FAIL` and is recorded by `rota worker gate` from a run of the issue's `## Try it` steps, its `transcript` the path of that run's output. Worst-of order is `PASS` < `CONCERNS` < `FAIL` < `INFRA-FAIL`.
- **body.** `--body-file` is optional and holds a JSON object `{"verdict"?: string, "summary"?: string, "findings"?: [finding], "items"?: [{"id": string, "verdict": string}], "declined"?: [declined]}`. A `finding` is `{"severity": "blocker"|"major"|"minor"|"info", "title": string, "file"?: string, "line"?: number, "detail"?: string, "blocking"?: bool}`. `blocking` says whether a bounce relays and counts the finding: when omitted it defaults from `severity` (`blocker` and `major` true, `minor` and `info` false) and an explicit value wins; the stored finding always carries the resolved value. A finding is **deferred** when it does not block and its severity is not `info`; deferred findings are filed as items (`verdict add --file-deferred`), never relayed. A `declined` entry is what the reviewer could not judge from the diff: `{"title": string, "file"?: string, "line"?: number, "detail"?: string}`, validated like a finding minus `severity`. It is stored and shown, never counted: `verdict route` and the ship gates ignore it. Validation is strict: an unknown key, a wrong type, an empty `title`, a `line` below 1, a `severity` or `items[].verdict` outside its set, or text that is not one JSON object is exit 2, and the message names the field. `--verdict` is always required; a `verdict` in the body must equal it (exit 2 otherwise), so a reviewer's JSON block can be passed through whole.
- **effective review verdict** of a branch. The latest `review-quality` record's `combined`, unless a `review-spec` record is newer, in which case that record's `verdict` (a spec-only review, or a spec FAIL that skipped Stage 2). `verdict route --for ship-review` and B3's ship refusal read it.
- **stale.** A record is stale when its `sha` differs from the branch's current tip. Routing reports it and does not change `next` because of it.

### rota verdict add
rota verdict add [<branch>] --kind <review-spec|review-quality|second-opinion|qa|behaviour> --verdict <verdict> [--sha <sha>] [--body-file <path|->] [--transcript <path>] [--file-deferred --pr <ref>]
repo: scoped
data: record, plus {"next": string, "combined"?: string, "filed"?: []string, "appended"?: number, "filedWhy"?: string, "changed": true}
exit: 2 when `--file-deferred` is given without `--pr` or `--pr` without `--file-deferred`, `review.fileDeferred` holds a value other than `minor`, `major` or `off`, the backlog scope cannot be resolved (both before the record is written), when --kind or --verdict is missing, the verdict is not one the kind takes, the body is invalid (see body), `--transcript` is given with a kind other than `behaviour`, or at an umbrella root without --repo; 3 when the branch does not exist, `--sha` names no commit, HEAD is detached and no branch is named, or no base branch resolves; 4 when `--sha` is not the head of the remote branch or, without `--sha`, the local branch differs from it; 5 when git fails
old: none (new in B2)
note: (#1292) A leading `<remote>/` on `<branch>` is stripped, so `origin/feat/x` and `feat/x` share the key `feat/x`, unless a local branch of the full name exists. The head `verdict show` and `route` compare against is still the revision named.
note: (#1292) `--sha <sha>` records that commit (short form) and must equal the head of `<remote>/<branch>` (`origin` unless the argument named another remote); with no such remote-tracking ref it must equal the local branch tip. Otherwise exit 4, the message names the commits after it. Without `--sha`, when `origin/<branch>` exists and the local branch differs, exit 4 with the hint to pass `--sha`; with no remote-tracking ref the local tip is recorded as before. The check reads the remote-tracking ref, so fetch first.
note: `<branch>` defaults to the current branch. The verb works on the base branch too, since `/rota-qa` may run there.
note: `combined` is set on `review-quality` and `review-spec` records. On `review-quality` it is the worst of this verdict and the branch's previous record, when that record is a `review-spec` at the same `sha`; otherwise it equals `verdict` (a quality-only review). On `review-spec` it equals `verdict`, so a spec-only review (`light` depth) reports its own verdict as the branch's combined one.
note: (#963) `--file-deferred --pr <ref>` collects the deferred findings of the body in **one** `tasks` item per PR, titled "Review follow-ups from PR #N" with `--related <ref>`. Each finding is a `### <title>` block under `## Problem` (detail, `file:line`, the PR and verdict it came from) and one `- [ ] AC-n:` line under `## Acceptance` (the finding's `fix`, else a neutral "the problem above no longer reproduces, and a test or check that failed before the fix passes after it"; the title is never an acceptance line), so the item is ready for `round candidates`. A later call for the same PR appends to the open item instead of filing another; a finding with the same title and file is not added twice. `filed` names the item (empty when nothing was filed) and `appended` counts the new lines. The config key `review.fileDeferred` sets the floor: `minor` (default: minor and major), `major` (only major) or `off` (nothing is filed; `filedWhy` says so). The record is written first, so a retry adds only what is missing. The read-modify-write of the item runs under a per-PR lock (`.rota/followup-<N>.lock`), so verdicts recorded together for one PR still end in one item. A failure while filing or appending (an open follow-up with no `## Acceptance` section, a tracker error, a cannot-build-an-item error) is returned with the record and the item found so far in `data` (`filed`); the findings not yet written are left to a by-hand edit. `--pr 77` and `--pr '#77'` name the same PR, matched by number.
note: `next` is the producer's next step. `review-spec`: `quality` on PASS or CONCERNS, `report` on FAIL (Stage 2 is skipped). Every other kind: `report`.

### rota verdict show
rota verdict show [<branch>]
repo: scoped
data: {"branch": string, "repo"?: string, "head": string, "records": [record + {"stale": bool}]}
exit: 2 at an umbrella root without --repo; 3 when the branch does not exist, HEAD is detached and no branch is named, or no base branch resolves; 5 when git fails
old: none (new in B2)
note: `records` holds the latest record of each kind for the branch, in kind order (`review-spec`, `review-quality`, `second-opinion`, `qa`, `behaviour`). A branch with none gives `records: []` and exit 0. `head` is the branch's current short tip.

### rota verdict route
rota verdict route [<branch>] --for <ship-review|ship-second-opinion|ship-qa|queue>
repo: scoped
data: {"for": string, "kind": string, "verdict": string, "sha": string, "stale": bool, "advisory": bool, "next": string}
exit: 2 when --for is missing or unknown, or at an umbrella root without --repo; 3 when no verdict of the kind --for reads is recorded for the branch (hint: rota verdict add), the branch does not exist, HEAD is detached and no branch is named, or no base branch resolves; 5 when git fails
old: none (new in B2)
note: a classifier (rule 7): exit 0 whatever the verdict. `ship-review` and `queue` read the effective review verdict, `ship-second-opinion` the latest `second-opinion` record, `ship-qa` the latest `qa` record.
note: `next` is the consumer's next step, from the verdict, `autonomy.level`, `qa.gate` and `ship.secondOpinionRunner`. `ask` is the Address / Ship anyway / Stop question; `surface` shows the findings and continues; `stop` halts. For `queue`, `ask` is the merge question.

| --for | PASS | CONCERNS | FAIL | INFRA-FAIL (qa only) |
|---|---|---|---|---|
| `ship-review` | `continue` | `ask` | `stop` | |
| `ship-second-opinion` | `continue` | `ask` | `stop` | |
| `ship-second-opinion`, runner `codex` (advisory) | `continue` | `surface` | `surface` | |
| `ship-qa`, `qa.gate` `advisory` | `continue` | `surface` | `surface` | `surface` |
| `ship-qa`, `qa.gate` `blocking` | `continue` | `ask` | `stop` | `surface` |
| `queue` | `ask` | `request-changes` | `request-changes` | |

note: `INFRA-FAIL` surfaces under either gate, as `/rota-ship` already treated it: QA could not run, so the product was not judged and a dev server that happened to be down does not block a ship. `advisory` is true in the two advisory rows. A leftover `ship.secondOpinionRunner: "codex"` runs the subagent in advisory mode (maintainer ruling, #158); `qa.gate` defaults to `advisory`.

### rota debug verdict
rota debug verdict <bugId> --verdict <PASS|FAIL> [--body-file <path|->]
repo: scoped
data: {"bugId": string, "kind": "debug-fix", "verdict": string, "sha": string, "recordedAt": string, "summary"?: string, "findings": [finding], "failedFixes": number, "next": string, "attempt"?: number, "changed": true}
exit: 2 when `<bugId>` is empty, --verdict is not PASS or FAIL, or the body is invalid; 5 when not in a git repository
old: none (new in B2)
note: records whether a committed fix held, under the canonical item key returned as `bugId`. Issue references resolve through the item backend; invalid or unknown items exit 3, ambiguous umbrella references exit 2, and tracker errors propagate. `--repo` or the sub-repo cwd scopes bare references; qualified references identify their repository directly. `failedFixes` counts the item's `FAIL` records (B3 adds a human reset that starts the count again). `next` is `complete` on PASS, `hypothesize` on FAIL below 3 failed fixes and `halt` (the Iron Law) at 3 or more.
note: when the branch's `debug counter` session has a pending last attempt, the verb closes it as passed or failed, the same as `debug counter pass` or `fail`, and reports its number in `attempt`, so `debug counter summary` keeps working. No session file or no pending attempt leaves the counter alone.
