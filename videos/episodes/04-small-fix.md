---
number: 4
slug: small-fix
title: Fix a small bug end to end
length: 75
audience: new users
rota: "0.28.0"
chain: small-item
---

# Episode 4: Fix a small bug end to end

Written for a new user with a captured bug, `B01`. It works the item with a red then a green proof, then ships it. Every terminal shot needs an agent session or a failing test, so all three are staged. Shot grammar and fields: [`../README.md`](../README.md#script-format). Look and voice: [`../STYLE.md`](../STYLE.md).

### 01 motion

vo: One captured bug. Now fix it, prove the fix, and ship it.

scene: Title card on `paper`. Line 1 in Atkinson Hyperlegible Next 600, `ink`: "Fix it. Prove it. Ship it." The three verbs enter one after another; the active one turns `duty`.

vertical: The three verbs stack over the vertical centre, 88 px, each on its own line.

### 02 terminal

vo: Give the work skill the ID. It checks your tree is clean, cuts a branch, and builds the fix in small commits.

```sh
/rota-work B01
```

staged: claude session with rota skills installed, in a project with one captured bug B01 and a failing test for it; the skill prints the branch name and the first task

crop: keep the last 10 lines.

lower-third: `/rota-work` | Branch, build, commit in small steps

vertical: Terminal box fills the frame width at 34 px; the branch line and the first task stay in view.

### 03 terminal

vo: A bug fix needs proof. First the test must fail. That is the red run. After the fix it must pass. Ro-Ta runs the command itself, so nobody types the result.

```sh
rota proof record B01 -- go test ./... -run TestSaveEmptyName
rota proof record B01 -- go test ./... -run TestSaveEmptyName
rota proof show B01
```

staged: scratch Go project with bug B01 and a test TestSaveEmptyName; the first run fails on an assertion, the fix lands between the first and second command, the second run passes

crop: keep the FAIL line, the PASS line and the two rows of proof show.

lower-third: `rota proof record` | Measured red, then green

vertical: Terminal box at 34 px; `FAIL` renders in `danger`, `PASS` in `merged`, each cropped to one line.

### 04 motion

vo: If the proof has no red run, the gate stops the item. A passing test that never failed proves nothing.

scene: Two proof rows on `rule`-bordered cards. Row 1: `FAIL`, border `danger`. Row 2: `PASS`, border `merged`. A third card, `PASS` only, enters with a `danger` border and a "no red run" label in `ask`.

vertical: Cards stack top to bottom, 952 px wide, 16 px apart.

### 05 terminal

vo: Then ship. It reviews the branch against the item first. On a pass it merges, or opens a pull request, depending on your config.

```sh
/rota-ship
```

staged: claude session with rota skills installed, on the branch /rota-work cut for B01; the review passes and the skill prints the merge or the PR URL

crop: keep the last 12 lines.

lower-third: `/rota-ship` | Review first, then merge or open the PR

vertical: Terminal box at 34 px; the review verdict and the final line stay in view.

### 06 motion

vo: Capture, work, ship. That is the whole loop for a small change.

scene: The three-step flow from episode 1, left to right: `/rota-capture`, `/rota-work`, `/rota-ship`. All three cards end in `merged` borders, one after the other.

vertical: The three cards stack top to bottom with a vertical `muted` arrow between them.

### 07 motion

vo: Coding agents write fast. Ro-Ta makes sure what they merge works. Find it at github dot com slash l four c i slash Ro-Ta.

scene: The outro card from STYLE.md, unchanged: icon, two lines, link band `github.com/l4ci/rota`.

vertical: Outro card per STYLE.md, link set on two lines at 64 px.
