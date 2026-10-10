# Sweeping tool-generated siblings

Loaded by `SKILL.md` Step 1 (dirty guard) and Step 8.5 (after the task commits).

Exit 1 from `rota git guard clean` (dirty tree): if **every** dirty path in `git status --porcelain` is a tool-generated sibling, sweep them into their own `chore:` commit (never inside a task commit) and continue; any user change stops with the guard's message. Siblings are:

- a path beside a tracked file (e.g. `Foo.gd.uid` beside tracked `Foo.gd`), or
- one of `*.gd.uid`, `*.xcworkspace/contents.xcworkspacedata`, `Package.resolved`, `*.xcodeproj/project.pbxproj` regenerated without a meaningful diff, `.DS_Store`.

Stage by name:

```bash
git add -- <sibling paths>
git commit -m "chore: sweep tool-generated siblings"
```

If a tool only regenerates siblings when the editor loads (Godot `class_name` → `.gd.uid`), force generation once headless first (`godot --headless --editor --quit`). Don't narrate the sweep unless it happened.

On a fresh `git init` with no commits the guard points at a `chore: import initial files` baseline; run it and re-invoke.

## Step 8.5 — after the task commits

Sweep untracked toolchain siblings into their own `chore:` commit (staged by name), as above, or the next `/rota-work` guard refuses a dirty tree. Non-sibling dirt is an unexpected subagent change: investigate before merging.
