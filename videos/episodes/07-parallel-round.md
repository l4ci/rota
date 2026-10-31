---
number: 7
slug: parallel-round
title: Run several issues in parallel
length: 100
audience: new users
rota: "0.28.0"
chain: parallel-round
---

# Episode 7: Run several issues in parallel

Written for someone who has shipped one item and has a few independent issues ready. Shot grammar and fields: [`../README.md`](../README.md#shot-grammar). Look and voice: [`../STYLE.md`](../STYLE.md).

### 01 motion

vo: One agent takes one issue at a time. A round runs several together, each in its own worktree, so they never touch the same files.

scene: Title card on `paper`. Line 1 in Atkinson Hyperlegible Next 600, `ink`: "One agent, one issue." Line 2 fades in 0.6 s later, `duty`: "Or a round of several." Left-aligned, no icon.

vertical: Same two lines stacked over the vertical centre, 88 px, each on its own line.

### 02 motion

vo: You start the round and play orchestrator. Named workers each take an issue and open one pull request. They never merge their own work. That stays with you.

scene: An `orchestrator` card on the left. Three arrows in `muted` lead to worker cards `ben`, `dana`, `nia` in Martian Mono, each over a small `worktree` label and an issue chip (`#59`, `#60`, `#61`). From each worker a `PR` chip returns to the orchestrator. The active path turns `duty`.

vertical: Orchestrator on top, three workers stacked below, PR chips returning upward.

### 03 terminal

vo: Start by taking the lease and setting up the slots. rota lists the issues that are ready and safe to run side by side. Issues that touch the same files are held back.

```sh
rota round start --slots 3
rota round candidates
```

staged: round-slate (a git project with a GitHub-style tracker holding three ready, non-overlapping open issues #59 to #61; needs a forge)

crop: rota round start keeps its last 5 lines; rota round candidates keeps its first 4 lines.

lower-third: `rota round start` | Take the lease, set up the slots

vertical: Terminal box at 34 px; lines cut at 40 columns by the fixture's short titles.

### 04 terminal

vo: Assign an issue and the first idle worker picks it up. Status shows each slot, its issue and its pull request.

```sh
rota round assign 59 --settled-file notes.md
rota round status
```

staged: live-round (a started round with slots ben, dana and nia, one assigned issue each, one PR open and one worker still building; needs a forge and a host session)

crop: rota round assign keeps its last 3 lines; rota round status keeps its last 8 lines.

lower-third: `rota round status` | Slots, issues and PRs at a glance

vertical: Terminal box at 34 px; the status table is trimmed to slot, issue and state.

### 05 motion

vo: When a worker needs a decision, it asks on the issue. Answer the questions one at a time. The round picks up again without anyone starting over.

scene: A chat panel. `/rota-answer` in Martian Mono, then a card "dana, #60: add the flag, or change the default?" with an `ask` border and `ask-wash` band. The reply "add the flag" appears in `duty`, and the card border turns `merged`.

vertical: Chat panel on top, question card below at 952 px wide.

### 06 terminal

vo: Each pull request goes through the merge gate. When all are in, wind the round down. rota re-checks main and releases the lease. Then the slots wait for the next round.

```sh
rota worker gate ben --base main
rota round wind-down
```

staged: round-merged (the live-round fixture after its PRs passed review; test.full passing on the merged tree; needs a forge)

crop: rota worker gate keeps its last 4 lines; rota round wind-down keeps its last 6 lines.

lower-third: `rota round wind-down` | Re-verify main, free the slots

vertical: Terminal box at 34 px; the closing summary stays on screen.

### 07 motion

vo: rota makes sure what they merge works.

scene: The outro card from STYLE.md, unchanged: icon, two lines, link band `github.com/l4ci/rota`.

vertical: Outro card per STYLE.md, link set on two lines at 64 px.
