import assert from 'node:assert/strict';
import test from 'node:test';
import { audioDeviceOptions } from '../src/renderer/audio-device-options.ts';

test('private screen mix cannot be selected as a voice microphone or playback output', () => {
  const devices = Object.freeze([
    { kind: 'audioinput', deviceId: 'default', label: 'Default' },
    { kind: 'audioinput', deviceId: 'microphone', label: 'Headset microphone' },
    { kind: 'audioinput', deviceId: 'private-input', label: 'Gul-Screen-Audio-' + 'a'.repeat(32) },
    { kind: 'audiooutput', deviceId: 'private-output', label: 'Gul-Screen-Audio-' + 'a'.repeat(32) },
    { kind: 'audiooutput', deviceId: 'headset', label: 'Headset' },
  ] as const);
  assert.deepEqual(
    audioDeviceOptions(devices, 'audioinput').map((device) => device.deviceId),
    ['microphone'],
  );
  assert.deepEqual(
    audioDeviceOptions(devices, 'audiooutput').map((device) => device.deviceId),
    ['headset'],
  );
  assert.equal(devices.length, 5);
});
