import test from 'node:test';
import assert from 'node:assert/strict';
import { voiceGatesOf } from './voiceGatesRule.ts';

test('other people are drawn from the tree, which is all there is to go on', () => {
  assert.deepEqual(voiceGatesOf(false, true, false, false, false), { muted: true, deaf: false });
  assert.deepEqual(voiceGatesOf(false, true, true, false, false), { muted: true, deaf: true });
});

// The whole point. Between the click and the server's answer the tree still
// carries the previous pair for our own row, and drawing it there put a mute
// glyph on a user who was not muted - and left a muted user with none.
test('our own row is drawn from core even while the tree still disagrees', () => {
  assert.deepEqual(voiceGatesOf(true, false, false, true, false), { muted: true, deaf: false });
  assert.deepEqual(voiceGatesOf(true, true, true, false, false), { muted: false, deaf: false });
});

test('and it agrees with the tree once the tree has caught up', () => {
  assert.deepEqual(voiceGatesOf(true, true, true, true, true), { muted: true, deaf: true });
});
