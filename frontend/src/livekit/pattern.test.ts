import test from 'node:test';
import assert from 'node:assert/strict';
import { createTestPattern } from './pattern.ts';

function setup(t, failure) {
  const calls = { videoStops: 0, audioStops: 0, closes: 0, oscillatorStops: 0, oscillatorDisconnects: 0, gainDisconnects: 0, clears: 0, timers: 0 };
  const failAt = (stage) => { if (failure === stage) throw new Error(`Failed at ${stage}`); };
  class FakeTrack extends EventTarget {
    constructor(kind) { super(); this.kind = kind; this.id = kind; this.readyState = 'live'; }
    getConstraints() { return {}; }
    getSettings() { return {}; }
    stop() { calls[this.kind === 'video' ? 'videoStops' : 'audioStops']++; this.readyState = 'ended'; }
  }
  const video = new FakeTrack('video');
  const sound = new FakeTrack('audio');
  class FakeStream {
    constructor(tracks) { this.tracks = tracks; }
    getTracks() { return this.tracks; }
    getVideoTracks() { return this.tracks.filter((track) => track.kind === 'video'); }
    getAudioTracks() { return this.tracks.filter((track) => track.kind === 'audio'); }
  }
  const oscillator = {
    frequency: { value: 0 }, connect: (node) => node,
    start: () => failAt('start'),
    stop: () => { calls.oscillatorStops++; },
    disconnect: () => { calls.oscillatorDisconnects++; },
  };
  const gain = {
    gain: { value: 0 }, connect: (node) => node,
    disconnect: () => { calls.gainDisconnects++; },
  };
  class FakeAudioContext {
    constructor() { failAt('context'); }
    createMediaStreamDestination() { failAt('destination'); return { stream: new FakeStream([sound]) }; }
    createOscillator() { failAt('oscillator'); return oscillator; }
    createGain() { failAt('gain'); return gain; }
    resume() { return failure === 'resume' ? Promise.reject(new Error('Failed at resume')) : Promise.resolve(); }
    close() { calls.closes++; return Promise.resolve(); }
  }
  const replacements = {
    document: { createElement: () => ({
      getContext: () => ({ fillRect() {}, fillText() {} }),
      captureStream: () => new FakeStream([video]),
    }) },
    window: {
      setInterval: () => { calls.timers++; return 1; },
      clearInterval: () => { calls.clears++; },
    },
    AudioContext: FakeAudioContext,
    MediaStream: FakeStream,
  };
  for (const [name, value] of Object.entries(replacements)) {
    const descriptor = Object.getOwnPropertyDescriptor(globalThis, name);
    Object.defineProperty(globalThis, name, { value, configurable: true });
    t.after(() => {
      if (descriptor) Object.defineProperty(globalThis, name, descriptor);
      else Reflect.deleteProperty(globalThis, name);
    });
  }
  return calls;
}

for (const stage of ['context', 'destination', 'oscillator', 'gain', 'start', 'resume']) {
  test(`test pattern releases already-created resources when ${stage} fails`, async (t) => {
    const calls = setup(t, stage);
    await assert.rejects(createTestPattern(), { message: `Failed at ${stage}` });
    assert.equal(calls.videoStops, 1);
    assert.equal(calls.closes, stage === 'context' ? 0 : 1);
    assert.equal(calls.audioStops, ['context', 'destination'].includes(stage) ? 0 : 1);
    assert.equal(calls.oscillatorDisconnects, ['gain', 'start', 'resume'].includes(stage) ? 1 : 0);
    assert.equal(calls.gainDisconnects, ['start', 'resume'].includes(stage) ? 1 : 0);
    assert.equal(calls.oscillatorStops, stage === 'resume' ? 1 : 0);
    assert.equal(calls.clears, calls.timers);
  });
}

test('successful pattern keeps its tracks alive until idempotent cleanup', async (t) => {
  const calls = setup(t);
  const capture = await createTestPattern();
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(capture.tracks.map((track) => track.kind), ['video', 'audio']);
  assert.equal(calls.videoStops + calls.audioStops + calls.closes, 0);
  capture.cleanup();
  capture.cleanup();
  assert.deepEqual(calls, {
    videoStops: 1, audioStops: 1, closes: 1, oscillatorStops: 1,
    oscillatorDisconnects: 1, gainDisconnects: 1, clears: 1, timers: 1,
  });
});
