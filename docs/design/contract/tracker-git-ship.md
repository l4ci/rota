## A8: tracker, git, review, ship, release

### rota tracker call
rota tracker call [--provider auto|github|gitlab] -- <cli-arg>...
repo: scoped
data: {"provider": string, "exitCode": number, "stdout": string, "stderr": string}
exit: 1 when the forge CLI ran and exited non-zero (`data.exitCode` carries its code); 2 when no CLI args follow `--`; tracker (5 also when no provider resolves)
old: hv-tracker-call [--provider <p>] -- <cli-arg>...  (flags map 1:1; `--` is required in both)
shim: runs the old helper from the repo's directory (`-C`, `--repo`), captures stdout and stderr and its exit code. Old exit 1 with a usage message becomes 2; old exit 0 and CLI-own non-zero exits (old passes them through) become 0 and 1; old exit 3 becomes 5; old exit 4 becomes 6. `provider` is the resolved one (the `--provider` value, else `issues.provider`, else the old detection).
note: debug passthrough; stdin is replayed only when a CLI arg is `-`, `@-` or `=-`, as before. Text mode writes the CLI's stdout to stdout and its stderr to stderr unchanged (exception to rule 8). No `changed`: the verb cannot know whether the call mutated the forge.
note: a non-zero CLI exit becomes exit 1 with the code in `data.exitCode`, because passing arbitrary codes through would break the exit table.

### rota tracker suggest-upstream
rota tracker suggest-upstream --title <text> --body-file <path|-> [--upstream-repo <owner/repo>] --confirm --confirm-note <answer>
repo: none
data: {"url": string, "number": number, "upstreamRepo": string, "changed": bool}
exit: 2 when --title or --body-file is missing, or a confirmation flag is given without the other; 4 when the `public-filing` gate is not cleared (B1; checked after the arguments, before `gh` runs); 5 when `gh` is missing or not authenticated (the hint carries the manual `https://github.com/<repo>/issues/new` URL)
old: hv-issue-suggest --title <text> [--upstream-repo <owner/repo>] < <body-file>
shim: `--body-file` is fed to the old helper's stdin. Old stdout JSON `{"url","number"}` becomes `data`; `upstreamRepo` is the flag, else `ROTA_UPSTREAM_REPO`, else `l4ci/hv-skills`. Old exit 1 with the `gh not available` fallback block becomes 5, and the shim keeps only the `https://github.com/<repo>/issues/new` URL as the hint.
note: the old stdout fallback (title and body echo) is dropped because the caller holds both, and old exit 1 becomes 5.

### rota git base
rota git base
repo: scoped
data: {"base": string}
exit: 2 at an umbrella root without --repo; 3 when no base branch resolves
old: hv-base-branch
shim: runs in the repo's directory; `base` is the stdout line. Old exit 1 with `umbrella` in stderr becomes 2; other old exit 1 becomes 3.
note: resolution order is unchanged: `git.baseBranch` if it exists, then main, master, trunk, origin/HEAD.

### rota git guard clean
rota git guard clean [--context <text>]
repo: scoped
data: {"clean": bool, "greenfield": bool, "dirtyRepos": []string}
exit: 1 when the tree is dirty (`clean: false`); 3 when the directory is not a git repo, or an umbrella root has no `repos.json`
old: hv-guard-clean [<context>]  (`--context <text>` is the old free-text positional)
shim: `clean` is true on old exit 0. `greenfield` is true when stderr carries the `fresh repo (no commits yet)` variant. `dirtyRepos` is the sub-repo list from the umbrella variant, else empty. Old exit 2 becomes 3.
note: `--context` only changes the stderr message (default `this command`). At an umbrella root the verb walks the registered sub-repos; `--repo` limits it to one.

### rota git guard feature-branch
rota git guard feature-branch [<branch>]
repo: scoped
data: {"feature": bool, "branch"?: string, "base"?: string, "reason"?: string}
exit: 1 when the branch is the base branch or HEAD is detached (`feature: false`, `reason` is `base` or `detached`); 2 at an umbrella root without --repo
old: hv-guard-feature-branch [<branch>]
shim: `feature` is true on exit 0. `branch` defaults to the current branch (absent when detached). `base` comes from `hv-base-branch`. `reason` is derived from the stderr text (`refusing to operate` is `base`, `detached HEAD` is `detached`). Old exit 1 with `umbrella` in stderr becomes 2.

### rota git branch
rota git branch <name> --repos <a,b,...>
repo: none
data: {"branch": string, "repos": []string, "changed": bool}
exit: 2 when --repos is empty; 3 when a named repo is unknown; 4 when the branch already exists in any listed repo (nothing is created anywhere); 5 when creating the branch fails in a repo
old: hv-multi-branch-create --branch <name> --repos "<a, b, ...>"
shim: `<name>` is `--branch`. `--repos` is passed as one string to the old `--repos`. Old exit 1 is split by message (`already exists in:` becomes 4 with the names in the message, `failed to create` becomes 5). Old pass-through exit from `hv-resolve-repos` becomes 3. `repos` is the resolved list. The branch is created without switching HEAD.
note: `--repos` is a list flag, not the global `--repo`. Spaces after commas, which the old helper accepted, are rejected.

### rota git worktree-path
rota git worktree-path <branch>
repo: scoped
data: {"path": string}
exit: 2 when --repo is missing; 3 when the umbrella or repo cannot be resolved
old: hv-worktree-path --repo <repo> <branch>  (global `--repo` becomes the old first-arg flag)
shim: `path` is the stdout line, `<umbrella>/.claude/worktrees/<repo>/<branch>`. Old exit 1 with a usage message becomes 2; other old exit 1 becomes 3.

### rota review scope
rota review scope [<branch>]
repo: scoped
data: {"branch": string, "base": string, "commitCount": number, "commits": [{"hash": string, "subject": string}], "touchedFiles": []string, "referencedIds": []string, "intents": [{"id": string, "type": "B"|"F"|"T"|null, "title": string, "entry": string}]}
issue mode: under backlog.backend "issues", `referencedIds` are issue refs (`"#12"`): the number in a `<agent>/<N>-slug` branch name plus the `#N` after a closing keyword (Closes/Fixes/Resolves and -s/-d/-ed, any case, comma lists) in commit messages. Each resolves through the backend's get into an intent; a ref that does not resolve (offline, unknown) stays in `referencedIds` with no intent. File mode is unchanged (bracketed IDs, BACKLOG/ARCHIVE corpus).
exit: 2 at an umbrella root without --repo; 3 when the branch does not exist; 1 when the branch is the base branch; 5 when git fails Exit 1 carries no `data`: there is no answer to give.
old: hv-review-scope [--repo <repo>] [<branch>]
shim: the old JSON is passed through as `data` (already camelCase). Old exit 1 is split by message (`umbrella` becomes 2, unknown branch becomes 3, branch equals base becomes 1, other git errors become 5). `branch` defaults to the current branch.

### rota review brief
rota review brief [<branch>]
repo: scoped
data: {"branch": string, "base": string, "commitCount": number, "brief": string}
exit: 2 at an umbrella root without --repo; 3 when the branch does not exist; 1 when the branch is the base branch or has no commits beyond it; 5 when git fails Exit 1 carries no `data`: there is no answer to give.
old: hv-second-opinion-brief [--repo <repo>] <branch>
shim: `brief` is the old stdout markdown; `branch` defaults to the current branch (the shim resolves it); `base` and `commitCount` come from a `hv-review-scope` call. Old exit 1 is split by message as in `rota review scope`; old exit 2 (no commits beyond base) becomes 1.
note: text mode prints the brief verbatim, as for `show` verbs, so `> "$BRIEF"` still works.
note: the triage tree's `brief --fresh` drops the flag, because the verb has no other mode.
note: `<branch>` is optional (default current branch) to match `scope`, where the old helper required it.

### rota review scaffolding
rota review scaffolding [<branch>] [--base <branch>]
repo: scoped
data: {"findings": [{"file": string, "line": number, "text": string}]}
exit: 2 at an umbrella root without --repo; 3 when a named branch or base does not exist; 5 when git fails
old: hv-review-scaffolding [--repo <repo>] <base> <branch>
shim: passes `--base`, else `hv-base-branch` (else `main`), and `<branch>`, else the current branch, as the old two positionals. Each stdout line `<file>:<line>:<text>` becomes one finding; empty output gives `{"findings": []}`.
note: findings are added diff lines matching `Task N`, `in flight`, `placeholder`, `added later` or `not yet wired`. Finding nothing is exit 0, as before.

### rota review queue
rota review queue
repo: scoped
data: {"items": [{"id": string, "type": "B"|"F"|"T", "number": number, "title": string, "repo"?: string, "prs": [{"number": number, "title": string, "branch": string, "url": string, "body": string}]}]}
exit: backend (issue-only); tracker
old: hv-review-queue
shim: wraps the old array as `items` (lowest issue number first). The old file backend printed `[]` with a stderr note and exit 0; the shim turns that into `backend`. Old exit 1 for usage becomes 2, other old exit 1 and old exit 3 become 5, old exit 4 becomes 6. With --repo, the shim filters `items` on `repo`.
note: at an umbrella root without --repo the verb covers every sub-repo and IDs are qualified (`ghrepo:12`); `--repo` narrows it to one sub-repo, which the old helper could not do.
note: the file backend was an empty success and is now `backend` (4), per the shared rule.

### rota ship body
rota ship body [<branch>]
repo: scoped
data: {"branch": string, "body": string}
exit: 2 at an umbrella root without --repo; 3 when the branch does not exist; 1 when the branch is the base branch or has no commits beyond it; 5 when git fails Exit 1 carries no `data`: there is no answer to give.
old: hv-ship-body <branch>
shim: runs in the repo's directory (`hv-resolve-repo-path`, since the old helper had no --repo), `branch` defaulting to the current one. `body` is the old stdout (`## Summary`, `## Items resolved`, `Closes #N` lines). Old exit 1 is split by message as in `rota review scope` (branch equals base or no commits beyond it becomes 1).
note: text mode prints the body verbatim. The caller appends `## Test plan` and pipes the result to `rota ship pr --body-file -`.
note: `<branch>` is optional (default current branch), as for `rota review brief`.

### rota ship pr
rota ship pr <branch> --title <text> --body-file <path|-> [--items <ID>[,<ID>…]]
repo: scoped
data: {"branch": string, "url": string, "provider": string, "number"?: number, "items": []string, "changed": bool}; failure data on exit 4 is the verdict-refusal shape (B3)
exit: 2 when --title or --body-file is missing or the body is empty, or at an umbrella root without --repo; 3 when an --items item is unknown, the branch does not exist, or no base branch resolves; 4 when a FAIL verdict blocks the branch (B3; checked after the --items lookup and before the push); 5 when the push fails; tracker
old: hv-pr [--repo <repo>] [--closes <ID,ID>] <branch> <title> < <body-file>    (`--items` is the old `--closes`)
shim: `--body-file` goes to the old stdin; `--title` is the second positional. `url` is the last stdout line, `number` the trailing digits of the URL, `provider` from the URL host. Old exit 1 is split by message (missing body becomes 2, unknown item becomes 3, CLI error becomes 5); old exit 3 becomes 5; old exit 4 becomes 6.
note: in issue mode `--items` appends `Closes #<n>` lines to the PR body; in file mode it is accepted and ignored. An unknown item fails before the push, and `data.items` echoes the IDs as given.
note: old `--closes` is renamed `--items`, the one item-ID list flag (rule 11).
note: the provider is `issues.provider` when it is `github` or `gitlab`, else the one the `origin` URL names; an origin that names neither falls back to `github`, as the old helper did.
note: no resolvable base branch is exit 3 (`resolution`, conventions); the shim maps the old helper's failure there to 5.
note: in umbrella issue mode `--items` resolves inside the sub-repo the PR opens in (`--repo`, else the one the working directory is in); an item of another sub-repo is unknown there (3), and with neither the verb exits 2.

### rota ship merge
rota ship merge <branch> --body-file <path|-> [--confirm --confirm-note <answer>]
repo: scoped
data: {"branch": string, "base": string, "sha": string, "changed": bool}; failure data on the B3 refusal is the verdict-refusal shape
exit: 2 when --body-file is missing or empty, at an umbrella root without --repo, a confirmation flag is given without the other, or `ship.mergeApproval` is not `none`, `all` or `paths`; 4 when a FAIL verdict blocks the branch (B3; checked after the base resolves, before the merge-approval gate and before any change); 4 when `ship.mergeApproval` covers this merge and the `merge-approval` gate is not cleared (B1; checked after the base resolves and before the worktree is cleared, so a refusal changes nothing); 3 when the branch does not exist or no base branch resolves; 4 when the merge conflicts (the merge is aborted, so the tree is left as it was and `changed` is false) or the branch is the base branch; 5 when git fails otherwise
old: hv-merge [--repo <repo>] <branch> < <body-file>
shim: `--body-file` goes to the old stdin. `sha` is the last stdout line (`git log -1 --format=%h`). `base` is `hv-base-branch`. The shim removes the branch's worktree first (the old `hv-worktree-clear` pre-step, which runs inside the old helper), then merges, deletes the branch. Old exit 1 is split by message.
note: the merge message is the body; its subject must start `merge: ` for `rota ship undo` to recognise the cycle. The old helper did not check it, and the contract does not either. Mutation is `git checkout <base>`, `git merge --no-ff`, `git branch -d`.

### rota ship pr-merge
rota ship pr-merge <pr> [--items <ID>[,<ID>…]] [--confirm --confirm-note <answer> | --approval <escalation> | --escalate]
repo: scoped
data: {"pr": number, "sha": string, "closed": []string, "changed": bool}; failure data on exit 4: {"pr": number, "merged": bool, "unproven": []string, "changesRequested": []string, "changed": bool}, or the manual-gate shape (B1)
exit: 2 when <pr> is not all digits, at an umbrella root without --repo, a confirmation flag is given without the other, or `ship.mergeApproval` is invalid; 4 when a FAIL verdict blocks the PR's head branch (B3; checked once the PR and its items resolve, before the merge-approval gate and the proof check, so a refusal records no `changes-requested`; failure data is the verdict-refusal shape plus `pr`); 4 also when `ship.mergeApproval` covers this PR and the `merge-approval` gate is not cleared (B1; checked before the proof check, so a refusal records no `changes-requested`); 3 when the PR or an item is unknown, or the PR is not open (closed or already merged); 4 when the merge fails or an item has no proof (the item is set to changes-requested); backend (issue-only); tracker; with `--approval`, 2, 3 or 4 as "C5: merge policy and approval requests" lists, and two of `--approval`, `--escalate`, `--confirm` together is 2
old: hv-pr-merge <pr> [--items <ID,ID>] [--repo <repo>]
shim: `sha` and `closed` come from `merged <pr> as <sha7>` and the `closed <ID>` lines. Old exit 1 is split by message (non-digit PR becomes 2, unknown PR or item becomes 3, tracker failure becomes 5); old exit 5 (unproven) becomes 4; otherwise the old backend rc mapping applies.
note: unproven items still move to `changes-requested` on exit 4; the failure `data` lists them in `unproven` and `changesRequested` with `changed: true` (conventions).
note: an unproven item was old exit 5 and is now 4, the table's "proof is missing".
note: in umbrella issue mode the verb acts on scope S (`--repo`, else the sub-repo the working directory is in; the umbrella root without `--repo` is exit 2), and an `--items` reference that belongs to another sub-repo is exit 2 (`usage`); the shim maps the old message to 5.

### rota ship undo
rota ship undo [--cycle <hash>] [--allow-post-merge] [--apply]
repo: scoped
data: {"applied": bool, "cycle": string, "subject": string, "base": string, "items": []string, "restoredTo"?: string, "restored"?: []string, "changed": bool}
exit: 3 when there is no cycle to undo; 4 when not on the base branch, the tree is dirty, HEAD is not a merge, the merge subject does not start `merge: `, there are post-merge commits without --allow-post-merge, or the merge is PR-mode with a remote ref; 5 when restoring an item fails after the reset (`error.message` says the reset already happened, since exit 5 carries no failure data)
old: hv-undo [--cycle <hash>] [--force] [--allow-post-merge]  (`--apply` is the old `--force`)
shim: without --apply, `applied: false`: the `Undo plan for last cycle:` block gives `cycle`, `subject`, `base` and `items`. With --apply, `applied: true`, `restoredTo` and `restored` come from `Undone cycle <sha7>. Reset <base> to <sha7>. Restored: <ids|none>.` `changed` is true only when applied. Old exit 1 is split by message (no cycle becomes 3, the rest become 4); old exit 2 (dirty tree, not a merge, bad HEAD shape) becomes 4; old exit 1 for invalid args becomes 2.
note: text mode prints the plan block or the final `Undone cycle` line.

### rota release version
rota release version [--level patch|minor|major | --to <X.Y.Z>]
repo: scoped
data: {"file": string, "version": string, "kind": string, "next"?: string}
exit: 3 when no version file is found or it cannot be parsed; 1 when --to is not greater than the current version
old: hv-release-detect-version  |  hv-release-bump-version --dry-run <file> <kind> <level|X.Y.Z>
shim: `file`, `version` and `kind` come from the old JSON. With --level or --to, the shim calls `hv-release-bump-version --dry-run <file> <kind> <bump>` and sets `next` to its stdout. Old exit 1 is split by message (`not found`, `parse` become 3; `must be greater` becomes 1; bad level becomes 2).
note: `kind` is one of `plugin-json`, `package-json`, `pyproject`, `cargo`, `plain`. `release.versionFile` overrides auto-detection.
note: `--level` and `--to` compute `next` read-only, because the release skill needs the new version before it writes anything.

### rota release bump
rota release bump (--level patch|minor|major | --to <X.Y.Z>) [--file <path>] [--kind <kind>]
repo: scoped
data: {"file": string, "kind": string, "from": string, "to": string, "changed": bool}
exit: 2 when neither or both of --level and --to are given, or --to is not bare X.Y.Z; 3 when the version file is missing or cannot be parsed; 4 when --to is not greater than the current version (a mutating verb declines, rule 7)
old: hv-release-bump-version <file> <kind> <level|X.Y.Z>
shim: `--file` and `--kind` default to the `hv-release-detect-version` result (`kind` from the basename for an explicit `--file`; unknown basename without --kind exits 2). `from` is read before the call; `to` is the stdout version. `changed` is true on success. Old exit 1 is split by message as in `rota release version`, except that `must be greater` becomes 4 here.
note: the old third positional (`patch|minor|major|X.Y.Z`) becomes `--level` or `--to`, so a version and a level keyword cannot be confused; the file and kind positionals become optional flags.

### rota release host
rota release host
repo: scoped
data: {"host": string}
exit: implied only
old: hv-release-detect-host
shim: `host` is the stdout line: `github`, `github-enterprise`, `gitlab`, `gitlab-self-hosted` or `none`.

### rota release notes
rota release notes --from commits [--since <ref>]
rota release notes --from issues <MNN> [--since <ref>]
repo: scoped
data: {"from": string, "markdown": string, "empty": bool}
exit: 2 when --from is missing or not `commits`/`issues`, when `<MNN>` is missing with `--from issues` or given with `--from commits`, or at an umbrella root without --repo (issues); 3 when the milestone is unknown, or `--since` is not a resolvable ref; backend (`--from issues` is issue-only); 5 when git fails; tracker
old: hv-release-changelog-from-commits <ref>..HEAD  |  hv-release-notes-from-issues <MNN> [--since <ref>] [--repo <repo>]
shim: `--from commits`: `<ref>..HEAD` is the old range (or `HEAD` without --since). Section headings `## X` are rewritten to `### X`; `empty` is true when stdout was empty (the `(no commits in range)` note goes to stderr). `--from issues`: stdout is used as is. The old backend rc mapping applies.
note: headings are normalised to `###` for both sources, because the notes go under the `## v<version>` changelog heading; the old commits helper used `##` (including `### Stats`) and the issues helper `###`. Sections are `New`, `Fixed`, `Changed`, `Other` for issues, plus `Breaking`, `Performance`, `Documentation` and `Stats` for commits. Text mode prints the markdown verbatim.
note: `--since <ref>` replaces the commits helper's positional range and means `<ref>..HEAD` for both sources, so arbitrary ranges are gone and no `--since` means all history for commits.

### rota release changelog
rota release changelog <X.Y.Z> --body-file <path|-> [--path <file>]
repo: scoped
data: {"path": string, "version": string, "changed": bool}
exit: 2 when the version is not bare X.Y.Z or --body-file is missing; 3 when the notes file is missing; 4 when the changelog already has a section for this version
old: hv-release-update-changelog <X.Y.Z> <notes-file> [--path <file>]
shim: `--body-file -` is spooled to a temp file. `path` is the stdout line, `changed` is true on exit 0. Old exit 1 is split by message (section exists becomes 4, bad version becomes 2, notes missing becomes 3).
note: `--path` defaults to `CHANGELOG.md`; the release skill passes `release.changelogPath`. The inserted heading is `## v<X.Y.Z> — <date>`.

### rota release pending
rota release pending
repo: scoped
data: {"lastTag": string, "commits": number, "days": number, "thresholdCommits": number, "thresholdDays": number, "shouldNudge": bool, "reason": string, "message": string}
exit: 5 when git fails
old: hv-release-pending
shim: the old single-line JSON is passed through as `data`. `lastTag` is `""` and `reason` is `no-tag` when the repo has no tag. Old exit 1 becomes 5.
note: thresholds come from `release.nudgeAfterCommits` (10) and `release.nudgeAfterDays` (14).

### rota release milestone-check
rota release milestone-check <MNN>
repo: scoped
data: {"clear": bool, "blocked": [{"number": number, "title": string, "label": string}], "stillOpen": [{"number": number, "title": string}]}
exit: 1 when any issue is blocked (`clear: false`; open issues alone still exit 0); 2 at an umbrella root without --repo; 3 when the milestone is unknown; backend (issue-only); tracker
old: hv-release-milestone-check <MNN> [--repo <repo>]
shim: `blocked` and `stillOpen` come from the `blocked: #<n> <title> [<label>]` and `warning: #<n> <title> (still open)` lines, lowest number first. Old exit 6 becomes 1; otherwise the old backend rc mapping applies.
note: the data list of open issues is `stillOpen`, not `warnings`, so it cannot be confused with the envelope's `warnings`.

### rota release close-milestone
rota release close-milestone <MNN> --release <X.Y.Z>
repo: scoped
data: {"milestone": string, "release": string, "tag": string, "issues": number, "changed": bool}
exit: 2 when --release is missing or not bare X.Y.Z, or at an umbrella root without --repo; 3 when the milestone is unknown; backend (issue-only); tracker
old: hv-release-close-milestone <MNN> v<X.Y.Z> [--repo <repo>]
shim: prefixes `v` to `--release` for the old tag argument. `milestone`, `tag` and `issues` come from `closed-out <MNN> <tag>: <k> issues`; `changed` is true when k is greater than 0. The old backend rc mapping applies.
note: re-running is a no-op success. `changed` is true only if an issue was newly labelled, commented or closed, so a re-run reports `changed: false`. `release` is the bare X.Y.Z; `tag` is the tag name, `v` plus the release.
note: the flag is `--release` because it names what the flag holds: the release being closed out, whose tag is `v` plus it. Per the conventions `--version` is only a global alias before the first command word, so a verb flag `--version` would not clash; it would read as the plugin version.
