// Pure timing for the animated motion scenes (scenes.tsx): which item is on screen
// or active at a frame of a shot whose voiceover is `voFrames` long. No React, so
// node --test can run it. Everything is a function of the frame number.

export type Anim = {name: string; params: Record<string, string>};
export type Item = {label: string; detail: string};

/** `a~detail; b; c~detail` -> items. `~` splits a label from its detail line. */
export function parseItems(v: string | undefined): Item[] {
  return (v ?? '')
    .split(';')
    .map((s) => s.trim())
    .filter(Boolean)
    .map((s) => {
      const [label, ...d] = s.split('~');
      return {label: label.trim(), detail: d.join('~').trim()};
    });
}

const FIRST = 6; // frames before the first item appears
const SPAN = 0.78; // the last item lands this share of the way through the voiceover

/** Frame at which item `i` of `n` enters, spread across the voiceover so the build follows the voice. */
export function appearFrame(i: number, n: number, voFrames: number, from = 0, to = SPAN): number {
  if (n <= 1) return FIRST + Math.round(from * voFrames);
  const a = FIRST + from * voFrames;
  const b = to * voFrames;
  return Math.round(a + (i / (n - 1)) * Math.max(0, b - a));
}

/** Index of the last item that has entered at `frame`, or -1. */
export function activeIndex(frame: number, n: number, voFrames: number, from = 0, to = SPAN): number {
  let k = -1;
  for (let i = 0; i < n; i++) if (frame >= appearFrame(i, n, voFrames, from, to)) k = i;
  return k;
}

const ENTER_FRAMES = 8; // 280 ms
/** Entrance at frame `at`: opacity 0 -> 1 and a 16 px slide, ease-out cubic (STYLE.md). */
export function enter(frame: number, at: number): {opacity: number; dy: number} {
  const t = Math.min(1, Math.max(0, (frame - at) / ENTER_FRAMES));
  const e = 1 - Math.pow(1 - t, 3);
  return {opacity: e, dy: (1 - e) * 16};
}

/** Share (0..1) of a lane filled at `frame`, linear between its start and end frame. */
export function fillAt(frame: number, start: number, end: number): number {
  if (end <= start) return frame >= end ? 1 : 0;
  return Math.min(1, Math.max(0, (frame - start) / (end - start)));
}
