# Install

Available from 0.9.0. `rota` is a single binary that carries the skills; there is no plugin to enable. Install the binary, install the skills, then run `rota init` in your project.

## The binary

### Install script

```bash
curl -fsSL https://raw.githubusercontent.com/l4ci/rota/main/install.sh | sh
```

It installs to `~/.local/bin/rota`. It needs no sudo and does not edit your shell profile; if the directory is not on your `PATH` it prints the line to add. It needs `curl` (or `wget` as a fallback) and [`minisign`](https://jedisct1.github.io/minisign/) (`brew install minisign`, `apt install minisign`); without minisign it stops rather than install an unverified binary.

| Option | Env var | Meaning |
|---|---|---|
| `--version X.Y.Z` | `ROTA_VERSION` | install this release instead of the latest |
| `--prefix DIR` | `ROTA_PREFIX` | install to `DIR/bin` instead of `~/.local/bin` |

### Homebrew

```bash
brew install l4ci/tap/rota
```

### Release binaries by hand

Download `rota_<os>_<arch>` from the [releases](https://github.com/l4ci/rota/releases) page (linux and macOS, amd64 and arm64), make it executable, and put it on your `PATH`. Download `rota_<os>_<arch>.minisig` too and verify it (below), then compare the sha256 with the line for that file in `checksums.txt`.

### Verifying a release

Every release asset has a detached [minisign](https://jedisct1.github.io/minisign/) signature, `<asset>.minisig`, made in the release workflow. `install.sh` verifies the binary against the public key embedded in the script, then against `checksums.txt`, and installs nothing if either check fails. The signature proves the file was signed with the release key (id `2153154F7AA18B5D`); the checksum alone only shows the download is intact, because `checksums.txt` comes from the same release. To check by hand:

```bash
minisign -V -P RWRdi6F6TxVTIW92f3/QsWBl5VHdXm1FABgexyAla0z3A5WT4JzG6/SP \
  -m rota_linux_amd64 -x rota_linux_amd64.minisig
```

The Homebrew formula is checked against the tarball sha256 only. Maintainers: [release signing](contributing/release-signing.md).

Check the result with `rota version`. `rota doctor` checks the machine, including whether the installed skills match the binary.

## The skills

```bash
rota skills install
```

With no flags this writes the skills to the user roots for both agents: `~/.claude/skills` (or `$CLAUDE_CONFIG_DIR/skills`) for Claude Code and `~/.agents/skills` for Codex.

| Flag | Meaning |
|---|---|
| `--agent claude\|codex\|all` | which agent's root to write (default: both) |
| `--scope user\|project` | `user` (default), or `project` for `.claude/skills` and `.agents/skills` in the current repo |
| `--overwrite` | replace files you edited or rota did not write |

The skills are copies, not symlinks, and `install` writes a `.rota-manifest.json` listing what it put there. A file you edited is kept and reported unless you pass `--overwrite`. A project-scope install can be committed so collaborators get the same skills.

`rota skills status` compares the installed skills with the binary. For Codex, see [skills in Codex](usage/codex-skills.md) and, to run Codex as a round worker, [Codex workers](usage/codex-workers.md).

### User or project scope

- **User scope** (the default) puts one copy in your home directory, and every project on the machine uses it. Pick it when you work alone or across many repos.
- **Project scope** writes into the repo. Commit `.claude/skills` and `.agents/skills` and everyone who clones gets the same skills, pinned to the rota version that wrote them. Pick it for a team, or when a project must not move when you upgrade rota. Collaborators still need the `rota` binary on their `PATH`.

If both exist, Claude Code uses the user copy of a skill over the project copy with the same name, so a stale user install hides a newer project one. Keep one scope per machine, or run `rota skills update` after every upgrade. `rota skills status` lists every root it finds.

User scope is per Claude Code config directory: rota writes to `$CLAUDE_CONFIG_DIR/skills` when that is set, else `~/.claude/skills`. Inside a rota project, user scope also covers every `work.accounts` `configDir` (install, update, uninstall and status each print one line per root). A configured dir that does not exist is reported and skipped, not created. Pass `--current-account` to touch only the current dir. Project scope is unchanged: one copy serves every account.

## 🔧 Upgrading

```bash
rota update
```

`rota update` (needs `gh`) works out how you installed, compares versions and prints the command to run. It does not run it. Typical output:

- Homebrew: `brew update && brew upgrade rota && rota skills update`
- install script: the `curl` line above, then `rota skills update`

`rota skills update` refreshes every skill root that has a manifest. In a project, `rota version --drift` compares the version stamped in `.rota/` with the binary.

## 🔧 Uninstalling

```bash
rota skills uninstall    # removes what rota installed
brew uninstall rota      # Homebrew; otherwise delete the rota binary
```

Your projects' `.rota/` folders are untouched.

## Coming from hv-skills

rota is the successor to hv-skills. Install rota as above, then run this in each project that has a `.hv/` folder:

```bash
rota migrate hv            # dry run: lists what would change
rota migrate hv --apply    # makes the changes
```

It moves `.hv/` to `.rota/`, rewrites the `.gitignore` entries, the managed blocks in `AGENTS.md` and `CLAUDE.md`, and the config key that stamps the version. It also replaces the installed `/hv-*` skills with `/rota-*`; pass `--skip-skills` to leave your skills installs alone. Commit the result like any other change.

## Next

Run `rota init` once at the project root (`rota init --no-blocks` skips `AGENTS.md` and the managed blocks; `rota init umbrella` sets up a multi-repo coordinator). [Getting started](getting-started.md) walks through the first cycle.
