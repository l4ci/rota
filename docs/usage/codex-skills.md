# Using the skills in Codex

The skills follow the [Agent Skills spec](https://agentskills.io/specification), so Codex reads them as well as Claude Code. Codex finds skills in `.agents/skills/<name>/SKILL.md`: `~/.agents/skills` for your user, and from the working directory up to the repo root for a project.

```sh
rota skills install                                # user: ~/.agents/skills (and ~/.claude/skills)
rota skills install --scope project --agent codex  # this repo: .agents/skills
```

`rota` carries the skills inside the binary. `install` copies each `rota-*` skill, plus the references it cites, into the root and writes a `.rota-manifest.json` that lists what it wrote. These are copies, not symlinks, so they work without a checkout and can be committed. A file you edited, or one rota did not write, is kept and reported; `--overwrite` replaces it.

To keep installed skills from drifting behind the binary, `rota skills install` writes each `SKILL.md` as a short stub (the default) that tells the agent to run `rota skills get <name>`. That prints the text embedded in the installed binary, a first line `Base directory for this skill: <dir>`, and the absolute paths of the files the text links to (`--references` lists a skill's reference files, `--reference <file>` prints one). The files live in a per-version tree in your user cache, so relative links resolve. Codex runs the command when it loads a skill. For a harness that cannot, `rota skills install --full` writes the full text and the references into the root. `rota skills list` names the embedded skills and the binary version.

A stub root follows the binary. Run `rota skills update` only when `rota skills status` (or `rota doctor`) reports it stale, which for a stub means a skill was added or removed or its stub changed (frontmatter or stub text); the report names the skill. A full root needs `rota skills update` after every upgrade. `update` keeps each root's recorded mode unless you pass `--stub` or `--full`.

`--references` lists both the skill's linked local files and its shared references, with paths relative to the skill directory. Use the listed path to fetch a file:

```sh
rota skills get rota-review --references
rota skills get rota-review --reference reviewer-briefs.md
rota skills get rota-review --reference references/test-quality.md
```

Bare shared names such as `test-quality.md` still work. When a linked local file has the same name, the bare name selects the local file; `references/<name>` selects the shared one. An unknown name exits with a list of valid paths.

To run Codex as a worker in a [parallel round](parallel-rounds.md), see [Codex workers](codex-workers.md).

In Codex, type `$rota-pause` where Claude Code uses `/rota-pause`. It is the same skill.

The skills use neutral instructions for file reads, edits, questions and subagent dispatch. If a question cannot use the current Codex picker, answer the numbered options in prose; approval gates still require an explicit answer. To invoke another skill, Codex reads its instructions and follows them with the supplied arguments. Subagents use `round.tiers.codex.*` when configured, otherwise the harness default. `rota` must be on `PATH` (see [install](../install.md)).

## Remaining Claude-only behaviour — 2026-10-08

- **`rota-work`:** the shared wave-planning guidance names Claude Code's "File has been modified since read" error and retains its retry with a byte-identical `old_string`. That tool-specific error and retry are labelled Claude-only.
- **`rota-orchestrate`:** solo mode still launches Claude subagents through its `Agent` interface. Codex must use a supported terminal host for rounds; see [Codex workers](codex-workers.md). This restriction applies to standing round workers, not ordinary subagents inside a skill.

Shared authoring guidance retains a Claude-only question-picker adapter and Claude task-title escaping advice. Neither is required for Codex skill execution.

The audit is enforced by a Go test running the tool-name grep, with exceptions for neutral prose and explicitly Claude-only instructions. Smoke section 144 installs the embedded skills and runs preview and capture through the scripted Codex worker in `test/fakes`: it checks output structure, read-only preview and capture's created IDs. It does not call a model or prove live interpretation of every skill. The audit itself was implemented in a Codex worker with a default-model subagent; live coverage beyond that remains unverified.

## Checking discovery

With `CODEX_HOME` unset, run `codex debug prompt-input hi` in a scratch repo. It prints the model-visible input, skills included, without starting a model session. Each `rota-*` skill should appear as `rota-x: <description>`. It reads your own `~/.codex` and writes nothing to the project. `rota doctor` checks that your Codex version is in the supported range, and that installed skills match the binary.

## Skill directories

`rota skills install` (above) is the route rota keeps up to date. `npx skills add l4ci/rota` also works.

- **skills.sh** lists a repo once someone installs it with the `skills` CLI. It has no submission form. `npx skills add l4ci/rota` is the common install route and puts the skills in `.agents/skills`, where Codex reads them. It sends anonymous install telemetry, and writes no `.rota-manifest.json`, so `rota skills status` and `update` do not track the copies. rota is listed at [skills.sh/l4ci/rota](https://skills.sh/l4ci/rota) (checked 2026-10-08, after one clean-environment run of that command).
- **openai/skills** is marked deprecated in its README, which points to [openai/plugins](https://github.com/openai/plugins) and the [Build plugins](https://developers.openai.com/codex/plugins/build) guide. Its `skills/` tree has `.curated` and `.system` and no community tier. Checked on 2026-10-08.
