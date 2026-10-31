# Video series: look and feel

The guide every episode in [`README.md`](README.md) follows. It states the target look and voice, not tool settings: the motion tool, TTS engine and render settings live with the pipeline (#2536). The identity is the public site's, reused as is (`rota/assets/rota.css` in `l4ci/l4ci.github.io`), so a video next to the site reads as one product.

## Formats

| | Master | Vertical cut |
|---|---|---|
| Aspect | 16:9 | 9:16 |
| Resolution | 1920×1080 | 1080×1920 |
| Frame rate | 30 fps | 30 fps |
| Codec | h264 / aac, mp4 | h264 / aac, mp4 |
| Audio | 24 kHz voiceover, mono, -16 LUFS, no music bed | same |

Every episode ships both. The vertical cut is a reframe of the same shots, never a crop of the master: each shot's `vertical:` line in the script says how it recomposes. Safe area: keep 64 px clear on every edge (96 px at the bottom of the 9:16 cut, where platform UI sits).

Length targets: episode 1 at 60 s or less, episodes 2 to 8 at 60 to 120 s, counted from the first frame of the logo sting to the last frame of the outro.

## Palette

The site's dark theme. Backgrounds are flat; no gradients, no glow, no drop shadows.

| Token | Hex | Use |
|---|---|---|
| `paper` | `#0D1117` | frame background |
| `ink` | `#F0F6FC` | titles, terminal text, captions |
| `rule` | `#30363D` | borders, dividers, terminal frame |
| `muted` | `#9198A1` | prompts, secondary text, comments in a terminal |
| `duty` | `#7C8CFF` | the primary accent: the thing the shot is about, active step |
| `duty-wash` | `#1B2148` | highlight band behind a `duty` item |
| `merged` | `#3FB950` | pass, merged, done |
| `merged-wash` | `#12301C` | band behind a `merged` item |
| `ask` | `#D29922` | a decision waiting on a human, a warning |
| `ask-wash` | `#33270C` | band behind an `ask` item |
| `danger` | `#FF7B72` | a red result, a failure |

State colours carry meaning and keep it across episodes: `duty` is what is being worked on, `merged` is what passed, `ask` is what needs the maintainer. Do not use them as decoration. At most one accent colour dominates a shot.

Radius is 3 px on every frame, card and band. No pill shapes.

## Typography

| Role | Font | Notes |
|---|---|---|
| Terminal, code, verbs, IDs | Martian Mono | regular 400; 500 for emphasis, never bold |
| Titles, lower thirds, captions, cards | Atkinson Hyperlegible Next | 600 for titles, 400 for body |

Both are bundled with the pipeline as static assets; a render must not fall back to a system font.

| Element | 16:9 | 9:16 |
|---|---|---|
| Terminal text | 30 px, line height 1.45 | 34 px, line height 1.45 |
| Title card | 96 px | 88 px |
| Caption | 44 px | 52 px |
| Lower third | 36 px | 40 px |

Terminal text is sized so a 60-column line stays legible on a phone: in 9:16 the terminal box is 952 px wide, which fits 40 columns of Martian Mono at 34 px. Captured output wider than the box is a script problem: shorten the command (`| head`, `--json`, a narrower fixture), do not shrink the font.

## Terminal shots

A terminal shot plays a real command and its real output, as data from the script, not as footage.

- **Theme.** `paper` background inside a 1 px `rule` frame with the 3 px radius, a title bar with three `rule`-coloured dots, no window chrome beyond that. Prompt `$ ` in `muted`; typed command in `ink`; output in `ink`; comments in `muted`. A green `merged` or red `danger` token appears only where the real output says pass or fail.
- **Bottom-anchored.** New lines appear at the bottom of the box and older lines scroll up and out of view. The newest line is always visible. The box never anchors to the top, so output cannot be pushed off screen.
- **Typing.** The command types at about 28 characters per second with no jitter, then a 0.4 s beat before output. Output prints line by line at 120 ms per line, or all at once if it exceeds 12 lines. Typing is a pure function of the frame number so renders stay byte-identical.
- **Length.** Keep captured output to 14 lines or fewer so the 9:16 box (14 lines at 34 px) needs no scrolling; longer output scrolls bottom-anchored and the script says which lines to keep with `crop:`.
- **Cursor.** A solid `ink` block, 530 ms blink, only while the shot waits for input.
- **Normalised output.** The pipeline strips the build stamp from `rota version` before comparing snapshots, so a script shows the version line but never depends on its commit or time.

## Motion graphics

For the why and the how: diagrams, flows and one-idea title cards.

- **Easing.** One curve everywhere: ease-out cubic, 280 ms for entrances and 200 ms for exits. Nothing bounces, overshoots or springs.
- **Movement.** Elements enter by fading in with a 16 px translate along their reading direction; they leave by fading out in place. No spins, no zooms, no parallax.
- **Pace.** One idea per shot, 4 to 9 seconds. A shot holds its final state for at least 0.8 s before the cut.
- **Cuts.** Hard cuts between shots, 0 frames. A cross-fade (12 frames) only between a motion shot and the terminal shot that runs the same thing.
- **Diagrams.** Boxes are `rule`-bordered cards with 3 px radius, labels in Atkinson Hyperlegible Next, verbs and IDs in Martian Mono. Arrows are 2 px `muted` lines with a filled head; the active path turns `duty`. Use the state colours for state only.
- **Layout.** Left-aligned, on an 8 px grid, one focal element per frame. 16:9 puts text left and visual right; 9:16 stacks text over visual.

## Captions and lower thirds

- **Captions** are on in every episode, burned in: voiceover text, split at phrases, at most two lines, 42 characters per line, `ink` on a `paper`-coloured band at 85% opacity. They sit in the lower third of the 16:9 frame and above the bottom safe area in 9:16. A caption never covers the last three terminal lines. Verbs and IDs inside a caption render in Martian Mono.
- **Lower thirds** name what is on screen: the skill or verb (`/rota-work`, `rota proof record`) in Martian Mono over a one-line gloss in Atkinson Hyperlegible Next. Left-aligned at the safe-area edge, a 3 px `duty` bar on the left, `duty-wash` band. One per shot at most, in for 280 ms, held 2.5 s, out for 200 ms.

## Intro and outro

Every episode opens with the logo sting and closes with the outro card. Both count toward the length target.

**Intro sting (1.5 s).** On `paper`, the rota icon (`docs/images/rota-icon.svg`) draws in centred: its stroke traces over 0.6 s, then the wheel turns one quarter turn in 0.5 s on the standard curve. The wordmark (`docs/images/rota-logo.svg`) fades in beneath it over 0.3 s. Hold 0.4 s, then a hard cut to shot 01. Silent except for the voiceover's first word starting at 1.5 s. In 9:16 the lockup sits at the optical centre, 40% from the top.

**Outro card (4 s).**

- Line 1, Atkinson Hyperlegible Next 600: `Coding agents write fast.`
- Line 2, same, `duty`: `rota makes sure what they merge works.`
- Link, Martian Mono, `ink` on a `duty-wash` band: `github.com/l4ci/rota`
- The rota icon at 64 px above line 1.

Timing: the card fades in 280 ms; the voiceover reads line 2 only, `rota makes sure what they merge works.`, in about 3 s; the link is on screen, never spoken, and the card holds 0.8 s after the voice ends, 4 s in all. The link is the only element above 44 px apart from line 1, and stays inside the safe area in both cuts: in 9:16 it is set on two lines (`github.com/` over `l4ci/rota`) at 64 px.

## Voice

Generated locally with open-weights TTS (the engine is chosen in #2536; Kokoro was proven in the `video-render-stack` spike).

- **Character.** A calm, clear, plain-spoken narrator. Neutral register, no sales tone, no excitement, no vocal fry. One voice for the whole series, fixed in the pipeline config so every episode matches.
- **Pace.** About 150 words per minute, short sentences, a natural pause at each full stop (0.35 s) and each paragraph break (0.6 s).
- **Pronunciation.** *rota* is **"Ro-Ta"**: two clear syllables, long O, a hard T. Never "roda": American voices flap the T between vowels, and the spike's voice did exactly that. Scripts always write plain `rota`: `vo:` is the caption text. The pipeline's pronunciation lexicon (#2536) maps `rota` to "Ro-Ta" with a hard T before synthesis, and it checks the rendered audio for it on every episode. Say it the same way in every episode, including the outro.
- **Read-outs.** Commands are not read letter by letter. The voiceover says what a command does; the terminal shows what it is. Where a name must be spoken, write it as spoken: "ship" rather than "slash rota dash ship".
- **Text.** Voiceover text is the data. It lives in the script, one `vo:` paragraph per shot, written to be read aloud, and captions come from it. Where the spoken form differs from what should show (a URL, `.rota` read as "dot rota"), the shot adds a `say:` line; audio comes from `say:` when present, otherwise from `vo:`. Numbers under ten are in words.

## Size budget

Rendered media is stored with Git LFS (#2536). Per episode:

| Output | Budget |
|---|---|
| 16:9 mp4, 60 to 120 s | 12 MB or less |
| 16:9 mp4, episode 1 | 8 MB or less |
| 9:16 mp4 | 9 MB or less |
| Captions (`.vtt`) | 20 KB or less |

The whole series, both cuts, stays under 150 MB. Flat colour and a fixed palette keep h264 files small; do not add film grain, gradients or a music bed to buy "polish".

## Accessibility

Captions are burned in and also shipped as a `.vtt` per language. Contrast: `ink` on `paper` is 16:1; `muted` on `paper` is 6:1, the floor for any text. No flash faster than 3 Hz. Motion stays inside the style above, so a viewer who dislikes movement sees fades, not travel.

## Not in the guide

English only; no light theme; no background music; no on-screen presenter. The render stack, voice engine settings and encoder flags are pipeline choices (#2536).
