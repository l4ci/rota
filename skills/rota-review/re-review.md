# Re-review (`/rota-review --since <sha>`)

After a bounce the worker pushes a fix. Review the fix, not the whole branch. `<sha>` is the `sha` of the branch's last recorded review (`rota verdict show <branch> --json`, the newest record of kind `review-quality`); with no recorded review, run the full review. Do these in place of the full Step 7 brief:

- Step 5 packages only `--since <sha>`. Steps 2-4 and 6 still run, on the fix range.
- Both reviewers run, each on its own axis. `rota verdict show <branch> --json` holds the latest `review-spec` and `review-quality` records: put each record's `findings` into its reviewer's brief as `**Earlier findings:**`, numbered.
- Each reviewer marks every earlier finding `ADDRESSED` or `NOT ADDRESSED`, with the diff line that settles it. A finding the fix does not touch is `NOT ADDRESSED`.
- Only the fix gets the rubric. Anything noticed outside the fix goes to a `**Deferred:**` list in the report: never a finding, never moves the verdict.
- The verdict block carries each `NOT ADDRESSED` finding again, plus any new finding the fix introduced. `ADDRESSED` ones appear only in `summary`, as a count. `PASS` needs every earlier finding `ADDRESSED` and no new finding in the fix.
- Relay the `**Deferred:**` list to the caller beside the verdict. File it with `/rota-capture` if the caller wants; never fold it into this branch.
