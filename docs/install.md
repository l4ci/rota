# Install rota

Install the binary and the skills once, then set up each project from your coding agent.

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

Open your project in Claude Code or Codex. Reopen the agent session if the new skills are not visible, then invoke `/rota-init` (`$rota-init` in Codex).

[Start your first parallel round](getting-started.md).

## Other options

The [installation reference](reference/installation.md) covers plugins, release binaries, agent-assisted installation, updates and removal.
