---
name: rota-release
description: Use on "release", "cut a release", "tag a release", "ship X.Y.Z".
---

# rota-release — Cut a Release

## Configuration

From `.rota/config.json` (all keys optional):

| Key | Default | Notes |
|---|---|---|
| `release.versionFile` | (auto-detect) | Explicit path override; skips auto-detect search |
| `release.changelogPath` | `CHANGELOG.md` | Project-root relative |
| `release.checklistPath` | `.rota/RELEASE.md` | Per-project release checklist walked in Step 2; absent = offer scaffold |
| `release.tagPrefix` | `v` | Set to `""` for unprefixed tags |
| `release.draft` | `false` | Pass `--draft` to `gh`/`glab` |
| `release.requireCleanTree` | `true` | Set `false` to allow dirty releases (testing only) |
| `release.confirmLargePushCommits` | `10` | Threshold (commits) above which auto autonomy still confirms before pushing unpushed HEAD |

Copy this checklist and track your progress:

```
- [ ] Step 1 — Guard
- [ ] Step 2 — Project Checklist
- [ ] Step 3 — Milestone Gate (issue mode)
- [ ] Step 4 — Version and Bump Level
- [ ] Step 5 — Generate Release Notes
- [ ] Step 6 — Review Notes
- [ ] Step 7 — Write Version File and CHANGELOG
- [ ] Step 8 — Commit
- [ ] Step 9 — Tag
- [ ] Step 10 — Push the Tag
- [ ] Step 11 — Publish Remote Release
- [ ] Step 11b — Push the Branch
- [ ] Step 12 — Close Out the Milestone (issue mode)
- [ ] Step 13 — Close Upstream Issues
- [ ] Step 14 — Docs Nudge
- [ ] Step 15 — Summary
```

## Step 1 — Guard

Stop with a one-liner on any failure.

1. **Clean tree**: `git status --porcelain`. Non-empty and `release.requireCleanTree` true: stop, show `git status -s`, suggest commit/stash or `release.requireCleanTree: false`.
2. **On trunk**: branch must be `main`, `master` or `trunk`.
3. **HEAD pushed**: `git rev-parse HEAD` vs `@{u}`. When HEAD has unpushed commits, read [`unpushed-commits.md`](unpushed-commits.md) and follow it.

`--dry-run` (any step): run the read-only verbs and the judgment questions, skip every write, commit, tag and push, and print what would happen instead.

## Step 2 — Project Checklist

Per-project release steps live in `release.checklistPath`; the skill hardcodes none. Skip in `--dry-run`: print the parsed items and `DRY RUN — checklist walk skipped.`

**File absent.** Read [`checklist-scaffold.md`](checklist-scaffold.md) and follow it (`"auto"` skips silently, `"off"` offers a starter scaffold).

**File present.** Every line matching `^\s*-\s+\[\s*\]\s+(.+)$` is a gate, in file order; `- [x]` lines are skipped. Zero gates: say *"Checklist has no open items — continuing."* Per gate:

- `"off"`: ask: header `"Checklist"`, *"Checklist item: \<text\>. Done?"*, options `Yes, continue (Recommended)` / `Fix it now and continue` (pause, re-ask the same item) / `Skip this item` (record `skipped: <text>`) / `Abort release` (*"Release aborted at checklist item: \<text\>. Nothing written."*).
- `"auto"`: auto-acknowledge items not ending in `(manual)`; ask the `"off"` question for items that do, so sensitive items stay confirmed in unattended runs.

## Step 3 — Milestone Gate (issue mode)

When `backlog.backend` is `"issues"` or `rota repo umbrella` exits 0, read [`milestone-gate.md`](milestone-gate.md), *Step 3*, and follow it. Otherwise skip.

## Step 4 — Version and Bump Level

`rota release version --json` gives `data` `{file, version, kind}`. Exit 3 (no version file or unparsable): surface the message and tell the user to run `rota config set release.versionFile <path>`.

A bump arg may be `major`, `minor`, `patch` or `X.Y.Z`. With none, get the previous tag (`git describe --tags --abbrev=0 2>/dev/null || true`; empty means full history, note it in the summary), run `rota release notes --from commits [--since <prev-tag>]` and read the bucket headings: `Breaking` recommends `major`, `New` recommends `minor`, otherwise `patch`. Ask: header `"Bump type"`, *"Current version: `<current>`. What bump type?"*, options `patch — <current> → <X.Y.Z+1>` / `minor — … → <X.Y+1.0>` / `major — … → <X+1.0.0>` (mark the recommended one) / `Explicit version` (exact string via Other) / `Abort`.

Compute the new version read-only: `rota release version --json --level <patch|minor|major>` (or `--to <X.Y.Z>`); `data.next` is `new_version`. An invalid or not-greater `--to` exits 1: surface it and stop.

If `Breaking` commits were found but the user chose `patch` or `minor`, ask before continuing: header `"Escalate"`, *"Commits contain `BREAKING CHANGE:` footers but bump type is `<chosen>`. Escalate to major?"*, options `Escalate to major (Recommended)` / `Keep <chosen>` / `Abort`.

## Step 5 — Generate Release Notes

`rota release notes --from commits [--since <prev_tag>]` (issue mode: `--from issues <MNN>`) returns categorized Markdown. Exit 3/4/5/6: stop and report. Editorial work is yours:

- **Compact dense buckets.** 3+ entries on one concern become one theme line plus at most 1-2 bullets for the highest-impact pieces (breaking change, flag flip, new public surface). Smaller buckets stay.
- **Stats.** Replace the verb's `## Stats` with `## Stats\n<N commits, M files changed, +X −Y lines>` from `git diff --shortstat <prev_tag>..HEAD` (drop `<prev_tag>..` with no previous tag).
- **Compare link.** With a previous tag, `rota release host --json` `data.host` picks the URL: `github`/`github-enterprise` → `https://<host>/<owner>/<repo>/compare/<prev_tag>...v<new_version>`; `gitlab`/`gitlab-self-hosted` → `https://<host>/<owner>/<repo>/-/compare/<prev_tag>...v<new_version>`. Append `**Full changelog:** <url>`. Host `none` or no previous tag: omit.
- **One-line summary.** Prepend one line on the top 2-3 themes. It becomes the release title suffix in Step 11.

Before showing the draft, silently apply `references/humanizing-prose.md` to all model-written prose.

## Step 6 — Review Notes

Show the full draft, then ask: header `"Notes"`, *"Release `v<new_version>` — notes look good? Yes pushes the tag and publishes the release."*, options `Looks good (Recommended)` / `Edit` (replacement text via Other replaces the draft verbatim; re-display it, one edit pass, no second prompt) / `Abort release` (*"Release aborted. Nothing written."*).

This answer is the human approval for Steps 10 and 11. Keep it verbatim as `$APPROVAL` (the option label, or the replacement text's first line after Edit) for `--confirm-note`. Never auto-pick this question in any autonomy mode.

Write the approved notes to `NOTES_FILE=$(mktemp /tmp/rota-release-notes.XXXXXX.md)`.

## Step 7 — Write Version File and CHANGELOG

```bash
rota release bump --json --level <patch|minor|major>   # or --to <X.Y.Z>; same arguments as Step 4
rota release changelog <new_version> --body-file "$NOTES_FILE" [--path <release.changelogPath>]
```

If `data.to` differs from `new_version`, stop and surface it (the file may be partly modified). Changelog exit 4 (section already exists): stop before committing. Skipped in `--dry-run`.

## Step 8 — Commit

`git add <version-file> <release.changelogPath>` then `git commit -m "chore: release v<new_version>"`. Skipped in `--dry-run`.

## Step 9 — Tag

`git tag -s` if `git config --get user.signingkey` is set, else `-a`: `git tag [-a|-s] v<new_version> -F "$NOTES_FILE"`. Skipped in `--dry-run`; print the command.

## Step 10 — Push the Tag

> **Manual gate — pushing the release tag.** Always asks, in every autonomy mode. `rota release push` enforces the `tag-push` gate (exit 4 without `--confirm`); Step 6's answer is the approval. See `references/manual-gates.md`.

```bash
rota release push <new_version> --tag-only --json --confirm --confirm-note "$APPROVAL"
```

Only the tag goes now (an unflagged push is refused where goreleaser builds the release); the branch waits for Step 11b. On failure, read [`publish-failures.md`](publish-failures.md). Skipped in `--dry-run`.

## Step 11 — Publish Remote Release

> **Manual gate — publishing the release.** Always asks. `rota release publish` enforces the `release-publish` gate and reuses Step 6's answer.

```bash
rota release publish <new_version> --json --title "v<new_version> — <one-line summary>" \
  --body-file "$NOTES_FILE" [--draft] --confirm --confirm-note "$APPROVAL"
```

Add `--draft` when `release.draft` is true and the host is GitHub. `data.url` goes in the summary. When it exits non-zero or reports `changed: false`, read [`publish-failures.md`](publish-failures.md). Skipped in `--dry-run`; print the command.

## Step 11b — Push the Branch

Once the release is published and the binaries resolve, the same `tag-push` gate and Step 6's answer cover the branch:

```bash
rota release push <new_version> --branch-only --json --confirm --confirm-note "$APPROVAL"
```

Skipped in `--dry-run`. On a non-zero exit, read [`publish-failures.md`](publish-failures.md).

## Step 12 — Close Out the Milestone (issue mode)

In issue mode, read [`milestone-gate.md`](milestone-gate.md), *Step 12*, after Steps 10 and 11. Otherwise skip.

## Step 13 — Close Upstream Issues

When `rota issues imported --json --open-only` lists open upstream issues, read [`upstream-issues.md`](upstream-issues.md) and follow it. Skip in `--dry-run`.

## Step 14 — Docs Nudge

When `docs.afterWork` is true, read [`docs-nudge.md`](docs-nudge.md); otherwise skip.

## Step 15 — Summary

```
Released v<new_version>
  Tag:      v<new_version> (<tag-SHA>)
  Commit:   <commit-SHA>
  CHANGELOG: <release.changelogPath>
  Remote:   <release-URL or "skipped">
  Checklist: <N> done / <M> skipped (or "no project checklist" / "skipped in dry-run")
  [No previous tag — full history used as range.]   ← only when no prev tag
```

List skipped checklist items under `Skipped checklist items:`. In `--dry-run`, prefix the block with `DRY RUN — no changes written.`

## Rules

- Never bump without a user-confirmed bump type.
- Never push the tag without the user approving the release notes (Step 6).
- Stop on any non-zero verb exit you have no branch for.
- Project-specific release steps live in `release.checklistPath`, never here.

## References

- [`unpushed-commits.md`](unpushed-commits.md): Step 1 handling of unpushed HEAD commits.
- [`milestone-gate.md`](milestone-gate.md): Issue-mode milestone gate, close-out and umbrella repos (Steps 3, 12).
- [`publish-failures.md`](publish-failures.md): Push/publish failure handling and edge cases (Steps 10-11b).
- [`upstream-issues.md`](upstream-issues.md): Close shipped upstream issues (Step 13).
- [`docs-nudge.md`](docs-nudge.md): After-work docs nudge (Step 14).
- [`checklist-scaffold.md`](checklist-scaffold.md): Starter checklist when the file is absent (Step 2).
- [`references/manual-gates.md`](references/manual-gates.md): The manual-gate registry (`rota gate list`): gates the verbs enforce with `--confirm`, and the skill-only callouts.
- [`references/issue-mode.md`](references/issue-mode.md): Issue-mode milestones and release.
- [`references/humanizing-prose.md`](references/humanizing-prose.md): Self-audit for model-written notes.
