import {test} from 'node:test';
import assert from 'node:assert/strict';
import {captionAt, captionChunks, layoutTerminal, linesAt, visibleWindow, wrapLines} from './timeline.ts';

const forty = Array.from({length: 40}, (_, i) => `line ${i + 1}`);

test('the terminal window is bottom-anchored: the last visible line is the newest', () => {
  const w = visibleWindow(forty, 12);
  assert.equal(w.length, 12);
  assert.equal(w[w.length - 1], 'line 40');
  assert.equal(w[0], 'line 29');
});

test('a short transcript is shown whole, oldest first', () => {
  assert.deepEqual(visibleWindow(['a', 'b'], 12), ['a', 'b']);
});

test('after the last line is typed the newest line is visible at every later frame', () => {
  const cap = [{shot: '01', cmd: 'x', stdout: forty.join('\n') + '\n', exit: 0}];
  const {rows, end} = layoutTerminal(['x'], cap);
  for (const f of [end - 1, end, end + 30, end + 300]) {
    const w = visibleWindow(wrapLines(linesAt(rows, f), 80), 12);
    assert.equal(w[w.length - 1], 'line 40', `frame ${f}`);
  }
});

test('output longer than 12 lines prints at once; shorter output prints line by line', () => {
  const long = layoutTerminal(['x'], [{shot: '1', cmd: 'x', stdout: forty.join('\n'), exit: 0}]).rows;
  assert.equal(new Set(long.slice(1).map((r) => r.appear)).size, 1);
  const short = layoutTerminal(['x'], [{shot: '1', cmd: 'x', stdout: 'a\nb\nc\n', exit: 0}]).rows;
  assert.equal(new Set(short.slice(1).map((r) => r.appear)).size, 3);
});

test('typing is a pure function of the frame: a prefix of the command', () => {
  const {rows} = layoutTerminal(['rota init'], []);
  assert.equal(linesAt(rows, 0)[0], '$ r');
  assert.equal(linesAt(rows, 1000)[0], '$ rota init');
  assert.deepEqual(linesAt(rows, 7), linesAt(rows, 7));
});

test('captions split at phrases, two lines of 42 characters at most, and cover the voiceover', () => {
  const text = 'Coding agents write code fast. Merging it safely is the hard part, and that is where rota helps you.';
  const chunks = captionChunks(text);
  assert.ok(chunks.length >= 2);
  for (const c of chunks) assert.ok(c.length <= 84, c);
  assert.equal(chunks.join(' '), text);
  assert.equal(captionAt(chunks, 90, 0), chunks[0]);
  assert.equal(captionAt(chunks, 90, 89), chunks[chunks.length - 1]);
  assert.equal(captionAt(chunks, 90, 500), null);
});
