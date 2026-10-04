# Tool-generated siblings

Used by `/rota-work` Step 1 (dirty-tree guard) and Step 8 (after the task commits). Some toolchains write sibling files after a wave finishes, and an untracked sibling makes the next guard refuse on a dirty tree.

## What counts as a sibling

A path that sits next to a tracked file (e.g. `Foo.gd.uid` beside tracked `Foo.gd`) or matches one of: `*.gd.uid`, `*.xcworkspace/contents.xcworkspacedata`, `Package.resolved`, `*.xcodeproj/project.pbxproj` regenerated without a meaningful diff, `.DS_Store`. Everything else is a user change.

## Sweep

Classify every line of `git status --porcelain`. If **every** dirty path is a sibling, stage those paths by name and commit them on their own, never inside a task commit:

```bash
git add -- <sibling paths>
git commit -m "chore: sweep tool-generated siblings"
```

If **any** path is a user change, stop with the guard's message: the user decides whether to stash, commit or discard. Siblings that appear mid-cycle after the task commits follow the same rule; other dirt means a worker produced unexpected changes, so investigate before merging.

If a tool only regenerates siblings when the editor loads (Godot `class_name` → `.gd.uid`), force generation once headless before the sweep (`godot --headless --editor --quit`). Record project-specific commands in `KNOWLEDGE.md`.

Don't narrate the sweep unless it happened.
