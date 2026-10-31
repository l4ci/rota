---
number: 1
slug: why-rota
title: Why rota exists
length: 60
audience: evaluators
rota: "0.28.0"
chain: small-item
---

# Episode 1: Why rota exists

Estimated length: sting 1.5 s plus the per-shot `time:` lines below, **53.6 s** in all (limit 60 s; terminal typing and output run under the voiceover of shot 04, about 3 s of typing, and fit its 8.7 s). Written for someone deciding whether rota is worth a try. It states the problem, shows the loop and the gate, and ends on the repo link. Shot grammar and fields: [`../README.md`](../README.md#shot-grammar). Look and voice: [`../STYLE.md`](../STYLE.md).

### 01 motion

vo: Coding agents write code fast. Merging it safely is the hard part.

time: 6.3 s (12 words at 150 wpm, 2 pauses, 0.8 s hold)

scene: Title card on `paper`. Line 1 in Atkinson Hyperlegible Next 600, `ink`: "Coding agents write fast." Line 2 fades in 0.6 s later, `ask`: "Merging is the hard part." Left-aligned, no icon.

vertical: Same two lines stacked over the vertical centre, 88 px, each on its own line.

### 02 motion

vo: Branches pile up. Tests get skipped. Agents collide on files. Yesterday's lessons are gone.

time: 7.8 s (14 words at 150 wpm, 4 pauses, 0.8 s hold)

scene: Four small cards enter in a row, one per problem, each a `rule`-bordered card with a one-line label in Atkinson Hyperlegible Next: "branches pile up", "tests skipped", "file collisions", "lessons lost". The last card turns `danger` at the border only.

vertical: Cards stack in a column, 952 px wide, 16 px apart, entering top to bottom.

### 03 motion

vo: rota is a workflow for Claude Code and Codex. You write work down as issues. Agents build each one in small, verified commits.

time: 11.1 s (23 words at 150 wpm, 3 pauses, 0.8 s hold)

scene: A three-step flow, left to right: `capture`, `work`, `ship`, each a card with the skill name in Martian Mono (`/rota-capture`, `/rota-work`, `/rota-ship`) over a one-word gloss. The active card turns `duty` as the voiceover reaches it.

vertical: The three cards stack top to bottom with a vertical `muted` arrow between them.

### 04 terminal

vo: One small binary sets up a project in one command. Backlog, knowledge and decisions live in the repo.

time: 8.7 s (18 words at 150 wpm, 2 pauses, 0.8 s hold)

```sh
rota version
rota init
rota summary
```

crop: rota init keeps its last 6 lines.

lower-third: `rota init` | Set up a project in one command

vertical: Terminal box fills the frame width at 34 px; the crop keeps all three commands on screen with the newest line at the bottom.

### 05 motion

vo: Every branch is reviewed against its spec. Only work that passes your tests lands on main.

time: 7.9 s (16 words at 150 wpm, 2 pauses, 0.8 s hold)

scene: A PR card moves right into a box labelled "gate". The gate box border turns `merged` and the card continues to a "main" box. A second card stops at the gate, border `danger`, and is sent back left.

vertical: The gate sits mid-frame; passing card exits downward to "main", the failing card bounces back upward.

### 06 motion

vo: Lessons and decisions stay in the repo, so the next session starts ahead.

time: 6.3 s (13 words at 150 wpm, 1 pauses, 0.8 s hold)

scene: Three file cards fade in under one repo outline: `KNOWLEDGE.md`, `DECISIONS.md`, `handoff`. Filenames in Martian Mono.

vertical: Cards stack in a column inside the repo outline.

### 07 motion

vo: rota makes sure what they merge works.

time: 4.0 s (7 words at 150 wpm, 1 pauses, 0.8 s hold)

scene: The outro card from STYLE.md, unchanged: icon, two lines, link band `github.com/l4ci/rota`.

vertical: Outro card per STYLE.md, link set on two lines at 64 px.
