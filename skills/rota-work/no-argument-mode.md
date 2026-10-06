# No-Argument Mode (reconcile, suggest, then work)

**1. Reconcile active streams.** `rota status show` lists them; git beats `status.json`. Per stream: a branch that no longer exists is dropped with `rota status rm <branch> [--repo <repo>]`; otherwise note whether it has commits past the base (`rota git base`) and whether `rota status handoff <branch> [--repo <repo>]` returns a `/rota-pause` note (read Stage, Next planned step, Current hypothesis). A paused orchestrator's note carries round state: surface it and point at `/rota-orchestrate` (resumes from `rota round status`). Resolve each stream with `AskUserQuestion`, Recommended first:

- handoff present: resume with the note as the brief (Recommended); leave it for later; abandon.
- commits, no handoff: ship via `/rota-ship` (Recommended); resume; leave as-is.
- no commits, no handoff: resume (Recommended); abandon; leave as-is.

Resume continues on the existing branch. For a stream with commits, read the task ledger (`references/task-ledger.md`) and carry only unfinished tasks into Step 4; a handoff's *Next planned step* never re-dispatches a finished task. Abandon is `git branch -D <branch>` plus `rota status rm`, and removes the handoff note. A note is deleted only when its stream is resumed or abandoned.

**2. Orient.** Run `rota backlog archive --days 5` (silent), `rota milestone active` and one `rota backlog ids --milestone <MID>` per active milestone, then `rota backlog list`. Print the list in full, every row and section (exempt from length limits). Prefix it with `Active milestones: <ids and titles>` when there are any. Advisories, never blocking: for each ID in `rota backlog drift --json` print `<ID> looks shipped on <hash> but still open`, and suggest `rota proof add` then `rota item complete <ID> --commit <hash>` (or `--no-proof`), never auto-complete. Print `stale: map=N, knowledge=M, todo=K` from `rota backlog stale` (zero kinds dropped) and `empty-active: <MID>` for an active milestone with no open items. `rota backlog drift` is file-backend only; skip it on the issue backend.

**3. Suggest one item.** Order: P0 bugs; clusters holding a blocking bug; quick wins (Cosmetic, P2); the highest-impact P1; blocking tasks (`Related:`); Minor features; Major features only when nothing else is pending or the user asks. Milestone bias at every level except P0: items tagged to an active milestone first, then untagged, then non-active milestones. Items labelled `changes-requested` (issue backend) rank right after P0. Skip items already active. Print `Suggested next: <ID> Title (tag)` and one sentence why. For a `[Major]` feature or `[P0]` bug with no design (`rota design show <ID>`), add *"consider `/rota-brainstorm <ID>` before this"*.

**4. Confirm.** `AskUserQuestion`: Start (Recommended); Peek approach first (`--preview`, offered for Major, P0/P1 or a batch); Write a plan first (`/rota-plan`, offered for a Major item with a supported plan key and no plan, using Step 4's backend key rules and `rota plan show` — issue items need no milestone); Pick different items; Stop here. "Other" text is the item spec.

On a terminal path (Stop here, or an empty backlog) run `rota release pending --json` and, when `shouldNudge` is true, print its `message` as one line. Skip it when work continues or there is no tag yet. Pass the item's text into Step 1.
