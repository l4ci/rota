# rota video series

A short video series that explains rota from "why" through install, setup and the everyday chains. This directory holds the **plan and the sources**: the outline below, the look-and-feel guide ([`STYLE.md`](STYLE.md)) and one script per episode under [`episodes/`](episodes/). Rendered media is not checked in here; the render pipeline (#2536) builds each video from these files.

It lives outside `docs/` on purpose: the docs site and the embedded binary never pick up half-made scripts or large media.

## Episodes

About 8 episodes. Episode 1 is for evaluators; episodes 2 to 8 walk a new user from install to a first round. Lengths are targets for the finished video, including the logo sting and outro card.

| # | Slug | Title | Goal | Audience | Length | Shows | Chain |
|---|---|---|---|---|---|---|---|
| 1 | [why-rota](episodes/01-why-rota.md) | Why rota exists | Name the problem (fast agents, unsafe merges) and the answer: capture, build, gate, remember | evaluators | ≤60 s | `rota version`, `rota init`, `rota summary`; `/rota-capture`, `/rota-work`, `/rota-ship` | small-item |
| 2 | [install-and-setup](episodes/02-install-and-setup.md) | Install and set up a project | Get the binary, the skills and a working `.rota/` in one project | new users | 60–120 s | `install.sh`, `rota version`, `rota skills install`, `rota init`, `rota doctor`, `/rota-init` | new-project |
| 3 | [capture](episodes/03-capture.md) | Capture work as issues | Turn a brain dump into backlog items without starting work | new users | 60–120 s | `/rota-capture`, `rota item create`, `rota backlog list`, `rota summary` | small-item |
| 4 | [small-fix](episodes/04-small-fix.md) | Fix a small bug end to end | Work one item with a red then green proof, then review and ship it | new users | 60–120 s | `/rota-work`, `rota proof record`, `rota proof show`, `/rota-ship` | small-item |
| 5 | [design-before-code](episodes/05-design-before-code.md) | Design before code | Settle a design with a brainstorm, a plan or a spike before any code | new users | 60–120 s | `/rota-brainstorm`, `/rota-plan`, `/rota-spike`, `rota design add`, `list`, `show` | big-feature, risky-idea |
| 6 | [merge-gate](episodes/06-merge-gate.md) | The merge gate and review verdicts | Show how a PR is checked and merged, or sent back | new users | 60–120 s | `rota gate list`, `rota verdict show`, `rota test run`, `rota worker gate` | big-feature |
| 7 | [parallel-round](episodes/07-parallel-round.md) | Run several issues in parallel | Run a round: an orchestrator, workers in worktrees, one PR each | new users | 60–120 s | `/rota-answer`, `rota round start`, `candidates`, `assign`, `status`, `wind-down`, `rota worker gate` | parallel-round |
| 8 | [memory](episodes/08-memory.md) | Memory that stays in the repo | Keep lessons, decisions and handoff notes so the next session starts ahead | new users | 60–120 s | `/rota-learn`, `/rota-decide`, `/rota-pause`, `/rota-work`, `rota knowledge topics`, `query`, `rota decisions topics`, `query` | end-of-session |

Chains are those in [`docs/skills/README.md`](../docs/skills/README.md); the commands are the ones in the current binary.

## Files

```text
videos/
  README.md                 this outline and the shot grammar
  STYLE.md                  look, voice, formats and size budget
  episodes/NN-slug.md       one script per episode
```

## Script format

A script is data. The render pipeline reads the voiceover text and the exact commands straight from it, and a drift check runs those commands against the binary, so a renamed flag fails CI rather than a published video.

### Front matter

Inside the subset `scripts/fmlite.py` parses (CI has no PyYAML): bare or double-quoted scalars, no comments. Quote anything that looks like a number or a date, such as the version.

| Key | Meaning |
|---|---|
| `number` | episode number |
| `slug` | file slug, matches the filename |
| `title` | episode title |
| `length` | target length in seconds |
| `audience` | `evaluators` or `new users` |
| `rota` | the rota version the commands were checked against, quoted |
| `chain` | the chain id from `docs/skills/README.md` it follows |

### Shots

The body is an ordered shot list. Each shot starts with `### NN motion` or `### NN terminal` (two-digit, in order), followed by these lines, each as a `key: value` paragraph:

| Line | Shots | Required | Meaning |
|---|---|---|---|
| `vo:` | all | yes | The exact voiceover text, one paragraph, written to be read aloud. Write the name as `Ro-Ta` so the voice says it with a hard T; captions render it as `rota`. |
| `scene:` | motion | yes | What is on screen, naming guide elements (palette tokens, fonts, the outro card). |
| fenced `sh` block | terminal | yes | The exact commands, one per line. Run in order in a scratch project (see below). |
| `vertical:` | all | yes | How the shot reframes for 9:16. |
| `lower-third:` | all | no | `<mono text> \| <gloss>`. |
| `crop:` | terminal | no | Which output lines to keep when the capture is longer than the 9:16 box (14 lines). |
| `staged:` | terminal | no | Names the fixture the shot needs (a live round, a forge, an agent session). The shot is not run by the drift check; the fixture is built in #2536. |

Voiceover text across an episode stays within its length: about 150 words per minute, less the 5.5 s of sting and outro.

### Running a script

Every terminal block that carries no `staged:` line runs with exit 0, line by line, in a scratch project: `git init` in a temp dir with `HOME` and `XDG_CONFIG_HOME` pointed at a temp dir. That is also how a script is checked when written. Output is captured separately from rendering; the `rota version` build stamp is normalised before comparing.

## Status

Scripts for episodes 1 to 8 are written. Episode 1 is final. No media has been rendered; rendering, the TTS engine and hosting are #2536 and the per-episode items after it.

## Staged fixtures

Terminal shots with a `staged:` line are not run by the drift check. #2536 builds these fixtures.

| Fixture | Episode, shot | Needs |
|---|---|---|
| `branch-with-verdicts` | 6, 03 | pushed branch with recorded `review-spec` and `review-quality` PASS verdicts at HEAD |
| `gate-pass-slot` | 6, 04 | round slot `ben`, open PR, passing `test.fast` and `test.full`, PASS verdict at HEAD, merge approval given; forge |
| `round-slate` | 7, 03 | three ready, non-overlapping open issues #59 to #61; forge |
| `live-round` | 7, 04 | started round, slots ben, dana, nia, one issue each, one PR open; forge and host session |
| `round-merged` | 7, 06 | `live-round` after review passed; `test.full` green on the merged tree; forge |
| `memory-repo` | 8, 04 | `.rota/KNOWLEDGE.md` with a `Timer` bullet, `.rota/DECISIONS.md` with an `Architecture` decision; built with `rota knowledge add` and `rota decisions add` |
