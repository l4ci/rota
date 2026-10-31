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

A script is data. The render pipeline reads the voiceover text and the exact commands straight from it, and `python3 scripts/videos.py --check` runs those commands against the binary in CI, so a renamed flag fails the lint rather than a published video.

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
| `chain` | the chain id from `docs/skills/README.md` it follows, or a flow list `[id, id]` when it follows two |

### Shots

The body is an ordered shot list. Each shot starts with `### NN motion` or `### NN terminal` (two-digit, in order), followed by these lines, each as a `key: value` paragraph:

| Line | Shots | Required | Meaning |
|---|---|---|---|
| `vo:` | all | yes | The exact voiceover text, one paragraph, written to be read aloud. Written as shown on screen: captions come from it, and the name is plain `rota`. The pipeline's lexicon (#2536) maps it to "Ro-Ta" with a hard T. |
| `say:` | all | no | The spoken form when it differs from `vo:`, such as a URL or `.rota` ("dot rota"). Audio comes from `say:` when present, otherwise from `vo:`. |
| `time:` | all | no | Estimated seconds for the shot (voiceover at 150 wpm, 0.35 s per sentence pause, 0.8 s hold). Episode 1 states it per shot with a total, to show it fits 60 s. |
| `scene:` | motion | yes | What is on screen, naming guide elements (palette tokens, fonts, the outro card). |
| fenced `sh` block | terminal | yes | The exact commands, one per line. Run in order in a scratch project (see below). |
| `vertical:` | all | yes | How the shot reframes for 9:16. |
| `lower-third:` | all | no | `<mono text> \| <gloss>`. |
| `crop:` | terminal | no | Which output lines to keep when the capture is longer than the 9:16 box (14 lines). |
| `staged:` | terminal | no | Names the fixture the shot needs (a live round, a forge, an agent session). The shot is not run by the drift check; its output comes from `videos/staged/<fixture>.txt` when that file exists. |

Voiceover text across an episode stays within its length: about 150 words per minute, less the 5.5 s of sting and outro.

### Running a script

Every terminal block that carries no `staged:` line runs with exit 0, line by line, in a scratch project: `git init` in a temp dir with `HOME` and `XDG_CONFIG_HOME` pointed at a temp dir. That is also how a script is checked when written. Output is captured separately from rendering; the `rota version` build stamp is normalised before comparing.

## Rendering

`videos/render.sh` builds an episode from its script in `videos/episodes/NN-slug.md` (format above). Everything shown or said is read from the script, so a change to rota or to a script means one re-render, not a re-recording.

### Set up once

Needs Node 20+, Python 3.10+, ffmpeg and (for a default `$ROTA_BIN`) Go. `videos/render.sh <episode> --setup` installs the rest:

- `npm ci` in `videos/render/` (Remotion 4.0.539, pinned by `package-lock.json`; it fetches its own headless Chrome on the first render);
- a Python venv at `videos/render/.venv` with `kokoro-onnx` (CPU, no torch);
- the Kokoro int8 model and voices (about 120 MB) in `videos/render/models/`.

All three are gitignored. Git LFS is needed to pull or commit the rendered `.mp4` files (`.gitattributes`).

### Render one episode, or all

```sh
videos/render.sh 01-why-rota      # one episode
videos/render.sh all              # every script in videos/episodes/
```

`ROTA_BIN=/path/to/rota` picks the binary the terminal shots run (default: built from this checkout). `RENDER_CONCURRENCY` sets Chrome tabs (default 2; machine memory is shared, keep it low).

Per episode it writes to `videos/out/`: `NN-slug.mp4` (1920x1080), `NN-slug.vertical.mp4` (1080x1920), `NN-slug.vtt`, and an entry in `manifest.json` (rota version, hash of the script, hash of each output). It also rewrites `videos/snapshots/NN-slug.json`. Commit the `out/` files and the snapshot together.

The stages, each cached:

1. **Capture.** `scripts/videos.py` runs each terminal shot's `sh` lines, in order, in a throwaway git project with `HOME`, `XDG_CONFIG_HOME`, `TZ` and the git identity pinned and every `ROTA_*`/`GIT_*` variable dropped. The `rota version` commit and build time are masked as `(build)`; nothing else is.
2. **Voice.** Kokoro speaks each shot's `say:` line, else its `vo:` line. Audio is cached in `videos/.cache/audio/` by text, voice and lexicon. `voice/lexicon.json` makes "rota" "Ro-Ta" with a hard T; check it with `videos/render/.venv/bin/python videos/render/voice/synth.py --phonemes rota`.
3. **Compose.** Remotion renders the shared intro, shots, captions and outro. Shot length is the voiceover length (plus a tail) or the terminal animation, whichever is longer. Typing is a function of the frame number, so the same sources and rota version give the same bytes.

`render.sh` fails when a file is over the per-episode size budget (the Size budget in [`STYLE.md`](STYLE.md); override with `VIDEO_BUDGET_KB_16x9` and `VIDEO_BUDGET_KB_9x16`).

`crop:` (the "first/last N lines" forms) and `lower-third:` are applied; a `crop:` in other words crops nothing and the bottom-anchored terminal scrolls. Motion shots render the shot's caption text as a title card. A designed scene for a shot is a component in `videos/render/src/` that `Episode.tsx` picks by shot.

### Is anything stale?

```sh
python3 scripts/videos.py --check
```

It re-captures every non-staged terminal shot against `$ROTA_BIN` and names each episode whose command failed, whose output differs from `videos/snapshots/`, or whose script hash differs from `videos/out/manifest.json`. An episode that has not been rendered yet is listed as "not rendered yet", not as stale, so CI stays green until the first render. It needs only Python and the binary, so `test/doclint.sh` runs it in CI. CI never renders: the maintainer re-renders locally, and `/rota-release` reminds them.

### Add an episode

1. Write `videos/episodes/NN-slug.md` (front matter, then `### NN motion|terminal` shots with `vo:`, an `sh` block for terminal shots, and `vertical:`). Add `say:` where the spoken form differs from the caption.
2. `videos/render.sh NN-slug`, then watch both cuts.
3. `python3 scripts/videos.py --check` must pass. Commit the script, snapshot, manifest, `.mp4` and `.vtt`.

A shot that cannot run in a scratch project carries `staged:`; the drift check skips it.

### Pipeline checks

- `node --experimental-strip-types --test videos/render/src/*.test.ts` covers the terminal window, typing and caption timing.
- `bash test/videos-drift.sh` covers the drift check (also part of `test/doclint.sh`).
- `videos/render.sh 00-fixture` renders `render/fixtures/00-fixture.md`, a five-second episode used to try the pipeline.

## Status

Scripts for episodes 1 to 8 are written. Episode 1 is final. The render pipeline (below) exists; no episode has been rendered yet.

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
