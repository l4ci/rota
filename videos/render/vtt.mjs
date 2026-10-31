// WebVTT captions from the same inputs as the burned-in captions: node vtt.mjs props.json
import {readFileSync} from 'node:fs';
import {FPS, INTRO_FRAMES, buildTimeline, captionChunks} from './src/timeline.ts';

const props = JSON.parse(readFileSync(process.argv[2], 'utf8'));
const tl = buildTimeline(props.script.shots, props.captures, props.clips);
const ts = (f) => {
  const ms = Math.round((f / FPS) * 1000);
  const p = (n, w = 2) => String(n).padStart(w, '0');
  return `${p(Math.floor(ms / 3600000))}:${p(Math.floor(ms / 60000) % 60)}:${p(Math.floor(ms / 1000) % 60)}.${p(ms % 1000, 3)}`;
};
let out = 'WEBVTT\n\n';
for (const s of tl.shots) {
  const chunks = captionChunks(s.caption);
  const total = chunks.reduce((a, c) => a + c.length, 0);
  let acc = 0;
  for (const c of chunks) {
    const from = s.start + Math.round((acc / total) * s.voFrames);
    acc += c.length;
    out += `${ts(from)} --> ${ts(s.start + Math.round((acc / total) * s.voFrames))}\n${c}\n\n`;
  }
}
process.stdout.write(out);
