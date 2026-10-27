---
refs:
  - internal/tracker
  - internal/ship
  - internal/config
  - internal/cli/git.go
---

## A8: tracker, git, review, ship, release

### rota tracker call
rota tracker call [--provider auto|github|gitlab] -- <cli-arg>...
repo: scoped
data: {"provider": string, "exitCode": number, "stdout": string, "stderr": string}
exit: 1 when the forge CLI ran and exited non-zero (`data.exitCode` carries its code); 2 when no CLI args follow `--`; tracker (5 also when no provider resolves)
old: hv-tracker-call [--provider <p>] -- <cli-arg>...  (flags map 1:1; `--` is required in both)
shim: runs the old helper from the repo's directory (`-C`, `--repo`), captures stdout and stderr and its exit code. Old exit 1 with a usage message becomes 2; old exit 0 and CLI-own non-zero exits (old passes them through) become 0 and 1; old exit 3 becomes 5; old exit 4 becomes 6. `provider` is the resolved one (the `--provider` value, else `forge.provider` when it names github or gitlab, else the old detection).
note: debug passthrough; stdin is replayed only when a CLI arg is `-`, `@-` or `=-`, as before. Text mode writes the CLI's stdout to stdout and its stderr to stderr unchanged (exception to rule 8). No `changed`: the verb cannot know whether the call mutated the forge.
note: a non-zero CLI exit becomes exit 1 with the code in `data.exitCode`, because passing arbitrary codes through would break the exit table.
note: the tracker adapters of one rota process share an in-memory read cache (never persisted). A repeated read (`issue`/`pr`/`mr` `list`, `ls` or `view`, or a plain `api` GET) is served from it, only a zero exit is cached, and any other call except `auth` drops the whole cache before and after it, so a process never reads data older than its own last write. Long-running loops in one process (`round watch` forge polls, autopilot ticks) drop the cache at the start of each pass (`Deps.freshReads`), so a watch never serves old reads. A whole open `list` (no label, assignee or milestone filter) is also held as a complete list, and a later open `list` narrowed by labels alone (all labels must be present, names compared case-insensitively) is answered from it without a forge call; closed, assignee and milestone lists are not. A single `tracker call` is one process, so it always reaches the forge. A tracker verb (and the issues provider) whose config holds an invalid `forge.provider` or `issues.source` fails as an internal tracker error (exit 70) instead of defaulting; `issues.retryWaitSeconds` is gone, the retry pause is a fixed 60 s.

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
exit: 2 at an umbrella root without --repo; 3 when no base branch resolves, or when `git.baseBranch` is set and does not resolve
old: hv-base-branch
shim: runs in the repo's directory; `base` is the stdout line. Old exit 1 with `umbrella` in stderr becomes 2; other old exit 1 becomes 3.
note: `git.baseBranch` if set, then main, master, trunk, origin/HEAD when it is empty. A set `git.baseBranch` that does not exist is an error (exit 3), never replaced by a conventional name.
note: in a worker slot (`ROTA_SLOT`) the round routed to a milestone branch, `git base`, the branch-scoped verbs and `ship pr` use that branch as the base. `ship pr` passes it to `gh pr create --base` on GitHub, as it already did for GitLab (`PRNeedsBase` is true for both forges).

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
issue mode: when `issues.source` is a tracker, `referencedIds` are issue refs (`"#12"`): the number in a `<agent>/<N>-slug` branch name plus the `#N` after a closing keyword (Closes/Fixes/Resolves and -s/-d/-ed, any case, comma lists) in commit messages. Each resolves through the backend's get into an intent; a ref that does not resolve (offline, unknown) stays in `referencedIds` with no intent. File mode is unchanged (bracketed IDs, BACKLOG/ARCHIVE corpus).
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

### rota review depth
rota review depth [<branch>] [--labels <a,b>]
repo: scoped
note: reads the issue behind a `<agent>/<issue>-<slug>` branch from the tracker when the `ship.review` policy can use it (a `labels` map to match, or scope drift on a policy that can come out below `full`); `--labels` adds to its labels. The base depth comes from a matching label (the strictest wins, `none` is final), then `lightBelow` against the changed lines, then `default`. A one-way door (a `Door: one-way` line in the branch's open PR body), a diff touching files the issue never named (scope drift; docs, frozen records, golden files and smoke sections are ignored unless `supportPaths` replaces them) or a changed file matching a `paths` glob raises the depth, never lowers it.
data: {"branch": string, "base": string, "depth": "full"|"light"|"none", "signal": "label"|"door"|"scope-drift"|"path"|"size"|"default", "why": string, "changedLines": number, "labels": [string], "bundle"?: [string], "previousDepth"?: string, "depthNote"?: string, "cross"?: string, "crossWhy"?: string, "lenses": [{"name": "security"|"perf"|"ux", "why": string}]}
exit: 2 at an umbrella root without --repo; 3 when the branch does not exist; 1 when ship.review, review.crossLabels, review.lenses, or round.harnesses is malformed, the issue's labels cannot be read, or the forge cannot be reached to read the PR body (a local forge or a missing forge CLI has no PR to read, and `why` then notes that no Door line was read)
note: `signal` names the rule that decided. `bundle` lists the member issues when the branch is a bundled slot's (or queued PR's): each bundled issue is read like the lead, its depth decided on its own labels, and the highest wins, with every issue's declared files counting for scope drift. `previousDepth` and `depthNote` appear when the depth rose since the branch's newest recorded review set (`review depth <old> -> <new> (<why>): record the missing review verdict before gating`); the text prints the note on a second line.
note: (#1612) `cross` is the cross-harness review requirement, present only when `round.harnesses` is set, `review.crossLabels` is non-empty and the issue carries a label in `review.crossLabels` or a `review:<harness>`/`review:none` label; the issue is then read even when `ship.review` has no use for it. It is the harness that must record a `review-cross` verdict, or `none`: `review:none` (which beats `review:<harness>`), a `review:<harness>` label without a trigger label, or no reviewer harness in `round.harnesses` other than the author's. A `review:<harness>` label (`claude` or `codex`) picks the reviewer; otherwise it is the first `claude` or `codex` entry of `round.harnesses` that is not the author's harness: the kind of the slot holding the branch, else the issue's `harness:<kind>` label, else the first of `round.harnesses`, else `claude`. A `review:<x>` naming neither is not a routing label. In a bundle each issue resolves on its own labels and a harness beats `none`. `crossWhy` names the labels and setting that decided; the text prints `REVIEW-CROSS <branch> — <cross> (<crossWhy>)` on its own line.
note: (#2064) `lenses` is always present: the review lenses the branch triggers, in `security`, `perf`, `ux` order, each `why` being `path <file>` (the first changed file, renames counting both paths, matching a `review.lenses.<lens>.paths` glob) or `label <name>` (a label of the lead or any bundled issue in `review.lenses.<lens>.labels`). A lens with no match is absent, depth `none` lists none, and an empty `review.lenses` lists none. The issue is read when a lens has labels, even when `ship.review` has no use for it. The text prints `REVIEW-LENS <branch> — <lens> (<why>)` on its own line per lens.

### rota review scaffolding
rota review scaffolding [<branch>] [--base <branch>]
repo: scoped
data: {"findings": [{"file": string, "line": number, "text": string}]}
exit: 2 at an umbrella root without --repo; 3 when a named branch or base does not exist, or when no base resolves and `--base` is not given (hint: pass `--base <ref>`); 5 when git fails
old: hv-review-scaffolding [--repo <repo>] <base> <branch>
shim: passes `--base`, else `hv-base-branch` (no `main` fallback), and `<branch>`, else the current branch, as the old two positionals. Each stdout line `<file>:<line>:<text>` becomes one finding; empty output gives `{"findings": []}`.
note: findings are added diff lines matching `Task N`, `in flight`, `placeholder`, `added later` or `not yet wired`. Finding nothing is exit 0, as before.

### rota review package
rota review package [<branch>] [--base <ref>] [--since <sha>]
repo: scoped
data: {"path": string, "files": number, "bytes": number, "head": string}
exit: 2 at an umbrella root without --repo; 3 when the branch, base or `--since` commit does not exist, when the range is empty, when `--since` is not an ancestor of the branch, or when the branch and base share no history; 1 when the branch is the resolved base; 5 when git fails
old: none (new verb)
note: writes `<project root>/.rota/review/<branch>.md` (gitignored; `/` in the branch becomes `-`) with the commits, `git diff --stat` and `git diff -U10`, and prints the absolute path. The file is rewritten on every call. `data.head` is the branch sha resolved once and diffed; pass it to `rota verdict add --sha`.
note: the base need not be an ancestor of the branch tip: the diff is `base...branch`, so a base that moved on since the fork is fine. Only unrelated histories fail. `--since` is a point on the branch and must be an ancestor; the range is then `since..branch`.

### rota review prepare
rota review prepare <commit>
repo: scoped
data: {"changed": bool, "path": string, "head": string}
exit: 2 at an umbrella root without --repo; 3 when the commit cannot be resolved; 5 when archive creation, extraction or setup fails
old: none (new verb)
note: resolves the requested commit once, archives that SHA, and extracts into a fresh private temporary directory without registering a git worktree. `path` is the absolute extracted tree, `head` the resolved SHA; `.review/` is created for caller inputs. A tracked `.review` refuses setup. Archive creation and extraction are checked separately, and setup failures return no launchable result and attempt removal of only the new temporary directory. A failed removal is reported with its path. No live-checkout fallback exists. The caller copies its inputs before launching and uses `path` as the reviewer cwd. This cwd is disposable, not an OS sandbox: absolute-path access and ambient user configuration remain reachable.

### rota review cleanup
rota review cleanup <workspace>
repo: none
data: {"changed": bool, "path": string, "head": string}
exit: 4 when the path is not an owned prepared workspace; 5 when removal fails
old: none (new verb)
note: accepts only the absolute `path` returned by preparation under the current temporary directory, with a matching ownership record outside the extracted tree. Removes that workspace's private temporary parent and no other workspace; symlinks inside it are removed without following their targets. Missing, malformed or symlinked ownership records and symlinked parent paths refuse. A cleanup failure is surfaced, never retried against a broader path. Call after reviewer success, failure or cancellation, with the same temporary-directory environment as preparation.

### rota review probe
rota review probe -- <command> [<arg>...]
repo: none
data: {"changed": bool, "exitCode": number, "stdout": string, "stderr": string}
exit: 2 without a command after `--`; 1 when the command exits nonzero (its exit code is in `data.exitCode`); 4 on a support machine (arbitrary-command fence); 5 when temporary setup, process launch or cleanup fails
old: none (new verb)
note: runs the command as an argument vector in the current directory, with empty stdin. Uses a fresh private temporary root for `HOME`, `XDG_CONFIG_HOME`, `XDG_CACHE_HOME`, `XDG_DATA_HOME`, `XDG_STATE_HOME`, `ROTA_CONFIG_DIR`, `GH_CONFIG_DIR`, `CODEX_HOME`, `CLAUDE_CONFIG_DIR` and `OPENCODE_CONFIG_DIR`, replacing inherited values. Each directory is created before launch; setup failure never launches the command. Other environment variables are inherited. The subprocess is bounded by the normal process timeout and runs in its own process group for cancellation. Output is captured in the result; text mode prints stdout and stderr. Removes the temporary root after success, nonzero exit, launch failure or cancellation, and reports a cleanup failure with its path. Use one shell command for multi-step probes that need the same temporary config state. The managed reviewer briefs require this operation for binary probes, using the caller's installed binary; harness launch and authentication are unchanged.
note: this is environment isolation, not an OS sandbox. Unsandboxed commands can still access arbitrary absolute paths, expand tildes through passwd, and inspect process environments. A command that explicitly overrides the temporary config roots defeats that isolation.

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
note: PR creation never removes a worktree; it can run inside a round slot or cycle worktree, including one with uncommitted files.
data: {"branch": string, "url": string, "provider": string, "number"?: number, "items": []string, "reports"?: [{"kind": string, "sha": string, "report": "posted"|"pending", "reportReason"?: string}], "report"?: "posted"|"pending", "reportReason"?: string, "changed": bool}; failure data on exit 4 is the verdict-refusal shape (B3)
exit: 2 when --title or --body-file is missing or the body is empty, or at an umbrella root without --repo; 3 when an --items item is unknown, the branch does not exist, or no base branch resolves; 4 when a FAIL verdict blocks the branch (B3; checked after the --items lookup and before the push); 4 when the origin names no forge and `forge.provider` names none (`blockedBy: forge`); 5 when the push fails; tracker
old: hv-pr [--repo <repo>] [--closes <ID,ID>] <branch> <title> < <body-file>    (`--items` is the old `--closes`)
shim: `--body-file` goes to the old stdin; `--title` is the second positional. `url` is the last stdout line, `number` the trailing digits of the URL, `provider` from the URL host. Old exit 1 is split by message (missing body becomes 2, unknown item becomes 3, CLI error becomes 5); old exit 3 becomes 5; old exit 4 becomes 6.
note: in issue mode `--items` appends `Closes #<n>` lines to the PR body; in file mode it is accepted and ignored. An unknown item fails before the push, and `data.items` echoes the IDs as given.
note: after opening the PR, retries the latest local verdict reports against that PR, including reviews recorded before it existed. When records exist, `reports` lists their delivery and `report` summarizes it. Posting failures leave the PR open and the verb successful, with `report: pending`, `reportReason` and a warning naming `rota verdict post <branch>` for retry. With no verdict records, no report fields or report tracker calls are added.
note: old `--closes` is renamed `--items`, the one item-ID list flag (rule 11).
note: the provider is the forge rule's, as an actor: an origin that names neither github nor gitlab is refused, where the old helper fell back to `github`. `forge.provider: local` (or `auto` with no origin remote) means direct merge: there is no PR to open.
note: no resolvable base branch is exit 3 (`resolution`, conventions); the shim maps the old helper's failure there to 5.
note: in umbrella issue mode `--items` resolves inside the sub-repo the PR opens in (`--repo`, else the one the working directory is in); an item of another sub-repo is unknown there (3), and with neither the verb exits 2.

### rota ship merge
rota ship merge <branch> --body-file <path|-> [--confirm --confirm-note <answer>]
repo: scoped
note: worktree cleanup requires a matching `(branch, repo, worktree)` entry in `status.json`, as recorded by `/rota-work`. Cleanup refuses with exit 4 and `blockedBy: "worktree"` before merging when the worktree belongs to a registered round slot, contains the current directory (including nested or symlink paths), lacks that ownership record, or has modified, staged, untracked or ignored files. Run from outside the cycle worktree. Cleanup never uses `--force` or guesses ownership from a path or branch name.
data: {"branch": string, "base": string, "sha": string, "changed": bool}; failure data on the B3 refusal is the verdict-refusal shape
exit: 2 when --body-file is missing or empty, at an umbrella root without --repo, a confirmation flag is given without the other, or `ship.mergeApproval` is not `none`, `all` or `paths`; 4 when a FAIL verdict blocks the branch (B3; checked after the base resolves, before the merge-approval gate and before any change); 4 when `ship.mergeApproval` covers this merge and the `merge-approval` gate is not cleared (B1; checked after the base resolves and before the worktree is cleared, so a refusal changes nothing); 3 when the branch does not exist or no base branch resolves; 4 when the merge conflicts (the merge is aborted, so the tree is left as it was and `changed` is false) or the branch is the base branch; 5 when git fails otherwise
old: hv-merge [--repo <repo>] <branch> < <body-file>
shim: `--body-file` goes to the old stdin. `sha` is the last stdout line (`git log -1 --format=%h`). `base` is `hv-base-branch`. The shim removes the branch's worktree first (the old `hv-worktree-clear` pre-step, which runs inside the old helper), then merges, deletes the branch. Old exit 1 is split by message.
note: the merge message is the body; its subject must start `merge: ` for `rota ship undo` to recognise the cycle. The old helper did not check it, and the contract does not either. Mutation is `git checkout <base>`, `git merge --no-ff`, `git branch -d`.

### rota ship pr-merge
rota ship pr-merge <pr> [--items <ID>[,<ID>…]] [--confirm --confirm-note <answer> | --approval <escalation> | --escalate]
repo: scoped
data: {"pr": number, "sha": string, "closed": []string, "changed": bool}; failure data on exit 4: {"pr": number, "merged": bool, "unproven": []string, "changesRequested": []string, "changed": bool}, or the manual-gate shape (B1)
exit: 2 when <pr> is not all digits, at an umbrella root without --repo, a confirmation flag is given without the other, or `ship.mergeApproval` is invalid; 4 when a FAIL verdict blocks the PR's head branch (B3; checked once the PR and its items resolve, before the merge-approval gate and the proof check, so a refusal records no `changes-requested`; failure data is the verdict-refusal shape plus `pr`); 4 also when `ship.mergeApproval` covers this PR and the `merge-approval` gate is not cleared (B1; checked before the proof check, so a refusal records no `changes-requested`); 4 also when an open linked item with explicit `AC-n` criteria has one unmet (`blockedBy: "unmet acceptance"`; checked after the merge-approval gate and before the proof check, so the item is not set to `changes-requested`; an already closed item is skipped); 3 when the PR or an item is unknown, or the PR is not open (closed or already merged); 4 when the merge fails or an item has no proof (the item is set to changes-requested); backend (issue-only); tracker; with `--approval`, 2, 3 or 4 as "C5: merge policy and approval requests" lists, and two of `--approval`, `--escalate`, `--confirm` together is 2
old: hv-pr-merge <pr> [--items <ID,ID>] [--repo <repo>]
shim: `sha` and `closed` come from `merged <pr> as <sha7>` and the `closed <ID>` lines. Old exit 1 is split by message (non-digit PR becomes 2, unknown PR or item becomes 3, tracker failure becomes 5); old exit 5 (unproven) becomes 4; otherwise the old backend rc mapping applies.
note: unproven items still move to `changes-requested` on exit 4; the failure `data` lists them in `unproven` and `changesRequested` with `changed: true` (conventions).
note: an unproven item was old exit 5 and is now 4, the table's "proof is missing".
note: in umbrella issue mode the verb acts on scope S (`--repo`, else the sub-repo the working directory is in; the umbrella root without `--repo` is exit 2), and an `--items` reference that belongs to another sub-repo is exit 2 (`usage`); the shim maps the old message to 5.

### rota ship strategy
rota ship strategy
repo: scoped
data: {"strategy": "direct"|"pr"|"unset", "forge": string, "reason": string}; failure data on exit 3: {"strategy": "", "forge": string, "changed": false}
exit: 2 when an argument is given; 3 when issues live on a tracker (`issues.source` is not `file`) but the forge is local, so no PR can close them (set `forge.provider` or an origin remote); 70 when `issues.source` or `forge.provider` holds an invalid value
note: how a finished branch lands. `/rota-ship` and `/rota-work` read it instead of `work.mergeStrategy` alone. Issues on a tracker give `pr`; a local forge (`forge.provider: local`, or `auto` with no origin remote) gives `direct` whatever `work.mergeStrategy` says; otherwise `work.mergeStrategy`, or `unset` when it is not set.
note: an origin that names no known forge leaves `forge` as `unknown` and the configured strategy stands.

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
exit: 4 when the origin names no forge and `forge.provider` names none (`blockedBy: forge`)
old: hv-release-detect-host
shim: `host` is the stdout line: `github`, `github-enterprise`, `gitlab`, `gitlab-self-hosted` or `none`.
note: the forge is the forge rule's, as an actor. The canonical host (`github.com`, `gitlab.com`) splits `github` from `github-enterprise` and `gitlab` from `gitlab-self-hosted`; the local forge is `none`.

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
data: {"clear": bool, "blocked": [{"number": number, "title": string, "label": string}], "stillOpen": [{"number": number, "title": string}], "check": {"faults": number, "uncovered": number, "ok": bool} | null}
exit: 1 when any issue is blocked (`clear: false`; open issues alone still exit 0); 2 at an umbrella root without --repo; 3 when the milestone is unknown; backend (issue-only); tracker
old: hv-release-milestone-check <MNN> [--repo <repo>]
shim: `blocked` and `stillOpen` come from the `blocked: #<n> <title> [<label>]` and `warning: #<n> <title> (still open)` lines, lowest number first. Old exit 6 becomes 1; otherwise the old backend rc mapping applies.
note: the data list of open issues is `stillOpen`, not `warnings`, so it cannot be confused with the envelope's `warnings`.
note: the last text line is `check:`, informational only and never a cause of exit 1: `check: clean`, or `check: <n> fault(s), <m> uncovered` from the same report as `rota milestone check <MNN>` (non-strict). `faults` counts the structural findings (missing `## Change`, unknown criterion, REMOVED without Reason or Migration, bad Touches, unlisted capability), `uncovered` the criteria no feature serves, `ok` is both zero. When the milestone body or its items cannot be read the line is `check: unavailable` and `check` is `null`.

### rota release close-milestone
rota release close-milestone <MNN> --release <X.Y.Z>
repo: scoped
data: {"milestone": string, "release": string, "tag": string, "issues": number, "changed": bool}
exit: 2 when --release is missing or not bare X.Y.Z, or at an umbrella root without --repo; 3 when the milestone is unknown; backend (issue-only); tracker
old: hv-release-close-milestone <MNN> v<X.Y.Z> [--repo <repo>]
shim: prefixes `v` to `--release` for the old tag argument. `milestone`, `tag` and `issues` come from `closed-out <MNN> <tag>: <k> issues`; `changed` is true when k is greater than 0. The old backend rc mapping applies.
note: re-running is a no-op success. `changed` is true only if an issue was newly labelled, commented or closed, so a re-run reports `changed: false`. `release` is the bare X.Y.Z; `tag` is the tag name, `v` plus the release.
note: the flag is `--release` because it names what the flag holds: the release being closed out, whose tag is `v` plus it. Per the conventions `--version` is only a global alias before the first command word, so a verb flag `--version` would not clash; it would read as the plugin version.
