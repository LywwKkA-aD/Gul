import test from 'node:test';
import assert from 'node:assert/strict';
import { showsMainScreen } from './screen.ts';

// The connect form is the only screen that renders status.error, and the main
// screen locks itself while reconnecting. A state that never had a session
// must therefore keep the form: sending 'connecting' to the main screen strands
// the user behind a dimmed session that does not exist, with no message.
test('the main screen belongs to states that had a session', () => {
  assert.equal(showsMainScreen('connected'), true);
  assert.equal(showsMainScreen('reconnecting'), true);
});

test('an attempt that has not connected stays on the connect form', () => {
  assert.equal(showsMainScreen('connecting'), false);
  assert.equal(showsMainScreen('disconnected'), false);
});
