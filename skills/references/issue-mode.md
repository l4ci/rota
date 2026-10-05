# Issue backend

The issue backend (`backlog.backend` is `"issues"` in `.rota/config.json`; check with `rota config show backlog.backend`) is the primary backlog: the tracker (GitHub or GitLab) holds the items, and every verb already branches on the backend. `.rota/BACKLOG.md` and `.rota/<kind>/` detail files belong to the legacy file backend, where skills keep their file-backend steps. Item IDs read `#N` (or a bare `N`) here and `[B07]` in file mode; this page covers the issue-backend mechanics, and notes where the file backend differs.

## IDs

An item ID is `#42` or a bare `42`; the legacy `F42` / `B42` / `T42` forms (type letter plus issue number) are also accepted, and the letter must match the issue's type label. In `--json` output `id` is the bare number (`"42"`) and `type` carries the letter.

## Verb map

| Need | Verb |
|------|------|
| Capture an item | `rota item create --kind <bugs\|features\|tasks> --title T [--tag TAG] [--desc D] [--body-file F] [--related R] [--milestone M] [--repos NAME] [--subsystem S]` |
| Take an item (lock) | `rota item claim <ref> --as <claim-id>`; the claim-id is the work branch name |
| Give it back | `rota item release <ref> --as <claim-id>` |
| Specified well enough? | `rota item ready <ref>` (`data.reasons` lists why not; exit 1 when not ready) |
| Workflow label | `rota item state <ref> --to in-progress\|needs-review\|changes-requested\|none` |
| Proof rows | `rota proof add <ID> --check <name> --result PASS\|FAIL --evidence <text> [--sha <commit>]` (stored in the item's proof note) |
| Design / plan artifact | `rota design add` / `rota plan add` create it, `rota design put <ID> --body-file F\|-` / `rota plan put <key> --body-file F\|-` fill it, `rota design show` / `rota plan show` read it; raw access: `rota item note add\|show\|rm <ref> --kind proof\|design\|plan`. Slice plans stay files. |
| Question, answer, decision, feedback | `rota item comment add <ref> --kind question\|answer\|decision\|feedback --body-file F\|-`; read back with `rota item comment list <ref> [--kind K]` |
| Status of one item | `rota item show <ref>` (state label, claim, assignee, milestone, notes present, comment rows; read-only; fails with `blockedBy: backend` in file mode) |
| Open the PR / MR | `rota ship pr <branch> --title T --body-file F\|- --items <ID[,ID...]>` |
| What needs review | `rota review queue` (`data.items`: `needs-review` items with the open PRs / MRs whose body closes them) |
| Merge a reviewed PR / MR | `rota ship pr-merge <pr> [--items <ID[,ID...]>]` (checks proof first, then merges and closes what the host left open; exit 4 = an item unproven) |
| Close | `rota item complete <ID> [--commit <hash>] [--reason done\|handed-off\|blocked\|dropped] [--note <text>]`; reopen with `rota item reopen` |

**Post every `AskUserQuestion` answer that changes an item's direction** as a `decision` (or `answer`) comment with `rota item comment add`, so later sessions, which share no memory with this one, see why the item took its shape.

## State labels

One of `in-progress`, `needs-review`, `changes-requested` at a time, cleared on close.

- `rota item claim` sets `in-progress` (and assigns the user).
- `rota item state <ref> --to needs-review` after the PR / MR is open.
- A reviewer sets `changes-requested` (`/rota-review --queue`, or `rota ship pr-merge` for an unproven item); the next `/rota-work` claim returns it to `in-progress`.

## PR flow

`/rota-work`, `/rota-debug` and `/rota-ship` in issue mode always open a PR / MR, whatever `work.mergeStrategy` says (it is treated as `pr`). `rota ship pr --items <IDs>` appends one `Closes #<n>` line per item, so the tracker closes the issues when the PR merges. The claim stays until then: do not call `rota item release` after opening the PR.

**Merging belongs to `/rota-review --queue`.** It lists the queue with `rota review queue`, reviews each PR / MR, and merges PASSes with `rota ship pr-merge <pr>`. It checks proof before merging: an open linked item with no proof blocks the merge (the item becomes `changes-requested` with a feedback comment, exit 4), because a merge into the default branch lets the host close the issue and skip the gate. After a merge, the host closes the linked issues itself when the PR targets the default branch; for any other base `rota ship pr-merge` closes them with `rota item complete` semantics (reason done, merge sha). `/rota-work` and `/rota-ship` never merge in issue mode and never call `rota item complete` for a `done` close: the merge closes the issue.

`rota item complete` is still how to close an item with `--reason handed-off|blocked|dropped`. Reasons: `done` closes as completed; `dropped` and `handed-off` close as not planned (comment carries reason and note); `blocked` keeps the issue open with the `blocked` label.

## Milestones and release

A milestone is a native tracker milestone `MNN — <title>` plus a tracking issue labelled `milestone-tracker` and `status:<status>`; its body is the milestone plan. Slice plans are `plan:SNN` notes on that issue. `/rota-vision` writes them via `rota milestone add` / `rota milestone put` / `rota milestone status`, `/rota-plan` via `rota plan add` / `rota plan put`. `/rota-release --milestone MNN` gates on `rota release milestone-check`, drafts notes with `rota release notes --from issues MNN`, and after the tag closes out with `rota release close-milestone`. Exit codes: `3` unknown milestone, `5` tracker unavailable, `6` rate-limited; file mode fails with `blockedBy: backend`, and `rota release milestone-check` exits `1` when blocked.

## Resuming an item

A fresh session has only the tracker. Load an item's context before working it:

1. `rota item show <ID>`: state label, claim holder, assignee, milestone, which notes exist (design / plan / proof) and every question / answer / decision / feedback comment. Read-only.
2. `rota item field list <ID>`: the issue body and fields.
3. `rota item note show <ID> --kind design` and `--kind plan`: the design and plan notes (`exists: false` when absent).
4. `rota item comment list <ID> [--kind K]`: just the comment rows, when only those are needed.

Treat `decision` comments as binding and `feedback` comments (from review) as the to-do list for a `changes-requested` item. The rules for working them are *Handling review feedback* in `worker-contract.md` (see also; `/rota-work` cites it directly).

## Umbrella

With `.rota/repos.json` registering sub-repos (`references/umbrella-mode.md`), the umbrella backend keeps each sub-repo's items on that sub-repo's own tracker. The provider is auto-detected from each origin, so GitHub and GitLab can mix.

- **Reads merge** into one backlog; bullets carry `Repos: <name>` and `rota backlog list` shows qualified IDs `<repo>:<ID>`. Known limitation: its Clusters section keys on plain IDs.
- **Refs.** `<repo>#<n>` and `<repo>:<ID>` always resolve. A bare `F42` / `#42` resolves only when exactly one sub-repo has it; otherwise exit 2 lists the candidates. `rota item create` returns qualified IDs.
- **Capture** needs a target repo (`--repos <name>` or cwd inside a sub-repo). Multi-repo items are refused: capture one per repo and link with `Related:`. `Repos` is immutable on an existing item.
- **Milestones.** The tracking issue and slice plans live in the home repo (`issues.homeRepo`, default the first registered sub-repo). A sub-repo's native milestone `MNN — <title>` is created when an item there is first assigned to MNN. Milestone reads report `shipped` only once every sub-repo's native milestone MNN is closed.
- **Release runs per sub-repo.** `rota release milestone-check`, `rota release notes --from issues` and `rota release close-milestone` take the global `--repo <name>`, required here (exit 2 without).
- **Review.** `rota review queue` spans all sub-repos (entries carry `repo`, IDs qualified). `rota ship pr --repo <name> --items ...` falls back to the cwd's sub-repo; `rota ship pr-merge --repo <name>` is required.

## Exit codes

Shared by the `rota item`, `rota ship pr` and `rota tracker call` verbs:

- `5` tracker unavailable (CLI missing, not authenticated, provider unknown): stop and report; do not fall back to files.
- `6` rate-limited: stop and report; never retry in a loop.
- `4` refused. `rota item claim`: another worker holds the item (claim lost): drop that item and pick another. `rota ship pr-merge`: a linked item has no proof and moves to `changes-requested`: report it; or, with `data.blockedBy: "verdict"`, the PR's branch carries a recorded FAIL and nothing changed: report it and skip the PR; or, with `data.blockedBy: "manual gate"`, `ship.mergeApproval` requires a human: ask, then re-run with `--confirm --confirm-note "<answer>"`.
