# Gates, hooks and mode skeleton

Loaded by `SKILL.md` when a caller or config flag decides whether `/rota-qa` runs, or when picking a mode.

## Config read only by gates and hooks

- `qa.gate` — `"advisory"` (default) emits the verdict, never blocks ship; `"blocking"` halts `ship.qa: true` invocations on `FAIL`.
- `qa.afterWork` — `false` (default). When `true`, `/rota-work` invokes `/rota-qa run` post-cycle if touched files match a QA target's `Watch globs`.
- `ship.qa` — `false` (default). When `true`, `/rota-ship` calls `/rota-qa run` between `/rota-review` and the merge/PR step.

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
