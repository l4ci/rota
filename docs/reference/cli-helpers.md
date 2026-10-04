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
  the flag. Skills do the asking.
- **Bodies on stdin.** Any flag that takes a file path also accepts `-`
  (`--body-file -`).
- **Project root.** Verbs walk up from the working directory to the nearest
  `.rota/`. Only `rota init`, `rota init check`, `rota init umbrella`, `rota version` and
  `rota update` run without one.
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
[CLI conventions](../design/5.0-cli-conventions.md). Per-verb `data` shapes,
exit codes and repo scope: [verb contract](../design/contract/README.md).
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
| `rota config set <key> <value>` | set one key in .rota/config.json |
| `rota config check` | compare .rota/config.json with the schema |
| `rota config fill` | write the schema default for every missing key |

## `rota repo`

| Usage | What it does |
|---|---|
| `rota repo which` | the registered sub-repo the working directory is in |
| `rota repo resolve [<name>…]` | names to registered sub-repo paths |
| `rota repo umbrella` | is this an umbrella project |

## `rota id`

| Usage | What it does |
|---|---|
| `rota id next --kind <bugs\|features\|tasks\|milestones>` | mint the next counter ID |

## `rota item`

| Usage | What it does |
|---|---|
| `rota item create --kind <bugs\|features\|tasks> --title <text> [--tag <tag>] [--desc <text>] [--body-file <path\|->] [--related <text>] [--milestone <text>] [--repos <csv>] [--subsystem <text>] [--captured <YYYY-MM-DD>] \| --kind <kind> --raw-file <path\|->` | capture one item |
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
| `rota item field list <ID>` | every field of an item |
| `rota item complete <ID> [--commit <hash>] [--reason <done\|handed-off\|blocked\|dropped>] [--note <text>] [--no-proof]` | close an item |
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
| `rota issues list [--mine] [--label <name>] [--limit <n>]` | open upstream issues |
| `rota issues label <issue> (--add <name> \| --remove <name>)` | add or remove a label on an upstream issue |
| `rota issues imported [--for-repo <name>] [--open-only]` | backlog items that point at upstream issues |
| `rota issues close <issue> --commit <sha> [--item <ID>]` | close an upstream issue naming the shipping commit |
| `rota issues provider` | github, gitlab or unknown for the origin remote |

## `rota status`

| Usage | What it does |
|---|---|
| `rota status add <branch> --items <csv> [--worktree <path>] [--if-absent]` | record an active work stream |
| `rota status rm <branch>` | end a work stream and drop its handoff note |
| `rota status show <branch>` | which repo a branch's stream is in |
| `rota status handoff <branch> [--canonical]` | path of a branch's handoff note |
| `rota status loop start` | stamp the loop start, first write wins |
| `rota status loop clear` | remove the loop start stamp |
| `rota status loop show` | print the loop start stamp |

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

## `rota knowledge`

| Usage | What it does |
|---|---|
| `rota knowledge query <topic>… [--tier provisional\|confirmed\|deprecated] [--include-deprecated]` | print topic sections, tier-aware |
| `rota knowledge stats` | bullet count and size per topic |
| `rota knowledge add --topic <T> --title <S> --body-file <path\|-> [--date YYYY-MM-DD]` | add a bullet under a topic |
| `rota knowledge amend --topic <T> --fragment <F> --mode append --body-file <path\|->` | append text to an existing bullet |
| `rota knowledge replace --topic <T> --old <text> --new <text>` | replace text inside the one bullet that contains it |
| `rota knowledge rename-topic --from <X> --to <Y> [--title <T>]` | rename a topic or move one bullet |
| `rota knowledge hit --topic <T> --title <S>` | register a consulted bullet |
| `rota knowledge tier get --topic <T> --title <S>` | show one bullet's tier |
| `rota knowledge tier set --topic <T> --title <S> --tier provisional\|confirmed\|deprecated` | set one bullet's tier |
| `rota knowledge tier list [--tier provisional\|confirmed\|deprecated]` | list tracked bullets |
| `rota knowledge contradiction add --topic <T> --title <S> --text <text>` | queue a contradiction candidate |
| `rota knowledge contradiction list` | list the queue |
| `rota knowledge contradiction clear` | empty the queue |
| `rota knowledge contradiction has --topic <T> --title <S>` | exit 0 when the pair is queued |

## `rota decisions`

| Usage | What it does |
|---|---|
| `rota decisions query <topic>…` | print topic sections |
| `rota decisions auto-log --topic <T> --title <rule-title> --why <text> [--plan-key <key>] [--date YYYY-MM-DD]` | log an [Auto:Loop] decision |
| `rota decisions auto-since` | list this loop session's auto-logged decisions |

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
| `rota milestone add --title <text> --summary <text> [--depends M01,M02]` | mint a milestone |
| `rota milestone list` | list milestones |
| `rota milestone show <id>` | print a milestone |
| `rota milestone put <id> --body-file <path\|->` | replace a milestone's text |
| `rota milestone overview --body-file <path\|->` | replace the MILESTONES.md overview text |
| `rota milestone status <id> --to <planned\|active\|shipped\|archived>` | change a milestone's status |
| `rota milestone active` | IDs of active milestones |
| `rota milestone index` | regenerate the overview and vision block |

## `rota plan`

| Usage | What it does |
|---|---|
| `rota plan add <milestone>-<unit> --title <text> [--design <ID>] [--repos a,b]` | create a plan stub |
| `rota plan list [--milestone M01]` | list plans |
| `rota plan show <key>` | print a plan |
| `rota plan put <key> --body-file <path\|->` | replace a plan's text |
| `rota plan rm <key>` | delete a plan |
| `rota plan validate-docs <key>` | check doc-by-path deliverables |
| `rota plan rename-check <old> [-- <pathspec>…]` | files that mention a name |
| `rota plan uncertain <ID>` | uncertainty pre-flight for an item |

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
| `rota worker dispatch <slot> --body-file <path\|-> [--task <id>] [--relay] [--round <n>] [--boot-timeout <s>] [--kind <claude\|codex>] [--accept-codex-version]` | send a brief into a slot's session |
| `rota worker poll [<slot>] [--settle <seconds>] [--lines <n>]` | classify slot states from their panes |
| `rota worker gate <slot> --base <branch> [--check-only] [--no-verify] [--confirm --confirm-note <answer> \| --approval <escalation> \| --escalate]` | merge gate for one slot's branch or PR; exit 4 when `ship.mergeApproval` needs a human, `--escalate` asks on the thread, `--approval` cites the answer |
| `rota worker session check [--session <name>]` | inside a managed host session? (exit 1 when outside) |
| `rota worker session ensure [--session <name>] [--body-file <path\|->] [--boot-timeout <s>]` | hand the orchestrator off into a host session |
| `rota worker account list` | list accounts with their usage verdict |
| `rota worker account pick [--exclude <name>[,<name>...]]` | name the account with the most headroom |
| `rota worker account assign <slot> [--account <name>]` | put an account's config dir on a slot |

## `rota round`

The orchestrator's verbs for a [parallel round](../usage/parallel-rounds.md). All of them read and write `.rota/workers.json` and the round lease.

| Usage | What it does |
|---|---|
| `rota round start [--scope <slate\|milestone\|next>] [--items <ID>[,<ID>…]] [--slots <n>] [--base <branch>] [--holder-pid <n>]` | take the orchestrator lease, provision the roster, list candidates |
| `rota round candidates [--scope <slate\|milestone\|next>]` | list the items the round's scope allows, with readiness |
| `rota round assign <ID> [--agent <name>] [--tier <light\|standard\|heavy>] [--tier-reason <text>] [--kind <claude\|codex>] [--body-file <path\|->] [--siblings <ID>[,<ID>…]] [--check-only] [--accept-overlap] [--accept-codex-version] [--holder-pid <n>]` | check an item's readiness and hand it to a slot |
| `rota round wait [<slot>…] [--timeout <seconds>] [--settle <seconds>] [--lines <n>]` | block until a worker needs attention |
| `rota round status` | list the round's slots with host, PR and drift |
| `rota round reconcile [--apply]` | report drift between registry, host, git and forge; `--apply` repairs the safe kinds |
| `rota round report <slot> --state <done\|blocked\|idle\|dead\|limited> [--evidence <text>] [--pr <url\|number>]` | record a solo worker's result: state and PR |
| `rota round escalate send <number> [--pr] [--slot <name>] --title <text> --body-file <path\|-> [--timeout <seconds>]` | ask the human on an issue or PR thread |
| `rota round escalate check [<id>…]` | look for the human's answers |
| `rota round return <slot> --reason <text> [--note-file <path\|->] [--holder-pid <n>]` | a worker hands its issue back: park, comment, release |
| `rota round transfer <issue> --to <slot\|human> [--note-file <path\|->] [--body-file <path\|->] [--accept-overlap] [--holder-pid <n>]` | move an assigned issue to another slot or to the human |
| `rota round reclaim <slot> [--force] [--note-file <path\|->] [--holder-pid <n>]` | free a dead or stalled slot and make its issue assignable |
| `rota round wind-down [--no-verify] [--holder-pid <n>]` | re-verify the base, park every slot, release the lease |

A second `round start` is refused (exit 4) while another orchestrator holds the lease. `round wait` exits 1 on `--timeout` with `data.timedOut: true`, which is an answer, not a fault.

## `rota doctor`

| Usage | What it does |
|---|---|
| `rota doctor` | preflight: git, host, forge, accounts, herdr hook, orchestrator hooks, rota, codex |

Read-only, runs without `.rota/`. Exit 1 when any check fails; every failure carries a `hint` with the fix. See [preflight](preflight.md#rota-doctor).

## `rota reap`

| Usage | What it does |
|---|---|
| `rota reap [--kind <worktree\|branch\|tab\|process\|lease>[,…]] [--apply]` | list, and with `--apply` remove, what a round left behind that nothing live owns |

Previews by default. It never kills a running agent and never deletes work: a candidate that holds uncommitted changes or unmerged commits is listed with `held` and left alone. Exit 1 under `--apply` when a deletion failed.

## `rota hook`

| Usage | What it does |
|---|---|
| `rota hook install [--scope <project-local\|project\|user>] [--wrap-statusline]` | merge the hooks and the statusline into a Claude Code settings file |
| `rota hook uninstall [--scope <project-local\|project\|user>]` | remove what install wrote and restore a wrapped statusline |
| `rota hook stop` | Stop hook: block above the context threshold until a handoff is written |
| `rota hook session-start` | SessionStart hook: inject and consume the handoff |

`install` and `uninstall` are the ones you run; `stop` and `session-start` are what Claude Code calls. Default scope is `project-local` (`.claude/settings.local.json`). `install` exits 4 rather than replace a statusline you already have unless you pass `--wrap-statusline`. The two hooks always exit 0.

## `rota statusline`

| Usage | What it does |
|---|---|
| `rota statusline dump [--then <command>]` | record the session state from stdin, then run `--then` with the same input |

A statusline command, not a query: it prints nothing of its own, always exits 0 and takes no `--json`. `rota hook install --wrap-statusline` sets it up around the statusline you have.

## `rota keepalive`

| Usage | What it does |
|---|---|
| `rota keepalive run [--max-restarts <n>] [--breaker <n>] [--backoff <seconds>] [--prompt <text>] [--no-limits] -- <command> [<arg>…]` | run the orchestrator as a supervisor and restart it when it exits with a fresh handoff |
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
| `rota verdict add [<branch>] --kind <review-spec\|review-quality\|second-opinion\|qa> --verdict <verdict> [--body-file <path\|->]` | record a verdict for a branch |
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
| `rota review queue` | open issues waiting for review |

## `rota ship`

| Usage | What it does |
|---|---|
| `rota ship body [<branch>]` | build a PR body from a branch's commits |
| `rota ship pr <branch> --title <text> --body-file <path\|-> [--items <ID>[,<ID>…]]` | push a branch and open a PR or MR |
| `rota ship merge <branch> --body-file <path\|-> [--confirm --confirm-note <answer>]` | merge a branch into the base branch with --no-ff; exit 4 when `ship.mergeApproval` needs a human |
| `rota ship pr-merge <pr> [--items <ID>[,<ID>…]] [--confirm --confirm-note <answer>]` | merge a PR in issue mode; exit 4 when `ship.mergeApproval` needs a human |
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
| `rota release milestone-check <MNN>` | list the open issues that block a milestone release |
| `rota release close-milestone <MNN> --release <X.Y.Z>` | close out a released milestone |
| `rota release push <X.Y.Z> [--branch <name>] [--tag-only\|--branch-only] --confirm --confirm-note <answer>` | push the release tag and branch to origin, or one of them (manual gate) |
| `rota release publish <X.Y.Z> --title <text> --body-file <path\|-> [--draft] --confirm --confirm-note <answer>` | create the GitHub or GitLab release, or finish the draft the release workflow made (manual gate) |

## `rota gate`

| Usage | What it does |
|---|---|
| `rota gate list` | list every manual gate and the verbs that enforce it |

A gated verb refuses with exit 4 (`blockedBy: "manual gate"`) at every autonomy level unless `--confirm` and `--confirm-note` carry the human's answer, and appends each approval to `.rota/gate-audit.jsonl`. See [`references/manual-gates.md`](../../references/manual-gates.md).

## `rota init`

| Usage | What it does |
|---|---|
| `rota init` | create or refresh `.rota/`, the managed blocks and `.gitignore` |
| `rota init check` | is `.rota/` initialized (exit 1 when not) |
| `rota init umbrella (--repos <csv> \| --all \| --list)` | register sub-repos and make this directory an umbrella |

## Keeping this page current

The tables are generated from the usage lines in the
[verb contract](../design/contract/README.md) and the summaries from
`rota <group> <verb> --help --json`. When a verb changes, regenerate the row
rather than editing prose around it.
