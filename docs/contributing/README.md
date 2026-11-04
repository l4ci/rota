# Contributing

Issues and pull requests are welcome. [Scope](../../SCOPE.md) explains which changes fit rota.

Run `python3 test/validate-skills.py` and `bash test/doclint.sh` for documentation changes. Run `bash test/gate.sh` at integration boundaries for code changes; see [rounds](rounds.md) for the full merge rules.

[Herdr App/Core validation](herdr-core-validation.md) records the real two-worker
round, native shell-tool checks, lifetime tests and validation boundaries.

Skill descriptions live in `docs/skills/`. Run `python3 scripts/skills-docs.py` after editing their metadata. The skill catalog and workflow lists are generated there; the README and documentation home are curated entry points.

The public website uses a separate Jekyll repository. `python3 scripts/site-sync.py <site-checkout>` generates its documentation and navigation from this tree. Edit generated pages here, then sync; edit the landing page in the site repository.

The [videos page](../videos.md) lists only episodes recorded in `videos/out/manifest.json`. The sync reads titles from the episode scripts, descriptions from the Goal column in `videos/README.md`, and actual lengths from the rendered MP4 files. Run `git lfs pull` before syncing from a fresh checkout. New episodes and re-renders reach the site through the same sync; the output hashes update the media links.
