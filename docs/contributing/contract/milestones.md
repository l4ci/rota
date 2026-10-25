---
refs:
  - internal/milestone
  - internal/cli/milestone.go
---

## A6: milestones, plans, designs, spikes, proof, debug

### rota milestone add
rota milestone add --title <text> --summary <text> [--depends M01,M02] [--kind <delivery|decision>]
repo: none
data: {"id": string, "changed": true}
exit: 1 when `--kind` is not `delivery` or `decision`; 2 when --title or --summary is missing; tracker
old: hv-vision-add "<--title>" "<--summary>" ["<--depends>"]    (title, summary, depends-csv were positionals 1, 2, 3)
shim: wraps the printed ID (`M01`) as `id`; old rc 2 and 3 both become 5, rc 4 becomes 6.
note: `--kind decision` marks a milestone of decision items (`decision:research`, `decision:grilling`): the plan gets `kind: decision` in its frontmatter plus `## Decisions` (one line per closed decision, written by `rota item complete --gist` and `rota round accept --gist`) and `## Not yet specified` (the fog). `--kind delivery` (the default) leaves the plan byte-identical to before. A decision milestone ships under the usual rule.
note: a new plan carries `## Capabilities` (one contract per bullet, tagged `(new)`, `(modified)` or `(removed)`) and `## Acceptance` with `- [ ]` criteria, as `milestone check` reads them.
note: the key is minted, so `add` takes no positional and never exits 4 for an existing key; `--depends` IDs are not validated, as old.
note: at an umbrella root in issue mode the milestone lives on the home sub-repo (`issues.homeRepo`, else the first registered): its tracking issue, plan text and slice plans. `add` mints the ID over the native milestones of every sub-repo and creates the native milestone on the home repo only (other sub-repos get theirs on first use of the ID). `list` and `active` report `shipped` only while every sub-repo's native milestone of it is closed, else `active`, and `ready` follows the adjusted status. `status` moves the home tracking issue, then opens or closes the native milestone in every other sub-repo that has one. `show` and `put` act on the home plan. No `--repo` is needed for any of these.

### rota milestone list
rota milestone list
repo: none
data: {"milestones": [{"id": string, "title": string, "status": string, "depends": []string, "ready": bool}]}
exit: tracker
old: hv-vision-list
shim: wraps the printed JSON array as `milestones`, keys unchanged. `status` is one of planned|active|shipped|archived.

### rota milestone show
rota milestone show <id>
repo: none
data: {"id": string, "kind": "delivery"|"decision", "body": string}
exit: 2 when <id> is not `M\d{2,}`; 3 when the milestone doesn't exist; tracker
old: hv-vision-show <id>
shim: stdout (the markdown) becomes `body`. Text mode prints the body verbatim. Frontmatter line 4 stays `status:` (smoke 61 reads it).

### rota milestone put
rota milestone put <id> --body-file <path|->
repo: none
data: {"id": string, "changed": bool}
exit: 2 when <id> is malformed, --body-file is missing or the body file is unreadable; 3 when the milestone doesn't exist (hint: rota milestone add); 4 when (file mode) the body has no frontmatter `id:` equal to <id>; tracker
old: hv-vision-put <id> --body-file <path|->
shim: `changed` compares `hv-vision-show <id>` before and after.
note: file mode: after writing, it regenerates the `## Items` section (a body without one gets it back).
note: there is no `milestone rm`, because milestones end as `archived` via `milestone status`.
note: in issue mode the label stays authoritative for status, so a body whose `status:` differs from the label is accepted.

### rota milestone overview
rota milestone overview --body-file <path|->
repo: none
data: {"changed": bool}; on exit 4 `{"blockedBy": "heading in body", "changed": false}`
exit: 2 when --body-file is missing, unreadable or the text is blank; 3 when `.rota/MILESTONES.md` doesn't exist (hint: rota milestone add); 4 when the text contains a heading line (`# ` or `## `)
old: none (new in T4)
note: replaces only the overview text of MILESTONES.md: everything between the optional `# ` title line and the first `## ` heading. The title, the `## Active milestones` list and the milestone entries are untouched, and `milestone index` keeps the new text. The file is tracked in file and issue mode alike, so the verb needs no tracker and does not branch on mode. `changed` is false when the text is already in place. Surrounding blank lines in the body are trimmed.

### rota milestone status
rota milestone status <id> --to <planned|active|shipped|archived> [--force] [--branch keep|delete]
repo: none
data: {"id": string, "status": string, "changed": bool}; on exit 4 `{"blockedBy": "unfinished milestone", "changed": false, "open": []string, "unticked": []string}`
exit: 2 when <id> is malformed or --to is missing or not one of the four; 3 when the milestone doesn't exist; 4 when `--to shipped` and the milestone still has an open child item (the set `milestone check` reads) or an unticked `- [ ]` criterion in its own body, with a hint naming `rota milestone check <id>` and `--force`; 4 when a branch step is refused: the push of `milestone/<id>` was rejected (including a protected branch; the message names the branch and the status is unchanged), or `--to planned` from active finds `milestone/<id>` holding commits that are not on the base; 5 when origin cannot be read for the branch (`ls-remote` or `fetch` fails; unavailable); 2 when `--to archived` finds `milestone/<id>` on origin and `--branch` is missing, or `--branch` is not keep or delete; tracker
old: hv-vision-status <id> <to>    (--to was positional 2)
shim: `changed` compares the `status:` line (`hv-vision-show`) before and after. The old helper also runs hv-vision-index on every call, and the new verb keeps that side effect, with `changed: false` too.
note: `--force` skips the ship audit. `planned`, `active` and `archived` never run it. The audit does not look at proof rows per item.
note: the milestone branch. Projects with an origin remote keep `milestone/<id>` there; no origin or no git repository means no branch step. `--to active` cuts it from the resolved base branch, `origin/<base>` when that exists (`git.baseBranch`, else main, master, trunk, origin/HEAD) and pushes it before the status is written; an existing branch is reused as it is, and a base that does not resolve is exit 3. `--to shipped` deletes it after the status is written. `--to archived` needs `--branch`: `keep` renames it to `archive/milestone/<id>`, `delete` removes it (the verb never prompts, so the answer is a flag); with or without a branch, the milestone's open items lose its tag, leaving any other milestone tag in place. `--to planned` from active deletes the branch unless it holds commits not on the base, which is refused; the delete runs before the status write, so a failed delete leaves the status active and a rerun converges. A shipped or archived delete that fails after the status was written returns the error and the status stays; running the same command again repeats the step.
note: issues of an active milestone route to its branch: `rota round assign` cuts the worker branch from `origin/milestone/<id>` and records it as the slot's base, and the worker's PR opens against it. `base-drift` in `round reconcile --apply` handles a re-tag after assign (`rounds.md`).
note: the status value moves from a second positional (the tree's `status <id> <s>`) to `--to`, per rule 1.

### rota milestone active
rota milestone active
repo: none
data: {"ids": []string}
exit: tracker (the old helper's pipe failure becomes a real exit)
old: hv-vision-active
shim: splits stdout on newlines into `ids`, and `[]` when stdout is empty.

### rota milestone index
rota milestone index
repo: none
data: {"changed": bool}
exit: tracker
old: hv-vision-index
shim: `changed` compares a hash of `.rota/MILESTONES.md` and CLAUDE.md before and after. The old helper's stdout (managed-block output) is dropped.
note: with no active milestone the vision block's intro is `Project milestones live in .rota/MILESTONES.md.` in file mode and, in issue mode, says milestones are tracker milestones with a tracking issue (`rota milestone show MNN`) and `.rota/MILESTONES.md` is the index. In issue mode a MILESTONES.md that still holds `shipped` or `archived` file-mode entries counts as history, so the block reads `no active milestones` rather than `no milestones yet`.

### rota migrate milestones
rota migrate milestones [--apply]
repo: none
data: {"applied": bool, "milestones": [{"id": string, "changed": bool}], "changed": bool}
exit: 2 on a positional argument; 3 when the milestone store can't be opened; tracker
old: none (new)
note: renames `## Acceptance criteria` to `## Acceptance` and prefixes every bare top-level bullet in that section with `[ ] ` (an indented sub-bullet stays detail under its criterion), so the acceptance package can number the criteria AC-n as it does for issues. Nothing else in the body changes. Preview by default: without `--apply` it prints `<id>: would migrate` and writes nothing. `changed` is true only when `--apply` rewrote at least one body. Idempotent: a second run prints `nothing to migrate`. It goes through the milestone store, so it works on `.rota/milestones` files and on tracker milestones alike.

### rota migrate milestone-files
rota migrate milestone-files [--apply]
repo: none
data: {"applied": bool, "files": [string], "changed": bool}
exit: 2 on a positional argument; 4 in file mode (`blockedBy: backend`), where the files are the milestone store; tracker
old: none (new)
note: with milestones on the tracker no verb reads `.rota/milestones/*.md`, so files left from file mode mislead (`milestone show` says not found while the file sits there). `rota doctor` warns (`milestones`) when they exist. This verb moves them to `.rota/archive/milestones/`. Preview by default: without `--apply` it prints `would archive <file>` and writes nothing. Idempotent: a second run prints `nothing to archive`. Both `.rota/milestones` and `.rota/archive` are tracked, so after `--apply` the tree holds deletions plus untracked files: the verb warns `commit .rota/milestones and .rota/archive/milestones` and leaves the commit to the caller.

### rota milestone check
rota milestone check <MNN> [--strict]
repo: none
data: {"milestone": string, "ok": bool, "strict": bool, "belowThreshold": bool, "features": int, "criteria": [string], "uncovered": [string], "noChange": [string], "unknown": [{"item": string, "ids": [string]}], "noMigration": [{"item": string, "fields": [string]}], "badTouches": [{"item": string, "entries": [string]}], "badCapability": [{"item": string, "capability": string}], "children": {"changed": bool, "count": int, "added": [string], "removed": [string]}, "itemsRegenerated": bool}
exit: 0 when ok, or when only uncovered criteria remain and --strict is off; 1 otherwise; 2 when <id> is malformed; 3 when the milestone doesn't exist; tracker
old: none (new)
note: it reads the milestone's `## Acceptance` criteria and `## Capabilities`, then every feature issue tagged with the milestone (bugs and tasks are ignored). It needs three feature issues; with fewer it prints `below threshold: N feature items, 3 needed`, sets `belowThreshold`, and reports nothing else.
note: `noChange` lists features with no `## Change` section or a first line that is not `ADDED|MODIFIED|REMOVED|RENAMED <capability>`. `unknown` lists features whose `Serves:` cites an AC id the milestone lacks. `noMigration` lists REMOVED features missing a `Reason:` or `Migration:`, with the empty field names. `badTouches` lists `## Touches` entries that are not a listed capability, compared normalised (case and backticks ignored). `badCapability` lists features whose `## Change` line names a capability the milestone does not list, compared the same way; a RENAMED line reads `RENAMED <old> -> <new>` and names the old capability before ` -> `. `uncovered` lists criteria no feature serves: a warning by default, a failure with `--strict`. Item ids print as `rota backlog ids` returns them.
note: on the tracker backend it also regenerates the tracking issue's generated child list (see `rota item create --milestone`) and adds `data.children`: `count` issues listed, `changed` true when the list was rewritten, `added` and `removed` the refs (`#12`) the list gained and lost against the native milestone (drift). A drift line `children: list regenerated (added ...; removed ...)` joins the text output. The verdict and exit code ignore the list; a failure to regenerate is a warning. `data.children` is absent on the file backend and at an umbrella root.
note: the child list is a `<!-- rota:children -->` ... `<!-- rota:/children -->` block in the tracking issue body, after the plan and before the fields block: `- #N title` per issue in the native milestone, open and closed, in issue order, the tracking issue itself left out. `rota item create --milestone` and `rota item set milestone` (old and new milestone) refresh it; `milestone show` and `milestone put` leave it out and keep it. It adds no sub-issue, epic or task-list link: the native milestone stays the back-reference.
note: file backend only: before reading, it regenerates the `## Items` section of every milestone file (see below). When milestone `<MNN>`'s section had drifted it sets `itemsRegenerated: true` in `data` (the key is absent otherwise, and on the tracker backend) and prints a line saying so; drift does not change the exit code. It writes the tracked milestone files, so it is a write verb in file mode.
note: a file-mode milestone file carries a generated `## Items` section: one bullet per item tagged with it, open items first in backlog order, then completed ones (`## Completed`, then `ARCHIVE.md`), `- [[B07] title](../bugs/B07.md)` (plain `- [B07] title` when the item has no detail file). A completed item carries its closure reason as a trailing mark, `- [[B07] title](../bugs/B07.md) (done)`, `(dropped)`, `(handed-off)` or `(blocked)`; `rota backlog ids --milestone` still returns open items only. `rota item create --milestone` (and `--raw-file` with a `Milestone:` field), `rota item field set <ID> milestone`, `rota item complete`, `rota item reopen`, `rota item rm` and `rota milestone put` rewrite it for every milestone file, so it agrees with the items' `Milestone:` field (each file's rows are read inside its lock, under the backlog locks, and one milestone that fails to sync does not stop the rest); `milestone put` keeps the section even when the new body has none. Text outside the section is kept; a milestone with no items and no section is left untouched. Tracker backends keep this list on the tracking issue.

### rota plan add
rota plan add <milestone>-<unit> --title <text> [--design <ID>] [--repos a,b]
rota plan add <#N|B7> --title <text> [--design <ID>] [--repos a,b]    (issue mode only: no milestone)
rota plan add --milestone <M01> --slice --title <text> [--design <ID>] [--repos a,b]
repo: none
data: {"key": string, "unitKind": "slice"|"item", "changed": true}
exit: 2 when the key is malformed, --title is missing, <ID> is not `[BFT]\d{2,}`, a key is given together with `--slice` or `--milestone`, or `--slice` has no `--milestone`; 3 when the --design document doesn't exist (file mode), a --repos name isn't registered in repos.json (the message names it), or (issue mode) a slice plan's milestone has no tracker; 4 when the key already exists; tracker
old: hv-plan-add [--repo <repos, joined ", ">] [--design .rota/designs/<ID>.md] <milestone> <unit|slice> "<title>"    (the key splits on its first `-`; `--slice` becomes the literal unit `slice`; flags go before the positionals)
shim: prints the minted key (`M01-S03`) as `key`; `unitKind` is `slice` when the old unit was `slice` or `S\d+`, else `item`.
note: `--design` takes `[BFT]\d{2,}` in file mode. In issue mode it takes an issue number (`3`) or the lettered form with any digit count (`F3`), matching issue-mode IDs (a number string, the letter in the type). Maintainer ruling, relayed by the orchestrator, round 3.
note: the first form names the key, like `show`, `put` and `rm`. `plan add --milestone M01 --slice` mints the next `S<NN>` and `data.key` returns the real one; it replaces the old `M01-slice` pseudo-key.
note: slice plans (`--slice`, an `S<NN>` key) live on the milestone tracking issue, so at an umbrella root in issue mode `plan add|show|put|rm|list` for a slice act on the home sub-repo. Item plans resolve through the item as usual.
note: the old `--repo` free-text tag list is renamed `--repos`, because `--repo` is global. It is a comma list (rule 2); the shim joins it with `, `.
note: `--design` takes a design ID instead of a `.rota/designs/…` path, and the shim builds the path.
note: in issue mode an item key may omit the milestone (`#7`, `F7`; a `#` needs quoting in a shell). `data.key` echoes the key as given, and `show`, `put` and `rm` accept the same forms. File mode still needs `M01-B07`.
note: in issue mode an item plan is a note on the item's issue, so its milestone is never validated; only a slice plan needs the milestone's tracker issue.

### rota plan list
rota plan list [--milestone M01]
repo: none
data: {"plans": [{"key": string, "milestone": string, "unit": string, "unitKind": "slice"|"item", "title": string, "status": string, "created": string, "repos": []string}]}
exit: 2 when --milestone is malformed; tracker
old: hv-plan-list [<--milestone>]
shim: wraps the JSON array as `plans`, and splits the old `repo` string ("web, api") on `, ` into `repos` (`[]` for ""). In issue mode the helper's stderr note (`item plans live on their issues …`) becomes one `warnings` entry, and only slice plans are listed.
note: the old positional filter becomes `--milestone`, because a list verb takes no key.

### rota plan show
rota plan show <key>
repo: none
data: {"key": string, "body": string}
exit: 2 when <key> is malformed; 3 when the plan doesn't exist (stdout stays empty, so callers can test emptiness); tracker
old: hv-plan-show <key>
shim: stdout becomes `body`.

### rota plan put
rota plan put <key> --body-file <path|->
repo: none
data: {"key": string, "changed": bool}
exit: 2 when <key> is malformed, --body-file is missing or unreadable; 3 when the plan doesn't exist (hint: rota plan add); tracker
old: hv-plan-put <key> --body-file <path|->
shim: `changed` compares `hv-plan-show <key>` before and after.
note: every plan verb takes the key shape `M\d{2,}-(S\d+|[BFT]\d+)` (issue mode also `#N` and `[BFT]\d+` for item plans), tighter than the `[A-Z]\d+` old `put` and `rm` accepted.

### rota plan rm
rota plan rm <key>
repo: none
data: {"key": string, "changed": true}
exit: 2 when <key> is malformed; 3 when the plan doesn't exist (not an idempotent no-op, because smoke 12/58 expect failure); tracker
old: hv-plan-rm <key>

### rota plan pass
rota plan pass <key> <AC-id> --proof <sha>:<check>
repo: none
data: {"key": string, "item": string, "ac": string, "proof": string, "changed": bool}
exit: 2 when <key> is malformed or a slice key, <AC-id> is not `AC-<n>`, the item has no such criterion or repeats an id, or `--proof` lacks a sha or a check; 3 when the item doesn't exist; 4 when no proof row matches `<sha>:<check>` or the latest match is FAIL (data `{"blockedBy": "proof", "changed": false}`), and under the file backend (data `{"blockedBy": "backend", "changed": false}`); tracker
old: none (new)
note: the only way to mark an acceptance criterion met. The mark goes in the item's `acceptance` note (`- AC-<n> · <date> · <sha> · <check> · <criterion text>`), never in the body. `--proof` splits on the first `:`, so the check may contain colons; it must equal a `check` that `rota proof show` prints, and the sha matches a row's sha as a prefix either way (4 characters or more). The latest matching row decides, so a FAIL recorded after a PASS refuses. Re-passing a criterion replaces its mark; `changed` is false when nothing differs.
note: `<key>` names the item (`M01-B07` is B07; `#7` and `B7` also work); the issue backend is required because item notes live there. A body captured without ids is numbered (`AC-1`, `AC-2`, … in order, existing ids kept) in the same call, after the proof check passes. `rota item create` numbers new bodies on the issue backend.
note: the `acceptance` note kind is reserved: `rota item note show` reads it, `rota item note add` and `rm` exit 2.

### rota plan check
rota plan check <key>
repo: none
data: {"key": string, "ok": bool, "criteria": [string], "uncovered": [string], "orphans": [string], "unknown": [{"task": string, "ids": [string]}], "noVerify": [string], "warnings": [string]}
exit: 0 when `ok`, warnings or not; 1 when any list is non-empty, and under the file backend (data `{"blockedBy": "backend", "changed": false}`); 2 when <key> is malformed or a slice key; 3 when the plan or item doesn't exist; tracker
old: none (new)
note: read-only; never writes and never exits 4. Holds the plan's Tasks against the item's `## Acceptance` ids (a body without ids is read as `rota plan pass` would number it). A task is a top-level `- **T<n>**` bullet; its `Serves: AC-1, AC-2` sub-bullet names the criteria it delivers and its `Verify:` sub-bullet (inline text or nested bullets, not a `_(placeholder)_`) the check. `uncovered`: criteria no task serves; `orphans`: tasks with no `Serves:` id; `unknown`: tasks naming an id the item lacks; `noVerify`: tasks without a Verify step. When the item has `decision` comments (#927), they are numbered `D1`, `D2` oldest first and three keys appear: `decisions` (each as `D<n>: <first 60 characters>`, also printed as text lines), `uncoveredDecisions` (decisions no task cites in a `Honors: D1, D2` sub-bullet, a placeholder skipped) and `unknownDecisions` (`{task, ids}` for a cited id the item lacks); a non-empty list of either fails with exit 1. An item with no decision comment has none of the three keys and the same exit code as before, unless a task cites a `D<n>` anyway, which lists as unknown. `warnings` (#922) are advisory size findings that never affect `ok` or the exit code, each also printed as a `warn: ` text line: one when the Tasks section holds more than `plan.maxTasks` top-level tasks (default 3), and one per task whose `Files:` line (inline list or nested bullets, placeholders skipped) names more than `plan.maxFilesPerTask` paths (default 5); `0` turns that warning off. Both keys are non-negative integers, which `config set` enforces; a hand-edited bad value fails the check with exit 1. `<key>` resolves as for `plan pass`. `/rota-work` runs it before dispatch and stops on exit 1, but only on the issue backend and for a plan with at least one `Serves:` line; legacy plans and file-mode projects skip it.

### rota plan validate-docs
rota plan validate-docs <key>
repo: none
data: {"key": string, "valid": bool, "mismatches": [{"path": string, "targetRepo"?: string, "issue": string, "suggestion"?: string}]}
exit: 2 when <key> is malformed; 3 when the plan file doesn't exist; backend (file-only); 70 when the plan has no parseable frontmatter (old 2)
old: hv-plan-validate-docs <key>
shim: empty stdout gives `valid: true, mismatches: []`. Otherwise it parses each `  - <path>` block: `target repo: <r>` becomes `targetRepo`, the issue line becomes `issue`, and `suggested alternative: <x>` becomes `suggestion`. Text mode prints the old stdout unchanged.
note: a classifier, not a check: mismatches exit 0 with `valid: false` and no `warnings`, because they block nothing and exit 1 would only break `set -e` callers.

### rota plan rename-check
rota plan rename-check <old> [-- <pathspec>…]
repo: none
data: {"files": []string}
exit: implied only. No match, a non-git directory and git failures all give `files: []`, as the old helper swallowed them.
old: hv-plan-rename-check <old> [<pathspec>…]    (pathspecs after `--` are the old trailing scope args; runs in the -C/cwd directory, not the project root)
shim: splits stdout lines into `files`.
note: `<old>` stays a git-grep basic regex, as before. Pathspecs follow the literal `--` that the conventions already define, which keeps them out of flag parsing.

### rota plan uncertain
rota plan uncertain <ID>
repo: none
data: {"id": string, "type": "B"|"F"|"T", "uncertain": bool, "reasons": []string}
exit: 1 when certain (`uncertain: false`, `reasons: []`); 3 when the item doesn't exist or BACKLOG.md is missing; tracker; 70 when the backend config is invalid (old 1)
old: hv-uncertain <ID>
shim: maps old rc 0 → 0, 1 → 1, 2 → 3 (or 5 when stderr says backend unavailable), 3 → 5, 4 → 6. stdout lines become `reasons`: `no detail file`, `multiple open-question signals`, `no concrete identifiers (unknown surface)`. Non-Major items return certain with no reasons.
note: `uncertain` is the passing answer (exit 0), because it is the case callers branch on, so rota-work keeps `if rota plan uncertain`.

### rota design add
rota design add <ID> --title <text>
repo: none
data: {"id": string, "type": "B"|"F"|"T", "changed": true}
exit: 2 when <ID> isn't `[BFT]\d{2,}` (issue mode `[BFT]\d+`; S01 and M01 are rejected) or --title is missing,; 3 when (issue mode) the item doesn't exist; 4 when the design already exists; tracker
old: hv-design-add <ID> "<title>"
shim: stdout (the ID) becomes `id`.

### rota design list
rota design list
repo: none
data: {"designs": [{"id": string, "title": string, "status": string, "created": string}]}
exit: backend (file-only)
old: hv-design-list
shim: wraps the JSON array as `designs`. `status` defaults to `draft`, and `id` falls back to the filename stem.

### rota design show
rota design show <ID>
repo: none
data: {"id": string, "type": "B"|"F"|"T", "body": string}
exit: 2 when <ID> is malformed; 3 when the design doesn't exist; tracker
old: hv-design-show <ID>
shim: stdout becomes `body`. Text mode prints the stored body verbatim, so skills can still grep `^status:`.

### rota design put
rota design put <ID> --body-file <path|->
repo: none
data: {"id": string, "type": "B"|"F"|"T", "changed": bool}
exit: 2 when <ID> is malformed, --body-file is missing or unreadable; 3 when the design doesn't exist (hint: rota design add); tracker
old: hv-design-put <ID> --body-file <path|->
shim: `changed` compares `hv-design-show <ID>` before and after.
note: every design verb takes `[BFT]\d{2,}` in file mode and `[BFT]\d+` in issue mode, which tightens old `put` in file mode. The body is stored whole with no validation, as before.

### rota design rm
rota design rm <ID>
repo: none
data: {"id": string, "type": "B"|"F"|"T", "changed": true}
exit: 3 when the design doesn't exist (a second `rm` fails, as smoke 31/58 expect); tracker
old: hv-design-rm <ID>

### rota design amend
rota design amend <ID> --section <heading> --mode <append|replace> --body-file <path|->
repo: none
data: {"id": string, "type": "B"|"F"|"T", "section": string, "mode": string, "changed": bool}
exit: 2 when <ID> is malformed, --section is missing, --mode isn't append|replace or --body-file is missing or unreadable; 3 when the design or the `## <section>` heading doesn't exist; backend (file-only)
old: hv-design-amend <ID> --section "<heading>" (--append|--replace) "<body text>"    (--mode picks the flag; the old text argument is the body file's contents)
shim: reads the body file (or stdin for `-`) into the text argument. `changed` compares the design body before and after.
note: `--append TEXT` becomes `--mode` plus `--body-file`, per rule 3. Trailing newlines are stripped and both modes keep the old block format, as before.

### rota spike add
rota spike add <name> --question <text>
repo: scoped    (global --repo names the sub-repo that holds the branch; required at an umbrella root)
data: {"name": string, "branch": string, "changed": true}
exit: 2 when <name> isn't `[a-z0-9][a-z0-9-]*`, --question is missing, or the umbrella root is used without --repo; 4 when the spike file or branch `spike/<name>` already exists (the file check runs first, so no branch is created); 5 when git fails
old: hv-spike-add [--repo <repo>] <name> "<question>"    (--repo goes first; it now arrives as the global flag)
shim: prints `spike/<name>` as `branch`.
note: spikes are files under both backends, so the verb has no `backend` exit.

### rota spike finish
rota spike finish <name>
repo: none
data: {"name": string, "status": "done", "changed": bool}
exit: 2 when <name> is malformed; 3 when the spike doesn't exist; 70 when its frontmatter has no `status` field (old 1)
old: hv-spike-finish <name>
shim: `changed` is false when the spike already had `status: done`. The old helper rewrote `finished:` on every call, and the new verb leaves an existing `finished:` date alone.
note: the repeat call is a no-op, unlike old, to fit the conventions' idempotent rule. The branch is left untouched.

### rota spike list
rota spike list
repo: none
data: {"spikes": [{"name": string, "branch": string, "repo"?: string, "status": string, "created": string, "branchExists": bool}]}
exit: implied only
old: hv-spike-list
shim: wraps the JSON array as `spikes`, and `repo` is dropped when the old value is "". `status` defaults to `open`.

### rota spike show
rota spike show <name>
repo: none
data: {"name": string, "body": string}
exit: 2 when <name> isn't `[a-z0-9][a-z0-9-]*`; 3 when the spike doesn't exist
old: hv-spike-show <name>
shim: stdout becomes `body`.
note: there is no `spike put` or `spike rm`, because `finish` ends the lifecycle.

### rota proof add
rota proof add <ID> --check <text> --result <PASS|FAIL> --evidence <text> [--sha <commit>]
repo: none    (an umbrella-issue item is addressed as `<repo>:<ID>`, as in shared definitions)
data: {"id": string, "type": "B"|"F"|"T", "check": string, "result": "PASS"|"FAIL", "sha": string, "evidence": string, "changed": bool}
exit: 2 when --check, --result or --evidence is missing or empty, or --result isn't exactly PASS or FAIL; 3 when the item doesn't exist; tracker
old: hv-proof-add <ID> --check "<check>" --result <result> --evidence "<evidence>" [--sha <sha>]
shim: echoes the flags into `data`. `sha` is the passed value, or `git log -1 --format=%h` (else `-`), the same default as the helper. `changed` is false when the old helper wrote nothing: an identical (check, result, sha, evidence) row exists, and the date is ignored. The shim compares `hv-proof-show --count` before and after.
note: `data.sha`, `check` and `evidence` carry the whitespace-collapsed values that were stored.

### rota proof record
rota proof record <ID> [--base <ref>] -- <cmd>...
repo: none    (an umbrella-issue item is addressed as `<repo>:<ID>`, as in shared definitions)
data: {"id": string, "type": "B"|"F"|"T", "check": string, "result": "PASS"|"FAIL", "sha": string, "evidence": string, "exitCode": number, "niced": bool, "changed": bool}
exit: 0 when the command exited 0; 1 when it exited non-zero (the FAIL row is still written, and `data` is the same shape); 2 when `--` or the command is missing; 3 when the item doesn't exist (checked before the command runs, so nothing runs for an unknown item) or `{files}` is used and no base resolves; tracker
old: none (new in the proof family)
note: runs the command in the project root and records what ran, so a row cannot be invented. `check` is the command as run: one argument is a shell line as typed, several are each single-quoted and joined by spaces. `{files}` expands as in `rota test run` (changed files against `--base`, default the base branch, each single-quoted), so an empty expansion shows in `check`. `result` is PASS for exit 0, else FAIL. `sha` is HEAD. `evidence` is `exit=<code> output-sha256=<hex>`, the hash over the combined stdout and stderr. The command's own output is not echoed or stored.
note: (#1815) the command runs holding a slot of the test lock (`rota test run` describes it) and at lowered priority, `nice -n 10` and `ionice -c3` when `ionice` is on PATH, which its descendants inherit; `niced` says whether it was wrapped (no `nice` on PATH: false). A niced run's evidence ends in ` niced=true`. The command's environment carries `ROTA_TEST_LOCK_HELD=1`, so a `rota test run` inside it does not wait behind its own slot.
note: dedup is `proof add`'s: an identical (check, result, sha, evidence) row is not added and `changed` is false. A re-run whose output differs (a timestamp, say) is a new row.
note: `rota proof add` is unchanged and stays for rows no command backs (docs-only changes).

### rota proof show
rota proof show <ID> [--count]
repo: none
data: {"id": string, "type": "B"|"F"|"T", "count": number, "rows": [{"date": string, "check": string, "result": "PASS"|"FAIL", "sha": string, "evidence": string}]}
exit: 3 when the item doesn't exist in issue mode (old 1); tracker
old: hv-proof-show <ID> [--count]
shim: `rows` splits each `- date · check · RESULT · sha · evidence` line on the first four ` · ` separators, so the rest of the line is the evidence. `count` is `len(rows)`, and 0 when stdout is empty. Text mode with `--count` prints only the integer, as before, and `--json` ignores `--count`.
note: `--count` stays as a text-mode flag, because hv-complete's gate reads it through `hvlib_backend.py` until A4 ports that to `data.count`.
note: in file mode an unknown ID or missing detail file stays a lenient `count: 0`, as old; only issue mode exits 3.

### rota debug counter init
rota debug counter init <bugId>
repo: scoped
data: {"session": string, "bugId": string, "changed": bool}; failure data on exit 4 is the Iron Law shape (B3)
exit: 4 when the item has 3 or more failed fixes since its last reset (B3, the Iron Law; checked before the state file is touched); 5 when not in a git repository (old 1)
old: hv-debug-counter init <bugId>
shim: `session` is the current branch with `/` replaced by `-`; `changed` is false when `.rota/debug/<session>.json` already existed, which keeps init idempotent.
note: init and record-attempt resolve issue identities as B3 specifies; unknown or invalid items exit 3, ambiguous umbrella references exit 2, and tracker errors propagate. New sessions store the canonical ID in `bug_id`.
note: the tree lists one `debug counter` verb, but its eight actions take different arguments and return different data, so each gets its own entry.
note: the state file keeps its old snake_case keys, so `rota` and the old helper can share it during the port.

### rota debug counter record-attempt
rota debug counter record-attempt --hypothesis <text> --commit <hash>
repo: scoped
data: {"attempt": number, "changed": true}; failure data on exit 4 is the Iron Law shape (B3)
exit: 2 when --hypothesis or --commit is missing; 3 when the session has no state file (hint: rota debug counter init); 4 when the session's item has 3 or more failed fixes since its last reset (B3, the Iron Law; nothing is recorded); 5 when not in a git repository
old: hv-debug-counter record-attempt --hypothesis "<text>" --commit <hash>
shim: stdout (the attempt number) becomes `attempt`.

### rota debug counter fail
rota debug counter fail
repo: none
data: {"failedFixes": number, "changed": true}
exit: 3 when the session has no state file; 4 when there is no attempt or the last attempt isn't `pending`; 5 when not in a git repository
old: hv-debug-counter fail
shim: stdout (the new count) becomes `failedFixes`.

### rota debug counter pass
rota debug counter pass
repo: none
data: {"attempt": number, "changed": true}
exit: 3 when the session has no state file; 4 when there is no attempt or the last attempt isn't `pending`; 5 when not in a git repository
old: hv-debug-counter pass
shim: `attempt` is the `n` of the last attempt, read from the state file after the call.

### rota debug counter show
rota debug counter show
repo: none
data: {"session": string, "bugId": string, "startedAt": string, "failedFixes": number, "hypothesisCycles": number, "attempts": [{"n": number, "startedAt": string, "hypothesis": string, "commit": string, "outcome": string, "endedAt"?: string}]}
exit: 3 when the session has no state file (old: silent rc 1); 5 when not in a git repository
old: hv-debug-counter show
shim: parses the raw file JSON and renames keys to camelCase. Text mode prints the state file verbatim (snake_case), as before.

### rota debug counter summary
rota debug counter summary
repo: none
data: {"bugId": string, "failedFixes": number, "markdown": string}
exit: 1 when there are no attempts (read-only verb, `markdown` empty); 3 when the session has no state file; 5 when not in a git repository
old: hv-debug-counter summary
shim: stdout becomes `markdown`. `bugId` and `failedFixes` are read from the state file. A state file with no attempts exits 1 with the same data and empty `markdown`.

### rota debug counter clear
rota debug counter clear
repo: none
data: {"changed": bool}
exit: 5 when not in a git repository
old: hv-debug-counter clear
shim: `changed` is false when no state file existed. `clear` stays idempotent.

### rota debug counter inc-cycle
rota debug counter inc-cycle
repo: none
data: {"hypothesisCycles": number, "changed": true}
exit: 3 when the session has no state file; 5 when not in a git repository
old: hv-debug-counter inc-cycle
shim: stdout (the new count) becomes `hypothesisCycles`.

### rota debug session put
rota debug session put <slug> --body-file <path|->
repo: none
data: {"slug": string, "changed": true}
exit: 2 when <slug> isn't `[A-Za-z0-9][A-Za-z0-9_-]*` (a `/` or `..` is refused) or --body-file is missing or unreadable
old: none (new in #925)
note: the file is `.rota/debug/<slug>.md`, per-developer and gitignored. `put` replaces it whole and stores the body with no validation; /rota-debug writes the headings Symptom, Hypotheses (each `open`, `eliminated` or `confirmed`), Evidence and Current focus. The Iron Law counter keeps `<session>.json` in the same directory and is untouched; the four `session` verbs only see `.md` files. `put` also writes `.rota/debug/.gitignore` (`*`) when missing, so sessions stay out of `git status` in a project whose root `.gitignore` predates the `.rota/debug/` line; an existing file is left alone.

### rota debug session show
rota debug session show <slug>
repo: none
data: {"slug": string, "body": string}
exit: 2 when <slug> is malformed; 3 when the session doesn't exist
old: none (new in #925)

### rota debug session list
rota debug session list
repo: none
data: {"sessions": [string]}
exit: 2 when given an argument
old: none (new in #925)
note: slugs sorted; a missing `.rota/debug/` is an empty list. Every file listed is an open session, since resolution removes it.

### rota debug session rm
rota debug session rm <slug>
repo: none
data: {"slug": string, "changed": true}
exit: 2 when <slug> is malformed; 3 when the session doesn't exist
old: none (new in #925)
