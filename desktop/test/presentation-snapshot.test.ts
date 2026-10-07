import test from 'node:test';
import assert from 'node:assert/strict';
import { initialSnapshot } from '../src/renderer/media/model.ts';
import { presentationSnapshot } from '../src/renderer/presentation-snapshot.ts';

test('meter-only updates keep the main view stable; controls and tracks are current', () => {
  let snapshot = initialSnapshot();
  const read = presentationSnapshot(() => snapshot);
  const first = read();
  snapshot = { ...snapshot, micLevel: 0.6, voiceActive: true };
  assert.equal(read(), first);
  snapshot = { ...snapshot, muted: true };
  assert.equal(read(), snapshot);
  assert.equal(read().micLevel, 0.6);
  const muted = read();
  snapshot = { ...snapshot, micLevel: 0 };
  assert.equal(read(), muted);
  snapshot = {
    ...snapshot,
    screens: [
      {
        identity: 'screen.1',
        ownerIdentity: 'voice.1',
        name: 'peer',
        videoSid: 'video',
        watching: false,
        local: false,
        state: 'available',
      },
    ],
  };
  assert.equal(read(), snapshot);
});
