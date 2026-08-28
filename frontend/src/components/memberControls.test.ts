import test from 'node:test';
import assert from 'node:assert/strict';

import { canAdjust } from './memberControls.ts';

// The regression, stated as the thing a user would notice: after the tunnel
// contract every session went anonymous, so every hash arrived empty, and the
// old rule hid the slider and the mute button from everybody at once.
test('a peer with no certificate still gets volume and mute', () => {
  assert.equal(canAdjust({ isSelf: false, key: 's:7' }), true);
});

test('a peer with a certificate gets them too', () => {
  assert.equal(canAdjust({ isSelf: false, key: 'h:abc123' }), true);
});

// Our own stream never comes back to us, so there is nothing to turn down.
test('our own row has nothing to adjust', () => {
  assert.equal(canAdjust({ isSelf: true, key: 'h:abc123' }), false);
});

// A key the client could not form at all is not a person it can remember.
test('a row without a key offers nothing', () => {
  assert.equal(canAdjust({ isSelf: false, key: '' }), false);
});
