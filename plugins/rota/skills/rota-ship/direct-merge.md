# Direct merge path (Steps 6b and 6c)

Loaded by `SKILL.md` when Step 5 picked "Direct merge".

## Step 6b — Direct Merge

```bash
printf 'merge: <summary>\n\n- item 1\n- item 2\n' | rota ship merge <branch> --body-file - [--repo <name>]
```

The subject must start `merge: ` (undo recognizes cycles by it). Share the hash from `data.sha`. Exit 4: `data.blockedBy: "verdict"` is a recorded FAIL; surface and stop. `"manual gate"` is the `merge-approval` gate (`ship.mergeApproval` requires a human; `data.paths` names the files that triggered it) and nothing changed: ask the user, then rerun with `--confirm --confirm-note "<their answer>"`. A merge conflict (also exit 4) is aborted by the verb; tell the user.

## Step 6c — Close Upstream Issues (direct-merge path only)

Skip on the PR path (`rota ship body` already emits `Closes #N`) and on the issue backend (the tracker issues close when the PR merges).

`rota issues imported --json --open-only`; keep `data.entries` whose `itemId` is in the shipped IDs from Step 2. None: skip silently.

> **Manual gate — closing public upstream issues (`issue-close`).** Closing posts a comment and changes issue state on the remote. This step is **always manual** — never auto-invoked, regardless of `autonomy.level`. Skill-enforced only. See `references/manual-gates.md`.

Ask (header `"Close"`, *"Close N upstream issue(s) tied to the shipped items? (`#N, …`)"*): `"Yes, close all"` / `"Pick subset"` / `"No, leave open"`. For a subset, a second multi-select question (header `"Pick issues"`, options `"#N (item <ID>)"`, chunk by 4). Close each selected issue in one parallel batch:

```bash
rota issues close <N> --commit <merge-sha> --item <ID> [--repo <name>]
```

On "No, leave open" print *"Skipping upstream issue close — N issue(s) left open. Run `gh issue close <N>` / `glab issue close <N>` manually if desired."*
