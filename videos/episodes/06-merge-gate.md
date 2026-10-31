---
number: 6
slug: merge-gate
title: The merge gate and review verdicts
length: 90
audience: new users
rota: "0.28.0"
chain: big-feature
---

# Episode 6: The merge gate and review verdicts

Written for someone whose agent has opened a PR. It shows what stands between that PR and the main branch. Shot grammar and fields: [`../README.md`](../README.md#shot-grammar). Look and voice: [`../STYLE.md`](../STYLE.md).

### 01 motion

vo: Opening a pull request does not finish the work. An agent can say it is done and still be wrong. The gate decides, and it does not take anyone's word.

scene: A PR card moves right toward a box labelled "gate" and stops in front of it. The box border is `duty`. Labels in Atkinson Hyperlegible Next, `PR` and `main` in Martian Mono.

vertical: PR card above, gate box in the middle, `main` box below.

### 02 terminal

vo: Some steps always need a human yes. rota lists them, with the verbs that enforce them. Merging into the base branch is one. The agent cannot skip it.

```sh
rota init
rota gate list | cut -d' ' -f1
```

crop: rota init keeps its last 2 lines; the gate names keep all 10 lines.

lower-third: `rota gate list` | Steps that need a human yes

vertical: Terminal box fills the frame width at 34 px; the cut keeps each line under 40 columns.

### 03 terminal

vo: First, review. It checks the branch against its spec and records one verdict: pass, concerns or fail. The merge reads that record, not a chat message, and a verdict for an older commit does not count.

```sh
rota verdict show
```

staged: branch-with-verdicts (a project on a pushed branch with a recorded review-spec and review-quality PASS verdict at HEAD; needs no forge for the capture)

lower-third: `rota verdict show` | The latest verdict of each kind

vertical: Terminal box at 34 px; the two verdict lines and the sha stay on screen.

### 04 terminal

vo: Then the gate itself. It runs your test tiers on the merged tree. On a pass it merges. If anything fails, it refuses and says why.

```sh
rota test run fast
rota worker gate ben --base main
```

staged: gate-pass-slot (a round slot `ben` with an open PR, test.fast and test.full configured and passing, a PASS verdict at HEAD, merge-approval pre-given; needs a forge and a PR)

crop: rota test run keeps its last 3 lines; rota worker gate keeps its last 8 lines.

lower-third: `rota worker gate` | Verify on the merged tree, merge on a pass

vertical: Terminal box at 34 px; the final `merged` line is the only green token.

### 05 motion

vo: A failing test or a fail verdict sends the findings back to the worker. It fixes the branch, and the gate checks again. Nothing lands on a guess, and nothing stays red for long. You get a main branch you can build from.

scene: A PR card reaches the gate and the gate border turns `danger`. A short list of findings leaves the gate to the left in `ask`. The card returns with a new commit tick, passes through the gate, the border turns `merged`, and the card enters `main`.

vertical: Gate mid-frame; findings leave upward, the card re-enters from the top and exits downward to `main`.

### 06 motion

vo: rota makes sure what they merge works.

scene: The outro card from STYLE.md, unchanged: icon, two lines, link band `github.com/l4ci/rota`.

vertical: Outro card per STYLE.md, link set on two lines at 64 px.
