# Gates, hooks and mode skeleton

Loaded by `SKILL.md` when a caller or config flag decides whether `/rota-qa` runs, or when picking a mode.

## Config read only by gates and hooks

- `qa.gate` — `"advisory"` (default) emits the verdict, never blocks ship; `"blocking"` halts `ship.qa: true` invocations on `FAIL`.
- `qa.afterWork` — `false` (default). When `true`, `/rota-work` invokes `/rota-qa run` post-cycle if touched files match a QA target's `Watch globs`.
- `ship.qa` — `false` (default). When `true`, `/rota-ship` calls `/rota-qa run` between `/rota-review` and the merge/PR step.

- `ship.uat` — `false` (default). When `true`, `/rota-ship` calls `/rota-qa uat <ID>` after review and the other gates, before the merge/PR step. Never read by `rota worker gate` or the round merge path. Calling `/rota-qa uat` directly works either way; `qa.gate` does not apply to it.

## Pillar filtering

`qa.afterWork` and `ship.qa` call `/rota-qa run --gate`; only that run filters pillars, by the rule in `SKILL.md` Step 4. A manual `/rota-qa run` runs every pillar.

Pillar keys are lowercase: `functional`, `security`, `performance`, `ui-a11y`. Globs are root-relative paths matched against the touched files; `*` stays within a segment and `**` spans directories. The caller passes the touched-file set it matched on; absent that, the run reads `rota review scope --json`. On main that set is empty, so every pillar with globs would be skipped and the run would record PASS: do not use `--gate` there.

## Modes

`/rota-qa` shares the three-mode skeleton with `/rota-ship`'s Docs Mode (scaffold / after-work / audit); see `references/three-mode-skill-shape.md`. Divergences:

| Aspect | `/rota-qa` |
|---|---|
| Artifact root | `.rota/qa/<target>.md`, one strategy per target. Umbrella: `<target>` is a registered repo name; single-repo: a user-named surface (`web`, `api`, `cli`, ...) |
| Mode-3 name | `restructure` (re-probe surfaces, retire dead strategies, fix broken commands) |
| After-work trigger gate | `qa.afterWork: true` AND touched files match a target's `Watch globs` (default off) |
| Commit ownership | `run` does not commit (read-only; verdict recorded with `rota verdict add`); `first-run` / `restructure` own a `chore(qa):` commit |

## Routing from `/rota-ship`

`/rota-ship` routes with `rota verdict route --for ship-qa`, which applies `qa.gate` (`"advisory"` never halts; `"blocking"` halts on `FAIL` and prompts on `CONCERNS`). Carrier label `QA concerns:` when invoked from `/rota-ship` (`references/review-verdict-routing.md`, "Carrier-label override").
