import test from 'node:test';
import assert from 'node:assert/strict';
import { needsPttReseed } from './pttSeed.ts';

// The first look at the connection has nothing to compare against, and a
// window opened onto a live session has to ask what the gate is doing.
test('the first connected state is read back from the engine', () => {
  assert.equal(needsPttReseed(null, 'connected'), true);
});

test('a session that has not come up yet has nothing to ask about', () => {
  assert.equal(needsPttReseed(null, 'connecting'), false);
  assert.equal(needsPttReseed(null, 'disconnected'), false);
  assert.equal(needsPttReseed('connecting', 'reconnecting'), false);
});

// The store clears pttHeld on the way down, and the engine keeps transmitting
// a key that was never released - so the way back up is exactly the moment the
// two have to be compared again.
test('coming back from a drop re-reads the transmit state', () => {
  assert.equal(needsPttReseed('reconnecting', 'connected'), true);
  assert.equal(needsPttReseed('disconnected', 'connected'), true);
  assert.equal(needsPttReseed('connecting', 'connected'), true);
});

// A repeated connection:state event (the tree or the self channel changed, not
// the connection) must not turn into a round trip on every push.
test('staying connected asks nothing', () => {
  assert.equal(needsPttReseed('connected', 'connected'), false);
});

test('going down asks nothing: the store already cleared the indicator', () => {
  assert.equal(needsPttReseed('connected', 'reconnecting'), false);
  assert.equal(needsPttReseed('connected', 'disconnected'), false);
});
