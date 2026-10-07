import assert from 'node:assert/strict';
import test from 'node:test';
import { captureCapabilities, CaptureChooser } from '../src/main/capture-policy.ts';

const source = { id: 'screen:0:0', name: 'Screen' };
test('Linux audio exclusion requires the built-in helper and detected audio server', () => {
  const unavailable = captureCapabilities('linux', '7.0.0', true, false);
  assert.equal(unavailable.systemAudio, false);
  assert.equal(unavailable.ownAudioExcluded, false);
  const linux = captureCapabilities('linux', '7.0.0', true, true);
  assert.equal(linux.systemAudio, true);
  assert.equal(linux.ownAudioExcluded, true);
  assert.doesNotMatch(linux.details, /выключите|другое устройство/iu);
  assert.equal(captureCapabilities('linux', '7.0.0', false, true).systemAudio, false);
  const windows = captureCapabilities('win32', '10.0.19045', false);
  assert.equal(windows.systemAudio, false);
  assert.equal(windows.ownAudioExcluded, false);
  assert.match(windows.details, /голос/iu);
  assert.equal(captureCapabilities('win32', '10.0.22000', true).ownAudioExcluded, false);
  assert.equal(captureCapabilities('win32', '10.0.20348', true).ownAudioExcluded, false);
  assert.equal(captureCapabilities('linux', '7.0.0', false).audioServer, 'not-detected');
  assert.equal(captureCapabilities('darwin', '22.0.0', false).systemAudio, false);
});
test('source selection grants loopback only after requested audio and explicit consent', async () => {
  const chooser = new CaptureChooser();
  for (const [requested, consent, expected] of [
    [true, true, 'loopback'],
    [true, false, undefined],
    [false, true, undefined],
  ] as const) {
    const selection = await chooser.choose({
      valid: () => true,
      getSources: async () => [source],
      pick: async () => ({ response: 1, checkboxChecked: consent }),
      capabilities: captureCapabilities('linux', '7.0.0', true, true),
      audioRequested: requested,
    });
    assert.deepEqual(selection, { video: source, ...(expected ? { audio: expected } : {}) });
  }
});
test('cancelled selection, unsupported audio and session changes cannot grant capture', async () => {
  const chooser = new CaptureChooser();
  const options = {
    valid: () => true,
    getSources: async () => [source],
    pick: async () => ({ response: 0, checkboxChecked: true }),
    capabilities: captureCapabilities('linux', '7.0.0', true, true),
    audioRequested: true,
  };
  assert.equal(await chooser.choose(options), null);
  assert.deepEqual(
    await chooser.choose({
      ...options,
      capabilities: captureCapabilities('darwin', '22.0.0', false),
      pick: async () => ({ response: 1, checkboxChecked: true }),
    }),
    { video: source },
  );
  let valid = true;
  assert.equal(
    await chooser.choose({
      ...options,
      valid: () => valid,
      pick: async () => {
        valid = false;
        return { response: 1, checkboxChecked: true };
      },
    }),
    null,
  );
  let enumerated = false;
  assert.equal(
    await chooser.choose({
      ...options,
      valid: () => false,
      getSources: async () => {
        enumerated = true;
        return [source];
      },
    }),
    null,
  );
  assert.equal(enumerated, false);
});
test('concurrent selection is rejected and failed pick releases the chooser', async () => {
  const chooser = new CaptureChooser();
  let resolve!: (sources: readonly (typeof source)[]) => void;
  const pending = new Promise<readonly (typeof source)[]>((yes) => {
    resolve = yes;
  });
  const options = {
    valid: () => true,
    getSources: () => pending,
    pick: async () => ({ response: 1, checkboxChecked: true }),
    capabilities: captureCapabilities('linux', '7.0.0', true, true),
    audioRequested: true,
  };
  const first = chooser.choose(options);
  assert.equal(await chooser.choose(options), null);
  resolve([source]);
  assert.equal((await first)?.audio, 'loopback');
  await assert.rejects(
    chooser.choose({
      ...options,
      pick: async () => {
        throw new Error('picker failed');
      },
    }),
  );
  assert.equal((await chooser.choose(options))?.audio, 'loopback');
});
