import {test} from 'node:test';
import assert from 'node:assert/strict';
import {activeIndex, appearFrame, enter, fillAt, parseItems} from './sceneTiming.ts';

test('items split on ; and carry an optional ~ detail', () => {
  assert.deepEqual(parseItems('a~x y; b ; c~d~e'), [
    {label: 'a', detail: 'x y'},
    {label: 'b', detail: ''},
    {label: 'c', detail: 'd~e'},
  ]);
  assert.deepEqual(parseItems(undefined), []);
});

test('items enter in order, spread across the voiceover and finish inside it', () => {
  const vo = 200;
  const at = [0, 1, 2, 3].map((i) => appearFrame(i, 4, vo));
  assert.deepEqual([...at].sort((a, b) => a - b), at);
  assert.ok(at[0] < at[1] && at[2] < at[3]);
  assert.ok(at[3] < vo);
});

test('the active step follows the voice and never runs ahead of its entrance', () => {
  const vo = 300;
  assert.equal(activeIndex(0, 3, vo), -1);
  assert.equal(activeIndex(vo, 3, vo), 2);
  for (let f = 0; f < vo; f++) assert.ok(activeIndex(f, 3, vo) <= activeIndex(f + 1, 3, vo));
});

test('an entrance is a 280 ms fade with a 16 px slide, nothing before or after', () => {
  assert.deepEqual(enter(5, 10), {opacity: 0, dy: 16});
  assert.deepEqual(enter(18, 10), {opacity: 1, dy: 0});
  const mid = enter(14, 10);
  assert.ok(mid.opacity > 0 && mid.opacity < 1 && mid.dy > 0 && mid.dy < 16);
});

test('a lane fills linearly between its start and end frame and clamps', () => {
  assert.equal(fillAt(0, 10, 20), 0);
  assert.equal(fillAt(15, 10, 20), 0.5);
  assert.equal(fillAt(99, 10, 20), 1);
});
