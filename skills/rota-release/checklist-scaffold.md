# Checklist file absent (Step 2)

**File absent.** `autonomy.level` `"auto"`: skip silently; never interrupt an unattended run to scaffold. `"off"`: header `"Checklist"`, *"No `<release.checklistPath>` found. Scaffold a starter checklist now, or continue without?"*, options `Scaffold starter (Recommended)` (write the template, let the user edit, re-read, walk it) / `Continue without` (summary says *"no project checklist"*) / `Abort`.

Starter template:

```markdown
# Release Checklist

Each `- [ ]` line is a gate `/rota-release` walks before bumping the version. `- [x]` items are ignored. Append `(manual)` to an item that must ask even in `autonomy.level: auto`.

- [ ] Sibling version-bearing files are in sync (e.g., `.claude-plugin/marketplace.json`, lockfiles, docs version refs)
- [ ] CI is green on the release branch (the merge gate already ran the full suite on the release commit: confirm it, don't re-run smoke or `go test`)
- [ ] Migration notes for users on the prior version are written
- [ ] Where releases are signed (`signs:` in `.goreleaser.yaml`): the Actions secret `MINISIGN_SECRET_KEY` is set and every release asset gets a `.minisig` (key setup: `docs/contributing/release-signing.md`) (manual)

(Add project-specific items below.)
```
