## F6a: embedded skills

F6a (#230, part of #44) makes `rota` the only artifact: the binary carries the skills and the `references/` they read, and installs them for Claude Code and Codex. The Claude plugin, the `bin/rota` launcher and `npx skills` go. Rulings (maintainer, 2026-10-04, relayed in round 5): skills ship "Embedded in rota", before the 5.0 tag; the verb names are open to argument. The version lives in the binary's ldflags, set from the tag (lea, F6b, agreed in round 5); F6a reads only `version.Get()`. F6b's root `VERSION` file is only the input to `release bump`, and nothing at runtime reads it.

- **Embedding.** `embed.go` at the module root (`package rota`, the only Go package at the root, since `go:embed` cannot reach a parent directory) embeds `rota-*/*.md` and `references/*.md`. `internal/skills` owns everything else. The **set digest** is the sha256 of the sorted `path NUL sha256(content) NL` lines over the files one install writes. It identifies a skill set without a version, so a dev build (`version` empty) still compares.
- **Install layout.** Each skill is installed as a self-contained directory: `<root>/<skill>/SKILL.md`, its other `*.md` files, and `<root>/<skill>/references/` holding only the references the skill cites, closed transitively over references that cite references. A skill's `references/x.md` therefore resolves relative to its own directory, which is the Agent Skills convention and the base directory both harnesses hand the model. No shared directory sits beside the skills, nothing is rewritten at install time, and installed bytes equal embedded bytes. To get there, the source changes once: links in `SKILL.md` drop the `../` (`](../references/x.md)` becomes `](references/x.md)`, the form the inline citations already use), and `test/validate-skills.py` resolves a skill's `references/` links against the repo's `references/`. A test installs into a temp root and fails if any `references/<name>.md` cited by an installed file is missing from that skill's directory.
- **Roots.** `--scope user` (default): `~/.claude/skills` and `~/.agents/skills`. The Claude root follows `CLAUDE_CONFIG_DIR` when it is set (`$CLAUDE_CONFIG_DIR/skills`, where Claude Code reads user skills), so a machine with several Claude accounts installs once per config dir. A user scope with no HOME (and, for Claude, no `CLAUDE_CONFIG_DIR`) exits 3. Manifest keys that are not local paths under an `rota-*` directory are ignored, and rota never writes or deletes through a symlinked parent directory. `--scope project`: `<git toplevel>/.claude/skills` and `<git toplevel>/.agents/skills`, for the toplevel of the working directory after `-C`, so it needs no `.rota/`. `--agent claude|codex|all` (default `all`) selects which of the two. `~/.agents/skills` is the user directory Codex scans (and other Agent Skills tools share); `CODEX_HOME` does not move it. A copied skill lists in Codex as `rota-pause`, not `hv-skills:rota-pause` (E2's probe), and `$rota-pause` invokes it.
- **Manifest.** `<root>/.rota-manifest.json`, one per root, so a project install carries its own: `{"schema": 1, "version": string, "digest": string, "agent": "claude"|"codex", "files": {"<skill>/<path>": "<sha256>"}}`. It lists exactly what rota wrote, and rota never touches a path outside it except to create one. It is written under the sidecar lock `.rota-manifest.json.lock`, with an atomic rename, after the files.
- **File states.** For each path the embedded set wants: `absent` (written: `created`); in the manifest, on-disk hash equal to the manifest's (`unchanged` when the content is the same, else rewritten: `updated`); in the manifest, on-disk hash different (`edited`: kept); on disk but not in the manifest (`unmanaged`: kept). A manifest path the new set no longer has is removed when unedited (`removed`), else kept (`edited`). Empty skill directories left behind are removed. A legacy `rota init --codex` symlink (`.agents/skills/rota-*` pointing at a directory with `SKILL.md`) holds no user content, so it is replaced (`replaced`) and not reported as unmanaged.
- **No `--force`.** Rule 5 forbids `--force`. `--overwrite` replaces `edited` and `unmanaged` paths. Without it, the verb still does all the other work, then exits 4 with `blockedBy: "edited"` or `"unmanaged"`, listing the paths in `kept`.

- **Built as** (worker calls, accepted by kit; unratified): `references/README.md` is copied when a file cites it, but its own links are not followed, since it links every reference and would put all of them in every skill (each skill gets 15 to 23 references, 308 files per root). The `CLAUDE_CONFIG_DIR` root, the manifest-key and symlinked-parent guards came out of review. A path on disk that is not in the manifest but has the embedded bytes is adopted as `unchanged`, so a lost manifest does not cause a false exit 4. `replaced` also covers an `--overwrite` of an `edited` or `unmanaged` path. `install` and `uninstall` default to `--scope user`; `update` and `status` default to both scopes. `uninstall` leaves the root directories and removes the lock sidecar. The text output gives one line per root with a count for each status, then the kept paths and warnings; `--json` lists every file.
- **Frontmatter must be strict YAML.** Codex 0.159.2 silently dropped `rota-capture`, whose unquoted description held `GH: #N`. Claude Code reads it leniently. `test/validate-skills.py` now fails an unquoted single-line value that holds `': '` or `' #'` or starts with a YAML indicator. `rota-capture` and `rota-orchestrate` use `description: >-`.
- **Proof** (`codex debug prompt-input`, Codex 0.159.2, CODEX_HOME unset, no model session): with the real HOME, Codex lists `~/.agents/skills` as skill root `r0`. After `rota skills install --agent codex` into a temp HOME, it lists all 16 rota skills from `<HOME>/.agents/skills`.

### rota skills install
rota skills install [--scope user|project] [--agent claude|codex|all] [--overwrite]
repo: none
data: {"roots": [{"root": string, "agent": "claude"|"codex", "scope": "user"|"project", "version": string, "digest": string, "files": [{"path": string, "status": "created"|"updated"|"unchanged"|"removed"|"replaced"|"edited"|"unmanaged"}]}], "kept"?: []string, "warnings"?: []string, "changed": bool}
exit: 4 when a path was kept (`blockedBy` `edited` or `unmanaged`, `changed` true when anything else was written); 3 when `--scope project` and the working directory is not in a git work tree; 5 when a root cannot be created or written
note: runs without `.rota/`. Idempotent: on a root that already has a manifest it does what `update` does there. `files` lists only paths whose status is not `unchanged`, unless `--json` is given, where every path is listed.

### rota skills update
rota skills update [--scope user|project] [--agent claude|codex|all] [--overwrite]
repo: none
data: as `skills install`
exit: 1 when no root in scope has a manifest (`roots` empty; hint `run: rota skills install`); otherwise as `skills install`
note: refreshes only roots that already have a manifest. With no flags, it covers every root in both scopes for the current directory, so `rota skills update` after an upgrade refreshes whatever was installed. It never creates a root.

### rota skills uninstall
rota skills uninstall [--scope user|project] [--agent claude|codex|all] [--overwrite]
repo: none
data: {"roots": [{"root": string, "agent": string, "scope": string, "removed": []string, "kept": []string}], "changed": bool}
exit: 4 when an `edited` path was kept (`blockedBy: "edited"`); the manifest then keeps only the kept paths
note: removes manifest paths whose hash matches, then the empty directories, then the manifest. Unmanaged paths are never removed, even with `--overwrite`. A root with no manifest is skipped silently.

### rota skills status
rota skills status [--scope user|project] [--agent claude|codex|all]
repo: none
data: {"version": string, "digest": string, "roots": [{"root": string, "agent": string, "scope": string, "installed": bool, "version"?: string, "digest"?: string, "current": bool, "edited": []string, "missing": []string}]}
exit: implied only (classifier, rule 7)
note: read-only. `version` and `digest` at the top are the binary's. `current` is the root's manifest digest equal to the binary's. `missing` are manifest paths absent from disk.

### Changes to existing verbs

- **`rota init`**: `--codex` and `--skills-dir` are removed. Either flag exits 2 with the hint `run: rota skills install --scope project --agent codex`. `internal/initproj/codex.go` and its `codex` data go. `.agents/skills/rota-*` lines that earlier inits wrote to `.gitignore` are left alone.
- **`rota doctor`**: the `rota` check (binary against `.claude-plugin/plugin.json`) becomes a `skills` check in the same position. It reads the manifests in both scopes. With no manifest anywhere it is `skip` with the hint `run: rota skills install` (the repo's opt-in doctor rule: skip until installed, fail only on a broken install; orchestrator, round 5). It fails when an installed root's digest differs from the binary's (`skills 4.5.0, rota 5.0.0`, hint `run: rota skills update`), when an installed root has missing or edited files.
- **`rota round assign`**: the brief lookup drops `CLAUDE_PLUGIN_ROOT`. The order becomes `round.brief`, then `<project>/references/worker-contract.md` (a source checkout), then `rota-orchestrate/references/worker-contract.md` under the first installed Claude root (project, then user). Exit 4 `brief missing` is unchanged.

### Removed

`.claude-plugin/plugin.json`, `.claude-plugin/marketplace.json`, `bin/rota` (the launcher), `cmd/rota/launcher_test.go`, `.stow-local-ignore`, `internal/initproj/codex.go` and its tests, `rota init --codex|--skills-dir`, the plugin-version check in `test/validate-skills.py` (plugin.json against CHANGELOG) along with its fixture in section 94, the launcher assertion in `test/grep-gate.sh`, the plugin.json lookup in `rota doctor`, and the `CLAUDE_PLUGIN_ROOT` lookup in `round assign`. Section 94 keeps its lint cases and replaces the `init --codex` cases with `rota skills` cases (no new section number). F6b (lea) owns `.goreleaser.yaml`, `install.sh`, `VERSION`, the release flow, `rota-release`, `.rota/RELEASE.md`, CHANGELOG, and `rota update` (its install detection becomes brew or script). README, `docs/install.md` and `docs/getting-started.md` are held for F5 slice B, so F6a does not edit them. `docs/usage/codex-skills.md` is rewritten for `rota skills install`.

Design reason for per-skill reference copies over one shared directory: a shared directory needs either a name every skill hard-codes or rewriting at install time, and neither harness promises to ignore a non-skill directory under a skills root.

Ratified (orchestrator, round 5): the verb names, the install/update split, `--overwrite`, `--agent all` and user scope as defaults, `.rota-manifest.json`, per-skill reference copies and the `../references` to `references/` source change. Doctor skips when nothing is installed (changed from the first draft).
