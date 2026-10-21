# `rota` verb reference

`rota` is the single binary behind every rota skill. Skills call it for all
backlog, knowledge, plan, status, git and release bookkeeping, and you can call
it directly when scripting against `.rota/`. There is no helper copy to refresh in a
project. Install `rota` (see [install](../install.md)).

```sh
rota item create --kind bugs --title "Crash on save" --tag P1 --desc "Why."
rota backlog list --json
rota knowledge query "Auth & Sessions"
```

## Conventions

- **Global flags.** `--json` prints one JSON envelope on stdout, `-C <dir>` runs
  as if started in `<dir>`, `--repo <name>` scopes a verb to an umbrella
  sub-repo, `-h` prints help for any group or verb.
- **No prompts.** `rota` never asks anything. A missing decision is exit 2 naming
  the flag. Skills do the asking. The exception is `rota setup`, which prompts only on a terminal.
- **Bodies on stdin.** Any flag that takes a file path also accepts `-`
  (`--body-file -`).
- **Project root.** Verbs walk up from the working directory to the nearest
  `.rota/`. Verbs such as `rota init` (with `check` and `umbrella`), `rota setup`, `rota projects`, `rota version`,
  `rota update`, `rota doctor` and `rota skills` run without one.
- **Idempotent writes.** A mutating verb reports `changed: true|false`. A no-op
  is exit 0.

| Exit | Meaning |
|---|---|
| 0 | Success, including idempotent no-ops |
| 1 | The verb ran and the answer is no (a guard or check failed) |
| 2 | Usage error: unknown verb or flag, missing argument |
| 3 | Something named could not be resolved (item, plan, sub-repo, base branch, `.rota/` itself) |
| 4 | A mutating verb refused to break an invariant |
| 5 | An external dependency is missing or failing (`git`, `gh`, `glab`, network) |
| 6 | Transient: rate limit or lock timeout, retry later |
| 70 | Bug in `rota`; report it |

Not a stable API across major versions, but `--json` shapes only change
additively within one. Full rules:
[CLI conventions](../contributing/contract/cli-conventions.md). Per-verb `data` shapes,
exit codes and repo scope: [verb contract](../contributing/contract/README.md).
`rota <group> --help` lists a group's verbs, `rota <group> <verb> --help` its flags.

## Verbs

## `rota version`

| Usage | What it does |
|---|---|
| `rota version [--drift]` | print the rota version |

## `rota update`

| Usage | What it does |
|---|---|
| `rota update` | check for a newer rota release |

## `rota config`

| Usage | What it does |
|---|---|
| `rota config show [<key>]` | effective value and source of config keys |
| `rota config edit` | change keys interactively (needs a terminal) |
| `rota config set <key> <value>` | set one key in .rota/config.json |
| `rota config check` | compare .rota/config.json with the schema |
| `rota config fill` | write the schema default for every missing key |
| `rota config show\|set\|edit --global` | the same verbs on the machine-wide global config; no project needed |
| `rota config save-global` | merge `.rota/config.json` into the machine-wide global config |

## `rota repo`

| Usage | What it does |
|---|---|
| `rota repo which` | the registered sub-repo the working directory is in |
| `rota repo resolve [<name>…]` | names to registered sub-repo paths |
| `rota repo umbrella` | is this an umbrella project |
| `rota repo add <path> [--name <name>]` | register one sub-repo in the umbrella registry |
| `rota repo rm <name>` | unregister one sub-repo; warns on open items and active streams |

## `rota id`

| Usage | What it does |
|---|---|
| `rota id next --kind <bugs\|features\|tasks\|milestones>` | mint the next counter ID |

## `rota item`

| Usage | What it does |
|---|---|
| `rota item create --kind <bugs\|features\|tasks> --title <text> [--tag <tag>] [--desc <text>] [--body-file <path\|->] [--depends-on <refs>] [--related <text>] [--milestone <text>] [--repos <csv>] [--subsystem <text>] [--captured <YYYY-MM-DD>] \| --kind <kind> --raw-file <path\|->` \| `--batch-file <path\|->` | capture one item, or a JSON array of records (kind, title, tag, desc, body, dependsOn, related, milestone, repos, subsystem, captured) checked for title, kind and field shape up front, then filed in order (a failure part-way reports the ids already filed); the title must be 3+ words in a batch, and never a placeholder (x, tbd, todo, title, untitled) |
| `rota item show <ID>` | status block of an issue-mode item |
| `rota item claim <ID> --as <claim-id>` | take an item so two agents never work it at once |
| `rota item release <ID> --as <claim-id>` | give a claimed item back |
| `rota item ready <ID>` | is the item specified well enough to start |
| `rota item state <ID> --to <in-progress\|needs-review\|changes-requested\|none>` | set the workflow state label of an item |
| `rota item comment add <ID> --kind <question\|answer\|decision\|feedback> --body-file <path\|->` | append a comment |
| `rota item comment list <ID> [--kind <question\|answer\|decision\|feedback>]` | list comments |
| `rota item note add <ID> --kind <proof\|design\|plan> --body-file <path\|->` | write a note |
| `rota item note show <ID> --kind <proof\|design\|plan>` | print a note |
| `rota item note rm <ID> --kind <proof\|design\|plan>` | delete a note |
| `rota item field get <ID> --name <title\|detail\|related\|milestone\|repos\|subsystem\|since\|reason\|note>` | print one field of an item |
| `rota item field set <ID> --name <milestone\|related\|repos\|subsystem\|detail> --value <text>` | set, replace or clear a field of an open item |
| `rota item section set <ID> --name <heading> --body-file <path\|->` | replace or add one `## <heading>` section of an issue-mode body, fields block kept |
| `rota item field list <ID>` | every field of an item |
| `rota item complete <ID> [--commit <hash>] [--reason <done\|handed-off\|blocked\|dropped>] [--note <text>] [--no-proof] [--gist <text>]` | close an item; a decision item needs `--gist`, its line in the milestone's `## Decisions` |
| `rota item reopen <ID>` | restore a completed item |
| `rota item rm <ID>... [--scrub-archive] [--apply]` | remove items with their cross-references and files |
| `rota item shipped <title>...` | look for evidence that titles already shipped |

## `rota backlog`

| Usage | What it does |
|---|---|
| `rota backlog list [--grep <pattern>]` | open items as sorted tables, with clusters |
| `rota backlog ids --milestone <id>` | IDs of the open items tagged with a milestone |
| `rota backlog milestones <ID>...` | milestones the given items are tagged with |
| `rota backlog drift` | open items that commits already mention |
| `rota backlog backfill` | stamp Since: on open items that lack it |
| `rota backlog archive [--days <n>]` | move old completed items to ARCHIVE.md |
| `rota backlog stale --kind <map\|knowledge\|todo> [--days <n>]` | stale map, knowledge or backlog entries |

## `rota summary`

| Usage | What it does |
|---|---|
| `rota summary` | compact project state |

## `rota issues`

| Usage | What it does |
|---|---|
| `rota issues label <issue> (--add <name> \| --remove <name>)` | add or remove a label on an upstream issue |
| `rota issues imported [--for-repo <name>] [--open-only]` | backlog items that point at upstream issues |
| `rota issues close <issue> --commit <sha> [--item <ID>]` | close an upstream issue naming the shipping commit |
| `rota issues provider` | github, gitlab or unknown for the origin remote |

## `rota status`

| Usage | What it does |
|---|---|
| `rota status add <branch> --items <csv> [--worktree <path> \| --repos <csv> --worktrees <csv>] [--if-absent]` | record an active work stream; in umbrella mode `--repos` names the sub-repos and `--worktrees` gives one path per name |
| `rota status rm <branch>` | end a work stream and drop its handoff note |
| `rota status show <branch>` | which repo a branch's stream is in |
| `rota status handoff <branch> [--canonical]` | path of a branch's handoff note |

## `rota refactor`

| Usage | What it does |
|---|---|
| `rota refactor age` | features and bugs completed since the last refactor |
| `rota refactor reset` | zero the since-refactor counters |
| `rota refactor targets` | what a refactor can cover |

## `rota migrate`

| Usage | What it does |
|---|---|
| `rota migrate hv [--apply] [--verbose] [--skip-skills]` | move a project from the old state folder and names to `.rota/` and rota (preview unless --apply) |
| `rota migrate issues [--apply] [--limit <n>]` | move the file backlog onto the issue tracker (preview unless --apply) |
| `rota migrate milestones [--apply]` | rename `## Acceptance criteria` to `## Acceptance` and make each bullet a checkbox (preview unless --apply) |
| `rota migrate milestone-files [--apply]` | move `.rota/milestones/*.md` left from file mode to `.rota/archive/milestones/` when milestones live on the tracker (preview unless --apply); refuses, preview included, when the archive already holds a file of the same name |

## `rota knowledge`

| Usage | What it does |
|---|---|
| `rota knowledge query <topic>… [--tier provisional\|confirmed\|deprecated] [--include-deprecated]` | print topic sections, tier-aware |
| `rota knowledge stats` | bullet count and size per topic |
| `rota knowledge topics` | list topic headings with bullet counts |
| `rota knowledge add --topic <T> --title <S> --body-file <path\|-> [--date YYYY-MM-DD] [--sha <hex>]` | add a bullet under a topic |
| `rota knowledge amend --topic <T> --fragment <F> --mode append --body-file <path\|->` | append text to an existing bullet |
| `rota knowledge replace --topic <T> --old <text> --new <text>` | replace text inside the one bullet that contains it |
| `rota knowledge rm --topic <T> --title <title>` | delete one bullet and its tier entry |
| `rota knowledge rename-topic --from <X> --to <Y> [--title <T>]` | rename a topic or move one bullet |
| `rota knowledge hit --topic <T> --title <S>` | register a consulted bullet; buffered in the gitignored `knowledge-hits.jsonl` until `tier fold` |
| `rota knowledge tier get --topic <T> --title <S>` | show one bullet's tier |
| `rota knowledge tier set --topic <T> --title <S> --tier provisional\|confirmed\|deprecated` | set one bullet's tier |
| `rota knowledge tier fold` | fold buffered hits into the tracked sidecar and empty the buffer |
| `rota knowledge tier list [--tier provisional\|confirmed\|deprecated]` | list tracked bullets |
| `rota knowledge contradiction add --topic <T> --title <S> --text <text>` | queue a contradiction candidate |
| `rota knowledge contradiction list` | list the queue |
| `rota knowledge contradiction clear [--topic <T> --title <S>]` | empty the queue, or drop one pair |
| `rota knowledge contradiction has --topic <T> --title <S>` | exit 0 when the pair is queued |

## `rota decisions`

| Usage | What it does |
|---|---|
| `rota decisions query <topic>…` | print topic sections |
| `rota decisions topics` | list topic headings with bullet counts |
| `rota decisions stats` | bullet count and size per topic |

## `rota glossary`

| Usage | What it does |
|---|---|
| `rota glossary read <term>…` | print term entries |
| `rota glossary write <term> --def <text> [--alias <a,b>] [--not <n,m>] [--touch]` | add or update one term |
| `rota glossary import --body-file <path\|-> [--touch]` | add many terms atomically |

## `rota block`

| Usage | What it does |
|---|---|
| `rota block <key> [--body-file <path\|->]` | regenerate a managed block in the instructions file |
| `rota block skills` | regenerate the skills block |

## `rota instructions`

| Usage | What it does |
|---|---|
| `rota instructions init` | make AGENTS.md the instructions file, CLAUDE.md its importer |

## `rota map`

| Usage | What it does |
|---|---|
| `rota map query <name>…` | print subsystem files |
| `rota map index` | regenerate the map block |
| `rota map stats [--cap]` | size and broken-reference counts |

## `rota qa`

| Usage | What it does |
|---|---|
| `rota qa query <target>…` | print QA target files |
| `rota qa index` | regenerate the QA block |

## `rota milestone`

| Usage | What it does |
|---|---|
| `rota milestone add --title <text> --summary <text> [--depends M01,M02] [--kind <delivery\|decision>]` | mint a milestone; `--kind decision` adds `kind: decision` and empty `## Decisions` and `## Not yet specified` sections |
| `rota milestone list` | list milestones |
| `rota milestone show <id>` | print a milestone |
| `rota milestone put <id> --body-file <path\|->` | replace a milestone's text |
| `rota milestone overview --body-file <path\|->` | replace the MILESTONES.md overview text |
| `rota milestone status <id> --to <planned\|active\|shipped\|archived>` | change a milestone's status |
| `rota milestone active` | IDs of active milestones |
| `rota milestone check <MNN> [--strict]` | check a milestone's feature issues against its capabilities and acceptance criteria (read-only; --strict fails on uncovered criteria) |
| `rota milestone index` | regenerate the overview and vision block |

## `rota plan`

| Usage | What it does |
|---|---|
| `rota plan add <milestone>-<unit> --title <text> [--design <ID>] [--repos a,b] [--milestone <M01> [--slice]]` | create a plan stub (issue mode: `<#N>` or `<B7>` item key, milestone optional); `--slice` with `--milestone` drops the key and mints the next `S<NN>` one |
| `rota plan list [--milestone M01]` | list plans |
| `rota plan show <key>` | print a plan |
| `rota plan put <key> --body-file <path\|->` | replace a plan's text |
| `rota plan rm <key>` | delete a plan |
| `rota plan validate-docs <key>` | check doc-by-path deliverables |
| `rota plan rename-check <old> [-- <pathspec>…]` | files that mention a name |
| `rota plan uncertain <ID>` | uncertainty pre-flight for an item |
| `rota plan check <key>` | issue mode: every acceptance criterion of the item has a task, every task serves a criterion and has a Verify step; exit 1 lists the gaps |
| `rota plan pass <key> <AC-n> --proof <sha>:<check>` | issue mode: mark one acceptance criterion met by a proof row (as `rota proof show` prints them) |

## `rota design`

| Usage | What it does |
|---|---|
| `rota design add <ID> --title <text>` | create a design stub |
| `rota design list` | list designs |
| `rota design show <ID>` | print a design |
| `rota design put <ID> --body-file <path\|->` | replace a design's text |
| `rota design rm <ID>` | delete a design |
| `rota design amend <ID> --section <heading> --mode <append\|replace> --body-file <path\|->` | amend one section of a design |

## `rota spike`

| Usage | What it does |
|---|---|
| `rota spike add <name> --question <text>` | create spike/<name> and its file |
| `rota spike finish <name>` | mark a spike done |
| `rota spike list` | list spikes |
| `rota spike show <name>` | print a spike file |

## `rota proof`

| Usage | What it does |
|---|---|
| `rota proof add <ID> --check <text> --result <PASS\|FAIL> --evidence <text> [--sha <commit>]` | append a proof row |
| `rota proof record <ID> [--base <ref>] -- <cmd>...` | run the command and append a proof row measured, not typed: PASS for exit 0, FAIL otherwise; exits 0 or 1 to match |
| `rota proof show <ID> [--count]` | list an item's proof rows |

## `rota debug`

| Usage | What it does |
|---|---|
| `rota debug counter init <bugId>` | start the counter for a bug |
| `rota debug counter record-attempt --hypothesis <text> --commit <hash>` | record a pending fix attempt |
| `rota debug counter fail` | mark the last attempt failed |
| `rota debug counter pass` | mark the last attempt passed |
| `rota debug counter show` | print the counter state |
| `rota debug counter summary` | Iron Law halt note |
| `rota debug counter clear` | delete the counter |
| `rota debug counter inc-cycle` | count a hypothesis cycle |
| `rota debug verdict <bugId> --verdict <PASS\|FAIL> [--body-file <path\|->]` | record whether a fix held and route on the item's failed-fix count |
| `rota debug reset <bugId> --reason <text> --confirm --confirm-note <answer>` | start an item's failed-fix count again (manual gate) |

## `rota worker`

| Usage | What it does |
|---|---|
| `rota worker pool init --slots <n> [--base <branch>] [--session <name>]` | create the slots' worktrees and register them |
| `rota worker pool list` | list the registered slots |
| `rota worker pool reap (<slot>... \| --all)` | remove slots, their worktrees and branches |
| `rota worker reset <slot> [--task <id>] [--check-only]` | refuse a slot that holds work, else cut a fresh task branch |
| `rota worker inbox <slot> [--ack [--upto <seq>]]` | the relays `round review-relay` wrote to the slot's durable inbox; `--ack` marks them read (`--upto` bounds it to what the listing showed). `worker done` refuses (exit 4) while any is unread |
| `rota worker reply <slot> --body-file <path\|->` | answer a reviewer on the slot's PR through the tracker; the comment carries a `worker-reply` marker |
| `rota worker dispatch <slot> --body-file <path\|-> [--task <id>] [--relay] [--round <n>] [--boot-timeout <s>] [--kind <claude\|codex>]` | send a brief into a slot's session; refuses (exit 5) a human draft on the prompt line, and any dialog except the autocompact picker (`blockedBy: dialog`), which it dismisses once with Escape |
| `rota worker poll [<slot>] [--settle <seconds>] [--lines <n>]` | classify slot states from their panes |
| `rota worker done <slot> [--base <ref>] [--attempt <id>] [--outcome done\|partial\|failed] [--summary-file <f>] [--require-red]` | the last step before `ROTA-DONE`, after the PR is open and pushed; exit 4 on a dirty worktree or when `origin/<branch>` is absent or not HEAD (`data.dirty`, `data.unpushed`; `--outcome failed` skips both); warns (exit 4 with `--require-red`) when `work.tdd` is on, non-test Go changed and no proof row is a FAIL, and likewise when that diff cannot be computed (`data.noRedUnchecked`); `--outcome` with a three-line summary types how it ended (`partial`/`failed` hold the PR out of the gate until a relay or transfer); exit 4 on a stale `--attempt` (the id from your brief; the assignment was reclaimed or transferred) or unless the slot's item has a PASS `test.fast` proof row at the branch HEAD (`rota proof record`), else mark the slot done |
| `rota worker send <slot> (--blocked <question> \| --question <text> \| --heartbeat)` | a worker's typed event on its own slot, read by `round wait` and `worker poll` before the pane: `--blocked` and `--question` mark the slot blocked with the text as evidence and write a ledger row, `--heartbeat` only refreshes the stall clock; works on herdr, tmux and solo |
| `rota worker gate <slot> --base <branch> [--check-only] [--no-verify] [--confirm --confirm-note <answer> \| --approval <escalation> \| --escalate]` | merge gate for one slot's branch or PR; exit 4 when `ship.mergeApproval` needs a human or `test.full` is empty (`blockedBy no-verify`, unless `--no-verify`) or the PR body does not close the slot's issue (`blockedBy closes`, unless the issue is labelled `partial-slice`), `--escalate` asks on the thread, `--approval` cites the answer |
| `rota worker train <slot\|PR>... --base <branch> [--order <list>] [--land-green] [--no-verify] [--confirm --confirm-note <answer> \| --approval <escalation> \| --escalate]` | merge several PRs in a conflict-minimising order (or `--order`) in a scratch tree, verify once, land them all on a pass; a red train bisects to the first member that breaks it |
| `rota test run <fast\|full\|e2e> [--base <ref>]` | run `test.<tier>` in order, stopping at the first failure; `{files}` expands to the files changed against base; exit 3 when the tier is empty |
| `rota test ledger check` | report expired and malformed entries in `.rota/test-ledger.json`; exit 1 when there are any |
| `rota worker prompt-check [--key <file>]` | Codex UserPromptSubmit hook: pass only signed or maintainer input |
| `rota worker session check [--session <name>]` | inside a managed host session? (exit 1 when outside) |
| `rota worker session ensure [--session <name>] [--body-file <path\|->] [--boot-timeout <s>]` | hand the orchestrator off into a host session |
| `rota worker account list` | list accounts with their usage verdict |
| `rota worker account pick [--exclude <name>[,<name>...]]` | name the account with the most headroom |
| `rota worker account assign <slot> [--account <name>]` | put an account's config dir on a slot |

## `rota orchestrate`

| Usage | What it does |
|---|---|
| `rota orchestrate [--dry-run]` | launch the orchestrator: an agent under `rota keepalive run` that starts `/rota-orchestrate`; `--dry-run` runs `rota doctor` and prints what would start |

Bare `rota` with no verb does the same in an initialized project and runs `rota setup` in a directory that is not one. In your home directory (with no `.rota/` there or above) it is a launcher instead: Projects is the default entry, Setup is hidden, and Config opens the global config. The agent comes from `orchestrator.harness`.

## `rota skills`

| Usage | What it does |
|---|---|
| `rota skills install [--scope <user\|project>] [--agent <claude\|codex\|all>] [--overwrite] [--current-account] [--stub \| --full]` | write the skills into the agent skill directories; the default (`--stub`) writes each `SKILL.md` as a stub that tells the agent to run `rota skills get <name>` and drops the skill's other files, which `get` serves from a per-version cache; `--full` writes the full text for harnesses that cannot run a command when a skill loads; the mode is recorded in the manifest; both flags exit 1 |
| `rota skills status [--scope <user\|project>] [--agent <claude\|codex\|all>] [--current-account]` | compare the installed skills with this binary; a stub root is compared on its skill set and frontmatter and the changed skills are named |
| `rota skills list` | name the skills embedded in this binary and its version |
| `rota skills get <name> [--references \| --reference <file>]` | print an embedded skill's `SKILL.md` as the binary carries it (first line `Base directory for this skill: <dir>` in the per-version cache, last block `Files this skill links to:` with absolute paths; a cache failure prints the text with a warning), list its reference files, or print one; exit 2 on an unknown name or reference (the known ones are listed) or when both flags are given |
| `rota skills update [--scope <user\|project>] [--agent <claude\|codex\|all>] [--overwrite] [--current-account] [--stub \| --full]` | refresh the skill directories that already have a manifest, each in its recorded mode unless a flag switches it (a manifest with no mode is full); both flags exit 1 |
| `rota skills uninstall [--scope <user\|project>] [--agent <claude\|codex\|all>] [--overwrite] [--current-account]` | remove what rota installed |

## `rota round`

The orchestrator's verbs for a [parallel round](../usage/parallel-rounds.md). All of them read and write `.rota/workers.json` and the round lease.

| Usage | What it does |
|---|---|
| `rota round start [--scope <slate\|milestone\|next\|open>] [--items <ID>[,<ID>…]] [--slots <n>] [--base <branch>] [--holder-pid <n>] [--consume-handoff]` | take the orchestrator lease, provision the roster, list candidates; report the `/rota-pause` orchestrator note (`--consume-handoff` archives it) |
| `rota round candidates [--scope <slate\|milestone\|next\|open>]` | list the items the round's scope allows, with readiness |
| `rota round architecture [--check] [--holder-pid <n>]` | show the architecture-review counter; when a review is due, mint one item per area and assign them to idle slots |
| `rota round scope <ID> --body-file <path\|->` | record the scope an explorer inferred for an item with no `## Touches` or `Subsystem:`: up to ten bullets and a last line `scoped.` (else exit 4), stored as a comment of kind `scope`; overlap compares it warn-only (`candidates --json` shows each item's `scopeSource`) |
| `rota round assign <ID> [--agent <name>] [--tier <light\|standard\|heavy>] [--tier-reason <text>] [--kind <claude\|codex>] [--model <id>] [--body-file <path\|->] [--siblings <ID>[,<ID>…]] [--check-only] [--accept-overlap] [--accept-open-pr] [--accept-batch] [--holder-pid <n>]` | check an item's readiness and hand it to a slot; the issue's `harness:`/`model:` labels pick the harness and model; an issue that mentions a smoke section (and a project with `test/sections/`) gets the next free number reserved on the slot and named in the brief; a `best-of:2` issue goes to two slots at once (see `round pick`) |
| `rota round wait [<slot>…] [--timeout <seconds>] [--settle <seconds>] [--lines <n>]` | block until a worker needs attention; adds an `assign before you gate` line (`assignNudge`) for a done slot with a ready candidate and a `nudge train` line (`trainNudge`) when two or more reviewed PRs wait on one base |
| `rota round watch [--heartbeat <seconds>] [--poll <seconds>] [--forge-poll <seconds>] [--settle <seconds>] [--lines <n>] [--autopilot [--base <branch>] [--holder-pid <n>]]` | background watch: exit on a slot, PR or escalation change, or at a heartbeat, with the same nudges as `round wait`; `--autopilot` runs a tick on every wake |
| `rota round tick [--base <branch>] [--holder-pid <n>]` | one autopilot pass: repair safe drift, merge finished PRs behind the gate, assign ready items (`autonomy.level: auto` only; exit 4 otherwise) |
| `rota round status` | list the round's slots with host, PR and drift |
| `rota round reconcile [--apply]` | report drift between registry, host, git and forge; `--apply` repairs the safe kinds, including parking slots whose PR merged |
| `rota round report <slot> --state <done\|blocked\|idle\|dead\|limited> [--evidence <text>] [--pr <url\|number> \| --issues <#a,#b>] [--attempt <id>]` | record a solo worker's result (exit 4 on a stale `--attempt`): state and PR (or, for a review item, the issues it filed) |
| `rota round escalate send <number|key> [--pr] [--slot <name>] --title <text> --body-file <path\|-> --options <text>… --recommend <n> [--cost-if-wrong <text>] [--timeout <seconds>]` | ask the human on an issue or PR thread; exit 4 without at least two `--options` and a `--recommend` |
| `rota round escalate check [<id>…]` | look for the human's answers; a bare `ok`, `yes` or option number resolves to that option |
| `rota round return <slot> --reason <text> [--note-file <path\|->] [--holder-pid <n>]` | a worker hands its issue back: park, comment, release |
| `rota round transfer <issue> --to <slot\|human> [--tier <light\|standard\|heavy>] [--tier-reason <text>] [--cap-reason <ambiguous-criteria\|tier>] [--note-file <path\|->] [--body-file <path\|->] [--accept-overlap] [--holder-pid <n>]` | move an assigned issue to another slot or to the human; at the bounce cap `--cap-reason` records why (`ambiguous-criteria` with `--to human`, `tier` with a slot) and `round summary` counts it |
| `rota round reclaim <slot> [--force] [--note-file <path\|->] [--holder-pid <n>]` | free a dead or stalled slot and make its issue assignable; an `unverifiable` slot (host unreachable, pane unreadable, stale handle) exits 4 with the reason unless `--force`, and `--force` still refuses when the host cannot be asked at all |
| `rota round review-relay <slot>` | relay new review input on a done slot's PR to its worker as a counted bounce (`REVIEW` relay); at `round.freshAt` transfer to a free slot instead, with the open findings only; at `round.maxBounces` escalate on the PR instead, exit 4; never gates or merges |
| `rota round bounce <ID> [--head <sha>] [--slot <slot>]` | count one by-hand review bounce of an item; the same `--head` twice counts once; `--slot` names the slot holding the bounced PR so a best-of attempt gets its own ledger row; exit 4 when `--slot` names a slot outside the pool (nothing written), or once the item is at `round.maxBounces`, then transfer it to a higher tier or `--to human` |
| `rota round accept <ID> [--issues <#a,#b>] [--gist <text>]` | close a finished round-minted architecture-review item, or a `decision:research` item with a finding comment, with no PR: comments the filed issues (or the gist), completes it without proof, releases its claim and frees the slot; `--issues` supplies them when the slot recorded none; a research item needs `--gist`, the line for the milestone's decision log; a repeat on a closed item is `changed: false` |
| `rota round pick <ID> --pr <N> --reason-file <path\|-> [--holder-pid <n>]` | `best-of:2` issue: name the winning PR; close the other attempt's PR with the reason, keep its branch |
| `rota round wind-down [--no-verify] [--holder-pid <n>]` | re-verify the base, park every slot, release the lease |
| `rota receipt show <request-id>` | after a lost response: report whether the call under that id applied, as `completed` (with the stored `result`), `pending` or `absent`; `absent` is not proof nothing happened |

**Request ids.** `round assign`, `round transfer`, `round reclaim`, `round pick`, `round return`, `round report`, `round tick`, `round escalate send`, `worker gate` and `worker train` take `--request-id <id>` and keep a receipt per call in `.rota/receipts.json` (gitignored, pruned by `round wind-down`). Without the flag the verb mints an id and prints it as `requestId` in `--json`. A retry with the same id and the same inputs (arguments, the content of `--body-file`/`--note-file`/`--reason-file`, and for `gate` and `train` the head of each branch judged) returns the first result and applies nothing (`replayed: true`, `changed: false`). The same id with other arguments exits 4 with `blockedBy: request-id`. A call that failed part way (a flaky `gh`, a lost pane) records `failedStage` and `residualResources`, and its error hint names the stage and the id; the retry runs again and resumes from the stage that failed (`resumedFrom` in the result). A call that wrote nothing (exit 2 to 4, or `--check-only`) leaves no receipt.

After a lost response (a timeout, a broken pipe, a `write | jq || write` whose `jq` failed), run `rota receipt show <request-id> [--json]` with the id of the first call, never the same write again. It reads `.rota/receipts.json` and exits 0 with `state`: `completed` (the call applied; `result` holds the stored data), `pending` (it stopped part way; replay it with the same id and arguments) or `absent`. `absent` is not proof that nothing happened: a call still running, a pruned receipt, a call that wrote nothing or a receipt that failed to record all leave none, so inspect the affected state before you re-run.

`round escalate check` is read-only and has no id. Two concurrent calls with one id run the mutation once: the second waits on a claim lock (`.rota/receipts.json.<hash>.lock`, at most 10 minutes, then a retry error) and replays. A reused `round tick` id replays the first tick, so give each tick a fresh one. `gate` and `train` take the branch head from the gate's own target resolution, so a queued or adopted PR gets a real head (a bare external PR's head comes from the forge). A push under the same id is refused, not replayed.

A second `round start` is refused (exit 4) while another orchestrator holds the lease. `round wait` exits 1 on `--timeout` with `data.timedOut: true`, which is an answer, not a fault.

## `rota agents`

| Usage | What it does |
|---|---|
| `rota agents write [--check]` | write `.claude/agents/rota-{explorer,implementer,reasoner}.md`, and `.codex/agents/rota-*.toml` when Codex is configured, from the `roles.*` and `round.tiers.*` config |

Idempotent. Exit 4 when a target exists without the generated-by-rota header (nothing is written) or a role's effort is one Codex cannot express. `--check` writes nothing and exits 1 when a file is missing or has drifted. See [configuration](../usage/configuration.md#roles-keys-and-rota-agents-write).

## `rota doctor`

| Usage | What it does |
|---|---|
| `rota doctor` | preflight: git, host, forge, accounts, herdr hook, orchestrator hooks, skills, codex |

Read-only, runs without `.rota/`. Exit 1 when any check fails; every failure carries a `hint` with the fix. See [preflight](preflight.md#rota-doctor).

## `rota reap`

| Usage | What it does |
|---|---|
| `rota reap [--kind <worktree\|branch\|tab\|process\|lease\|skillcache>[,…]] [--apply]` | list, and with `--apply` remove, what a round left behind that nothing live owns, and the extracted skill trees of other rota versions |

Previews by default. It never kills a running agent and never deletes work: a candidate that holds uncommitted changes or unmerged commits is listed with `held` and left alone. Exit 1 under `--apply` when a deletion failed. Exit 5, with nothing listed or deleted, when `.rota/workers.json` exists but does not parse; `rota worker pool reap` refuses the same way.

## `rota layout`

| Usage | What it does |
|---|---|
| `rota layout [--project <dir>]` | show each project's panes as `split`, `tabs` or `mixed` |
| `rota layout split [--project <dir>]` | fold the live workers into the orchestrator's tab as one grid (wide screen) |
| `rota layout tabs [--project <dir>]` | give every worker a tab of its own again, labelled with its slot (narrow screen) |

`split` and `tabs` are remembered for the round (`layout` in `.rota/workers.json`): later spawns join the grid while it is `split`. A new round starts in tabs.

herdr only; a tmux or solo round is skipped. Moves live panes without restarting or typing into them, and never steals focus. See [parallel rounds](../usage/parallel-rounds.md#layout).

## `rota hook`

| Usage | What it does |
|---|---|
| `rota hook install [--scope <project-local\|project\|user>] [--wrap-statusline]` | merge the hooks and the statusline into a Claude Code settings file |
| `rota hook uninstall [--scope <project-local\|project\|user>]` | remove what install wrote and restore a wrapped statusline |
| `rota hook stop` | Stop hook: block above the context threshold until a handoff is written, or while workers run with no watch armed |
| `rota hook prompt` | UserPromptSubmit hook: one-line round digest and a missing-watch reminder |
| `rota hook session-start` | SessionStart hook: inject and consume the handoff |
| `rota hook guard` | PreToolUse hook on `Bash`: deny destructive git and `gh pr merge` for a worker (`ROTA_SLOT` set), exit 2 with a one-line reason; no-op otherwise |

`install` and `uninstall` are the ones you run; `stop`, `prompt`, `session-start` and `guard` are what Claude Code calls. Default scope is `project-local` (`.claude/settings.local.json`). `install` exits 4 rather than replace a statusline you already have unless you pass `--wrap-statusline`. `stop`, `prompt` and `session-start` always exit 0; `guard` exits 2 to refuse a worker's command. `install` warns when the guard sits only in `project-local` scope, which worker worktrees do not read. See [the worker guard](../usage/parallel-rounds.md#worker-guard).

## `rota statusline`

| Usage | What it does |
|---|---|
| `rota statusline dump [--then <command>]` | record the session state from stdin, then run `--then` with the same input |

A statusline command, not a query: it prints nothing of its own, always exits 0 and takes no `--json`. `rota hook install --wrap-statusline` sets it up around the statusline you have.

## `rota keepalive`

| Usage | What it does |
|---|---|
| `rota keepalive run [--max-restarts <n>] [--breaker <n>] [--backoff <seconds>] [--prompt <text>] [--first-prompt <text>] [--config-dir <path>] [--no-limits] -- <command> [<arg>…]` | run the orchestrator as a supervisor and restart it when it exits with a fresh handoff; `--first-prompt` is appended to the command on the first start only, `--config-dir` sets `CLAUDE_CONFIG_DIR` for it (default: the inherited one) |
| `rota keepalive status` | show the supervisor state and the round lease |

`run` blocks for the life of the orchestrator. Exit 0 when it stopped on `no-handoff` or `interrupted`, 1 on `max-restarts` or `breaker`, 4 when the lease is held by someone else.

## `rota limit`

| Usage | What it does |
|---|---|
| `rota limit watch [--timeout <seconds>] [--settle <seconds>]` | watch the orchestrator and the slots for a usage limit and sleep through it or switch accounts |
| `rota limit status` | show the usage-limit log |

`keepalive run` already runs the watcher; use `watch` for an orchestrator started another way. It refuses (exit 4) under a live supervisor or without the lease.

## `rota verdict`

| Usage | What it does |
|---|---|
| `rota verdict add [<branch>] --kind <review-spec\|review-quality\|second-opinion\|qa> --verdict <verdict> [--sha <sha>] [--body-file <path\|->] [--file-deferred --pr <ref>]` | record a verdict for a branch; `--sha` binds it to the reviewed commit and is refused (exit 4) unless that is the head of `origin/<branch>`; `--file-deferred` files each deferred (non-blocking, above `info`) finding as a task item related to the PR |
| `rota verdict show [<branch>]` | latest verdict of each kind for a branch |
| `rota verdict route [<branch>] --for <ship-review\|ship-second-opinion\|ship-qa\|queue>` | next step for a consumer of a branch's verdict |

Verdicts are `PASS`, `CONCERNS` or `FAIL` (`qa` also takes `INFRA-FAIL`). They live in `.rota/verdicts.json`, which `/rota-ship` routes on.

## `rota tracker`

| Usage | What it does |
|---|---|
| `rota tracker call [--provider auto\|github\|gitlab] -- <cli-arg>...` | run gh or glab with list limits and rate-limit handling |
| `rota tracker suggest-upstream --title <text> --body-file <path\|-> [--upstream-repo <owner/repo>] --confirm --confirm-note <answer>` | file a rota issue from a learning (manual gate) |

## `rota git`

| Usage | What it does |
|---|---|
| `rota git base` | print the resolved base branch |
| `rota git guard clean [--context <text>]` | fail when the working tree is dirty |
| `rota git guard feature-branch [<branch>]` | fail on the base branch or a detached HEAD |
| `rota git branch <name> --repos <a,b,...>` | create one branch in several sub-repos, or in none |
| `rota git worktree-path <branch>` | print the umbrella worktree path of a sub-repo branch |

## `rota review`

| Usage | What it does |
|---|---|
| `rota review scope [<branch>]` | commits, files, item IDs and origin entries of a branch |
| `rota review brief [<branch>]` | fresh-eyes second-opinion brief for a branch |
| `rota review scaffolding [<branch>] [--base <branch>]` | added diff lines that look like leftover task scaffolding |
| `rota review package [<branch>] [--base <ref>] [--since <sha>]` | write a branch's commits, `--stat` and full diff (`-U10`) to `.rota/review/<branch>.md` and print the path (`data.head` is the branch sha it diffed, the sha to pass `verdict add --sha`); `--since` packages only the commits after a sha |
| `rota review depth [<branch>] [--labels <a,b>]` | review depth `ship.review` picks for a branch (`full`, `light`, `none`) from its diff size and the labels given, and why |
| `rota review queue` | open issues waiting for review |

## `rota ship`

| Usage | What it does |
|---|---|
| `rota ship body [<branch>]` | build a PR body from a branch's commits: summary, items resolved, and an evidence table from the items' proof rows |
| `rota ship pr <branch> --title <text> --body-file <path\|-> [--items <ID>[,<ID>…]]` | push a branch and open a PR or MR; never removes a slot, the current directory or a worktree with changes |
| `rota ship merge <branch> --body-file <path\|-> [--confirm --confirm-note <answer>]` | merge a branch into the base branch with --no-ff; exit 4 when `ship.mergeApproval` needs a human |
| `rota ship pr-merge <pr> [--items <ID>[,<ID>…]] [--confirm --confirm-note <answer> \| --approval <escalation> \| --escalate]` | merge a PR in issue mode; exit 4 when `ship.mergeApproval` needs a human; `--escalate` asks on the approval thread, `--approval` cites the answer; give only one of the three |
| `rota ship undo [--cycle <hash>] [--allow-post-merge] [--apply]` | roll back the last cycle merge on the base branch |

## `rota release`

| Usage | What it does |
|---|---|
| `rota release version [--level patch\|minor\|major \| --to <X.Y.Z>]` | print the version file, and the next version with --level or --to |
| `rota release bump (--level patch\|minor\|major \| --to <X.Y.Z>) [--file <path>] [--kind <kind>]` | write the next version into the version file |
| `rota release host` | print the hosting kind of origin |
| `rota release notes --from commits [--since <ref>]` | release notes from commits |
| `rota release changelog <X.Y.Z> --body-file <path\|-> [--path <file>]` | add a release section to the changelog |
| `rota release pending` | how much has landed since the last release tag |
| `rota release milestone-check <MNN>` | list the open issues that block a milestone release, then one informational `check:` line from `rota milestone check` (`data.check`) |
| `rota release close-milestone <MNN> --release <X.Y.Z>` | close out a released milestone |
| `rota release push <X.Y.Z> [--branch <name>] [--tag-only\|--branch-only] --confirm --confirm-note <answer>` | push the release tag and branch to origin, or one of them (manual gate) |
| `rota release publish <X.Y.Z> --title <text> --body-file <path\|-> [--draft] --confirm --confirm-note <answer>` | create the GitHub or GitLab release, or finish the draft the release workflow made (manual gate) |

## `rota bugreport`

| Usage | What it does |
|---|---|
| `rota bugreport add --summary <text> [--expected <text>] [--verb <verb>] [--harness <name>] [--model <name>]` | record a mistake rota made in `.rota/bugreports.jsonl` |
| `rota bugreport list` | list the open records, redacted |
| `rota bugreport draft <id> [--harness <name>] [--model <name>]` | print the redacted report for a record |
| `rota bugreport duplicates <id>` | search the open issues on the rota repo for a likely duplicate |
| `rota bugreport file <id> --confirm --confirm-note <answer> [--comment <number>]` | file the report on the rota repo, or comment on a duplicate (manual gate) |
| `rota bugreport dismiss <id>` | drop a record without filing it |

A verb that fails with exit 70 writes a record by itself. See [`/rota-bugreport`](slash-commands.md#rota-bugreport).

## `rota gate`

| Usage | What it does |
|---|---|
| `rota gate list` | list every manual gate and the verbs that enforce it |

A gated verb refuses with exit 4 (`blockedBy: "manual gate"`) at every autonomy level unless `--confirm` and `--confirm-note` carry the human's answer, and appends each approval to `.rota/gate-audit.jsonl`. See [`references/manual-gates.md`](../../skills/references/manual-gates.md).

## `rota init`

| Usage | What it does |
|---|---|
| `rota init [--force]` | create or refresh `.rota/`, the managed blocks and `.gitignore`; in your home directory (no `.rota/` there or above) it refuses and points at `rota projects`, and `--force` inits there anyway |
| `rota projects` | list the projects registered on this machine; `rota init` registers, a missing path is marked, not pruned |
| `rota projects cleanup` | delete every entry whose directory is gone or has no `.rota/`, printing each; no preview |
| `rota projects remove <dir>` | drop one registry entry; the directory stays |
| `rota projects --ui` | terminal screen over the registry: status, last round, lease; open, cleanup, remove, add |
| `rota connect add <name> --kind jira\|linear [--site <url>] [--user <u>] [--deployment cloud\|datacenter] --token-stdin` | add or `--replace` a machine-wide tracker connection and store its token in the keychain, else a 0600 file; reports which. `--site` is required for jira; linear defaults to `https://api.linear.app` |
| `rota connect list` | connections with the store holding each secret (`keychain`, `file`, `none`) and whether `ROTA_<NAME>_TOKEN` overrides it; never the secret |
| `rota connect test <name>` | one authenticated read through the connection; exit 1 on an unknown name, no secret or a failed read; exit 5 when this build has no test read for the connection's kind yet |
| `rota connect remove <name>` | drop the connection and clear its secret from every store |
| `rota setup [--force] [--yes] [--set <key>=<value>]... [--save-global] \| --list` | `rota init` plus the main config choices, asked on a terminal; `--yes` takes defaults, `--list` prints the questions; on a terminal it ends by offering to save the answers (all on first use, else those that differ) as the global defaults, and `--save-global` does that without asking; refuses in your home directory like `rota init`, `--force` overrides |
| `rota init check` | is `.rota/` initialized (exit 1 when not) |
| `rota init umbrella (--repos <csv> \| --all \| --list)` | register sub-repos and make this directory an umbrella |

## Keeping this page current

The tables are generated from the usage lines in the
[verb contract](../contributing/contract/README.md) and the summaries from
`rota <group> <verb> --help --json`. When a verb changes, regenerate the row
rather than editing prose around it.
