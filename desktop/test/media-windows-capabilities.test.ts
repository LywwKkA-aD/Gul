import assert from 'node:assert/strict';
import test from 'node:test';
import { captureCapabilities, CaptureChooser } from '../src/main/capture-policy.ts';

test('Windows audio capabilities depend on actual excluded-process activation, including Windows 10', () => {
  const supported = captureCapabilities('win32', '10.0.19045', false, false, 'application', true);
  assert.equal(supported.systemAudio, true);
  assert.equal(supported.ownAudioExcluded, true);
  for (const version of ['10.0.19045', '10.0.22631']) {
    const unavailable = captureCapabilities('win32', version, false, false, 'application', false);
    assert.equal(unavailable.systemAudio, false);
    assert.equal(unavailable.ownAudioExcluded, false);
  }
});
test('a refused native exclusion probe never grants whole-system loopback as fallback', async () => {
  const selected = { id: 'screen:1:0', name: 'Screen' };
  const result = await new CaptureChooser().choose({
    valid: () => true,
    getSources: async () => [selected],
    pick: async () => ({ response: 1, checkboxChecked: true }),
    capabilities: captureCapabilities('win32', '10.0.19045', false, false, 'application', false),
    audioRequested: true,
    loopbackAudio: false,
  });
  assert.deepEqual(result, { video: selected });
});
