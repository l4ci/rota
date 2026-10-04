# Using the skills in Codex

The skills follow the [Agent Skills spec](https://agentskills.io/specification), so Codex reads them as well as Claude Code. Codex finds skills in `.agents/skills/<name>/SKILL.md`: `~/.agents/skills` for your user, and from the working directory up to the repo root for a project.

```sh
rota skills install                                # user: ~/.agents/skills (and ~/.claude/skills)
rota skills install --scope project --agent codex  # this repo: .agents/skills
```

`rota` carries the skills inside the binary. `install` copies each `rota-*` skill, plus the references it cites, into the root and writes a `.rota-manifest.json` that lists what it wrote. These are copies, not symlinks, so they work without a checkout and can be committed. A file you edited, or one rota did not write, is kept and reported; `--overwrite` replaces it.

After upgrading `rota`, run `rota skills update` to refresh every root that has a manifest. `rota skills status` shows whether each root matches the binary.

This page is about calling the skills from Codex. To run Codex as a worker in a [parallel round](parallel-rounds.md), see [Codex workers](codex-workers.md).

In Codex, type `$rota-pause` where Claude Code uses `/rota-pause`. It is the same skill.

Not covered: skill bodies still name Claude Code tools (`AskUserQuestion`, `TaskCreate`, `Agent`), so a skill may not run end to end in Codex. `rota` also has to be on `PATH` (install rota: see [install](../install.md)).

## Checking discovery

With `CODEX_HOME` unset, run `codex debug prompt-input hi` in a scratch repo. It prints the model-visible input, skills included, without starting a model session. Each `rota-*` skill should appear as `rota-x: <description>`. It reads your own `~/.codex` and writes nothing to the project. `rota doctor` checks that your Codex version is in the supported range, and that installed skills match the binary.
