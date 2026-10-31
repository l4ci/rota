---
number: 5
slug: design-before-code
title: Design before code
length: 90
audience: new users
rota: "0.28.0"
chain: [big-feature, risky-idea]
---

# Episode 5: Design before code

Written for someone who has the basics and is about to hand an agent a big feature. It shows where the design lives, who settles it, and when code starts. Shot grammar and fields: [`../README.md`](../README.md#shot-grammar). Look and voice: [`../STYLE.md`](../STYLE.md).

### 01 motion

vo: A big feature needs agreement before code. Otherwise an agent builds the wrong thing, and builds it fast. Fixing that costs more than a short talk up front.

scene: Title card on `paper`. Line 1 in Atkinson Hyperlegible Next 600, `ink`: "Big feature?" Line 2 fades in 0.6 s later, `ask`: "Agree on the design first." Left-aligned, no icon.

vertical: Same two lines stacked over the vertical centre, 88 px, each on its own line.

### 02 motion

vo: rota has three ways to do that. Brainstorm settles the design. Plan breaks it into tasks. Spike answers a question by trying it, and ships no code, only a finding. Pick the one that fits the doubt you have.

scene: Three cards in a row, each a `rule`-bordered card with the skill in Martian Mono over a one-line gloss in Atkinson Hyperlegible Next: `/rota-brainstorm` "settle the design", `/rota-plan` "break it into tasks", `/rota-spike` "try it, keep the finding". Each card turns `duty` in turn as the voiceover reaches it.

vertical: The three cards stack top to bottom, 952 px wide, 16 px apart.

### 03 terminal

vo: Each design is a file in the repo. Start one, list it, and read it. The stub has a goal, the design, the approaches considered and the open questions. It stays a draft until you settle it.

```sh
rota init
rota design add F12 --title "Archive old items"
rota design list
rota design show F12
```

crop: rota init keeps its last 3 lines; rota design show F12 keeps its first 8 lines.

lower-third: `rota design show` | A design is a file in the repo

vertical: Terminal box fills the frame width at 34 px; the crop keeps the four commands on screen with the newest line at the bottom.

### 04 motion

vo: In the brainstorm, the agent weighs two or three approaches and asks you to pick. Your answer goes into the design, with the reasons, so nobody has to guess later.

scene: A chat panel, `rule` frame. Line `/rota-brainstorm F12` in Martian Mono, then an agent reply fades in: "A. Archive on a schedule. B. Archive on demand." and the question "Which one?". The `you` reply "B" appears in `duty`, and a small `design F12` card to the right gains a line, "Chosen: on demand", with a `duty-wash` band.

vertical: Chat panel on top, design card below it, both 952 px wide.

### 05 motion

vo: Then plan turns the design into tasks. Each task is tied to an acceptance criterion and a check. Nothing is built until you confirm. Then the work is small, checked steps.

scene: A plan card lists `T1`, `T2`, each with a `verify:` line in Martian Mono. Arrows in `muted` link each task to an `AC-1` / `AC-2` chip. The last line, "Confirm this plan? (yes / changes)", is set in `ask`.

vertical: The card stacks tasks and chips in one column; the confirm line sits at the bottom.

### 06 motion

vo: rota makes sure what they merge works.

scene: The outro card from STYLE.md, unchanged: icon, two lines, link band `github.com/l4ci/rota`.

vertical: Outro card per STYLE.md, link set on two lines at 64 px.
