import { test } from 'node:test';
import assert from 'node:assert/strict';

import { pingTone } from './pingTone.ts';

test('no sample is not a quality', () => {
  assert.equal(pingTone(null), 'none');
});

test('a link a voice call does not notice reads good', () => {
  assert.equal(pingTone(0), 'good');
  assert.equal(pingTone(100), 'good');
});

test('the band where pauses start to show reads usable', () => {
  assert.equal(pingTone(101), 'usable');
  assert.equal(pingTone(250), 'usable');
});

test('past the point where people talk over each other reads bad', () => {
  assert.equal(pingTone(251), 'bad');
  assert.equal(pingTone(2000), 'bad');
});
