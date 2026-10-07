---
verified-sha: 3fe9abdb78d41ab20ad0545652af7ca33e1dd786
refs:
  - internal/initproj
  - internal/cli/init.go
---

## A9: init

A9 amendments (orchestrator rulings G1 to G7, round 3): `rota init` runs the managed blocks and returns `blocks` (G2), `rota init umbrella --list` (G3), the new `rota config fill` (G1, under `config`), schema key order for the seed `config.json` (G7), `rota version --drift` (G6, entry above), and two decisions recorded here:

- **Parity exception (G4).** Seed and generated block text name `rota` verbs instead of `.hv/bin/hv-*` paths: the `MAP.md` seed, `rota block skills` and the `milestone index` block. This is the one deliberate break from byte parity with the old helpers. Parity tests exclude exactly that text; everything else stays byte-identical.
- **`.rota/bin/` (G5).** Dropped with the rest of the hv-skills v3/v4 compatibility (#236): `rota init` neither writes a `.rota/bin/` line nor removes a 4.x mirror.

### rota init
rota init [--no-blocks]
repo: none
data: {"root": string, "created": []string, "configFilled": []string, "versionStamped": string, "projectRegistered": bool, "noVerify": bool, "blocks"?: [{"key": string, "status": string, "changed": bool}], "instructions"?: [{"action": string, "file": string, "keys"?: []string}], "warnings"?: []string, "changed": bool}
exit: 2 when `--codex` or `--skills-dir` is given (F6a removed both; hint `run: rota skills install --scope project --agent codex`; nothing is written); 70 when a seed file exists but is unreadable or corrupt, or `.rota/config.json` is not a valid JSON object. Acts on the working directory after `-C`, with no walk-up, so it runs without a `.rota/` root.
old: hv-bootstrap (no args).
shim: `created` is the diff of `find .rota .gitignore` before and after (paths relative to the root), and `changed` is `created` non-empty. The shim does not run the block steps and omits `blocks` and `instructions`.
note: steps, in order: seed `.rota/` and `.gitignore` (bootstrap); `instructions init`; strip the deprecated managed blocks; then the six blocks `block skills`, `block knowledge`, `milestone index`, `block decisions`, `map index`, `qa index` (G2). `instructions` is `instructions init`'s `actions`. `blocks` has one entry per block in that order, keyed `skills`, `knowledge`, `milestones`, `decisions`, `map`, `qa`, with the status the verb reports (`created`, `updated`, `appended`, `unchanged`); `milestones` reports only `changed`, so its status is `updated` or `unchanged`. A deprecated block that was stripped adds an entry `{"key": "<key>", "status": "removed", "changed": true}` before the six; a project without one gets no entry. `changed` is true when anything was created, migrated or written, blocks included.
note: config step (#71, replaces the removed init skill's fill and stamp), after seeding and before the blocks, runs on every `rota init`, `--no-blocks` included: it does what `config fill` does (writes the schema default of every missing required key, never touching a present one; `configFilled` lists them, empty when none) and then sets `rota.version` to the binary's version (`versionStamped` is that version when the stamp changed, else `""`), which clears the drift nudge. An unreleased `dev` binary stamps nothing. A re-run on a current project fills and stamps nothing and `changed` is false. Text mode adds `config filled: <keys>` and `stamped rota.version: <v>`.
note: a block step that fails (a tracker error from `milestone index` in issue mode, say) does not fail `rota init`: its entry gets `status: "failed"`, `changed: false`, and `warnings` names it. Seeding has already happened, and a re-run retries it. Exit 70 still applies to the seed step.
note: closing line (#493): when `test.full` and `test.e2e` are both empty under a local verify, `noVerify` is true and text mode ends with `test.full is empty: the merge gate will refuse to merge until it is set (rota config set test.full '[...]')`. No prompt; it does not affect `changed`.
note: registry step (#24), last, on every `rota init` (`rota setup` runs it too): adds the directory (realpath) to `projects.json` in the global rota config dir, `$XDG_CONFIG_HOME/rota` when that is an absolute path, else `~/.config/rota`. `projectRegistered` is true when the entry is new; a re-init only refreshes its `lastSeen`. The registry is machine state, not the project's, so it never makes `changed` true. A registry that cannot be written adds an entry to `warnings` and does not fail init.
note: `--no-blocks` seeds only: no `instructions init`, no strip, no blocks, and `blocks` and `instructions` are absent. Sections that test seeding use it; a user who manages `AGENTS.md` by hand can too, so it is a real flag, not a test hook (rule 10).
note: seed `config.json` content stays the issues-only defaults (`issues.label`, `issues.autoCreateLabel`), written in schema key order, so it differs from the old helper's byte order (G7). `rota config fill` completes it, and `rota.version` is stamped with `rota config set`. The `MAP.md` seed names `rota map query <name>` and `rota map index` (G4).
note: `--codex` and `--skills-dir` were removed by F6a (#230). They stay parseable only to exit 2 with the hint above; Codex discovery is `rota skills install --scope project --agent codex`, which copies the skills. `.agents/skills/rota-*` lines that earlier inits wrote to `.gitignore` are left alone.

### rota init check
rota init check
repo: none
data: {"initialized": bool, "missing": []string}
exit: 1 when not initialized. Like `rota init`, it acts on the working directory after `-C`, with no walk-up, and runs without a `.rota/` root (see shared definitions).
old: hv-preflight (no args).
shim: old rc 0 and rc 3 give `initialized: true` (rc 3 is the old helper-mirror check, dropped in 5.0); rc 2 gives `initialized: false` and exit 1, and the shim lists every missing core path itself in `missing` (the old helper names only the first); stderr `warn:` and drift lines go to `warnings`.
note: split from `init --check` because its `data` shape differs from `init`. It verifies `.rota/` and its seven core files only (DECISIONS.md, BACKLOG.md, KNOWLEDGE.md, MILESTONES.md, counters.json, config.json, status.json). It reports every missing path, where old stopped at the first. The old helper-mirror check goes away because the `rota` binary is on PATH. The advisory umbrella-flag mismatch and the version-drift line (`rota version --drift`) become entries in `warnings`.

### rota init umbrella
rota init umbrella (--repos <csv> | --all | --list)
repo: none
data: {"root": string, "created": []string, "registered": []string, "umbrellaIsGitRepo": bool, "configFilled": []string, "versionStamped": string, "umbrellaEnabled": bool, "changed": bool}; with `--list`: {"root": string, "candidates": []string, "isGitRepo": bool}
exit: 2 when not exactly one of `--repos`, `--all` and `--list` is given; 3 when the directory has no immediate child with a `.git` entry (checked before any seeding, so exit 3 leaves nothing behind); 70 when a seed file exists but is unreadable or corrupt. Like `rota init`, it acts on the working directory after `-C`, with no walk-up, and runs without a `.rota/` root (see shared definitions).
old: hv-bootstrap, then `printf '%s\n' <value> | hv-umbrella-init` where `--repos a,b` becomes stdin `a,b`, `--repos ""` becomes `none`, and `--all` becomes `all`.
shim: `created` is the diff of `find .rota .gitignore` before and after (paths relative to the root), `registered` and `umbrellaIsGitRepo` come from hv-umbrella-init's stdout JSON, and `changed` is `created` non-empty or a registry change.
note: after registering, it runs the same config fill and version stamp as `rota init` and, when at least one sub-repo is registered and `umbrella.enabled` is not already true, sets `umbrella.enabled` to true (`umbrellaEnabled` says whether it did). `changed` includes those config writes.
note: it also runs the base seeding that plain `init` does, since the init skill always runs hv-bootstrap first and a registry cannot exist without `.rota/`. Re-running is idempotent and never overwrites.
note: the old helper read one line from stdin, which `rota` never does (conventions: no prompts). `--repos` takes comma-separated names (empty value means none registered) or `--all`. Unknown names are ignored with a warning, and prior registrations still on disk as git children are kept with a warning, same as old.
note: `--list` is read-only (G3): it writes nothing, never seeds and never exits 3. `candidates` are the names of the immediate children with a `.git` entry (directory or file), sorted, the same scan `--all` registers; an empty list is exit 0. `isGitRepo` is whether the working directory itself is a git repo, the same test as `umbrellaIsGitRepo`. The init skill reads it to ask its umbrella question before anything is written. No `--list` old mapping exists; the shim runs the scan itself.

### rota projects
rota projects
repo: none
data: {"dir": string, "projects": [{"name": string, "path": string, "lastSeen": string, "missing": bool}]}
exit: 70 when the global config dir cannot be located (neither `XDG_CONFIG_HOME` nor `HOME` is set). Read-only; runs without a `.rota/` root and ignores the working directory.
old: none.
note: the machine-wide registry `rota init` fills (#24): `<global config dir>/projects.json`, `{"projects": [{"path", "name", "lastSeen"}]}`, `path` absolute and realpath-resolved, `name` its directory name, `lastSeen` the UTC RFC 3339 time of the last `rota init` there. Rows are sorted by name, then path. `missing` is true when the path no longer exists; such an entry is flagged, never pruned by this verb (`rota projects cleanup` does), so a project on an unmounted drive returns with it. Text mode prints `name<TAB>path` per row, plus `(missing)`. An absent or unreadable registry lists nothing and exits 0. This is not `.rota/repos.json`, the per-project umbrella sub-repo registry.

### rota projects cleanup
rota projects cleanup
repo: none
data: {"removed": [{"name": string, "path": string, "lastSeen": string}], "changed": bool}
exit: 70 as `rota projects`. Runs without a `.rota/` root and ignores the working directory.
old: none.
note: deletes at once, no preview (#195): every registry entry whose directory is gone or no longer holds `.rota/`, under the registry's lock. Text mode prints `removed<TAB>name<TAB>path` per entry, or `noop: no stale projects`. An absent registry removes nothing and is not created. Plain `rota projects` still never prunes.

### rota setup
rota setup [--yes] [--set <key>=<value>]... | --list
repo: none
data: init's `data` plus `"answers": {<key>: value}`; with `--list`: {"questions": [{"key": string, "title": string, "default": string, "choices": [{"value": string, "description": string}], "if"?: "<key>=<value>"}]}
exit: 2 when there is no terminal (or `--json`) and neither `--yes` nor `--set` was given, when `--set` names a key that is not a setup question, gives a value outside its choices, or names a conditional question whose condition fails, and when terminal input ends before the last question (nothing is written in any of these); 4 when `.rota/` already exists here; 70 as `rota init`. Acts on the working directory after `-C`, like `rota init`.
old: none (the init skill asked Q1 to Q5 in chat).
note: `rota init` plus the main config choices, asked first and written after init has seeded `.rota/`. This is the one verb that reads a terminal (#25, a maintainer ruling; the "never prompts" convention has this exception). Questions, choices, descriptions and defaults are `config.Prompts` in `internal/config/schema.go`, defaults being the schema's. Interactive means stdin is a terminal and neither `--yes` nor `--json` is given: prompts go to stderr, one numbered list per question, Enter takes the default, a number or a choice value answers. `--yes` takes the default for every question `--set` did not answer; `--set` alone suffices and skips prompting for the keys it names. `issues.provider` is asked only when `backlog.backend` is `issues`. Every answer is written, defaults included. All validation happens before init runs. `--list` is read-only and answers with the questions and how to pass them.
note: bare `rota` in a directory without `.rota/` is meant to run this verb; the routing lives in `cmd/rota` (#19), not here.
