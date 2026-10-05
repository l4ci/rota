# Baseline: plain agent vs round worker

Follow-up to the round-speed report ([`round-speed.md`](round-speed.md), #46), which did not run this. Tracked in #86.

## Setup

Task: issue #26, "Smoke reads the developer's installed Codex skills", merged as PR #28 (`282c23f`). It is small (one file, 8 added lines) and has a known-good fix to diff against.

Both runs started from the same base, `de253be` (first parent of the merge), in separate scratch clones. Each clone had a remote-less history cut at the base: later refs removed, reflog expired, objects pruned. `git log --all` in either clone shows 17 commits and no trace of the fix.

| | Plain | Worker |
|---|---|---|
| Harness | `claude -p`, `--model sonnet`, bypass permissions | same |
| Prompt | issue text + "Fix this issue." | issue text + the standing worker contract from `skills/references/worker-contract.md` |
| Shared rules | commit locally, no push/PR/GitHub, no full smoke or test suite | same |

Both were given the issue body and its thread inline. Run in parallel, so they saw the same load.

## Results

Wall times were taken on a loaded box: load average 5 to 10 on 8 cores, with sibling workers running (#84, #62, #55). Read them as relative, not as idle-box numbers. Single run each, so no variance estimate.

| | Plain | Worker |
|---|---|---|
| Wall time | 79 s | 91 s |
| Turns | 6 | 10 |
| Output tokens | 2,557 | 2,938 |
| Cache-read tokens | 241,920 | 358,467 |
| Cache-creation tokens | 39,801 | 37,876 |
| Cost (reported) | $0.233 | $0.253 |
| Verification | sections 83, 89 | sections 83, 89, 98, with a stale skill planted in a fake real `HOME` |
| PR body / `## Approvals` | none | written (`None needed.`) |
| Sentinel (`ROTA-DONE`) | none | printed |
| Disputed the ticket | no | no |
| Stray files | none | none |

## What each skipped

- **Plain** wrote no PR body and no approvals section, printed no sentinel, and never reproduced the failure: it ran two sections against a clean `HOME`, so its claim rests on reading the code path, which it says itself.
- **Worker** skipped nothing the contract asks for in this sandbox. It did not dispute the ticket because the ticket is sound.
- **Both** skipped the full smoke suite and the "passes on PR #12's branch" acceptance item. That was my sandbox rule, not a contract effect, so it does not separate the two.
- **Neither** scoped HOME to the sections that need it. Both took the runner-wide route and argued why it is safe.

## Correctness against the merged fix

Code lines are identical in all three:

```
+export HOME="$RUN_TMP/home"
+mkdir -p "$HOME"
```

Placed after the `CLAUDE_CONFIG_DIR` block in `test/runner.sh`, as merged. Only the comment wording differs. Both safety arguments match the merged comment: go build runs first, git identity comes from `GIT_*`, gh/glab/herdr/tmux/codex are poisoned, and sections 83/94/97/98 set their own `HOME`.

Neither run is a correctness difference. The plain result would very likely pass the merge gate on the evidence of an identical diff, but the gate was not run (see below).

## Findings

- **The contract bought process, not correctness, on this task.** Same diff, about 15% more wall time and 8% more cost for the worker. What the extra spend bought: a stronger check (a planted stale skill, which is the actual failure from the ticket) and a PR body a reviewer can read. Whether that is worth it on a one-file fix is a judgment call; on a change where the plain agent guesses wrong it would matter more. One task cannot show that.
- **Both commits carried a `Co-Authored-By` trailer**, from the harness attribution reminder. The user rule forbids it. Not checked whether the user's global rule reached the headless agents. In a real round the worker's PR passes through the orchestrator, so this is where a trailer would slip in; worth a check in the gate.
- **Hint leak.** The inlined thread included a rota handoff comment naming the merged commit's title. Both agents reused it verbatim as their commit subject. It did not leak the diff, but it means the two runs are less independent than a cold start.

## Caveats

- n = 1 task, 1 run per setup. A small, well-specified, single-file ticket favours the plain agent; the contract's value shows on larger or ambiguous work.
- The agents ran headless from the same Sonnet model; the plain setup still loaded the repo's `AGENTS.md` and `CLAUDE.md`, as any agent in this repo would.
- A first attempt was discarded: the issue text failed to load into the prompts (`gh issue view` hit the projectCards GraphQL deprecation error), and the worker then found the merged fix through the shared git object store and recommitted it. The reruns use isolated, pruned clones and inline issue text. Anyone repeating this must prune the clone, not just check out the parent.

## Deferred

- **Gate-pass check.** Not run: the full gate is ~589 s, saturates the CPU, and siblings were active. Follow-up on #86 for an idle box: run `bash test/smoke.sh`, `go test -race ./...`, vet and validate on the plain result.
- A larger or ambiguous task, where the thread-reading and dispute steps could change the outcome.
