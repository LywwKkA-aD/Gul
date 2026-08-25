import { test } from 'node:test';
import assert from 'node:assert/strict';

import { voiceGlyph } from './voiceState.ts';

test('a member with both gates open shows nothing', () => {
  assert.equal(voiceGlyph(false, false), 'none');
});

test('a muted member shows the microphone', () => {
  assert.equal(voiceGlyph(true, false), 'muted');
});

// Deafened implies muted on the wire and in the bottom bar, so one glyph
// carries both facts - two would say the same thing twice.
test('a deafened member shows one glyph, the output gate', () => {
  assert.equal(voiceGlyph(true, true), 'deaf');
  assert.equal(voiceGlyph(false, true), 'deaf');
});
