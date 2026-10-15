# Issue backend

With `issues.source` set to `github`, `gitlab` or `jira` the tracker is the backlog. `.rota/BACKLOG.md` and the `.rota/<kind>/` detail files are not used; items, milestones and design/plan artifacts live on the tracker. `issues.source: file` is the default and is unchanged. Skills keep their file-mode steps and branch on the source; the issue-mode differences are listed in `references/issue-mode.md`.

## Issue source and forge

Two settings, two questions:

| Key | Question | Values |
|-----|----------|--------|
| `issues.source` | Where do the issues live? | `file` (default), `github`, `gitlab`, `forge`, `jira`, `linear` |
| `forge.provider` | Where does code land? | `auto` (default), `github`, `gitlab`, `local` |

`forge.provider: auto` reads the forge from the `origin` remote; with no remote at all it resolves to `local`. `local` means direct merge: no PR is opened and the merge gate never looks for one, and `rota ship strategy` (which `/rota-ship` and `/rota-work` read to choose between `rota ship merge` and `rota ship pr`) answers `direct` whatever `work.mergeStrategy` says. Issues on a tracker still need a PR to close them, so `issues.source: github` or `gitlab` with a local forge is an error (`rota doctor` fails it, `rota ship strategy` exits 3). The two are independent: a file backlog with PRs on a GitHub or GitLab forge works as before (`issues.source: file`, forge from `origin`), and a repo with no remote can keep a file backlog and merge directly.

`issues.source: github` or `gitlab` must name the forge the repo resolves to. When it does not (say `issues.source: gitlab` with a GitHub `origin`), `rota doctor` fails the `tracker` check with the hint to change `issues.source` or set `forge.provider`. Verbs that need a tracker with no resolvable provider exit 5 with `cannot determine the issue tracker provider (set issues.source to github or gitlab, or forge.provider)`. `issues.source: forge` is for an umbrella whose sub-repos sit on different hosts (one on GitHub, one on GitLab): each sub-repo's issues live on that sub-repo's own forge, resolved from its `origin` (or `forge.provider`). `rota migrate source` picks it for such an umbrella; a single repo never gets it. `rota doctor` does not compare it with the forge. `jira` and `linear` use a machine-wide connection instead; see [Tracker connections](configuration.md#tracker-connections).

Verbs that exist for one source only refuse the other with `not available when issues.source is a tracker` (file-only verbs) or `not available when issues.source is file` (tracker-only verbs).

Older configs used `backlog.backend` (`file` or `issues`) and `issues.provider`. They keep working until you run `rota migrate source` (preview) and `rota migrate source --apply`; `rota config fill` and `rota update` run the same migration. See [Configuration](configuration.md#issues-backend-keys).

## Setup

1. Install and authenticate the CLI for your host: `gh auth status` (GitHub) or `glab auth status` (GitLab). With no CLI or no auth, `rota` exits 5 and does not fall back to files.
2. Set the source: `rota config set issues.source github` (or `gitlab`).
3. Optional keys (all in `.rota/config.json`, full list in [Configuration](configuration.md#issues-backend-keys)):

| Key | Default | Use |
|-----|---------|-----|
| `forge.provider` | `"auto"` | `"auto"` detects from `origin` (no remote is `local`); or `"github"` / `"gitlab"` / `"local"`. Set it when detection is wrong. |
| `issues.labels.*` | `in-progress`, `needs-review`, `changes-requested`, `released`, `not-planned`, `blocked`, `milestone-tracker`, `type:bug` / `type:feature` / `type:task`, prefix `p`, prefix `size:` | Rename any label role. |
| `issues.autoCreateLabel` | `true` | Create a missing label on first use. When off, a missing label is an error. |
| `issues.homeRepo` | `""` | Umbrella only: sub-repo holding milestone tracking issues; empty is the first registered sub-repo. |
| `issues.bulkPaceMs` | `1000` | Pause between writes in `rota migrate issues`. |
| `issues.retryWaitSeconds` | `60` | Seconds to wait before the one retry after a primary rate limit. |

## Jira

`issues.source: jira` keeps the issues in one Jira project and the code on your forge (GitHub or GitLab). rota talks to the Jira REST API directly, so it needs no CLI: Cloud through REST v3, Data Center through REST v2.

1. Create a token. On Jira Cloud, make an API token at <https://id.atlassian.com/manage-profile/security/api-tokens>; rota sends it with your account email. On Data Center, create a personal access token under your profile.
2. Add a connection. It lives on your machine, never in the repo (see [Tracker connections](configuration.md#tracker-connections)):

   ```sh
   # Cloud: --user is the account email the API token belongs to
   echo "$JIRA_TOKEN" | rota connect add work --kind jira --site https://acme.atlassian.net --user me@acme.com --token-stdin
   # Data Center: no --user; the token is a personal access token
   echo "$JIRA_PAT" | rota connect add work --kind jira --site https://jira.acme.com --token-stdin
   rota connect test work
   ```

3. Point the project at it: `rota config set issues.source jira`, `rota config set issues.connection work`, `rota config set issues.jira.project PROJ`.

Organization admins on Jira Cloud can block API tokens for managed accounts. When they do, `rota connect test` fails with HTTP 401 or 403 even though the token is correct; ask an admin to allow API tokens for your account. OAuth is not supported.

How rota uses Jira:

- **Keys.** Items are named by their key (`PROJ-123`), and so are branches (`ben/PROJ-123-...`). A `## Depends on` line counts keys of the project's own prefix; other key-shaped words (`UTF-8`, `ISO-8601`) are prose.
- **Hidden state.** rota keeps its state in issue properties, never in text people read: a claim is the property `rota.claim.<claim id>`, a marker comment (proof, design and plan notes, feedback, handoff) is posted without its markers and kept whole in `rota.comment.<comment id>`, and the fields block of an issue body is `rota.fields`.
- **Claims.** Each claimer writes its own property with the time of the claim; the earliest one wins, and a release deletes only its own property. The time comes from the claimer's clock, since Jira records none on a property.
- **Status.** Close and reopen pick the workflow transition by status category (to do, in progress, done), never by status name, so translated or renamed statuses work. A workflow with no transition into the wanted category fails with a hint. A not-planned close also adds the `not-planned` label.
- **Labels and milestones.** Labels are Jira labels, created on first use; a label with a space is refused. Milestones are project versions (`fixVersions`); closing a milestone releases its version.
- **Closing PRs.** A PR counts as closing a Jira issue when its head branch or title names the key; `rota worker gate` refuses one that names neither. Merging does not move the issue to done the way `Closes #N` does on GitHub: let Jira's GitHub or GitLab integration transition it on merge, or run `rota issues close PROJ-123 --commit <sha>`.
- **Rate limits.** A 429 waits its `Retry-After` (up to two minutes) and retries, at most three times. A 5xx is retried only for reads and other idempotent calls, never for a create or a comment.

Out of scope: Jira Service Management, boards and sprints.

## How items map to issues

An ID is the type letter plus the issue number: `#42` is `F42`, `B42` or `T42`. The letter must match the issue's type label.

- **Labels** carry type (`type:bug`), priority (`p0`..) and, for features, size (`size:Major`).
- **Body** holds the description and a fields block (`Related`, `Milestone`, `Repos`, ...).
- **Notes** are marker comments on the issue, edited in place: `proof`, `design`, `plan`, `acceptance`. Read them with `rota item note show <ID> --kind design`, `rota design show`, `rota plan show`.
- **Comments** record `question`, `answer`, `decision` and `feedback`. Decisions are binding for later sessions.

### Claim lock and state labels

`rota item claim <ref> --as <claim-id>` takes an item: claim comment, `in-progress` label, assignee. The earliest unreleased claim wins; a loser gets exit 4 and picks another item. State labels are one of `in-progress`, `needs-review`, `changes-requested` at a time, cleared on close.

### Review and close

`/rota-work`, `/rota-debug` and `/rota-ship` always open a PR / MR in issue mode and never merge. `rota ship pr --items <IDs>` adds `Closes #<n>` lines. `/rota-review --queue` lists `needs-review` items with their PRs (`rota review queue`), reviews them and merges with `rota ship pr-merge`. Proof comes first: an open linked item with no proof blocks the merge, becomes `changes-requested` and gets a feedback comment (exit 4). Proof rows are added with `rota proof record` (it runs a command and writes the row) or, for a docs-only change with no command, `rota proof add`, into the item's proof note.

In a [round](parallel-rounds.md), `rota worker gate` also wants the PR body to carry `Closes #N` (or `Fixes #N`) for the slot's issue. Without it the merge would leave the issue open and claimed, so the gate refuses (exit 4, `blockedBy: closes`). A PR that lands only part of an issue says `Refs #N` and the issue carries the `partial-slice` label, which exempts it.

`rota item complete <ID> --reason handed-off|blocked|dropped` closes without a merge: `dropped` and `handed-off` close as not planned; `blocked` keeps the issue open with the `blocked` label.

## Milestones and release

A milestone is a native tracker milestone `MNN — <title>` plus a tracking issue labelled `milestone-tracker` and `status:<status>`. The issue body is the milestone plan; slice plans are `plan:SNN` notes on it. `/rota-vision` and `/rota-plan` write them through the same helpers as file mode.

`/rota-release --milestone MNN` gates on `rota release milestone-check` (exit 1 when blocked), drafts notes with `rota release notes`, and after the tag runs `rota release close-milestone`.

## Umbrella mode

Each sub-repo's items stay on that sub-repo's own tracker; the provider is detected per origin, so GitHub and GitLab can mix. Reads merge into one backlog with qualified IDs `<repo>:<ID>`. `<repo>#<n>` always resolves; a bare `F42` resolves only when exactly one sub-repo has it. Capture needs a target repo (`--repos <name>` or cwd in a sub-repo); multi-repo items are refused, so capture one per repo and link with `Related:`. Milestone tracking issues live in `issues.homeRepo`. Release helpers take `--repo <name>` (required). See [Umbrella mode](umbrella-mode.md).

## Rate limits

Exit 5 means the tracker is unavailable; exit 6 means rate-limited. Both stop the run and report. On a primary rate limit `rota` waits `issues.retryWaitSeconds` and retries the call once, then stops with exit 6. A secondary rate limit stops at once, and other failures are not retried. There is no retry loop; wait, then re-run.

## Migrating a file backlog

```
rota migrate issues            # dry run: planned operations and would-be map
rota migrate issues --apply    # create everything
rota config set issues.source github   # or gitlab
```

Open items, their detail files, proof rows, design and plan artifacts, and planned/active milestones with their slice plans move to the tracker. Completed items, `ARCHIVE.md` and shipped/archived milestones stay in the files as history, so after the flip `rota milestone list` shows only the planned and active milestones. `Related:` fields and old IDs in migrated text are rewritten to the new IDs after every item exists; `Related:` IDs that are not migrated (completed items) are dropped from the field and listed in the issue body as `Related before migration (not migrated): F79, F80`. A bullet's `Since:` anchor is kept in the issue's fields block.

The run is resumable: `.rota/issue-map.json` records each old ID, its new ID and URL. Commit it. A rate limit (exit 6) stops the run with the map saved; wait and re-run. `--limit N` creates at most N items per run. Writes are paced by `issues.bulkPaceMs`.

`--apply` never changes `issues.source`. When everything is migrated it adds a "Frozen" banner to `.rota/BACKLOG.md` and prints `Next: rota config set issues.source github` (or `gitlab`). Old IDs are not resolved through the map after the flip; an old `F82` and a new issue `F82` can both exist. Umbrella projects migrate each sub-repo separately.
