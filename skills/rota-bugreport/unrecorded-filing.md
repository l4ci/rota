# Filed but not recorded

Read from `SKILL.md` Step 5 when `rota bugreport file` exits 0 with `recorded:false` or a `warning:` line.

Exit 0 with `recorded:false` (or a `warning:` line) is not plain success: the issue exists, but the record could not be marked `filed` and stays open. Tell the user the issue is filed and give the URL, run `rota bugreport dismiss <id>` to close the record, and never refile. Left open, the Stop hook and wind-down re-offer it, and a later yes would file a duplicate.
