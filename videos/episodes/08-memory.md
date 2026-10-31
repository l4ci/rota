---
number: 8
slug: memory
title: Memory that stays in the repo
length: 90
audience: new users
rota: "0.28.0"
chain: end-of-session
---

# Episode 8: Memory that stays in the repo

Written for someone who has finished a session and wants the next one to start ahead. Shot grammar and fields: [`../README.md`](../README.md#shot-grammar). Look and voice: [`../STYLE.md`](../STYLE.md).

### 01 motion

vo: An agent session ends. What it learned should not. Today's lesson is tomorrow's repeated mistake otherwise.

scene: Title card on `paper`. Line 1 in Atkinson Hyperlegible Next 600, `ink`: "The session ends." Line 2 fades in 0.6 s later, `duty`: "The lessons stay." Left-aligned, no icon.

vertical: Same two lines stacked over the vertical centre, 88 px, each on its own line.

### 02 terminal

vo: Ro-Ta keeps two files for this in the repo. One holds knowledge, the other holds decisions. A new project starts with both nearly empty. They are plain files, so they travel with the code and show up in review.

```sh
rota init
rota knowledge topics
rota decisions topics
```

crop: rota init keeps its last 3 lines.

lower-third: `rota knowledge topics` | Lessons live in the repo, by topic

vertical: Terminal box fills the frame width at 34 px; the three commands stay on screen with the newest line at the bottom.

### 03 motion

vo: Learn saves a gotcha you hit, so the next session does not hit it again. It keeps the reason, not just the rule. Decide records a hard boundary, and only when you ask for it. Neither is written without your yes.

scene: Two chat panels side by side. Left: `/rota-learn` and a proposed bullet "The badge reads a stale count until the first tick. Re-subscribe in the quick-switch handler." ending "Written to .rota/KNOWLEDGE.md." in `merged`. Right: `/rota-decide` and a four-part decision card, ending in an `ask` line "Save this decision? (yes / changes)".

vertical: The two panels stack top to bottom, 952 px wide.

### 04 terminal

vo: Later, anyone can read a topic back. The agent does it before it touches that area, so old advice reaches the new session.

```sh
rota knowledge query Timer
rota decisions query Architecture
```

staged: memory-repo (a project whose .rota/KNOWLEDGE.md has a Timer topic with one date-stamped bullet and whose .rota/DECISIONS.md has an Architecture decision; built from rota knowledge add and rota decisions add)

crop: each query keeps its first 6 lines.

lower-third: `rota knowledge query` | Read back what the repo remembers

vertical: Terminal box at 34 px; each answer is cut to 6 lines.

### 05 motion

vo: When your context fills up, pause. It writes a handoff note. The next session reads it and carries on from where you stopped. You do not retell the story. You just continue.

scene: A `/rota-pause` chat line, then a `handoff` file card appears with three one-line entries: "item", "state", "next step". A second chat panel opens beneath: `/rota-work`, with the agent reply "Resuming from the handoff." in `duty`.

vertical: Pause panel on top, handoff card in the middle, resume panel below.

### 06 motion

vo: Coding agents write fast. Ro-Ta makes sure what they merge works. Find it at github dot com slash l four c i slash Ro-Ta.

scene: The outro card from STYLE.md, unchanged: icon, two lines, link band `github.com/l4ci/rota`.

vertical: Outro card per STYLE.md, link set on two lines at 64 px.
