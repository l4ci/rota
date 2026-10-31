---
number: 3
slug: capture
title: Capture work as issues
length: 70
audience: new users
rota: "0.28.0"
chain: small-item
---

# Episode 3: Capture work as issues

Written for a new user with a project set up. It turns a brain dump into backlog items and stops before any code. Shot grammar and fields: [`../README.md`](../README.md#script-format). Look and voice: [`../STYLE.md`](../STYLE.md).

### 01 motion

vo: You have a pile of things to fix. Write them down first. Do not start coding yet.

scene: Title card on `paper`. Line 1 in Atkinson Hyperlegible Next 600, `ink`: "Write it down first." Line 2 fades in 0.6 s later, `muted`: "Capture records the work and stops."

vertical: Same two lines stacked over the vertical centre, 88 px, each on its own line.

### 02 terminal

vo: Tell the capture skill what you found, in plain words. One message can hold a bug and a task.

```sh
/rota-capture the save button accepts an empty name, and update the linter config
```

staged: claude session with rota skills installed, in a project set up with rota init; the skill splits the message into one bug and one task and prints both IDs

crop: keep the last 12 lines.

lower-third: `/rota-capture` | Brain dump in, backlog items out

vertical: Terminal box fills the frame width at 34 px; the typed message wraps onto three lines, the two IDs stay in view.

### 03 motion

vo: It sorts each point by kind. Bugs get a priority. Features get a size. Tasks need no tag.

scene: Three cards in a row: "bug, P0 to P2", "feature, Major, Minor or Cosmetic", "task, no tag". Labels in Atkinson Hyperlegible Next, tags in Martian Mono. The bug card border is `duty`.

vertical: Cards stack in a column, 952 px wide, 16 px apart, entering top to bottom.

### 04 terminal

vo: Underneath, the skill calls one command per item. You can run it yourself too. It prints the new ID.

```sh
rota init
rota item create --kind bugs --tag P2 --title "Save button accepts an empty name" --desc "Saving a profile with an empty name succeeds"
rota item create --kind tasks --title "Update the linter config" --desc "Match the new rules"
```

crop: rota init keeps its last 4 lines; each item create keeps the printed ID line.

lower-third: `rota item create` | One command per backlog item

vertical: Terminal box at 34 px; the long command wraps onto three lines, so the crop drops the init output first.

### 05 terminal

vo: List the backlog to see what is waiting, and the summary for the count.

```sh
rota backlog list
rota summary
```

crop: keep all output; the list is 13 lines.

lower-third: `rota backlog list` | What is waiting

vertical: Terminal box at 34 px; the list and the summary fit in 14 lines, newest at the bottom.

### 06 motion

vo: Nothing has been built. Each item has an ID. Pick one and work it in the next episode.

scene: Two item rows from the backlog, `B01` and `T01` in Martian Mono, each a `rule`-bordered card with its title. `B01` takes a `duty` border and an arrow labelled "next: work".

vertical: Rows stack top to bottom; the arrow points down from `B01`.

### 07 motion

vo: rota makes sure what they merge works.

scene: The outro card from STYLE.md, unchanged: icon, two lines, link band `github.com/l4ci/rota`.

vertical: Outro card per STYLE.md, link set on two lines at 64 px.
