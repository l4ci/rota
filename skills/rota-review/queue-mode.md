# Queue mode (`/rota-review --queue`, issue mode)

Works through every `needs-review` item's open PR / MR. Issue mode only (`backlog.backend: "issues"`; label lifecycle in `references/issue-mode.md`). In file mode say *"`--queue` needs the issue backend; use `/rota-ship` and `rota ship merge` here"* and stop.

**Who merges.** Whoever runs the queue owns the merge: you, outside a round. Inside a round the orchestrator merges through `rota worker gate` (rota-orchestrate section 6) and workers never run `--queue`, so don't run it against round PRs.

```bash
rota review queue --json
```

`data.items` is `[{"id","number","title","prs":[{"number","title","branch","url","body"}]}]`. Empty: report *"Review queue is empty"* and stop. Per entry, in order:

1. **PRs.** None: report *"<ID> is `needs-review` but has no PR with a closing keyword"* and skip. Several: review each.
2. **Checkout.** `git status --short` must be clean, else stop. Check the PR out through the adapter: `rota tracker call -- pr checkout <n>` (GitHub) or `-- mr checkout <n>` (GitLab).
3. **Review.** Skip when `rota proof show <ID> --json` already holds a PASS at the PR's current head sha (`git rev-parse HEAD`): the merge gate that follows is the only full run. Go to Route as a PASS. Otherwise run Steps 2-8 on the checked-out branch, scoped to `<base>...HEAD` (`<base>` from `rota git base`). The reviewer and the queue are read-only apart from the verbs below.
4. **Route** on `rota verdict route <branch> --for queue --json`, field `data.next`:
   - **`ask`** (PASS) — `AskUserQuestion` (Header `"Merge"`, *"Merge PR <n> for <ID>?"*, options *Merge (Recommended)* / *Skip* / *Stop*). Merge with `rota ship pr-merge <n>` (in an umbrella `--repo <name>` is required; queue entries carry `repo` and qualified IDs): it merges and closes the linked items the host left open; `data.sha` and `data.closed` report the result. Pass the Merge answer along: `--confirm --confirm-note "<answer>"` (ignored unless `ship.mergeApproval` covers the PR). Exit 4 with `data.unproven`: nothing merged because an item has no proof; it is now `changes-requested`; report and move on. Exit 4 with `data.blockedBy: "verdict"`: the PR's branch has a recorded review or second-opinion FAIL; nothing changed; report and move on. Exit 4 with `data.blockedBy: "manual gate"` is the `merge-approval` gate (`ship.mergeApproval` requires a human for this merge; `data.paths` names the files that put it there): nothing changed. Ask the user in an `AskUserQuestion`, then re-run with `--confirm --confirm-note "<their answer>"`. Then post the verdict on each linked item (`rota item comment add <ID> --kind feedback --body-file -`) and on the PR (`rota tracker call -- pr comment <n> --body-file -` on GitHub, `-- mr note <n> --message "<verdict>"` on GitLab).
   - **`request-changes`** (CONCERNS / FAIL) — post the findings as a `feedback` comment on each linked item and on the PR (same commands), then `rota item state <ID> --to changes-requested`. The author's next `/rota-work` claim reads the feedback. No merge, under any autonomy level.
5. **Return.** `git checkout <base>` before the next entry.

Exit 5 or 6 (tracker unavailable or rate-limited) from any verb stops the queue with a report of what was done and what is left. Never retry. Routing table: `references/review-verdict-routing.md` (Queue routing).
