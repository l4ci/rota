# Docs nudge (Step 14)

Skip unless `docs.afterWork` (default `false`) is true, in `--dry-run`, or already run this session. `"off"`: append to the summary *"Release shipped. Run `/rota-ship --docs` to review and update public docs (after-work mode)."* `"auto"`: dispatch `rota-ship --docs` by reading and following its instructions immediately, no prompt, with a brief naming the version, bump type and the one-line summary. `/rota-ship` self-skips if the docs path is missing or empty.
