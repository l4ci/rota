---
name: video-render-stack
branch: spike/video-render-stack
status: done
finished: 2026-10-31
created: 2026-10-31
---

# spike/video-render-stack

## Question

Can Kokoro (local CPU TTS) and Remotion (headless Chrome) render a 5s captioned clip that types captured rota output in the site style, as 16:9 and 9:16, on this machine within ~3 GB RAM and a few minutes?

## What was tried

Reference sources are on `spike/video-render-stack` under `spikes/video-render-stack/`:
- `capture.sh` runs `rota --version`, `rota init` and `rota --help | head -15` in a temp git project with HOME and XDG_CONFIG_HOME isolated, and writes `capture.json`.
- `synth.py` synthesises one voiceover line with `kokoro-onnx` 0.6.1 (int8 model, voice `af_heart`).
- A minimal Remotion 4.0.539 project (`src/`, `remotion.config.ts`) has 16:9 and 9:16 compositions that type the capture frame by frame, with a caption line and the audio.
- `render.sh` renders both formats with `npx remotion render` at concurrency 2 and 4, measured with `/usr/bin/time -v` and sampled `free -m`.

## Findings

- **Kokoro runs comfortably on CPU.** The `kokoro-onnx` install is 187 MB for the venv plus 116 MB of models. It synthesised 3.75 s of 24 kHz audio in 3.2 s at 289 MB peak RSS, with no torch needed.
- **Remotion renders both formats fast.**
  - A cold 16:9 render took 27 s; warm 16:9 and 9:16 renders took about 9 s each, for a 4.25 s clip.
  - Rendering added about 2.1 GB of system memory cold and 0.4 to 1.2 GB warm, under the 3 GB budget.
  - Output is an h264 mp4 of 1.1 MB (16:9) or 613 KB (9:16).
  - The install is 718 MB, about 250 MB of it Chrome headless shell.
- **Renders are byte-identical.** Three 16:9 renders, at concurrency 2 and 4, gave the same sha256, so reproducibility (#2536 AC-2) holds.
- **Captured output is stable.** Two runs gave identical output, with no ANSI codes, timestamps or temp paths, except the `rota --version` line, which carries commit and build time. The drift check needs a normaliser for it.
- **Remotion 4.0.250 is broken here.** Its Chrome headless shell segfaults (exit 139), so pin 4.0.539 or newer. Martian Mono must be bundled as a static font asset, since the spike fell back to Liberation Mono. Long captures need cropping or a smaller font (a 56-line capture overflowed).

## Decision

viable

## Recommended approach

Build the #2536 pipeline as designed.

**Toolchain**
- `kokoro-onnx` in a pinned venv for the voice.
- Remotion ≥4.0.539 pinned in `videos/render/package.json`, with the Chrome headless shell fetched by Remotion.
- Martian Mono and Atkinson Hyperlegible Next bundled as static assets.

**Timing**
- Shot length comes from the synthesised clip length. Typing is a pure function of the frame number, so renders stay deterministic.

**Capture**
- Capture runs separately from rendering, in a temp project with an isolated environment.
- It normalises the `rota --version` build stamp before comparing snapshots, so the CI drift check needs only Python and the binary.
- Terminal shots keep each capture short enough for the 9:16 box, or the script crops it explicitly.

**Rendering**
- Default to concurrency 2: it was as fast as 4 on short clips and uses less memory.
- Output as mp4 (h264/aac); webm is optional.
