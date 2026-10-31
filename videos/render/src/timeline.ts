// Pure timing and layout: every frame is a function of the frame number, so a
// render is deterministic. No React in here, so node --test can run it.

export const FPS = 30;
export const INTRO_FRAMES = 45; // 1.5 s logo sting
export const OUTRO_FRAMES = 120; // 4 s outro card
export const TAIL_FRAMES = 12; // 0.4 s of air after the voiceover
export const HOLD_FRAMES = 24; // a shot holds its last state 0.8 s
const TYPE_CPS = 28;
const BEAT_FRAMES = 12; // 0.4 s between typed command and output
const LINE_FRAMES = 4; // 120 ms per output line
const ALL_AT_ONCE_OVER = 12; // more output lines than this print at once
export const OUTRO_VO = 'Find it at github dot com slash L four C I slash Ro-Ta.';

export type Capture = {shot: string; cmd: string; stdout: string; exit: number};
export type Shot = {
  n: string;
  kind: 'motion' | 'terminal';
  vo: string;
  caption: string;
  scene: string | null;
  lower_third: string | null;
  cmds: string[];
};
export type Clip = {n: string; file: string; seconds: number};

export type Row = {text: string; appear: number; typedFrom?: number; typedChars?: number};

/** Rows of a terminal shot: each command typed at 28 cps, then its output after a beat. */
export function layoutTerminal(cmds: string[], captures: Capture[]): {rows: Row[]; end: number} {
  const rows: Row[] = [];
  let t = 0;
  for (const cmd of cmds) {
    const typeFrames = Math.ceil((cmd.length / TYPE_CPS) * FPS);
    rows.push({text: '$ ' + cmd, appear: t, typedFrom: t, typedChars: cmd.length});
    t += typeFrames + BEAT_FRAMES;
    const cap = captures.find((c) => c.cmd === cmd);
    const out = cap ? cap.stdout.replace(/\n+$/, '').split('\n').filter((_, i, a) => !(a.length === 1 && a[0] === '')) : [];
    const allAtOnce = out.length > ALL_AT_ONCE_OVER;
    out.forEach((line, i) => rows.push({text: line, appear: allAtOnce ? t : t + i * LINE_FRAMES}));
    t += (allAtOnce ? 0 : out.length * LINE_FRAMES) + BEAT_FRAMES;
  }
  return {rows, end: t};
}

/** Lines on screen at `frame`; a command being typed shows a prefix of its text. */
export function linesAt(rows: Row[], frame: number): string[] {
  const out: string[] = [];
  for (const r of rows) {
    if (frame < r.appear) break;
    if (r.typedFrom !== undefined) {
      const n = Math.min(r.typedChars!, Math.floor(((frame - r.typedFrom) / FPS) * TYPE_CPS) + 1);
      out.push('$ ' + r.text.slice(2, 2 + n));
    } else out.push(r.text);
  }
  return out;
}

/** Hard-wrap to the box width; a wrapped line stays in order. */
export function wrapLines(lines: string[], cols: number): string[] {
  const out: string[] = [];
  for (const l of lines) {
    if (l.length <= cols) out.push(l);
    else for (let i = 0; i < l.length; i += cols) out.push(l.slice(i, i + cols));
  }
  return out;
}

/** Bottom-anchored viewport: the newest line is always the last visible one. */
export function visibleWindow<T>(lines: T[], height: number): T[] {
  return height >= lines.length ? lines : lines.slice(lines.length - height);
}

export type PlacedShot = Shot & {start: number; frames: number; voFrames: number; audio: string; rows: Row[]};

export function buildTimeline(shots: Shot[], captures: Capture[], clips: Clip[]) {
  let start = INTRO_FRAMES;
  const placed: PlacedShot[] = shots.map((s) => {
    const clip = clips.find((c) => c.n === s.n);
    if (!clip) throw new Error(`no voiceover clip for shot ${s.n}`);
    const voFrames = Math.ceil(clip.seconds * FPS);
    const term = s.kind === 'terminal' ? layoutTerminal(s.cmds, captures.filter((c) => c.shot === s.n)) : {rows: [], end: 0};
    const frames = Math.max(voFrames + TAIL_FRAMES, term.end + HOLD_FRAMES);
    const p = {...s, start, frames, voFrames, audio: clip.file, rows: term.rows};
    start += frames;
    return p;
  });
  return {shots: placed, outroStart: start, total: start + OUTRO_FRAMES};
}

/** Split a caption into phrases of at most `lines` lines of `cols` characters. */
export function captionChunks(text: string, cols = 42, lines = 2): string[] {
  const words = text.split(/\s+/).filter(Boolean);
  const chunks: string[] = [];
  let cur: string[] = [];
  const fits = (ws: string[]) => wrapWords(ws, cols).length <= lines;
  for (const w of words) {
    if (cur.length && !fits([...cur, w])) {
      chunks.push(cur.join(' '));
      cur = [];
    }
    cur.push(w);
    if (/[.!?]$/.test(w)) {
      chunks.push(cur.join(' '));
      cur = [];
    }
  }
  if (cur.length) chunks.push(cur.join(' '));
  return chunks;
}

export function wrapWords(words: string[], cols: number): string[] {
  const out: string[] = [];
  let line = '';
  for (const w of words) {
    if (line && (line + ' ' + w).length > cols) {
      out.push(line);
      line = w;
    } else line = line ? line + ' ' + w : w;
  }
  if (line) out.push(line);
  return out;
}

/** Which chunk shows at `frame` of a voiceover `voFrames` long (time share by characters). */
export function captionAt(chunks: string[], voFrames: number, frame: number): string | null {
  if (frame < 0 || frame >= voFrames + TAIL_FRAMES) return null;
  const total = chunks.reduce((a, c) => a + c.length, 0);
  let acc = 0;
  for (const c of chunks) {
    acc += c.length;
    if (frame < (acc / total) * voFrames) return c;
  }
  return chunks[chunks.length - 1] ?? null;
}
