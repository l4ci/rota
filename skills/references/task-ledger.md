# Task ledger

Used by `/rota-work` (writes a marker on every task commit, reads the markers on resume) and `/rota-pause` (reads them into the handoff note). Git history is the ledger: it survives compaction, a fresh session and a lost handoff note, and it cannot disagree with the tree.

## Marker

Every task commit from `/rota-work` Step 7.5 ends with a trailer:

```
Task: <key>/<n>
```

- `<key>` is the plan key when `rota plan show` found a plan (`M01-B07`, `M01-S01`), else the first item ID of the run (`#42`, `B07`).
- `<n>` is the task's number in that plan or decomposition (the `N` of the brief's *Task N of [total]*), stable across a resume.
- A same-file carve-out commit (one commit, several tasks) carries one `Task:` trailer per task.
- The trailer sits in the last paragraph of the message, after a blank line. No other trailer is needed or removed.

## Read

```bash
git log --format='%(trailers:key=Task,valueonly,separator=%x0a)' "$(rota git base)"..HEAD
```

One `<key>/<n>` per line, newest first. A task is **finished** when its marker is there. Unmarked commits (a `wip:` commit, `chore:` sweep, hand-made fix) are not tasks and never count.

## Resume rule

1. Re-derive the decomposition exactly as Step 4 does (the plan wins when there is one).
2. Drop every task whose `<key>/<n>` is in the ledger. Do not re-dispatch it, even when the handoff note's *Next planned step* names it: the ledger is newer than the note.
3. Dispatch what is left. All tasks finished means go straight to verify, close and ship.
4. Say the split in one line before dispatching: `Resuming <key>: done 1,2; remaining 3,4`.

If the decomposition no longer matches the ledger (a marker whose `<n>` has no task), the plan changed under the work: name it and ask once rather than guess.

## Pause

`/rota-pause` writes the ledger result into the note's *Stage* (`tasks 1-2 of 4 done`) and derives *Next planned step* from the first unfinished task. It reads the ledger from git, never from memory of the session.
