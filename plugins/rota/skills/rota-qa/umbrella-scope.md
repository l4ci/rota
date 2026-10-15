# `/rota-qa` umbrella scope

Loaded by `SKILL.md` Steps 1 and 6 when `.rota/repos.json` is non-empty. Per-repo resolution: `references/umbrella-mode.md`.

## Step 1 — scope

- **Umbrella** (`.rota/repos.json` non-empty): the repo of the current branch (`rota repo which`, field `name`). `--repo <name>` or `--all` override. `<target>` is a registered repo name.
- **Single-repo**: all `.rota/qa/*.md` entries.

## Step 6 — verdict

Record the verdict once per repo with `--repo <name>` carrying that repo's verdict. `--all` rollup is worst-of across targets; per-target verdicts still report individually.
