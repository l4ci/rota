# Unpushed commits at HEAD (Step 1)

Unpushed commits ship with the release; not an error. Branch on `autonomy.level`:

- `"auto"`: count `git rev-list @{u}..HEAD --count`. Below `release.confirmLargePushCommits`: run `git push origin <current-branch>` silently and continue. At or above it, ask once whatever the autonomy: header `"Large push"`, *"<N> unpushed commits about to be pushed as part of this release. Continue?"*, options `Push and continue (Recommended)` / `Abort`.
- `"off"`: header `"Unpushed"`, *"HEAD has unpushed commits. Push them as part of this release?"*, options `Push and continue (Recommended)` / `Abort`.
