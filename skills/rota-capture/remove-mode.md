# Remove Mode (`--remove`)

`rota item rm` handles the BACKLOG entry, `Related:` cross-references, detail file and any plan keyed to the item. It previews by default; only `--apply` writes. ARCHIVE entries stay unless `--scrub-archive`. Counters never decrement. Contract: `docs/contributing/contract/backlog.md` (*rota item rm*).

### Step R1 — Resolve IDs

Split on commas, pass IDs as positionals. Exit 3 = unknown ID: show stderr and stop.

### Step R2 — Dry-Run Preview

Run `rota item rm <IDS>` and show stdout verbatim. If an item has `activeBranch` (`--json`: `data.items[].activeBranch`), tell the user: *"The item is active on `<branch>`. Apply refuses it until the stream is dropped with `rota status rm <branch>`."* and continue.

### Step R3 — De-tag Upstream Issues

> Removing the `in-progress` label upstream is externally visible. The item delete proceeds either way.

Run `rota issues imported --json`, keep `data.entries` whose `itemId` is in the removal set. Label: `rota config show --json issues.label` (default `in-progress`). No matches: skip to Step R4.

Otherwise ask:

- **Header:** `"De-tag"`; **Question:** *"Remove the `<label>` label on <N> upstream issue(s)? <list of #N>."*
- **Options:**
  1. *"Yes, remove the label upstream"* — `rota issues label <issue> --remove "<label>"` per entry, adding `--repo <repo>` when the entry's `repo` is non-null. Propagate a failure.
  2. *"No, just delete the item"* — print: *"Note: upstream issues still carry the `<label>` label. Remove via `gh issue edit <N> --remove-label <label>` or `glab issue update <N> --unlabel <label>` if desired."*

### Step R4 — Confirmation Gate

Show the preview, then one `AskUserQuestion`. Header `"Apply"`, question *"Apply this removal plan for <IDS>?"*:

1. *"Apply (Recommended)"* — `rota item rm --apply <IDS>`; ARCHIVE entries stay as the historical record.
2. *"Apply + scrub ARCHIVE"* — `rota item rm --apply --scrub-archive <IDS>`; also removes the ARCHIVE entry and its cross-references.
3. *"Cancel"* — print *"No changes."* and stop.

Anything but an explicit yes cancels. This is a destructive gate: it always asks.

### Step R5 — Apply

Run the chosen command and pass its per-ID output through verbatim. On exit 4 (`data.blockedBy: "active"`), tell the user to run `rota status rm <branch>` first and stop. Don't nudge any other skill.

`--remove` does not close upstream issues; that is always manual.
