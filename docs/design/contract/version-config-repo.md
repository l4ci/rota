---
verified-sha: 5e9b425a84bdc61cedf349412b0aa15c738689eb
refs:
  - internal/version
  - internal/config
  - internal/cli/config.go
---

## A3 and A4: version, update, config, repo

`rota version` ports in A3 (#47). `rota update`, `rota config` and `rota repo` were routed to A4 (#48) after the triage; their shapes stay here.

### rota version
rota version [--drift]
repo: none
data: {"version": string}; with `--drift`: {"version": string, "stamped": string, "installed": string, "status": "match"|"drift"|"unknown", "drift": bool}
exit: 3 when `--drift` and no `.rota/` is found. `status` is `unknown` when `stamped` or `installed` is empty.
old: plain `rota version`: hv-resolve-plugin-root --root-only (no positional or flag maps; prints the plugin root). `--drift`: hv-version-check --json (new `--drift` selects the old `--json` form; no other args).
shim: plain form reads the binary's stamped version; `--drift` wraps the old JSON (`stamped`, `installed`, `status`) and adds `version` (= `installed`) and `drift` (= `status == "drift"`); an old rc 0 with empty output (no `.rota/`) becomes exit 3.
note: `stamped` is `rota.version` from the merged config; `installed` is the running binary's embedded version (old: plugin.json at the resolved plugin root). In 5.0 they are the same source, so `version` and `installed` are equal.
note: old `hv-version-check` exited 0 silently without `.rota/`, where 5.0 exits 3.

### rota update
rota update
repo: none
data: {"installType": "brew"|"script"|"dev"|"unknown", "installRoot": string, "currentVersion": string, "latestVersion": string, "status": "behind"|"current"|"ahead"|"unknown", "updateCommand": string}
exit: implied only. A missing `gh`, a network failure or an unresolved install gives `latestVersion` or `currentVersion` empty and `status: "unknown"`, as old. Read-only: never runs the update.
old: hv-update-check (no args). Env: `HV_INSTALL_ROOT` stays as the real override (`installType: "override"`, not a test hook); `ROTA_TEST_LATEST_VERSION` is the test hook, for old `HV_LATEST_VERSION` (rule 10).
shim: old stdout is already this JSON; pass it through unchanged and export `HV_LATEST_VERSION="$HV_TEST_LATEST_VERSION"` when set.
note: strings may be empty rather than absent, as in the old helper.
note: F6b (#230) replaced the plugin-era detection. `currentVersion` is the running binary's version and `installRoot` is the directory holding the binary (symlinks resolved). `installType` is `brew` for a path under a Homebrew prefix (`/Cellar/`, `/homebrew/`, `/linuxbrew/`), `dev` for an unstamped or `-dev` build, `unknown` when the binary path does not resolve, otherwise `script`. `updateCommand` is `brew update && brew upgrade rota`, the `install.sh` pipe, or a rebuild, each followed by `&& rota skills update`. `HV_INSTALL_ROOT` and `CLAUDE_PLUGIN_ROOT` are no longer read.

### rota config show
rota config show [<key>]
repo: scoped
data: {"entries": [{"key": string, "value": any, "source": "local"|"project"|"default"}]}
exit: 3 when `<key>` is neither a schema key nor present in the merged config; 2 when more than one positional is given. `value` is the raw JSON value (not a JSON string of it). No key: every schema key in schema order. With a key: `entries` holds exactly one element, so both forms share one shape.
old: hv-config-show [<key>] (new `<key>` maps to the old single positional; unchanged).
shim: parse each stdout line with `^(\S+) = (.*)  \(source: (local|project|default)\)$` into `key`, `value` (JSON-parsed), `source`; old rc 1 with `unknown key` on stderr becomes exit 3, rc 1 with `usage:` becomes exit 2.
note: old rejected any key outside the schema table. 5.0 accepts a key that is in the schema or present in the merged config, so a hand-edited key stays readable (`config set` itself is schema-only). The shim cannot do this; it fails with exit 3 as before.
note: schema values resolve from the same merged configuration as runtime reads. A local null or non-object parent replaces the project value; an absent or null key after merging uses its schema default and reports `source: default`. Otherwise the source is `local` when that layer supplies the key, or `project`.

### rota config set
rota config set <key> <value>
repo: scoped
data: {"key": string, "value": any, "previous"?: any, "changed": bool}
exit: 2 when `<key>` is not in the schema that `config check` uses (the shared table in `bin/hvlib_config.py`), is a malformed path (empty segment: `""`, `.a`, `a.`), or an argument is missing; 70 when `.rota/config.json` exists but is not a JSON object, or the write fails.
old: hv-config-set <key> <value> (both map to the old positionals in order). Writes `.rota/config.json` only, never `config.local.json`. `<value>` is parsed as JSON first (`true`, `42`, `"x"`, `[…]`, `{…}`) and falls back to a raw string (`opus`, empty string). `previous` is the stored value before the write and is absent when the key was unset.
shim: old accepts any key, so the shim checks `<key>` against the schema table first and exits 2 without calling the helper when it is absent; old stdout is empty; read `.rota/config.json` before and after the call to fill `value`, `previous` and `changed`; old rc 1 with `malformed key path` on stderr becomes exit 2, rc 1 for missing argv becomes exit 2, any other rc 1 becomes exit 70.
note: the JSON-then-string coercion is kept because `hv-init` and `hv-config` skills depend on it. A string that looks like JSON (`"true"`) still needs shell quoting (`'"true"'`). No `--string` or `--local` flag is added.
note: a key outside the schema is rejected (maintainer decision), where old accepted any key.
note: an empty `<value>` is valid and stores the empty string; the old helper rejected it, so the shim passes `""` as a JSON literal.

### rota config check
rota config check
repo: scoped
data: {"status": "upToDate"|"fresh"|"stale"|"corrupt", "upToDate": bool, "missing": []string}
exit: 1 when `status` is anything but `upToDate` (`fresh`, `stale` or `corrupt`); 3 when no `.rota/` is found. `missing` lists dotted required keys that are absent or null, in schema order, and is non-empty only for `stale`. `fresh` means `config.json` does not exist; `corrupt` means it is invalid JSON or not an object (this is a defined verdict, so it does not use exit 70).
old: hv-config-schema-check (no args).
shim: map stdout `UP_TO_DATE` to `status: upToDate`, `FRESH` to `fresh`, `CORRUPT` to `corrupt`, and `STALE:a,b` to `stale` with `missing` = the split list; exit 0 for `upToDate`, else 1 (old always exited 0).
note: old always exited 0, so skills that branch on the four tokens must read `data.status`.

### rota config fill
rota config fill
repo: scoped
data: {"filled": []string, "changed": bool}
exit: 3 when no `.rota/` is found; 70 when `.rota/config.json` exists but is invalid JSON or not an object (`config check` status `corrupt`), and nothing is written.
old: none (A9 addition, G1; replaces the ~20 default `config set` calls the hv-init skill carried as prose).
note: writes the schema default (`config show`'s `source: default` value) for every required key that `config check` lists in `missing`, so `config check` reports `upToDate` afterwards. `filled` lists those keys in schema order. A missing `config.json` is created holding every required key. Keys already present, keys outside the schema and `config.local.json` are never touched. Nothing missing gives `filled: []`, `changed: false`, and the file is not rewritten.
note: each added key goes in at its schema position among its siblings: before the first existing sibling that comes later in schema order (an object's position is that of its first schema key), so a file that starts in schema order stays in it (G7). Siblings outside the schema keep their place. The file is written as `config set` writes it (two-space indent).
note: `umbrella.enabled` and `rota.version` are required, so `fill` writes their defaults (`false`, `""`) when they are missing. Callers that know better set them with `config set`; `config set` on a present key keeps its position. The init skill runs `rota init`, then `rota config fill`, then `config set` for the answered keys, which keeps the file in schema order.

### rota repo which
rota repo which
repo: none (the answer comes from the working directory)
data: {"name": string, "path": string}
exit: 3 when the working directory is not inside a registered sub-repo, is not in a git repo, there is no umbrella, or a stray `.rota/` inside a registered sub-repo masks the umbrella (`error.message` says which, and the hint names the stray `.rota/`); 5 when `git` is missing. `path` is the absolute realpath of the registered sub-repo root (a Layout B worktree maps to its main repo).
old: hv-resolve-repo (no args; run from the same working directory, `-C` becomes `cd`).
shim: old stdout is the name; fill `path` by running `hv-resolve-repo-path <name>` from the umbrella root printed by hv-resolve-umbrella; old rc 1 maps to 3, including the `masking` case.
note: the triage tree writes name-to-path as `rota repo which --path`. That shape is dropped: `which` now returns both name and path, and the name-to-path lookup is `rota repo resolve <name>`, so hv-resolve-repo-path (merged) needs no flag.

### rota repo resolve
rota repo resolve [<name>…]
repo: none (reads the umbrella registry)
data: {"repos": [{"name": string, "path": string}]}
exit: 3 when any name is not registered, or the project is not an umbrella (empty registry); `error.message` names every unregistered name. No names gives `{"repos": []}` and exit 0, as the old helper did for `""`. Order follows the arguments, duplicates preserved; `path` is the absolute realpath.
old: hv-resolve-repos "<names joined by comma>" (variadic positionals become the one comma-joined old positional; zero names become `""`). Run from the umbrella root.
shim: wrap the old stdout JSON array as `{"repos": [...]}`; old rc 1 becomes exit 3.
note: the triage tree writes `resolve <csv>`; rule 2 makes it variadic. Merged hv-resolve-repo-path (`<name>` to path) is `rota repo resolve <name>` and reads `repos[0].path`.

### rota repo umbrella
rota repo umbrella
repo: none
data: {"umbrella": bool}
exit: 1 when the project is not an umbrella (no `.rota/`, no `.rota/repos.json`, or an empty registry). It never exits 3 for a missing `.rota/`: no root means no.
old: hv-umbrella-on [<dir>] (new `-C <dir>` global flag replaces the old optional positional; no positional remains).
shim: map stdout `yes` to `umbrella: true` and exit 0, `no` to `umbrella: false` and exit 1; pass `-C <dir>` as the old positional.
note: umbrella status is true iff `.rota/repos.json` holds at least one entry, and `umbrella.enabled` is ignored, same as old. The old positional dir is dropped for `-C`; unlike old, discovery walks up to the nearest `.rota/` (conventions).

### rota repo add
rota repo add <path> [--name <name>]
repo: none (edits the umbrella registry)
data: {"name": string, "path": string, "changed": bool}
exit: 3 when `<path>` is missing, is not a directory, has no `.git` entry, or is not strictly below the umbrella root; 4 when the name is already registered for another directory or the directory under another name; 2 without exactly one `<path>`. Adding an entry that is already registered (same name, same directory) is exit 0 with `changed: false` and does not rewrite the file.
note: `<path>` is relative to the working directory. `name` defaults to the directory's basename. The entry is written as `./<path relative to the root>`, the file stays sorted by name, and entries `repos.json` holds that `add` does not know are kept. A new entry also gets `.rota/knowledge/<name>/` and a `/<path>/` line in the umbrella's `.gitignore` when that is a git repo, as `rota init umbrella` leaves them. The sub-repo is never touched. `umbrella.enabled` is not changed.

### rota repo rm
rota repo rm <name>
repo: none (edits the umbrella registry)
data: {"name": string, "path": string, "changed": true, "openItems": [string], "activeStreams": [string]}
exit: 3 when `<name>` is not registered (so a second `rm` of the same name exits 3, not 0); 2 without exactly one `<name>`.
note: only the registry entry goes. The sub-repo, its `.rota/knowledge/<name>/` and its `.gitignore` line stay. Before removing, `rm` lists the open items whose `Repos:` field names the repo (`openItems`, item IDs) and the active streams in `status.json` whose `repo` is the name (`activeStreams`, branches), and warns for each non-empty list; neither blocks. A backlog that cannot be read gives a warning and an empty `openItems`.
