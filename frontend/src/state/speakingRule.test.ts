import test from 'node:test';
import assert from 'node:assert/strict';
import { isSpeaking } from './speakingRule.ts';

test('a remote peer speaks while the server says so', () => {
  assert.equal(isSpeaking(false, 7, false, new Set([7])), true);
  assert.equal(isSpeaking(false, 7, false, new Set([8])), false);
});

// Our own voice never comes back from the server, so the talking set is not
// the place to look for it - reading it there would leave our own halo dark.
test('our own row follows the transmit gate, not the talking set', () => {
  assert.equal(isSpeaking(true, 7, true, new Set()), true);
  assert.equal(isSpeaking(true, 7, false, new Set([7])), false);
});

test('nobody speaks while nothing is on the wire', () => {
  assert.equal(isSpeaking(false, 1, false, new Set()), false);
  assert.equal(isSpeaking(true, 1, false, new Set()), false);
});
