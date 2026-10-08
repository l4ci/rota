---
verified-sha: d0f5a4238843e7def00adf8d11e729c4333379fa
refs:
  - docs/contributing/contract/cli-conventions.md
  - internal/cli/contract_test.go
---

# `rota` verb contract

This directory defines every `rota` verb's arguments, flags, `--json` data and exit codes. The black-box smoke suite (A2, #46) tests against it, and the Go port (A3 to A9) implements it. Global flags, the JSON envelope, stderr format, the exit-code table, write rules and config loading are fixed by the [CLI conventions](cli-conventions.md) (A3, #47). Nothing here overrides them: failure `data` on exit 1 and 4, exit 4 for mutating verbs only, and `changed` in failure data all come from the conventions.

Status: **reviewed by the maintainer on 2026-10-02, who signed off on the defaults pending the changes now applied** (quote: "Defaults + you verify"). Arguments were redesigned on purpose (maintainer, 2026-10-02: "Redesign arguments now"), so many verbs differ from the helper they replace. During the port, `test/hv-shim` translates each new-shape call into the old helper call. The `old:` line under each verb is the shim's mapping.

## Per-verb rules

These rules fill in what the conventions leave to each verb. Every verb below follows them, and any exception says why.

1. **Positionals are for the thing the verb acts on**: an item ID, a key, a topic, a name. Everything else is a flag. A verb takes at most one kind of positional. A kind is a category, not the target, so `kind` is always the flag `--kind` (`rota id next --kind bugs`, `rota item create --kind bugs`). Setting a state or status is `--to <value>` everywhere (`item state --to`, `milestone status --to`).
2. **Lists.** Several targets of the same kind are variadic positionals (`rota backlog milestones B01 F02`), never one comma-joined argument. A list carried in a flag is comma-separated (`--repos a,b`); no flag is repeatable.
3. **Bodies** come from `--body-file <path|->`, never from positional text or inline flag values. A verb that already takes `--body-file` and needs a second, different file input names it after its content; the only case is `item create --raw-file`, the preformatted bullet. Short single-line values (title, summary, note) are flag values: `--title`, `--summary`, `--note`.
4. **Families share one shape where the verb exists.** milestone, plan, design and spike use `add` (create; refused with exit 4 if the key exists), `list`, `show <key>`, `put <key> --body-file`, `rm <key>`. The key is always the first positional. Not every family has every verb: milestone has no `rm` (it ends as `archived`), spike has neither `put` nor `rm` (`finish` ends it), and `milestone add` mints its key.
5. **Preview before bulk rewrites.** Verbs that rewrite many files, history or several items at once (`migrate hv`, `migrate issues`, `ship undo`, `item rm`) only report what they would do unless `--apply` is given. Every other mutating verb acts directly. No verb uses `--dry-run`, `--force` or `--yes`. A preview that writes nothing adds the warning `preview only; pass --apply` to `warnings`.
6. **Check verbs** answer a question: guards, gates, `has`-style lookups. Exit 0 means the predicate in the verb's name holds (`item ready` is ready, `git guard clean` is clean, `item shipped` found evidence); every other answer is 1. `data` carries the answer (a named boolean or a verdict) on both exits (conventions: failure data). `--json` callers never need the exit code.
7. **Verdict vs check.** A classifier verb (`worker poll`, `update`, `version --drift`, `plan validate-docs`) returns its verdict in `data` and exits 0 whatever the verdict. A check verb exits 0 when the predicate in its name holds and 1 for every other verdict, with the verdict in `data`. The predicate is the one the name asks: `plan uncertain` exits 0 when the item is uncertain, `item shipped` when ship evidence is found, `worker gate` when the gate passes, `worker session check` when inside a managed session, `worker reset --check-only` when the slot holds no work. The check verbs are `config check`, `repo umbrella`, `item ready`, `item shipped`, `plan uncertain`, `knowledge contradiction has`, `worker gate`, `worker session check`, `worker reset --check-only`, `worker account pick`, `git guard clean`, `git guard feature-branch`, `release milestone-check`, `init check` and `doctor`. A mutating verb that declines to act exits 4 (`worker reset` without `--check-only`, `worker done` without a `test.fast` proof row at HEAD, `release bump` to a version that is not greater, `ship pr-merge` with an unproven item, `item complete` without proof, any gated verb whose manual gate is not cleared, `ship pr`, `ship merge` or `ship pr-merge` after a recorded FAIL, a debug attempt past the Iron Law), and its failure `data` says what blocked it. `worker gate` is a check verb that still exits 4 on a manual-gate refusal (B1), because it refuses before judging anything. A verb that changed state on the way to exit 1 or 4 (`worker gate` after a merge, `ship pr-merge` recording `changes-requested`) reports `"changed": true` in its failure `data`. A read-only verb never exits 4.
8. **Text output** is one human line per result, or the body of the requested file for `show` verbs. The contract defines only `--json` data. Smoke asserts on `--json` except where it checks that a `show` verb prints the stored body verbatim.
9. **`--repo` is global.** Each verb states whether it is repo-scoped. A verb with no repo scope rejects `--repo` (exit 2), per the conventions. On a repo-scoped verb, a name that is not a registered sub-repo exits 3 (`resolution`) before any check of the verb's own, so an entry's "`--repo` exits 2" means a registered name the verb does not accept.
10. **No test-only flags.** Fixture hooks the old helpers carried as flags (`--fixture`, `--status`) become environment variables named `ROTA_TEST_*`, which the contract lists per verb and which are not part of the CLI.
11. **Item IDs** are strings in `data`. File mode: `"B07"`. Issue mode: the issue number, `"12"`. Umbrella issue mode: `"<repo>:12"`. Every `data` object that names an item by `id` also has `type` (`"B"`, `"F"` or `"T"`) in both modes, so the type letter is never lost; rows inside the `bugs`, `features` and `tasks` lists of `backlog list` are already grouped by kind and omit it. Lists of bare IDs (`ids`, `items` as `[]string`) use the same `id` spelling and carry no types. Inputs accept every form the old helpers accepted (`B7`, `#7`, `7`, `repo:B7`, `repo#7`). A flag that carries a list of item IDs is always `--items <ID>[,<ID>…]`.
12. **Data shapes.** A list verb wraps its list in an object keyed by a domain noun (`{"items": […]}`, `{"plans": […]}`). A verb whose subject is one item names it `id`. Every mutating verb reports `changed`.
13. **Flag names.** No verb flag reuses a global flag's name (`--json`, `--ui`, `-C`/`--cwd`, `--repo`, `-h`/`--help`), as the conventions require.

## Verb entry format

```
### rota <group> <verb>
rota <group> <verb> <positional> [--flag <value>] [--bool]
repo: scoped | none
data: {"field": type, …}
exit: 1 when …; 3 when …; 4 when …
old: hv-helper <args in old order>    (shim mapping)
shim: how the shim builds `data` and maps old exit codes    (optional)
note: …    (optional)
env: ROTA_TEST_* hooks    (optional; rule 10)
```

Field types use JSON names (`string`, `number`, `bool`, `object`, `[]string`). `?` after a field name means it may be absent. Exits 0, 2 and 70 are implied and not listed. `exit: implied only` means the verb adds no other code.

## Verb index

| Group | Verbs | Phase |
|---|---|---|
| core | `version` (A3), `update` (A4) | A3, A4 |
| config | `show`, `set`, `check` (A4), `fill` (A9) | A4, A9 |
| repo | `which`, `resolve`, `umbrella` | A4 |
| id | `next` | A4 |
| item | `create`, `show`, `claim`, `release`, `ready`, `state`, `comment add`, `comment list`, `note add`, `note show`, `note rm`, `field get`, `field set`, `field list`, `complete`, `reopen`, `rm`, `shipped` | A4 |
| backlog | `list`, `ids`, `milestones`, `drift`, `backfill`, `archive`, `stale` | A4 |
| summary | `summary` | A4 |
| issues | `list`, `label`, `imported`, `close`, `provider` | A4 |
| status | `add`, `rm`, `show`, `handoff` | A4 |
| refactor | `age`, `reset`, `targets` | A4 |
| migrate | `issues` (A4), `hv` (#236) | A4 |
| knowledge | `query`, `stats`, `topics`, `add`, `amend`, `rename-topic`, `hit`, `tier get`, `tier set`, `tier list`, `contradiction add`, `contradiction list`, `contradiction clear`, `contradiction has` | A5 |
| decisions | `query`, `topics` | A5 |
| glossary | `read`, `write`, `import` | A5 |
| block | `<key>`, `skills` | A5 |
| instructions | `init` | A5 |
| map | `query`, `index`, `stats` | A5 |
| qa | `query`, `index` | A5 |
| milestone | `add`, `list`, `show`, `put`, `status`, `active`, `index` | A6 |
| plan | `add`, `list`, `show`, `put`, `rm`, `validate-docs`, `rename-check`, `uncertain` | A6 |
| design | `add`, `list`, `show`, `put`, `rm`, `amend` | A6 |
| spike | `add`, `finish`, `list`, `show` | A6 |
| verdict | `add`, `show`, `route`, and `debug verdict` | B2 |
| proof | `add`, `record`, `show` | A6 |
| debug | `counter init`, `counter record-attempt`, `counter fail`, `counter pass`, `counter show`, `counter summary`, `counter clear`, `counter inc-cycle` (A6), `reset` (B3) | A6, B3 |
| worker | `pool init`, `pool list`, `pool reap`, `reset`, `dispatch`, `poll`, `gate` (B1 gates its merge), `session check`, `session ensure`, `account list`, `account pick`, `account assign` | A7 |
| tracker | `call`, `suggest-upstream` (B1 adds the gate) | A8, B1 |
| git | `base`, `guard clean`, `guard feature-branch`, `branch`, `worktree-path` | A8 |
| review | `scope`, `brief`, `scaffolding`, `package`, `queue`, `depth` | A8 |
| ship | `body`, `pr`, `merge`, `pr-merge`, `undo` (B1 gates `merge` and `pr-merge`) | A8, B1 |
| release | `version`, `bump`, `host`, `notes`, `changelog`, `pending`, `milestone-check`, `close-milestone` (A8), `push`, `publish` (B1) | A8, B1 |
| init | `init`, `init check`, `init umbrella`, `projects` (#24) | A9 |
| skills | `install`, `update`, `uninstall`, `status` (F6a) | F6a |
| gate | `list` | B1 |
| round | `wait` (C1), `status`, `reconcile` (C2), `start`, `candidates`, `assign`, `wind-down` (C3), `escalate send`, `escalate check` (C4), `return`, `transfer`, `reclaim` (C10), `report` (C8) | C1, C2, C3, C4, C10, C8 |
| doctor | `doctor` (C6; D1 adds `statusline` and `stop-hook` checks) | C6, D1 |
| statusline | `dump` (D1) | D1 |
| hook | `stop`, `session-start`, `install`, `uninstall` (D1) | D1 |
| keepalive | `run`, `status` (D2); `run --first-prompt` (#19) | D2 |
| orchestrate | `orchestrate` (#19) | C11 |
| limit | `watch`, `status` (D3) | D3 |
| reap | `reap` (C6) | C6 |

## Group files

The entries live one file per verb group, so a change to one group does not conflict with another. The preamble above (rules, entry format, index, shared definitions) stays here.

| File | Section | Verbs |
|---|---|---|
| [version-config-repo.md](version-config-repo.md) | A3 and A4 | `version`, `update`, `config`, `repo` |
| [backlog.md](backlog.md) | A4 | `id`, `item`, `backlog`, `summary`, `issues`, `status`, `refactor`, `migrate issues` |
| [knowledge.md](knowledge.md) | A5 | `knowledge`, `decisions`, `glossary`, `block`, `instructions`, `map`, `qa`, `migrate hv` |
| [milestones.md](milestones.md) | A6 | `milestone`, `plan`, `design`, `spike`, `proof`, `debug` |
| [workers.md](workers.md) | A7 | `worker` (pool, dispatch, poll, gate, session, account), `test run`, `test ledger check` |
| [tracker-git-ship.md](tracker-git-ship.md) | A8 | `tracker`, `git`, `review`, `ship`, `release` |
| [init.md](init.md) | A9 | `init` |
| [verdicts.md](verdicts.md) | B2 | `verdict` |
| [gates.md](gates.md) | B1 | manual gates, `gate list` |
| [verdict-refusals.md](verdict-refusals.md) | B3 | verdict refusals, the Iron Law, loop-only flags |
| [rounds.md](rounds.md) | C | `round`, `doctor`, `reap` |
| [codex-workers.md](codex-workers.md) | D1, D2, E | Codex workers, `statusline`, `hook`, `keepalive` |
| [agent-skills-spec.md](agent-skills-spec.md) | D3, E2 | Agent Skills spec and Codex discovery, `limit` |
| [embedded-skills.md](embedded-skills.md) | F6a | `skills` |

## Shared definitions

Entries use these names instead of restating them.

- **scope S.** A repo-scoped verb's scope. `--repo <name>` selects a registered sub-repo. With no `--repo`, S resolves from the working directory: inside a registered sub-repo it is that sub-repo, otherwise the umbrella, as the old `hv-knowledge-scope.sh` did. To reach umbrella scope from inside a sub-repo, pass `-C <umbrella root>`. Outside umbrella mode S is always the project.
- **text-read data.** `{"text": string, "missing"?: []string}`. `text` is the stdout the old helper printed (also what text mode prints). `missing` lists requested names that matched nothing. The shim cannot compute `missing` for helpers that don't report it, and omits it there.
- **`backend` in an `exit:` line.** The verb works on one backlog backend only (`backlog.backend` `file` or `issues`) and was called under the other. A mutating verb exits 4 `refused`; a read-only verb (no `changed` in its `data`) exits 1 `failed`, because the conventions forbid exit 4 for read-only verbs. Either way the failure data is `{"blockedBy": "backend", "changed": false}`, and the hint names the verb to use instead when one exists. Old backend helpers exited 2 for this.
- **`tracker` in an `exit:` line.** Exit 5 `unavailable` when the tracker or forge CLI is missing or failing, exit 6 `retry` when it rate-limits. Old backend helpers exited 3 and 4 for these.
- **Failure data.** Exit 1 carries the verb's answer in `data`, which is the same shape as its success `data` unless the entry says otherwise. Exit 4 carries `{"blockedBy": string, "changed": bool}` unless the entry defines more: `blockedBy` names the invariant in one word or phrase (`exists`, `base branch`, `proof missing`, `backend`), and `changed` is true only if the verb changed state before refusing. Every other failure sends `error` alone (conventions).
- **Invalid `backlog.backend`.** A value other than `file` or `issues` is a corrupt config: every verb that reads the backend exits 70.
- **Lock timeout.** Every verb that writes a state file exits 6 when the lock times out (conventions). Entries don't repeat it.
- **Old backend rc mapping.** Unless an entry says otherwise, the shim maps an old backend helper's rc 1 to 3 when stderr says `not found` and to 2 for bad arguments, rc 2 to 4 (`backend`), rc 3 to 5 and rc 4 to 6 (`tracker`).
- **Umbrella issue mode IDs.** `<repo>:<ID>` and `<repo>#<n>` always resolve. A bare ID that exactly one sub-repo holds resolves to it, as the old helper did (`_pick` in `bin/hvlib_backend.py`). A bare ID that several sub-repos hold (`F42`, `#42`) is exit 2, and the message lists the qualified candidates in the `data.id` spelling (`ghrepo:42, other:42`). The global `--repo` selects a sub-repo scope for the verb and never rewrites an ID. The shim maps the old TrackerError rc 1 with `ambiguous across sub-repos` to 2.
- **Dual spelling.** The backend still writes the old spelling. `bin/hvlib_backend.py` renders the issue bullet head as `**[{letter}{number}] …**` (line 714) and returns `f"{letter}{number}"` from create (lines 857 to 858, where the `{ID}` placeholder in the issue body is also replaced by it), so commits, PR bodies and issue bodies carry `[B7]`. A caller maps `[B7]` to `id` `"7"` by dropping the type letter; the letter is `type`. This PR does not change the backend's write format. The shim does the mapping when it builds `data`.
- **Checkout verbs under `--repo`.** A repo-scoped verb that reads or writes a git checkout or files in it (`release version|bump|host|changelog|pending`, `release notes --from commits`, `git`, `review` and `ship` verbs) runs in the named sub-repo's checkout, not at the umbrella root.
- **Commands that run without `.rota/`.** The conventions name `rota init` as running without a project root. `init check` and `init umbrella` count as part of it: they act on the working directory after `-C`, with no walk-up.
- **`slot`** (A7 verbs, which all act on `.rota/workers.json` at the project root). `{"name": string, "branch": string, "worktree": string, "base": string, "handle"?: string, "state": string, "task"?: string, "pr"?: string, "relays": [{"round": number, "ts": string, "summary": string}], "configDir"?: string, "account"?: string}`. Null registry fields are absent in `data`.
