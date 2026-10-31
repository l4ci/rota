# Rendering the video series

`videos/render.sh` builds an episode from its script in `videos/episodes/NN-slug.md` (format: [`../README.md`](../README.md)). Everything shown or said is read from the script, so a change to rota or to a script means one re-render, not a re-recording.

## Set up once

Needs Node 20+, Python 3.10+, ffmpeg and (for a default `$ROTA_BIN`) Go. `videos/render.sh <episode> --setup` installs the rest:

- `npm ci` in `videos/render/` (Remotion 4.0.539, pinned by `package-lock.json`; it fetches its own headless Chrome on the first render);
- a Python venv at `videos/render/.venv` with `kokoro-onnx` (CPU, no torch);
- the Kokoro int8 model and voices (about 120 MB) in `videos/render/models/`.

All three are gitignored. Git LFS is needed to pull or commit the rendered `.mp4` files (`.gitattributes`).

## Render one episode, or all

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

`render.sh` fails when a file is over the per-episode size budget (12 MB for 16:9, 9 MB for 9:16; override with `VIDEO_BUDGET_KB_16x9` and `VIDEO_BUDGET_KB_9x16`).

Motion shots render the shot's caption text as a title card. A designed scene for a shot is a component in `videos/render/src/` that `Episode.tsx` picks by shot.

## Is anything stale?

```sh
python3 scripts/videos.py --check
```

It re-captures every non-staged terminal shot against `$ROTA_BIN` and names each episode whose command failed, whose output differs from `videos/snapshots/`, or whose script hash differs from `videos/out/manifest.json`. It needs only Python and the binary, so `test/doclint.sh` runs it in CI. CI never renders: the maintainer re-renders locally, and `/rota-release` reminds them.

## Add an episode

1. Write `videos/episodes/NN-slug.md` (front matter, then `### NN motion|terminal` shots with `vo:`, an `sh` block for terminal shots, and `vertical:`). Add `say:` where the spoken form differs from the caption.
2. `videos/render.sh NN-slug`, then watch both cuts.
3. `python3 scripts/videos.py --check` must pass. Commit the script, snapshot, manifest, `.mp4` and `.vtt`.

A shot that cannot run in a scratch project carries `staged:`; the drift check skips it.

## Pipeline checks

- `node --experimental-strip-types --test videos/render/src/*.test.ts` covers the terminal window, typing and caption timing.
- `bash test/videos-drift.sh` covers the drift check (also part of `test/doclint.sh`).
- `videos/render.sh 00-fixture` renders `fixtures/00-fixture.md`, a five-second episode used to try the pipeline.
