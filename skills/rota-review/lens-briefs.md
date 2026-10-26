# Lens reviewers (Step 7)

## Contents

- When a lens runs
- Lens brief
- Rubrics: security, perf, ux

`rota review depth <branch> --json` carries `lenses`: one `{name, why}` per lens the branch triggers (`security`, `perf`, `ux`), from `review.lenses` globs over the diff's files and the labels of the branch's issues. `why` is `path <file>` or `label <name>`. An empty list runs nothing, and depth `none` lists none. Triggers are deterministic: never pick a lens yourself, never skip a listed one.

Each triggered lens gets one reviewer, at `full` and at `light`, dispatched in the same message as Spec and Standards. Fresh context each, the same diff file and Steps 2-6 context.

- **security** — **orchestrator** model.
- **perf**, **ux** — **`standard`** tier.

## Lens brief

The shared block from `reviewer-briefs.md`, plus:

```
**Axis: <lens>.** Judge only the <lens> axis, from the diff. Do not judge spec fit, conventions or test quality: other reviewers do. This lens triggered because: <why from the depth JSON>.

**Rubric.** <the lens's rubric below>

Report findings with file:line, ranked by severity. Anything that needs a running product goes in `Declined to judge`, never in a finding or a PASS.
```

End it with the calibration rules and verdict block from `reviewer-briefs.md`, unchanged. `items` may be omitted. The reviewer follows the read-only rules of the shared block (git archive extract, scratch `HOME`, named tests only).

## Rubrics

**security.** Command and argument injection (a value reaching a shell or an `exec` argv unquoted, a leading `-` read as a flag). Path traversal and symlink following. File permissions and non-atomic writes of files others read. Secrets in logs, args or output. Token handling. Unsafe parsing of untrusted input. TOCTOU between a check and its use. A new dependency.

**perf.** Complexity on inputs that grow (nested loops over issues, files or records). Subprocess or I/O inside a loop. Unbounded reads or memory. Repeated work a nearby cache already covers. Cliffs only: no tuning advice, no micro-optimisation.

**ux**, by surface. For a CLI: help and usage text, error messages that say what to do next, exit codes, stability of the human and `--json` output (a renamed key or reordered column breaks callers), flag naming against sibling verbs. For web: empty, error and loading states and accessibility visible in the markup.

## Record

Step 8 records each lens at `$REVIEWED`: `rota verdict add <branch> --kind review-<lens> --verdict <V> --sha "$REVIEWED" --body-file "$LENS" --json`, after spec and quality. A lens FAIL blocks the gate like any review FAIL; a CONCERNS passes. Without the record `rota worker gate` refuses `review-missing`, naming the lens and why.
