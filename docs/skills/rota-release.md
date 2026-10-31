---
skill: rota-release
lane: maintenance
order: 125
situation: Bigger picture
summary: "Cut a release: checklist, version bump, notes, tag, publish."
not_when:
  - use: rota-ship
    when: you are landing one branch, not tagging a release
  - use: rota-docs
    when: you only need the public docs brought up to date
before: [rota-learn]
after: []
manual_only: true
flags:
  - flag: "major | minor | patch | <semver>"
    does: set the bump level instead of being asked
  - flag: "--dry-run"
    does: run the flow without tagging or publishing; skips the close-upstream-issues gate
example:
  source: walkthroughs/round-cycle.md#4-release
  reviewed: true
  steps:
    - you: /rota-release
    - agent: |
        Checklist (.rota/RELEASE.md): all items pass.
        Commits since v0.25.0: 1 feature, 4 fixes. Recommend minor.
    - you: minor
    - agent: |
        Release notes drafted for v0.26.0.
        Notes look good? Yes pushes the tag and publishes the release. (manual gate)
  changes:
    - "version file and `CHANGELOG.md` updated in a `chore: release` commit"
    - an annotated git tag, pushed after your yes
    - a published GitHub or GitLab release
---
# /rota-release

Cuts a release end to end: walks the project's release checklist (`.rota/RELEASE.md` by default; override with `release.checklistPath`) as a preflight gate, bumps the project version (`major`/`minor`/`patch` or explicit semver), generates categorized release notes from commits since the last tag, prepends a section to `CHANGELOG.md` (creating it if absent), creates an annotated git tag, pushes commit and tag, and publishes a release on GitHub or GitLab when origin is set. It detects the version source (`plugin.json`, `package.json`, `pyproject.toml`, `Cargo.toml`, plain `VERSION`) and honors a `release.versionFile` override.

The checklist file is per-project and tracked by default; see [release checklist](../usage/review-and-ship.md#release-checklist) for the format. When absent, the skill offers to scaffold a starter (under `autonomy.level: off`) or skips the gate silently (under `auto`). Items ending in `(manual)` always interject even in unattended modes.

Where the repo carries a video series (`videos/`), the preflight also runs `python3 scripts/videos.py --check` and reminds the maintainer to re-render any stale episode with `videos/render.sh <episode>`; it never blocks the release.

After publishing, an always-manual gate offers to close any upstream GitHub/GitLab issues that landed in the release but are still open. It covers the direct-push path where work skipped `/rota-ship` (or chose "leave open") and the linked issues never closed. Candidates come from `rota issues imported --open-only`; the gate is silent when nothing is open. Skipped entirely in `--dry-run` mode.

