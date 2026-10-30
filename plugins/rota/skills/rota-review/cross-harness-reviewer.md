# Cross-harness reviewer (Step 7)

A `size:Major` or `risk:high` issue's PR gets one more review from a model family other than the author's (#1612). `rota review depth <branch> --json` decides it: `data.cross` is the harness to run (`codex` or `claude`), `none` when it is skipped (`review:none`, or no other harness in `round.harnesses`), absent when it does not apply. A `review:<harness>` label on the issue picks the harness. The run follows [`references/cross-harness-run.md`](references/cross-harness-run.md): one-shot and read-only, no worker slot, worktree or pane.

## Brief and inputs

1. Run `rota review prepare "$REVIEWED" --json` (Step 5) and require success with `data.head == REVIEWED`. Use its `data.path` as `<scratch>`; `.review` is already created. A preparation failure stops the run as unavailable; never use the live checkout.
2. Copy the Step 5 package to `<scratch>/.review/diff.txt` with the token line first (`cross-harness-run.md`, *Inputs*). Write the items' spec (Step 3) to `<scratch>/.review/items.md`.
On a re-review (`re-review.md`), `$REVIEWED` is the new head and the extract is of it, while `diff.txt` is the `--since <sha>` package of Step 5, the fix range only. The brief then carries the latest `review-cross` record's blocking findings as `**Earlier findings:**`.

3. Build the **Standards** brief from `reviewer-briefs.md` (Step 7) with its shared block, then the Spec rubric's *Intent match* item, then *Both briefs end with*. Two edits: the diff and spec paths are `.review/diff.txt` and `.review/items.md`, relative to the extract, and *Running the branch* becomes: `You run in an extract of the branch; read code there. Do not run tests: the merge gate runs them. Any binary probe must use the caller's installed rota review probe -- <command> [<arg>...] from the extract; setup or cleanup failure makes the probe unavailable. Do not override its temporary config roots with developer paths. This isolates environment paths, not absolute-path access by unsandboxed commands.` End the prompt with the token line.

## Run and record

Run it on `data.cross` with `cross-harness-run.md`, *Run*, *Account* and the repo guard, from inside the extract. Then, in this order:

1. **Check.** Read the output by `cross-harness-run.md`, *Read the output*: the first line must be the matching `TOKEN`, and the repo guard must show the real repo unchanged. A failed check means unavailable (below); nothing is recorded.
2. **Record.** Save the last fenced `json` block of `<out>` to a temp file and record it in Step 8 as `review-cross`, with `--harness <data.cross>` and `--model <the model that ran>` (`cross-harness-run.md`, *Account*).
3. **Retry once.** When `verdict add` exits 2 (it names the field), run the same harness once more with the brief plus `Your verdict block was rejected: <the exit-2 message>. Send the whole report again with a corrected block.`, check that run as in 1 (a fresh token), and record as in 2. A second exit 2 is a failure: record nothing.

- **Unavailable.** A failed check, or the second block failed: record nothing, never a guessed verdict, and print `cross-harness review unavailable (<harness>: <reason>)` in the report. `rota worker gate` keeps refusing `review-missing` until a `review-cross` record from that harness exists at head. In a round that line goes to the orchestrator, who reruns the review later (a quota resets) or adds the `review:none` label to the issue, a visible choice on the issue.
- **Fallback is not another harness.** Never record a `review-cross` from the harness you run in, or from the author's: the gate refuses a record whose `harness` differs from `data.cross`.

Run `rota review cleanup <scratch>` when the record is written or the run is given up (also on failure or cancellation). Report cleanup failures with the path; do not remove any other directory.

## Report

Present the cross-harness report verbatim under its own heading, `## Cross-harness (<harness> <model>) — <verdict>`, after Spec and Standards, with its `Declined to judge` list. It is not merged into the combined verdict, but a FAIL blocks the merge and its blocking findings bounce like any reviewer's. On a re-review (`re-review.md`) it runs again on the fix range, with its own earlier blocking findings as `**Earlier findings:**`.
