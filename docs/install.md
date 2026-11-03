# Install rota

Open your project in Claude Code, Codex or OpenCode and paste:

```text
Install rota and set it up in this project. Follow https://raw.githubusercontent.com/l4ci/rota/main/install.md
```

The agent installs rota and its skills, then helps configure the project.

Alternative: [Homebrew](reference/installation.md#homebrew) · [curl](reference/installation.md#install-script) · [Claude Plugin](reference/installation.md#-plugin-marketplace) · [Codex Plugin](reference/installation.md#codex) · [More](reference/installation.md).

## With Homebrew

```sh
brew install l4ci/tap/rota
rota skills install
```

## Without Homebrew

On Linux or macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/l4ci/rota/main/install.sh | sh
rota skills install
```

The script installs to `~/.local/bin`. It checks the download checksum and, when `minisign` is available, the release signature. [Verification details](reference/installation.md#verifying-a-release).

On Windows, use [WSL2](reference/installation.md#windows-wsl2).

## In your project

For a manual install, open your project in Claude Code or Codex. Reopen the agent session if the new skills are not visible, then invoke `/rota-init` (`$rota-init` in Codex).

[Start your first parallel round](getting-started.md).

## Other options

The [installation reference](reference/installation.md) covers plugins, release binaries, updates and removal.
