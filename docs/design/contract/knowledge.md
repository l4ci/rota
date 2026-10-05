## A5: knowledge, decisions, glossary, blocks, map, qa

### rota knowledge query
rota knowledge query <topic>… [--tier provisional|confirmed|deprecated] [--include-deprecated]
repo: scoped (scope S). Umbrella scope reads `.rota/KNOWLEDGE.md`. A sub-repo scope reads umbrella then sub-repo, grouped per topic with `> from:` lines, as the old helper did.
data: text-read, with `missing` always present
exit: 2 when no topic is given or `--tier` is not one of the three values
old: hv-knowledge-query [--repo <name>] [--include-deprecated] [--tier <t>] <topic>…    (all new args map 1:1 to the same flag or positional)
shim: stdout is `text`; each stderr `warning: … no topic heading matches '<x>'` line gives one `missing` entry and one `warnings` entry.
note: zero topics is exit 2, where the old helper exited 0 silently; a topic with no matching heading stays a warning and exit 0.
note: `text` is the old markdown verbatim, including the ` (provisional)` suffix and `> from:` lines, not a structured bullet list.

### rota knowledge stats
rota knowledge stats
repo: none
data: {"topics": [{"name": string, "bullets": number, "bytes": number}]}
exit: implied only. A missing KNOWLEDGE.md gives `{"topics": []}`.
old: hv-knowledge-stats
shim: stdout JSON passes through unchanged; `topics[].name|bullets|bytes` already match.
note: the old helper reads only the umbrella file, so the verb has no repo scope and a learn run inside a sub-repo still gets umbrella numbers.

### rota knowledge add
rota knowledge add --topic <T> --title <S> --body-file <path|-> [--date YYYY-MM-DD]
repo: scoped (scope S)
data: {"topic": string, "title": string, "changed": bool}
exit: 2 when `--topic`, `--title` or `--body-file` is missing; 3 when the `## <T>` heading does not exist in S's KNOWLEDGE.md
old: hv-knowledge-merge [--repo <name>] --topic <T> --title <S> [--date <d>] [--body <text>]    (`--body-file -` pipes the shim's stdin to old stdin; `--body-file <p>` becomes `< <p>`; `--date` is the same flag)
shim: stderr `wrote:` gives `changed: true`; `noop: … (title already present)` gives `changed: false`.
note: the old inline `--body` and the TTY-blocking stdin fallback are dropped. Dedup is by case-insensitive title, so a repeated title is `changed: false`. A successful add also initializes the bullet's tier entry as provisional. The missing-topic exit moves from 1 to 3.

### rota knowledge amend
rota knowledge amend --topic <T> --fragment <F> --mode append --body-file <path|->
repo: scoped (scope S)
data: {"topic": string, "changed": bool}
exit: 2 when a flag is missing, `--mode` is not `append`, or when no `--repo` is given and the fragment matches in both the umbrella and the resolved sub-repo (hint: pass `--repo`); 3 when the topic or fragment is not found
old: hv-knowledge-amend [--repo <name>] --topic <T> --fragment <F> --append <text>    (`--mode append` selects `--append`; the text is the body file's contents)
shim: reads the body file (or stdin for `-`) into the text argument. The old helper prints nothing, so `changed` is computed by comparing the target KNOWLEDGE.md before and after the call.
note: `--append TEXT` becomes `--mode append` plus `--body-file`, per rule 3, as for `design amend`; `append` is the only mode, so a later mode can be added. Without `--repo`, a single match across umbrella and the resolved sub-repo is amended, as before. A multi-file match was exit 1 and is now exit 2 (a missing decision, per the conventions).

### rota knowledge replace
rota knowledge replace --topic <T> --old <text> --new <text>
repo: scoped (scope S); edits S's KNOWLEDGE.md only, with no umbrella/sub-repo fallback search
data: {"topic": string, "changed": bool}; on exit 4 `{"blockedBy": "ambiguous", "changed": false}`
exit: 2 when `--topic`, `--old` or `--new` is missing (`--new ""` is allowed and deletes the text); 3 when the topic is not found or `<old>` is in no bullet of it; 4 when `<old>` is in more than one bullet of it (hint: use a longer fragment)
old: none (new in T4)
note: `--old` is a case-sensitive exact substring. A bullet runs from its `- ` line to the next sibling bullet, so a wrapped bullet matches as a whole. Every occurrence inside the one matched bullet is replaced. `changed` is false when `--old` equals `--new`. When the edit changes the bullet's bold title, the tier entry is re-keyed to the new title (hits and tier kept). The Glossary topic has no tier entries, so nothing is re-keyed there.
note: `amend` only appends and `rename-topic` only renames or moves; this is the verb that corrects a stale bullet. Unlike `amend`, ambiguity is exit 4 (the file is intact and the caller can retry with a longer fragment), not 2. The ticket asked for exit 4 on zero matches too; zero matches stays exit 3, as for `amend` and `rename-topic`, because the bullet could not be resolved (unratified).

### rota knowledge rename-topic
rota knowledge rename-topic --from <X> --to <Y> [--title <T>]
repo: scoped (scope S). The old helper applied `--repo` to the tier sidecar only and always edited the umbrella `.rota/KNOWLEDGE.md`. The contract requires both the heading edit and the sidecar re-key to use S.
data: {"from": string, "to": string, "title"?: string, "mode": "topic"|"bullet", "changed": bool}
exit: 2 when `--from` or `--to` is missing; 3 when topic X is not found, when bullet T is not found, or (bullet mode) when topic Y does not exist; 4 when (topic mode) topic Y already exists
old: hv-knowledge-rename-topic [--repo <name>] --from <X> --to <Y> [--title <T>]    (same flags)
shim: `mode` is `bullet` iff `--title` was passed; `changed` is false only for `--from` equal to `--to` (the old helper printed nothing).
note: the shim cannot honor the contract's file scope, so `--repo <sub-repo>` runs sidecar-only until the port, and smoke must not assert sub-repo file edits for this verb before then.
note: one verb keeps both modes, selected by `--title`, instead of splitting out a `move-bullet` verb.

### rota knowledge hit
rota knowledge hit --topic <T> --title <S>
repo: scoped (scope S)
data: {"topic": string, "title": string, "hits": number, "tier": "provisional"|"confirmed"|"deprecated", "promoted": bool, "promotionBlocked": bool, "changed": bool}
exit: 2 when `--topic` or `--title` is missing
old: hv-knowledge-hit [--repo <name>] --topic <T> --title <S>    (same flags)
shim: stderr `auto-promoted:` sets `promoted: true`; stderr `skip-auto-promote:` sets `promotionBlocked: true`; `hits` and `tier` come from a follow-up `hv-knowledge-tier --get` on the same scope.
note: the result moves from stderr into `data`. For topic `Glossary` the old helper does nothing and exits 0; the contract returns `changed: false` with `hits` 0 and `tier` `provisional`. An untracked bullet is created with hits 1, provisional (`bump_hit` behaviour). The threshold is config `learn.promoteThreshold` (default 3), and promotion is skipped while the pair is in the contradiction queue.
note: the port reads the value it reports in the same locked pass as the write; the shim's follow-up read can skew it under concurrent writers.

### rota knowledge tier get
rota knowledge tier get --topic <T> --title <S>
repo: scoped (scope S)
data: {"topic": string, "title": string, "found": bool, "tier"?: string, "hits"?: number, "lastSeen"?: string}
exit: 2 when `--topic` or `--title` is missing
old: hv-knowledge-tier [--repo <name>] --get --topic <T> --title <S>
shim: stdout `{}` gives `found: false`; otherwise the keys are copied across (`lastSeen` is already camelCase).
note: redesign. `rota knowledge tier` is now a group with sub-verbs `get`, `set` and `list`, replacing the old `--get/--set/--init/--inc/--list` mode flags. An untracked entry is `found: false` with exit 0, as the old helper returned `{}`. Topic `Glossary` is always `found: false`.

### rota knowledge tier set
rota knowledge tier set --topic <T> --title <S> --tier provisional|confirmed|deprecated
repo: scoped (scope S)
data: {"topic": string, "title": string, "tier": string, "previousTier"?: string, "changed": bool}
exit: 2 when a flag is missing or `--tier` is not one of the three values
old: hv-knowledge-tier [--repo <name>] --set --topic <T> --title <S> --tier <t>    (`--tier` is always emitted after `--set`)
shim: `previousTier` and `changed` come from a `--get` call made before the `--set`.
note: `--tier` is a plain value here and a filter only in `tier list`. Its meaning no longer depends on flag order, because each sub-verb fixes it. Setting an untracked entry creates it with hits 0 (old behaviour), with `previousTier` absent. Topic `Glossary` is a no-op with `changed: false` and exit 0.
note: the port reads the value it reports in the same locked pass as the write; the shim's follow-up read can skew it under concurrent writers.

### rota knowledge tier list
rota knowledge tier list [--tier provisional|confirmed|deprecated]
repo: scoped (scope S)
data: {"entries": [{"topic": string, "title": string, "tier": string, "hits": number, "lastSeen": string}]}
exit: 2 when `--tier` is not one of the three values
old: hv-knowledge-tier [--repo <name>] --list [--tier <t>]    (`--tier` is emitted after `--list`, so it is read as a filter)
shim: stdout JSON array becomes `entries`.
note: Glossary entries are never listed. `tier init` and `tier inc` are not verbs: `init` happens lazily inside `knowledge add` and on first read (the absorbed `hv-knowledge-migrate`), and `inc` is covered by `knowledge hit`. Smoke that used `--init` to seed state must seed through `knowledge add` instead.

### rota knowledge contradiction add
rota knowledge contradiction add --topic <T> --title <S> --text <text>
repo: none (the queue is the umbrella file `.rota/knowledge-contradictions.json`)
data: {"topic": string, "title": string, "pending": number, "changed": true}
exit: 2 when a flag is missing
old: hv-knowledge-contradiction --add --topic <T> --title <S> --text <text>
shim: `pending` is the length of a follow-up `--list`.
note: redesign. `rota knowledge contradiction` is now a group with sub-verbs `add`, `list`, `clear` and `has`, replacing the `--add/--list/--clear/--has` flags. The old helper appends without dedup and `changed` is always true; this is kept. `--text` is a single-line flag (the skill truncates to 200 characters).
note: the port reads the value it reports in the same locked pass as the write; the shim's follow-up read can skew it under concurrent writers.

### rota knowledge contradiction list
rota knowledge contradiction list
repo: none
data: {"items": [{"topic": string, "title": string, "correctionText": string, "loggedAt": string}]}
exit: implied only
old: hv-knowledge-contradiction --list
shim: stdout JSON array becomes `items` (keys already camelCase).

### rota knowledge contradiction clear
rota knowledge contradiction clear [--topic <T> --title <S>]
repo: none
data: {"cleared": number, "changed": bool}
exit: implied only
old: hv-knowledge-contradiction --clear
shim: `cleared` is the length of a `--list` made before the `--clear`; `changed` is `cleared > 0`.
note: addition. With `--topic` and `--title` (given together, else usage exit 2) only entries for that pair are dropped and `cleared` counts them; the rest of the queue stays. Bare `clear` still empties the queue. `/rota-learn` uses the pair form so a deferred candidate survives.

### rota knowledge contradiction has
rota knowledge contradiction has --topic <T> --title <S>
repo: none
data: {"has": bool}
exit: 1 when the pair is not in the queue; 2 when a flag is missing
old: hv-knowledge-contradiction --has --topic <T> --title <S>
shim: old exit 0 gives `has: true`; old exit 1 gives `has: false` and exit 1.

### rota decisions query
rota decisions query <topic>…
repo: none (umbrella `.rota/DECISIONS.md` only)
data: text-read
exit: 2 when no topic is given
old: hv-decisions-query <topic>…    (same positionals)
shim: stdout is `text`; `missing` is omitted.
note: unlike `knowledge query`, an unmatched topic emits no warning, as old, and only the port fills `missing`. Zero topics moves from exit 1 to 2.

### rota glossary read
rota glossary read <term>…
repo: scoped (scope S). A sub-repo scope reads umbrella first, then the sub-repo, as before.
data: text-read
exit: 2 when no term is given
old: hv-glossary-read [--repo <name>] <term>…    (`--repo` placed first)
shim: stdout is `text`, including the `> from: <path> (## Glossary)` lines that callers parse; `missing` is omitted.
note: matching is case-insensitive on the term name, and a term with no match is silent, exit 0. Exit codes changed: no terms or a bad flag was 1 and is now 2; a scope error was 2 and is now 3.

### rota glossary write
rota glossary write <term> --def <text> [--alias <a,b>] [--not <n,m>] [--touch]
repo: scoped (scope S)
data: {"term": string, "changed": bool}
exit: 2 when `--def` is missing; 3 when S's KNOWLEDGE.md or its `## Glossary` heading is missing; 4 when an alias already belongs to another term (`blockedBy: "alias-collision"`)
old: hv-glossary-write <term> --def <text> [--alias <a,b>] [--not <n,m>] [--touch] [--repo <name>]    (same flags; `--not ""` clears the Not list)
shim: stderr `wrote: Glossary/<term>` is the success signal; `changed` is computed by comparing KNOWLEDGE.md before and after; the old `--batch` rejection is dropped.
note: `--alias` and `--not` take comma-separated lists (rule 2). Passing `--not` with an empty value clears the list, and omitting it leaves the list alone. As a side effect the verb rewrites the `knowledge` managed block in S's instructions file. The old exit 3 for alias collision is now 4; missing `--def` and parse errors stay 2; a missing target file is now 3 (was 2).

### rota glossary import
rota glossary import --body-file <path|-> [--touch]
repo: scoped (scope S)
data: {"imported": number, "terms": []string, "changed": bool}
exit: 2 when `--body-file` is missing or a manifest line lacks a term or definition; 3 when the manifest file, S's KNOWLEDGE.md or its `## Glossary` heading is missing; 4 when an alias collides with an existing term or inside the batch, or a term is repeated in the batch (`blockedBy: "alias-collision"`)
old: hv-glossary-import <manifest> [--touch] [--repo <name>]    (`--body-file <p>` is the old positional; `--body-file -` makes the shim write stdin to a temp file and pass its path)
shim: `imported` comes from stderr `batch ok: N term(s)`; `noop: batch manifest contained no terms` gives `imported: 0, changed: false`; `terms` are the `wrote: Glossary/<term>` lines.
note: the manifest is a body, so it arrives as `--body-file` instead of a positional.
note: format is unchanged: TSV `term<TAB>def<TAB>aliases<TAB>nots`, with `#` comment lines and blank lines skipped. The import is all-or-nothing, and a refused batch writes nothing. `--touch` stamps today's date on every imported term. Exit codes shift from the old 2 and 3 to 2, 3 and 4 as listed.

### rota block <key>
rota block <key> [--body-file <path|->]
repo: scoped (scope S) for key `knowledge` only. Key `decisions` is umbrella-only, so `--repo` with `decisions` or with `--body-file` exits 2. Sub-repo scope writes `<sub-repo>/AGENTS.md` or `CLAUDE.md`.
data: {"key": string, "status": "created"|"updated"|"appended"|"unchanged", "changed": bool}
exit: 2 when the key is unknown in generated mode (without `--body-file` only `knowledge` and `decisions` are valid) or a registered `--repo` is passed where it is not allowed; an unregistered `--repo` is 3 (rule 9)
old: hv-managed-block <key> [--body-stdin] [--repo <name>]    (`--body-file -` becomes `--body-stdin` with the shim's stdin; `--body-file <p>` becomes `--body-stdin < <p>`)
shim: the stdout status word becomes `status`; `changed` is `status != "unchanged"`.
note: the key must be the first positional. With `--body-file`, any key is accepted and the body is wrapped in `<!-- rota-<key>-start -->` … `<!-- rota-<key>-end -->`. The managed file is `AGENTS.md` if it exists, else `CLAUDE.md` (created if absent). Legacy `<!-- hv:knowledge:start -->` markers migrate in place. The unknown-key exit moves from 1 to 2.
note: `--repo` with `--body-file`, silently ignored by the old helper, is now exit 2, because ignoring it writes to the wrong place. The key `skills` is reserved for `rota block skills` below and takes no body flag.

### rota block skills
rota block skills
repo: none
data: {"key": "skills", "status": "created"|"updated"|"appended"|"unchanged", "changed": bool}
exit: implied only
old: hv-skills-index    (stdout status word as for `rota block`)
shim: same transform as `rota block`.
note: the body text is generated by the binary and names `rota` verbs, not `.hv/bin/hv-*` paths (A9, G4). This is a deliberate break from byte parity with hv-skills-index: the old helper's body differs, so parity tests compare everything but the body. It changes the checked-in AGENTS.md section on the next run. Passing `--body-file` exits 2.

### rota instructions init
rota instructions init
repo: none (umbrella-root `AGENTS.md` and `CLAUDE.md` only; sub-repo files are untouched)
data: {"actions": [{"action": "created"|"moved"|"linked"|"skippedSymlink", "file": string, "keys"?: []string}], "changed": bool}
exit: implied only
old: hv-instructions-init
shim: stdout lines become actions: `created: <f>` gives `created`; `moved: <keys> → <f>` gives `moved` with `keys`; `linked: CLAUDE.md → @AGENTS.md` gives `linked`; `created: CLAUDE.md` gives `created`; the `note: … is a symlink` line gives `skippedSymlink`. `changed` is false when no lines were printed.
note: it is idempotent and prints nothing when it has nothing to do (`actions: []`).

### rota map query
rota map query <name>…
repo: none
data: text-read
exit: 2 when no name is given
old: hv-map-query <name>…    (same positionals)
shim: stdout is `text`; `missing` is omitted.
note: returns each `.rota/map/<name>.md` body without frontmatter, in argument order. A missing file is silent, exit 0. The zero-argument exit moves from 1 to 2.

### rota map index
rota map index
repo: none
data: {"key": "map", "status": "created"|"updated"|"appended"|"unchanged", "changed": bool}
exit: implied only
old: hv-map-index    (stdout status word)
shim: same transform as `rota block`.
note: the generated block text names `rota map query <name>` (it names `.hv/bin/hv-map-query` today).

### rota map stats
rota map stats [--cap]
repo: none
data: {"subsystems": [{"name": string, "bytes": number, "touched": string, "entryPoints": number, "brokenRefs": number}], "count": number, "cap"?: number, "overCap"?: bool}
exit: implied only. `--cap` is advisory and never fails.
old: hv-map-stats    (and, with `--cap`, also hv-map-cap-check)
shim: the old JSON keys `entry_points` and `broken_refs` are renamed to `entryPoints` and `brokenRefs`, and `count` is `len(subsystems)`. With `--cap` the shim also runs hv-map-cap-check; a stderr `note: project map has N subsystems (cap C)…` sets `overCap: true` and becomes a `warnings` entry. `cap` is config `map.softcap_subsystems`, default 20.
note: `touched` is `YYYY-MM-DD` from frontmatter `touched:`, else the git mtime, else `""`. A missing `.rota/map/` gives `subsystems: []`. In text mode `--cap` prints only the nudge, or nothing below the cap.
note: `--cap` only adds `cap`, `overCap` and the warning, so the verb keeps one output shape where the old pair had two.

### rota qa query
rota qa query <target>…
repo: none
data: text-read
exit: 2 when no target is given
old: hv-qa-query <target>…    (same positionals)
shim: stdout is `text`; `missing` is omitted.
note: same shape and rules as `rota map query`, over `.rota/qa/<target>.md`. The zero-argument exit moves from 1 to 2.

### rota qa index
rota qa index
repo: none
data: {"key": "qa", "status": "created"|"updated"|"appended"|"unchanged", "changed": bool}
exit: implied only
old: hv-qa-index    (stdout status word)
shim: same transform as `rota block`.
note: the generated block text names `rota qa query <target>` (it names `.hv/bin/hv-qa-query` today).

### rota migrate hv
rota migrate hv [--apply] [--verbose] [--skip-skills]
repo: none (it migrates the project around the working directory and every registered sub-repo that has its own `.hv/`, each with its own backup)
data: {"applied": bool, "noop": bool, "changed": bool, "projects": [{"scope": "umbrella"|<sub-repo name>, "dir": string, "move": bool, "files": []string, "blocks": bool, "stamp": bool, "backup"?: string}], "skillRoots": [{"root": string, "agent": string, "scope": string, "removed": number, "kept": []string, "reinstalled": bool}], "settings": []string, "workerBranches": []string, "manualReview": []string, "versionStamp"?: string, "diffs"?: []string}
exit: 3 when neither `.hv/` nor `.rota/` is found here or above; 4 when the verb refuses (cwd inside a `migrate-backup/` directory, uncommitted changes outside `.hv/` and `.rota/`, `.hv/` and `.rota/` both present, a held `.hv/**/*.lock`), with `blockedBy` in the failure data and nothing written; 5 when `git` is missing or the project is not a git repo
old: none (new in 5.0, #236: hv became rota)
shim: none
note: the one-shot move from the hv era. A preview (the default) writes nothing and exits 4 on the same refusals as `--apply`; it adds the warning `preview only; pass --apply`. A project with nothing left to migrate answers `noop: true` before any refusal, so a second `--apply` is `noop: true, changed: false` even with the tree uncommitted. A run killed after the move resumes: `.hv/` absent and `.rota/` present is not a refusal.
note: steps, in order: backup to `.hv/migrate-backup/<YYYYmmddTHHMMSS>/` (it moves with the folder, so `backup` is `.rota/migrate-backup/<...>` relative to the project) holding every file about to change, the old skills manifests and settings files under `external/`; rename `.hv/` to `.rota/` (the git index is left alone: stage with `git add -A .hv .rota`); `.gitignore` (header `# ── hv ──` and the `.hv/...` lines become `# ── rota ──` and `.rota/...`, in place); the managed blocks of `AGENTS.md` and `CLAUDE.md` (`<!-- hv-<key>-start/end -->` and `<!-- hv:<key>:start/end -->` become `<!-- rota-<key>-start/end -->`, the heading `## hv` or `## hv-skills` inside a block becomes `## rota`, then the blocks are regenerated as `rota init` writes them; text outside the blocks is never touched); tracked state files under `.rota/` (item markers `<!-- hv:<kind> ... -->` become `<!-- rota:<kind> ... -->`, the handoff first line `<!-- hv-handoff: orchestrator -->` becomes `<!-- rota-handoff: orchestrator -->`, the backlog row `Detail:` links and markdown link targets into `.hv/` follow; prose that still names `.hv/` or a `/hv-*` skill is reported in `manualReview`, not rewritten); `config.json` and `config.local.json` (keys under `hv` and `hvSkills` move under `rota`); every skills root (`~/.claude/skills` or `$CLAUDE_CONFIG_DIR/skills`, `~/.agents/skills`, and the project's `.claude/skills` and `.agents/skills`) that holds `.hv-manifest.json` loses exactly the files that manifest lists and whose content still matches, then the manifest, then gets the `rota-*` skills installed (`--skip-skills` skips all of that; a file the user edited or added stays); Claude settings files (user, each `work.accounts[].configDir`, project and project-local) hold the hv hook entries and statusline: `# hv-hook` becomes `# rota-hook`, `hv hook stop` and `hv hook session-start` become `rota hook ...`, `hv statusline dump` (plain or with `--then`) becomes `rota statusline dump`, `hvWrapped` and `hvWrappedFrom` become `rotaWrapped` and `rotaWrappedFrom`, and an hv hook next to a rota one is dropped, so `rota hook install` after the migration is a noop; last, `rota.version` is stamped (the running version, or the old value on a dev build) and `hv.version`, `hvSkills.version` and the top-level `version` go.
note: `hv-worker/*` branches are listed in `workerBranches` and `manualReview` and are not renamed. A `.hv/bin` mirror moves with the folder; `rota init` removes it.
note: until a project is migrated, every verb except `migrate hv`, `doctor`, `--help`, `version`, `update`, `skills`, `hook` and `statusline` stops with exit 3 and `this project still uses .hv/ (<dir>); run: rota migrate hv`; the check is one place, in the dispatcher. `rota doctor` reports the leftover as a failed check named `state` (present only then) with the hint `run: rota migrate hv`.
