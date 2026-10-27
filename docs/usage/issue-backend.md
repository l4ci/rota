# Issue backend

With `issues.source` set to `github`, `gitlab`, `jira`, `linear` or `itsaplan` the tracker is the backlog. `.rota/BACKLOG.md` and the `.rota/<kind>/` detail files are not used; items, milestones and design/plan artifacts live on the tracker. `issues.source: file` is the default and is unchanged. Skills keep their file-mode steps and branch on the source; the issue-mode differences are listed in `references/issue-mode.md`.

## Issue source and forge

Two settings, two questions:

| Key | Question | Values |
|-----|----------|--------|
| `issues.source` | Where do the issues live? | `file` (default), `github`, `gitlab`, `forge`, `jira`, `linear`, `itsaplan` |
| `forge.provider` | Where does code land? | `auto` (default), `github`, `gitlab`, `local` |

`forge.provider: auto` reads the forge from the `origin` remote; with no remote at all it resolves to `local`. `local` means direct merge: no PR is opened and the merge gate never looks for one, and `rota ship strategy` (which `/rota-ship` and `/rota-work` read to choose between `rota ship merge` and `rota ship pr`) answers `direct` whatever `work.mergeStrategy` says. Issues on a tracker still need a PR to close them, so `issues.source: github` or `gitlab` with a local forge is an error (`rota doctor` fails it, `rota ship strategy` exits 3). The two are independent: a file backlog with PRs on a GitHub or GitLab forge works as before (`issues.source: file`, forge from `origin`), and a repo with no remote can keep a file backlog and merge directly.

`issues.source: github` or `gitlab` must name the forge the repo resolves to. When it does not (say `issues.source: gitlab` with a GitHub `origin`), `rota doctor` fails the `tracker` check with the hint to change `issues.source` or set `forge.provider`. Verbs that need a tracker with no resolvable provider exit 5 with `cannot determine the issue tracker provider (set issues.source to github or gitlab, or forge.provider)`. `issues.source: forge` is for an umbrella whose sub-repos sit on different hosts (one on GitHub, one on GitLab): each sub-repo's issues live on that sub-repo's own forge, resolved from its `origin` (or `forge.provider`). `rota migrate source` picks it for such an umbrella; a single repo never gets it. `rota doctor` does not compare it with the forge. `jira`, `linear` and `itsaplan` use a machine-wide connection instead; see [Tracker connections](configuration.md#tracker-connections).

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

### Label registry and sync

The [label registry](../reference/labels.md) lists every label rota sets or reads, including
claim prefixes and review triggers. Configured names under `issues.labels.*` are respected.
Declare project labels with descriptions in `issues.labels.extra`:

```sh
rota config set issues.labels.extra '{"area:api":"API ownership"}'
rota labels sync             # preview missing labels, metadata drift and unknown labels
rota labels sync --apply     # create missing labels and correct descriptions and colors
```

Sync writes only with `--apply`. GitHub, GitLab and Linear support metadata changes; Jira and
It's a Plan report names only and stay read-only. Tracker defaults such as `bug`, unused
`claimed:*` definitions and unknown or retired labels are reported without changes. A claim
definition is unused when no open issue carries it. Sync never deletes or renames labels.

`rota doctor` warns about missing or undescribed registry labels and points to this preview.
An unreachable or name-only tracker (Jira, It's a Plan) skips the definition check with a reason; missing names on these report-only trackers do not warn. The file backend has no label check.

## Jira

`issues.source: jira` keeps the issues in one Jira project and the code on your forge (GitHub or GitLab). rota talks to the Jira REST API directly, so it needs no CLI: Cloud through REST v3, Data Center through REST v2.

1. Create a token. On Jira Cloud, make an API token at <https://id.atlassian.com/manage-profile/security/api-tokens>; rota sends it with your account email. On Data Center, create a personal access token under your profile.
2. Add a connection. It lives on your machine, never in the repo (see [Tracker connections](configuration.md#tracker-connections)). `--deployment` says Cloud or Data Center; an `*.atlassian.net` site defaults to `cloud`, and any other site must pass it:

   ```sh
   # Cloud: --user is the account email the API token belongs to
   echo "$JIRA_TOKEN" | rota connect add work --kind jira --site https://acme.atlassian.net --user me@acme.com --token-stdin
   # Data Center: the token is a personal access token
   echo "$JIRA_PAT" | rota connect add work --kind jira --site https://jira.acme.com --deployment datacenter --token-stdin
   rota connect test work
   ```

3. Point the project at it: `rota config set issues.source jira`, `rota config set issues.connection work`, `rota config set issues.jira.project PROJ`.

Organization admins on Jira Cloud can block API tokens for managed accounts. When they do, `rota connect test` fails with HTTP 401 or 403 even though the token is correct; ask an admin to allow API tokens for your account. OAuth is not supported.

How rota uses Jira:

- **Keys.** Items are named by their key (`PROJ-123`), and so are branches (`ben/PROJ-123-...`). A `## Depends on` line counts keys of the project's own prefix; other key-shaped words (`UTF-8`, `ISO-8601`) are prose.
- **Hidden state.** rota keeps its markers in issue properties, never in text people read: a marker comment (proof, design and plan notes, feedback, handoff) is posted without its markers and kept whole in `rota.comment.<comment id>`, and the fields block of an issue body is `rota.fields`.
- **Claims.** A claim is a plain comment, `Claimed by <claim id>`. The earliest one wins by the time Jira recorded it (then by comment id), never by the claimer's clock, and a release deletes that claimer's comment.
- **Status.** Close and reopen pick the workflow transition by status category (to do, in progress, done), never by status name, so translated or renamed statuses work. A workflow with no transition into the wanted category fails with a hint. A not-planned close also adds the `not-planned` label.
- **Labels and milestones.** Labels are Jira labels, created on first use; a label with a space is refused. Milestones are project versions (`fixVersions`); closing a milestone releases its version.
- **Closing PRs.** A PR counts as closing a Jira issue when its head branch or title names the key; `rota worker gate` refuses one that names neither. Jira closes nothing on a merge by itself, so `rota worker gate` moves the issue to a done status after the merge and comments with the PR, unless the issue is labelled `partial-slice`. A PR merged any other way (`rota ship pr-merge`, the forge UI) leaves the issue open: run `rota issues close PROJ-123 --commit <sha>`, or let Jira's GitHub or GitLab integration transition it.
- **Rate limits.** A 429 waits its `Retry-After` (up to two minutes) and retries, at most three times. A 5xx is retried only for reads and other idempotent calls, never for a create or a comment.

Out of scope: Jira Service Management, boards and sprints.

## Linear

`issues.source: linear` keeps the issues in one Linear team and the code on your forge (GitHub or GitLab). rota talks to Linear's GraphQL API directly; Linear ships no official CLI.

1. Create a personal API key under Settings → Account → Security & access in Linear.
2. Add a connection. It lives on your machine, never in the repo (see [Tracker connections](configuration.md#tracker-connections)). It needs no `--site`; rota talks to `https://api.linear.app`:

   ```sh
   echo "$LINEAR_API_KEY" | rota connect add linear --kind linear --token-stdin
   rota connect test linear
   ```

3. Point the project at it: `rota config set issues.source linear`, `rota config set issues.connection linear`, `rota config set issues.linear.team ENG` (the team's key, the prefix of its identifiers). `rota doctor` checks the connection and that the key can see the team.

How rota uses Linear:

- **Keys.** Items are named by their identifier (`ENG-42`), and so are branches (`ben/ENG-42-...`). A `## Depends on` line counts identifiers of the team's own key, as on Jira.
- **Hidden state.** Linear keeps no hidden text in Markdown and has no issue properties, so rota keeps its state in issue attachments titled `rota state`, with the body in the attachment's metadata (`{"v": 1, "body": ...}`; a missing `v` reads as 1). A marker comment (proof, design and plan notes, feedback, handoff) is posted without its markers and kept whole in the attachment at `https://rota.invalid/state/comment/<comment id>`; the fields block of an issue body is at `https://rota.invalid/state/fields`. Every marker comment therefore adds one visible `rota state` attachment to the issue. Do not remove them. Deleting such a comment in Linear's UI leaves its attachment behind (rota's own deletes remove both); the leftover is harmless and can be removed by hand. Linear documents no size limit on attachment metadata, so rota caps each at 64 KiB and refuses the write, before changing anything, above it.
- **Claims.** A claim is a plain comment, `Claimed by <claim id>`. The earliest one wins by the time Linear recorded it (then by comment id), and a release deletes that claimer's comment.
- **Status.** Close and reopen pick the team's workflow state by type, never by name: close moves the issue to a `completed` state, a not-planned close to a `canceled` one, and reopen to an `unstarted` state (else a `started` one).
- **Labels.** Labels are workspace labels or the team's own, matched without case; a missing one is created on the team. Another team's label is never used.
- **Milestones.** None: Linear projects and cycles are not rota milestones, and the milestone verbs fail on a Linear source.
- **Closing PRs.** A PR counts as closing a Linear issue when its head branch or title names the identifier. rota does not rely on Linear's GitHub or GitLab integration: `rota worker gate` moves the issue to a completed state after the merge and comments with the PR, unless the issue is labelled `partial-slice`. A PR merged any other way leaves the issue open (unless the integration closes it): run `rota issues close ENG-42 --commit <sha>`.
- **Rate limits and errors.** Linear answers some failures with HTTP 200 and a GraphQL `errors` list; rota reads those like HTTP errors (a rate limit is exit 6, a bad key or an unknown team is exit 5). Reads are retried on a 5xx, writes never are.

Out of scope: Linear projects and cycles as milestones, and OAuth (personal API keys only).

## It's a Plan

`issues.source: itsaplan` keeps the issues in one project of a self-hosted [It's a Plan](https://github.com/croffasia/itsaplan) instance and the code on your forge (GitHub or GitLab). rota polls the instance's REST API; it does not use It's a Plan's agent runner, MCP server or webhooks. The upstream project warns of breaking changes before its first stable release and rota pins no API version: `rota doctor` checks the endpoints rota uses, and the instance's own version is for information only.

1. Create a personal API key under Account settings → API keys in It's a Plan. The key carries its owner's permissions and **expires**: a new key is valid for a limited time, and a key that has expired is rejected like a wrong one (`rota doctor` and `rota connect test` fail with `did not accept the API key`). Replace it with `rota connect add <name> --replace`.
2. Add a connection. It lives on your machine, never in the repo (see [Tracker connections](configuration.md#tracker-connections)). `--site` is required: the URL of the instance's **api**, not of the web app. There is no hosted default:

   ```sh
   echo "$ITSAPLAN_API_KEY" | rota connect add plan --kind itsaplan --site https://api.plan.example.com --token-stdin
   rota connect test plan
   ```

3. Point the project at it: `rota config set issues.source itsaplan`, `rota config set issues.connection plan`, `rota config set issues.itsaplan.project MKT` (the project's key, the prefix of its issue keys). `rota doctor` checks the connection, that the key can see the project, and the `rota` custom field.

How rota uses It's a Plan:

- **Keys.** An issue is named by its key, `MKT-42`.
- **States.** rota picks a column by its state type (backlog, unstarted, started, completed, canceled), never by its name, so a renamed or added column changes nothing. Open means backlog, unstarted or started; closed means completed or canceled. Closing moves the issue to the first completed column (a canceled one for not-planned), reopening to the first unstarted column. In progress is the `issues.labels.inProgress` label plus self-assignment.
- **Hidden state.** rota's fields block (`Related`, `Milestone`, ...) lives in a project custom field named `rota` (type text), not in the description: a person editing the description in the web editor may drop an HTML comment, and a custom field is left alone. rota creates the field the first time it needs it; where the key may not create custom fields, `rota doctor` fails and names the field, and a project admin adds it. Do not rename or delete it. A marker comment (proof, notes, feedback, handoff) is stored as written; the API keeps an HTML comment in a comment body.
- **Claims.** A claim is a plain comment, `Claimed by <claim id>`, read from the issue's comment feed (every page). The earliest one wins, and a release deletes that claimer's comment.
- **Labels.** Labels are project labels, created on first use. Adding labels uses the API's bulk route, which only adds, so two writers adding at once keep both. Removing a label has to send the issue's whole label set (the API offers no compare-and-swap): rota reads the issue, writes the reduced set, re-reads, and writes once more if another writer overwrote it. A label another writer adds in the moment between rota's read and its write can still be lost.
- **Reading.** `backlog list` reads the project board and the archive, which have no limit, so a project with more than 500 issues is read whole. The board includes descriptions and is read once per command.
- **Milestones.** None: It's a Plan initiatives are not rota milestones, and the milestone verbs fail on this source. `rota migrate issues` reports each milestone it skips.
- **Closing PRs.** A PR counts as closing an issue when its head branch or title names the key. rota does not rely on It's a Plan's own forge webhook, which is optional: `rota worker gate` moves the issue to a completed column after the merge and comments with the PR, unless the issue is labelled `partial-slice` or is already closed. A PR merged any other way leaves the issue open (unless the webhook closes it): run `rota issues close MKT-42 --commit <sha>`.
- **Rate limits.** A 429 waits its `Retry-After` and retries; a 5xx is retried for reads and other idempotent calls only, never for creating an issue or a comment.

To move an existing file backlog onto It's a Plan, set `issues.source`, the connection and the project first, then run `rota migrate issues --apply` (see [Migrating a file backlog](#migrating-a-file-backlog)): It's a Plan is no forge, so the run cannot find the project from the `origin` remote. Going back is `rota config set issues.source file`; issues already created stay in It's a Plan and the files keep the history.

Out of scope: It's a Plan's agent runner, MCP server and webhooks, initiatives as milestones, and pinning an API version.

## How items map to issues

An ID is the type letter plus the issue number: `#42` is `F42`, `B42` or `T42`. The letter must match the issue's type label.

- **Labels** carry type (`type:bug`), priority (`p0`..) and, for features, size (`size:Major`).
- **Body** holds the description and a fields block (`Related`, `Repos`, `Subsystem`, ...). The milestone is the native tracker milestone, not a body field; a `Milestone` line in the block is only a read fallback. `item create --milestone` and `item field set --name milestone` refuse an unknown milestone ID with exit 3 (not found); `rota milestone list` shows the known ones.
- **Notes** are marker comments on the issue, edited in place: `proof`, `design`, `plan`, `acceptance`. Read them with `rota item note show <ID> --kind design`, `rota design show`, `rota plan show`.
- **Comments** record `question`, `answer`, `decision` and `feedback`. Decisions are binding for later sessions.

### Claim lock and state labels

`rota item claim <ref> --as <claim-id>` takes an item: claim comment, `in-progress` label, assignee. The earliest unreleased claim wins; a loser gets exit 4 and picks another item. State labels are one of `in-progress`, `needs-review`, `changes-requested` at a time, cleared on close.

### Review and close

`/rota-work`, `/rota-debug` and `/rota-ship` always open a PR / MR in issue mode and never merge. `rota ship pr --items <IDs>` adds `Closes #<n>` lines. `/rota-review --queue` lists `needs-review` items with their PRs (`rota review queue`), reviews them and merges with `rota ship pr-merge`. Proof comes first: an open linked item with no proof blocks the merge, becomes `changes-requested` and gets a feedback comment (exit 4). Proof rows are added with `rota proof record` (it runs a command and writes the row) or, for a docs-only change with no command, `rota proof add`, into the item's proof note.

Overlapping `rota proof record` runs on the same item preserve both rows. Each append reads the current proof note under its lock, including rows recorded while its command was running. File mode preserves concurrent rows in the item's proof sidecar too.

In a [round](parallel-rounds.md), `rota worker gate` also wants the PR body to carry `Closes #N` (or `Fixes #N`) for the slot's issue. Without it the merge would leave the issue open and claimed, so the gate refuses (exit 4, `blockedBy: closes`). A PR that lands only part of an issue says `Refs #N` and the issue carries the `partial-slice` label, which exempts it.

A `done` close refuses while any explicit `AC-n` criterion lacks a current `plan pass` mark backed by PASS proof. The refusal lists the unmet criteria. To close with known gaps, pass `--unmet-reason "<why>"`; the close comment records both the gaps and your reason. `--no-proof` only bypasses the proof-row requirement. The merge gate checks acceptance before merging a PR that closes an item, including a merge into a milestone branch.

`rota item complete <ID> --reason handed-off|blocked|dropped` closes without a merge: `dropped` and `handed-off` close as not planned; `blocked` keeps the issue open with the `blocked` label.

## Milestones and release

A milestone is a native tracker milestone `MNN — <title>` plus a tracking issue labelled `milestone-tracker` and `status:<status>`. The issue body is the milestone plan; slice plans are `plan:SNN` notes on it. Below the plan sits a generated, linked list of the issues in the native milestone (`- #12 title`, open and closed); `rota item create --milestone`, `rota item set milestone` and `rota milestone check` refresh it, and `rota milestone put` keeps it. `/rota-vision` and `/rota-plan` write them through the same helpers as file mode.

`/rota-release --milestone MNN` gates on `rota release milestone-check` (exit 1 when blocked), drafts notes with `rota release notes`, and after the tag runs `rota release close-milestone`. The gate's output ends with one informational `check:` line from `rota milestone check` (structural faults and uncovered acceptance criteria, or `clean`); it never changes the exit code.

## Umbrella mode

Each sub-repo's items stay on that sub-repo's own tracker; the provider is detected per origin, so GitHub and GitLab can mix. Reads merge into one backlog with qualified IDs `<repo>:<ID>`. `<repo>#<n>` always resolves; a bare `F42` resolves only when exactly one sub-repo has it. Capture needs a target repo (`--repos <name>` or cwd in a sub-repo); multi-repo items are refused, so capture one per repo and link with `Related:`. Milestone tracking issues live in `issues.homeRepo`. Release helpers take `--repo <name>` (required). See [Umbrella mode](umbrella-mode.md).

## Rate limits

Exit 5 means the tracker is unavailable; exit 6 means rate-limited. Both stop the run and report. On a primary rate limit `rota` waits 60 seconds and retries the call once, then stops with exit 6. A secondary rate limit stops at once, and other failures are not retried. There is no retry loop; wait, then re-run.

## Migrating a file backlog

```
rota migrate issues            # dry run: planned operations and would-be map
rota migrate issues --apply    # create everything
rota config set issues.source github   # or gitlab
```

Open items, their detail files, proof rows, design and plan artifacts, and planned/active milestones with their slice plans move to the tracker. Completed items, `ARCHIVE.md` and shipped/archived milestones stay in the files as history, so after the flip `rota milestone list` shows only the planned and active milestones. `Related:` fields and old IDs in migrated text are rewritten to the new IDs after every item exists; `Related:` IDs that are not migrated (completed items) are dropped from the field and listed in the issue body as `Related before migration (not migrated): F79, F80`. A bullet's `Since:` anchor is kept in the issue's fields block.

The run is resumable: `.rota/issue-map.json` records each old ID, its new ID and URL. Commit it. A rate limit (exit 6) stops the run with the map saved; wait and re-run. `--limit N` creates at most N items per run. Writes are paced at a fixed one second.

On a tracker with no milestones (It's a Plan) a milestone is reported as skipped and its items migrate without a `Milestone` field. A forge is found from `origin`; It's a Plan is not one, so set `issues.source itsaplan`, `issues.connection` and `issues.itsaplan.project` before the run.

`--apply` never changes `issues.source`. When everything is migrated it adds a "Frozen" banner to `.rota/BACKLOG.md` and prints `Next: rota config set issues.source github` (or `gitlab`). On It's a Plan it prints no such line, since `issues.source itsaplan` was set before the run. Old IDs are not resolved through the map after the flip; an old `F82` and a new issue `F82` can both exist. Umbrella projects migrate each sub-repo separately.
