## A4: backlog, capture, IDs, status

### rota id next
rota id next --kind <bugs|features|tasks|milestones>
repo: scoped
data: {"kind": string, "id": string, "changed": bool}
exit: 2 when `--kind` is missing or not bugs|features|tasks|milestones; backend (file-only: issue mode IDs are issue numbers)
old: hv-next-id <kind>    (`--kind` is the old positional)
shim: `id` is the printed ID, `changed` is always true (the counter is written).
note: `--kind` is a flag, as in `item create` (rule 1). Exception to rule 11: `id` is a fresh counter ID and `kind` already gives the type, so there is no `type` field.

### rota item create
rota item create --kind <bugs|features|tasks> --title <text> [--tag <tag>] [--desc <text>] [--body-file <path|->] [--related <text>] [--milestone <text>] [--repos <csv>] [--subsystem <text>] [--captured <YYYY-MM-DD>] | --kind <kind> --raw-file <path|->
repo: scoped
data: {"id": string, "type": "B"|"F"|"T", "kind": string, "detail"?: string, "changed": bool}
exit: 3 when BACKLOG.md or its section is missing, `--body-file` or `--raw-file` cannot be read, the milestone is absent on the tracker, or the umbrella repo is unknown or ambiguous; backend (`--raw-file` is file-only); tracker
old: hv-item-create <kind> --title <text> [--tag T] [--desc D] [--body-file F] [--field Related=<v>] [--field Milestone=<v>] [--field Repos=<v>] [--field Subsystem=<v>] [--field Captured=<v>]; with `--raw-file`: hv-append "<section for kind>" "$(cat <file>)"
shim: the helper prints one ID line. File mode: `id` is that line (for `--raw-file`, the ID parsed from the bullet). Issue mode: the line is `B12`, so `id` is `12` (letter dropped, rule 11). `type` is the dropped or leading letter; `detail` is `.rota/<kind>/<ID>.md` when `--body-file` was given; `kind` and `changed:true` are set by the shim.
note: `--kind` is a flag, not the old first positional, because the verb creates an item rather than acting on one (rule 1).
note: the repeated `--field Name=Value` becomes one flag per field (`Name` lowercased, values keep their commas), because Go `flag` does not repeat.
note: `--raw-file` absorbs hv-append: it appends one preformatted bullet (ID already in it) verbatim except for the `Since: <HEAD>` drift baseline it stamps, as hv-append did, excludes `--title`, and stays because smoke fixtures depend on it. The verb already takes `--body-file` for the detail body, so rule 3 lets this second file input take a name after its content.
note: `--tag` is P0..P3 for bugs and Major|Minor|Cosmetic for features; tasks take none (2).
note: a field flag given with an empty value (`--related ""`) exits 2, as old did; `--milestone` takes exactly one milestone (a list exits 2); a label the tracker lacks while `autoCreateLabel` is off exits 3. The bullet's fields keep the old helper's order (Related, Milestone, Repos, Subsystem, Captured).

### rota item show
rota item show <ID>
repo: scoped
data: {"id": string, "type": "B"|"F"|"T", "title": string, "status": string, "state": string|null, "claimedBy": string|null, "assignees": []string, "milestone": string|null, "notes": []string, "comments": [{"who": string, "kind": string, "text": string}]}
exit: 3 item unknown; backend (issue-only); tracker
old: hv-item-show <ID>
shim: parse the fixed lines (`[id] title`, `type:`, `status:`, `state:`, `claimed by:`, `assignee:`, `milestone:`, `notes:`, `comments: N`) and the comment rows `- <who> · <kind> · <text>`; `none` becomes null and csv values become lists. The old `type:` line is a word (`bug`, `feature`, `task`); `type` is its letter. In issue mode the bracketed id loses its letter (rule 11).

### rota item claim
rota item claim <ID> --as <claim-id>
repo: scoped
data: {"id": string, "type": "B"|"F"|"T", "claimId": string, "changed": bool}
exit: 3 item unknown or closed; 4 when another claim holds the item (old rc 5; the message names the holder; failure `data` has `changed: true`, because the losing claim was posted and then released on the issue); tracker
old: hv-item-claim <ID> --as <claim-id>
shim: `changed` is false for the file backend (silent no-op), true when stdout printed `claimed ...`. The file backend is a success no-op, not `backend`. `--as` must be non-empty with no whitespace and no `-->` (else 2).

### rota item release
rota item release <ID> --as <claim-id>
repo: scoped
data: {"id": string, "type": "B"|"F"|"T", "claimId": string, "changed": bool}
exit: 3 item unknown; tracker
old: hv-item-release <ID> --as <claim-id>
shim: old prints nothing, so `changed` is true when the backend is issues and rc 0, false for file backend; the Go port reports false when no open claim matched.

### rota item ready
rota item ready <ID>
repo: scoped
data: {"id": string, "type": "B"|"F"|"T", "ready": bool, "reasons": []string}
exit: 1 when not ready (`reasons` filled); 3 item unknown; tracker
old: hv-item-ready <ID>
shim: rc 0 gives ready true; rc 1 with stdout reasons (no `error:` on stderr) gives ready false and one reason per stdout line; rc 1 with `error:` on stderr maps to 3 or 2 by message. Works in file mode too.

### rota item state
rota item state <ID> --to <in-progress|needs-review|changes-requested|none>
repo: scoped
data: {"id": string, "type": "B"|"F"|"T", "state": string|null, "changed": bool}
exit: 2 when `--to` is missing or not one of the four; 3 item unknown; tracker
old: hv-item-state <ID> <state>
shim: `state` echoes the argument (`none` becomes null); `changed` is false for the file backend, true otherwise (old cannot say if the label already matched).
note: the state moves from a second positional to `--to`, the one spelling for setting a state (`milestone status --to`, rule 1).

### rota item comment add
rota item comment add <ID> --kind <question|answer|decision|feedback> --body-file <path|->
repo: scoped
data: {"id": string, "type": "B"|"F"|"T", "kind": string, "commentId"?: string, "changed": bool}
exit: 2 for an empty body or an invalid kind; 3 item unknown; tracker
old: hv-item-comment <ID> --kind <kind> --body-file <path|->
shim: `commentId` is the printed id (issue mode only), `changed` true.
note: split from the old `--list` mode, because read and write have different data.

### rota item comment list
rota item comment list <ID> [--kind <question|answer|decision|feedback>]
repo: scoped
data: {"id": string, "type": "B"|"F"|"T", "comments": [{"who": string, "kind": string, "text": string}]}
exit: 2 for an invalid kind; 3 item unknown; tracker
old: hv-item-comment <ID> --list [--kind <kind>]
shim: parse rows `- <who> · <kind> · <text>` (continuation lines joined into `text`). `who` is the author in issue mode and the date in file mode.

### rota item note add
rota item note add <ID> --kind <proof|design|plan> --body-file <path|->
repo: scoped
data: {"id": string, "type": "B"|"F"|"T", "kind": string, "changed": bool}
exit: 2 for an invalid kind or an empty body; 3 item unknown; backend (issue-only; hint: `rota design` or `rota plan`); tracker
old: hv-item-note <ID> --kind <kind> --body-file <path|->
shim: `changed` true (old skips identical writes silently). `ROTA_NOTE_LIMIT` stays an env hook for the split size.
note: split from the old `--show` and `--rm` action flags (old let the last one win).

### rota item note show
rota item note show <ID> --kind <proof|design|plan>
repo: scoped
data: {"id": string, "type": "B"|"F"|"T", "kind": string, "exists": bool, "body": string}
exit: 2 for an invalid kind; 3 item unknown; backend (issue-only; hint: `rota design` or `rota plan`); tracker
old: hv-item-note <ID> --kind <kind> --show
shim: `show` prints text, so `exists` is `body != ""` (old cannot tell absent from empty), trailing newlines stripped.

### rota item note rm
rota item note rm <ID> --kind <proof|design|plan>
repo: scoped
data: {"id": string, "type": "B"|"F"|"T", "kind": string, "changed": bool}
exit: 2 for an invalid kind; 3 item unknown; backend (issue-only; hint: `rota design` or `rota plan`); tracker
old: hv-item-note <ID> --kind <kind> --rm
shim: `changed` true (old cannot say whether a note was there).

### rota item field get
rota item field get <ID> --name <title|detail|related|milestone|repos|subsystem|since|reason|note>
repo: scoped
data: {"id": string, "type": "B"|"F"|"T", "field": string, "value": string}
exit: 2 for an unknown field; 3 item unknown; tracker
old: hv-todo-field <ID> <field>
shim: `value` is stdout minus the newline.
note: two positionals become `<ID>` plus `--name` (rule 1).

### rota item field set
rota item field set <ID> --name <milestone|related|repos|subsystem|detail> --value <text>
repo: scoped
data: {"id": string, "type": "B"|"F"|"T", "field": string, "value": string, "changed": bool}
exit: 2 for an unknown or read-only field, when `--value` is missing, or when `--name detail` gets an empty value (backticks only); 3 item unknown, or the `--name detail` file does not exist; 4 for a completed or archived item; backend (`--name detail` is file-only); tracker
old: hv-todo-set-field <ID> <field> <value>
shim: `value` echoes the argument; `changed` is false when the stored value already matched (the shim compares BACKLOG.md before and after). `--value ""` clears the field. `--name detail` takes an existing file path as the value, file mode only.
note: the value is a flag, not a positional, so a value that starts with `-` cannot be mistaken for a flag and `--value ""` is explicit (conventions: a flag takes the next token).
note: setting a field on a closed item is refused (4) where old returned rc 1.

### rota item field list
rota item field list <ID>
repo: scoped
data: {"id": string, "type": "B"|"F"|"T", "fields": {"title": string, "detail": string, "related": string, "milestone": string, "repos": string, "subsystem": string, "since": string, "reason": string, "note": string}}
exit: 3 item unknown; tracker
old: hv-todo-field --dump <ID>
shim: parse the one-line JSON (keys already camel-safe).
note: the old `--dump` flag becomes the sub-verb `list`.

### rota item complete
rota item complete <ID> [--commit <hash>] [--reason <done|handed-off|blocked|dropped>] [--note <text>] [--no-proof]
repo: scoped
data: {"id": string, "type": "B"|"F"|"T", "reason": string, "commit": string, "changed": bool}
exit: 3 unknown ID or type-letter mismatch; 4 when a `done` close has no `## Proof` row (old rc 3, bypass with `--no-proof`); 5 when git has no HEAD to default `--commit`; tracker
old: hv-complete <ID> [<hash>] [--reason R] [--note N] [--no-proof]
shim: stdout is empty, so `commit` is `--commit` or `git log -1 --format=%h`, `reason` defaults to `done`, `changed` is false when already completed (stderr says no-op), else true. The shim tells proof-missing from tracker-unavailable (both old rc 3) by the stderr text.
note: `--note` newlines are flattened to spaces and `--reason` defaults to `done`, as old.

### rota item reopen
rota item reopen <ID>
repo: scoped
data: {"id": string, "type": "B"|"F"|"T", "changed": bool}
exit: 3 item unknown; tracker
old: hv-uncomplete <ID>
shim: `changed` is false when stderr says `noop: ... already active`, else true.

### rota item rm
rota item rm <ID>... [--scrub-archive] [--apply]
repo: scoped
data: {"applied": bool, "items": [{"id": string, "type": "B"|"F"|"T", "todoEntry": bool, "crossRefs": number, "detailFile"?: string, "planFiles": []string, "archive": bool, "activeBranch"?: string}], "changed": bool}
exit: 3 when any ID is in neither BACKLOG.md nor ARCHIVE.md; 4 when `--apply` is given and an ID is active on a branch (old rc 2; failure `data` is `{"blockedBy": "active", "id": string, "activeBranch": string, "changed": false}`); backend (file-only; hint: `rota item complete <ID> --reason dropped`)
old: hv-rm [--force] [--scrub-archive] <ID1>,<ID2>,... (`--apply` maps to `--force`; no `--apply` runs old without `--force`)
shim: parse the per-ID plan or `removed:` lines into `items`; `applied` is true when `--apply` was given; `changed` equals `applied`. A preview with no `--apply` adds the warning `preview only; pass --apply`. Old refuses a preview of an active ID (rc 2, no plan), so until the port the shim fills that item from `.rota/status.json` (`id`, `type`, `activeBranch`; other fields empty) and previews the other IDs through old; smoke asserts only those three fields for an active ID.
note: the preview always shows and exits 0, and `activeBranch` marks an active ID. Only `--apply` refuses it (4). Old `--force` also stripped an active stream; 5.0 drops that, so an active item can only be removed after `rota status rm <branch>`.

### rota item shipped
rota item shipped <title>...
repo: scoped
data: {"found": bool, "titles": [{"title": string, "hits": [{"level": "strong"|"medium"|"path", "hash"?: string, "subject"?: string, "tokens"?: []string, "token"?: string, "path"?: string}]}]}
exit: 1 when no title has shipped-looking evidence (`found` false); 2 when no title is given. Exit 0 means ship evidence was found, and `data.titles[].hits` names it.
old: hv-capture-audit <title> [<title>...]
shim: old rc 2 (evidence) becomes 0, old rc 0 (nothing) becomes 1, old rc 1 (no titles) becomes 2; parse `=== title ===` blocks and `[STRONG]`, `[MEDIUM]`, `[PATH]` lines into `titles` (titles with no hits are listed with empty `hits`). Runs in the caller's cwd git repo, but like every verb it needs a `.rota/` root above it (conventions), so a bare fixture needs an empty `.rota/`.
note: renamed from `item audit` so the exit follows the predicate in the name (rule 6): `shipped` holds when evidence is found. This inverts old rc 0 and 2. The A9 rewrite of `rota-capture/SKILL.md` lines 104 to 112 must flip its branch: it branches on the old helper's meaning (rc 2 = evidence, rc 0 = none, rc 1 = usage), and under `item shipped` 0 = evidence, 1 = none, 2 = usage. Blank titles are ignored.

### rota backlog list
rota backlog list [--grep <pattern>]
repo: scoped
data: {"inProgress": [{"id": string, "type": "B"|"F"|"T", "title": string, "branch": string, "startedAt": string, "repo"?: string}], "bugs": [{"id": string, "priority": string, "title": string, "related": []string, "milestone"?: string}], "features": [{"id": string, "size": string, "title": string, "related": []string, "milestone"?: string}], "tasks": [{"id": string, "title": string, "related": []string, "milestone"?: string}], "clusters": [[]string]}
exit: tracker (works under both backends)
old: hv-backlog [--grep <pattern>]
shim: parse the markdown tables (`### In Progress`, `### Bugs`, `### Features`, `### Tasks`) into the lists, `Related` cells `[F3], [B1]` into `["F3","B1"]`, and `### Clusters` bullets into ID arrays; the placeholder lines become all-empty lists. Items active in status.json appear only in `inProgress`. `--grep` is a case-insensitive substring of the raw bullet; `--grep ""` is unfiltered; in-progress rows are never filtered.
note: Single spelling `--grep <pattern>` (the `--grep=<p>` form still parses under Go `flag`).
note: `related` entries use the rule-11 `id` spelling, so in issue mode they are bare issue numbers.

### rota backlog ids
rota backlog ids --milestone <id>
repo: scoped
data: {"milestone": string, "ids": []string}
exit: tracker
old: hv-todo-by-milestone <id>
shim: one ID per stdout line into `ids`, backlog order, open items only; a multi-valued `Milestone: M01, M03` matches either.
note: the milestone is a required flag, not a positional, per the tree; an unknown milestone returns an empty list, not 3, as old.

### rota backlog milestones
rota backlog milestones <ID>...
repo: scoped
data: {"milestones": []string}
exit: tracker
old: hv-find-milestone-for-items <ID> [<ID>...]
shim: one milestone per stdout line, unique, numerically sorted, open sections only; unknown or untagged IDs are skipped silently.

### rota backlog drift
rota backlog drift
repo: scoped
data: {"drift": [{"id": string, "type": "B"|"F"|"T", "commits": [{"repo": string, "hash": string, "subject": string}]}], "symbolDrift": [{"id": string, "type": "B"|"F"|"T", "symbols": []string, "files": []string}]}
exit: backend (file-only)
old: hv-todo-drift
shim: rename `symbol_drift` to `symbolDrift`, always emit both keys (old drops `symbol_drift` and emits `{"drift": []}` when there are no open IDs). In an umbrella without `--repo`, all registered sub-repos are walked; with `--repo`, the shim keeps only that repo's commits.

### rota backlog backfill
rota backlog backfill
repo: scoped
data: {"stamped": number, "changed": bool}
exit: backend (file-only); 5 when no git repo has a HEAD commit
old: hv-backfill-since
shim: `stamped` is the printed integer (0 when silent) and `changed` is `stamped > 0`.
note: split from `backlog drift --backfill`, because it writes `Since:` stamps and returns a different `data` shape. It is a one-shot that silences drift false positives, and drift keeps no `--backfill` flag.

### rota backlog archive
rota backlog archive [--days <n>]
repo: scoped
data: {"days": number, "moved": number, "changed": bool}
exit: backend (file-only)
old: hv-archive-old [<n>]
shim: `days` defaults to 5; `moved` is the printed count (0 when there is no output because BACKLOG.md or `## Completed` is missing); `changed` is `moved > 0`.
note: Ported on the maintainer's decision (the triage had it as a delete). Moves `## Completed` lines older than `--days` to `.rota/ARCHIVE.md`.

### rota backlog stale
rota backlog stale --kind <map|knowledge|todo> [--days <n>]
repo: scoped
data: {"kind": string, "days": number, "entries": [{"name": string, "date": string|null}]}
exit: 2 when `--kind` is missing or not map|knowledge|todo
old: hv-staleness <kind> [--days <n>]    (`--kind` is the old positional)
shim: each stdout line `<name> <YYYY-MM-DD>` becomes an entry; the literal `unknown` becomes null. `days` defaults to 90. Test hook `--today` becomes env `ROTA_TEST_TODAY=YYYY-MM-DD`, passed through as `--today`.
note: the verb stays under `backlog stale`, as the tree says, though map and knowledge are not backlog data. `kind` is a flag (rule 1).

### rota summary
rota summary
repo: scoped
data: {"backlog": {"bugs": number, "features": number, "tasks": number}, "active": [{"items": []string, "branch": string, "worktree"?: string, "repo"?: string, "since": string}], "recent": [{"id": string, "type": "B"|"F"|"T", "date": string, "reason"?: string}], "milestones": [{"id": string, "title": string}], "knowledge"?: {"count": number, "topics": []string}, "decisions"?: {"count": number, "topics": []string}, "archive"?: number}
exit: tracker
old: hv-summary
shim: parse the labelled lines (`Backlog:`, `Active:` one per entry, `Recent:` top 3, `Active milestones:`, `Knowledge:`, `Decisions:`, `Archive:`); an absent line gives an empty list or an absent object. `topics` holds the names as printed (old truncates to a few). The placeholder `No .rota/ yet` becomes the root-not-found error (3).

### rota issues list
rota issues list [--mine] [--label <name>] [--limit <n>]
repo: scoped
data: {"issues": [{"number": number, "title": string, "body": string, "labels": []string, "url": string, "author": string}]}
exit: 3 when no provider resolves (no `origin` remote naming a forge and `issues.provider` unset or not `github`/`gitlab`); tracker (works under both backends, so there is no `backend` exit)
old: hv-issues-list [--repo <name>] [--mine] [--label <name>] [--limit <n>]
shim: wrap the JSON array in `issues`; global `--repo` maps to old `--repo`. `--limit` defaults to 30. The provider is the `origin` host; `issues.provider` is the fallback when `origin` is missing or names no forge. With neither, exit 3 with a message naming `issues.provider`; old returned an empty list. Unknown flags are now rejected (2); old swallowed them.

### rota issues label
rota issues label <issue> (--add <name> | --remove <name>)
repo: scoped
data: {"issue": number, "label": string, "action": "add"|"remove", "changed": bool}
exit: 3 when the issue does not exist upstream (the forge CLI says not found) or no provider resolves (as `issues list`); tracker (works under both backends, so there is no `backend` exit)
old: hv-issues-label <apply|remove> --issue <issue> --label <name> [--repo <name>]
shim: `--add` maps to `apply`, `--remove` to `remove`; stdout is empty, so `changed` is true on rc 0 (old cannot report a no-op); the Go port reports the real value. Config `issues.autoCreateLabel` (default true) still governs label creation. Old exits 5 for a missing issue or provider; both are 3 now.
note: the subcommand `apply|remove` becomes the flags `--add` and `--remove` (exactly one), and the issue number becomes the positional, so `--label` only ever means a filter (`issues list`).

### rota issues imported
rota issues imported [--for-repo <name>] [--open-only]
repo: none
data: {"entries": [{"provider": "github"|"gitlab", "repo": string|null, "issue": number, "itemId": string, "status": "open"|"archived"}]}
exit: implied only. With `--open-only`, entries `gh` or `glab` cannot resolve are dropped, as old.
old: hv-issues-imported [--repo <name>] [--open-only]
shim: wrap the array in `entries` and rename `item_id` to `itemId`; `--for-repo` maps to the old `--repo`. Scans BACKLOG.md, ARCHIVE.md and `.rota/{bugs,features,tasks}/*.md` for `GH: #N` and `GL: #N`; a multi-repo `Repos:` list yields one entry per repo.
note: `repo: none`, because the verb reads the umbrella-root files and filters on a field; the old `--repo` filter is renamed `--for-repo` so it can't collide with the global flag.

### rota issues close
rota issues close <issue> --commit <sha> [--item <ID>]
repo: scoped
data: {"issue": number, "commit": string, "changed": bool}
exit: 3 when the commit is not found, the issue does not exist upstream (the forge CLI says not found) or no provider resolves (as `issues list`); tracker (works under both backends, so there is no `backend` exit)
old: hv-issues-close --issue <issue> --commit <sha> [--item <ID>] [--repo <name>]
shim: stdout is empty (gh output passes through), so `changed` is true on rc 0 (old cannot report "already closed"); the Go port reports the real value. Old exits 5 for a missing issue or provider; both are 3 now. The comment posted is `Closed by hv-skills: shipped in <short-sha>[ ([<ITEM>])]`. Global `--repo` maps to old `--repo`.
note: the issue number moves from `--issue` to the positional.

### rota issues provider
rota issues provider
repo: scoped
data: {"provider": "github"|"gitlab"|"unknown"}
exit: implied only. No `origin` remote gives `unknown`, unless `issues.provider` is `github` or `gitlab`, which then answers.
old: hv-issues-provider [--repo <name>]
shim: the single stdout word becomes `provider`.

### rota status add
rota status add <branch> --items <csv> [--worktree <path>] [--if-absent]
rota status add <branch> --items <csv> --repos <csv> [--worktrees <csv>] [--if-absent]
repo: scoped
data: {"branch": string, "entries": [{"repo": string|null, "items": []string, "worktree": string|null, "startedAt": string}], "changed": bool}
exit: 3 when a `--repos` name, or a global `--repo`, is not a registered sub-repo; 2 for `--repo` with `--repos`, or for a `--worktrees` length that differs from `--repos`
old: single: hv-status-add [--if-absent] [--repo <r>] <branch> <items-csv> [<worktree>]; multi: hv-status-add-multi [--if-absent] --branch <branch> --items <csv> --repos <csv> [--worktrees <csv>]
shim: read the written entries back from `.rota/status.json` for `entries`; `changed` is false only when `--if-absent` met an existing (branch, repo) entry, otherwise true (an existing entry is replaced, with a new `startedAt`). Repo names are validated by `hv-resolve-repos` for both forms; `--repos` accepts spaces after commas.
note: single-repo `--repo` is now validated (3), per the conventions, where old `hv-status-add` accepted any name (smoke 16:88 depends on that).

### rota status rm
rota status rm <branch>
repo: scoped
data: {"branch": string, "removed": number, "handoffRemoved": bool, "changed": bool}
exit: implied only
old: hv-status-remove [--repo <r>] <branch> (`--repo` must come first)
shim: count entries and check the handoff path before and after for `removed` and `handoffRemoved`; `changed` is `removed > 0 || handoffRemoved`. Without `--repo`, only entries with repo null are removed; umbrella-tagged entries stay. Also deletes the branch's handoff note.

### rota status show
rota status show <branch>
repo: scoped
data: {"branch": string, "active": bool, "repo": string|null, "items": []string, "worktree": string|null, "startedAt"?: string}
exit: implied only. A branch with no entry returns `active: false`.
old: hv-status-repo-for <branch>
shim: old returns only the repo name, so the shim reads the matching entry from `.rota/status.json` (first entry by branch) and fills the rest; an empty stdout with no entry gives `active: false`.
note: not-active is `active: false` with exit 0, not 3, because skills call this to read the repo name and old returned empty.
note: the global `--repo` narrows the match to that (branch, repo) pair, which the old helper could not do.

### rota status handoff
rota status handoff <branch> [--canonical]
repo: scoped
data: {"branch": string, "path": string|null, "exists": bool}
exit: implied only. A read miss returns `path: null`.
old: hv-resolve-handoff [--repo <r>] [--write] <branch>
shim: read mode: stdout path or empty (null, `exists` false); `--canonical`: the path is returned without probing, `exists` is whether the file is there. Paths are relative to the project root, branches with `/` stay literal, and `--repo` gives `<branch>@<repo>.md`, falling back to `<branch>.md` in read mode.
note: `--write` becomes `--canonical`, because the verb never writes anything.

### rota status loop start
rota status loop start
repo: none
data: {"loopStartedAt": string, "changed": bool}
exit: implied only
old: hv-loop-stamp start
shim: `changed` is true when `loopStartedAt` was unset before the call (read it first with `hv-loop-stamp read`); the value is first-write-wins, so an existing stamp is returned unchanged.

### rota status loop clear
rota status loop clear
repo: none
data: {"changed": bool}
exit: implied only
old: hv-loop-stamp clear
shim: `changed` is true when `hv-loop-stamp read` returned a value before the call.

### rota status loop show
rota status loop show
repo: none
data: {"loopStartedAt": string|null}
exit: implied only
old: hv-loop-stamp read
shim: empty stdout becomes null. Old subcommand `read` is renamed `show`.

### rota refactor age
rota refactor age
repo: scoped
data: {"features": number, "bugs": number}
exit: implied only
old: hv-refactor-age
shim: the JSON passes through unchanged (zeros when `since_refactor` is absent).

### rota refactor reset
rota refactor reset
repo: scoped
data: {"changed": bool}
exit: implied only
old: hv-refactor-reset
shim: read `hv-refactor-age` first; `changed` is true when either count was non-zero.

### rota refactor targets
rota refactor targets
repo: none
data: {"umbrella": {"hasCode": bool}|null, "subRepos": [{"name": string, "path": string}]}
exit: implied only
old: hv-refactor-targets
shim: the JSON passes through (single repo is `{"umbrella": null, "subRepos": []}`); `path` is absolute, `subRepos` sorted by name. Runs in the caller's cwd (no self-locate).

### rota migrate issues
rota migrate issues [--apply] [--limit <n>]
repo: none
data: {"applied": bool, "operations": [{"action": "create-milestone"|"create-issue"|"note"|"set"|"rewrite"|"skip", "text": string}], "map": object, "migrated": number, "total": number, "changed": bool}
exit: 3 when BACKLOG.md is missing; 4 in umbrella mode; tracker (on 5 and 6, `.rota/issue-map.json` keeps the progress, a re-run resumes, and the message ends with `N of M migrated`, since those exits carry no failure data)
old: hv-migrate-issues [--dry-run | --apply] [--limit <n>]
shim: parse the op lines (`create milestone ...`, `create issue ...`, `note ... on ...`, `rewrite Related ...`, `skip ...`) into `operations` by their first word, with `text` the rest; `map` is the JSON after `would-be map:` in preview, or the contents of `.rota/issue-map.json` after `--apply`; `changed` is true when `--apply` changed state: it created an item or milestone on the tracker, wrote `.rota/issue-map.json`, or froze `.rota/BACKLOG.md` (a freeze-only run counts, because freezing is a state change; orchestrator ruling on #117). `--limit` must be digits (else 2). Pace comes from config `issues.bulkPaceMs` (default 1000). With `--apply` the old verb also freezes `.rota/BACKLOG.md` and hints to set `backlog.backend=issues`.
