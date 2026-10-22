# `/rota-qa` restructure mode

Loaded by `SKILL.md` on `/rota-qa restructure`. On demand, when strategy files drifted (new surfaces, retired tools, dead targets).

1. Re-run the `Detect surfaces` and `Detect existing test infra` probes (steps 1 and 2 of `first-run.md`).
2. Diff against `.rota/qa/*.md`; flag targets with no surface (dead), surfaces with no target (uncovered), commands using tools not installed (broken), `Watch globs` matching no files (stale).
3. Propose changes (archive dead, draft new, fix broken, update globs) and show the user before writing.
4. On approval, write, run `rota qa index`, commit `chore(qa): restructure QA strategy (<summary>)`.
