# Direct merge path (Steps 6b and 6c)

Loaded by `SKILL.md` when Step 5 picked "Direct merge".

## Step 6b — Direct Merge

```bash
printf 'merge: <summary>\n\n- item 1\n- item 2\n' | rota ship merge <branch> --body-file - --json [--repo <name>]
```

The subject must start `merge: ` (undo recognizes cycles by it). Read the JSON even on a nonzero exit and share the hash from `data.sha`.

Exit 4 with `data.blockedBy: "branch moved"` and `data.changed: true` is a landed merge with the branch kept. Save `data.sha` as the merge commit and continue the post-merge steps for the pinned work only (`M^1..M^2`, where `M` is that merge commit). When Step 7 uses `ship finish`, run `rota ship finish --json <branch> --commit <data.sha>` even though the branch still exists; the explicit commit keeps its later work out of bookkeeping. The umbrella skip still applies: its step-by-step bookkeeping also uses the merge SHA and pinned range. Tell the user that `data.pinned` landed on `data.base` as `data.sha`, that the branch remains at `data.tip`, and that commits added after the pin need their own review and merge. Never retry this merge.

Other exit-4 cases: `data.blockedBy: "verdict"` is a recorded FAIL; surface and stop. `"manual gate"` is the `merge-approval` gate (`ship.mergeApproval` requires a human; `data.paths` names the files that triggered it) and nothing changed: ask the user, then rerun with `--confirm --confirm-note "<their answer>"`. A merge conflict (also exit 4) is aborted by the verb; tell the user.

## Step 6c — Close Upstream Issues (direct-merge path only)

Skip on the PR path (`rota ship body` already emits `Closes #N`) and on the issue backend (the tracker issues close when the PR merges; on Jira and Linear, `rota worker gate` closes them).

`rota issues imported --json --open-only`; keep `data.entries` whose `itemId` is in the shipped IDs from Step 2. None: skip silently.

> **Manual gate — closing public upstream issues (`issue-close`).** Closing posts a comment and changes issue state on the remote. This step is **always manual** — never auto-invoked, regardless of `autonomy.level` (see [`references/autonomy.md`](references/autonomy.md)). Skill-enforced only. See `references/manual-gates.md`.

Ask (header `"Close"`, *"Close N upstream issue(s) tied to the shipped items? (`#N, …`)"*): `"Yes, close all"` / `"Pick subset"` / `"No, leave open"`. For a subset, a second multi-select question (header `"Pick issues"`, options `"#N (item <ID>)"`, chunk by 4). Close each selected issue in one parallel batch:

```bash
rota issues close <N> --commit <merge-sha> --item <ID> [--repo <name>]
```

On "No, leave open" print *"Skipping upstream issue close — N issue(s) left open. Run `gh issue close <N>` / `glab issue close <N>` manually if desired."*
