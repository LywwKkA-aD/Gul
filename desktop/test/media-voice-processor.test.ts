import assert from 'node:assert/strict';
import test from 'node:test';
import { VoiceProcessor } from '../src/renderer/media/voice-processor.ts';
import { defaultVoiceSettings } from '../src/renderer/media/voice-gate.ts';

function harness(module?: Promise<void>, ready = true) {
  let automaticReady = ready;
  const output = {
    enabled: true,
    stops: 0,
    stop() {
      this.stops++;
    },
    getSettings: () => ({ channelCount: destination.channelCount }),
  };
  const source = {
    connects: 0,
    disconnects: 0,
    connect() {
      this.connects++;
    },
    disconnect() {
      this.disconnects++;
    },
  };
  const destination = { channelCount: 2, stream: { getAudioTracks: () => [output] } };
  const port = {
    onmessage: null as ((event: { data: unknown }) => void) | null,
    closed: false,
    messages: [] as unknown[],
    postMessage(message: unknown) {
      this.messages.push(message);
    },
    close() {
      this.closed = true;
    },
  };
  const node = {
    port,
    disconnects: 0,
    connect() {},
    disconnect() {
      this.disconnects++;
    },
  };
  let modules = 0;
  let nodes = 0;
  let failures = 0;
  const context = {
    audioWorklet: {
      addModule: async () => {
        modules++;
        await module;
      },
    },
    createMediaStreamSource: () => source,
    createMediaStreamDestination: () => destination,
    close() {
      throw new Error('Must not close SDK context');
    },
  };
  const readings: unknown[] = [];
  const processor = new VoiceProcessor(defaultVoiceSettings, (reading) => readings.push(reading), {
    node: () => {
      nodes++;
      if (automaticReady)
        queueMicrotask(() =>
          port.onmessage?.({ data: { type: 'ready', neuralNoise: true, sampleRate: 48000 } }),
        );
      return node as any;
    },
    stream: () => ({}) as any,
    moduleURL: 'gul://app/voice-worklet.js',
    failure: () => {
      failures++;
    },
  });
  const options = { kind: 'audio', track: { enabled: false }, audioContext: context } as any;
  return {
    processor,
    options,
    readings,
    context,
    output,
    source,
    node,
    port,
    modules: () => modules,
    nodes: () => nodes,
    failures: () => failures,
    pauseReadiness: () => {
      automaticReady = false;
    },
  };
}
test('processor uses the SDK context, preserves muted track and destroys output without touching raw capture', async () => {
  const { processor, options, output, source, node, port } = harness();
  await processor.init(options);
  assert.equal(processor.processedTrack, output);
  assert.equal(output.enabled, false);
  await processor.destroy();
  await processor.destroy();
  assert.equal(output.stops, 1);
  assert.equal(source.disconnects, 1);
  assert.equal(node.disconnects, 1);
  assert.equal(port.closed, true);
  assert.equal(port.onmessage, null);
});
test('processed microphone remains mono through the WebAudio destination and output track', async () => {
  const { processor, options, output } = harness();
  await processor.init(options);
  assert.equal(output.getSettings().channelCount, 1);
  await processor.destroy();
});
test('requested neural suppression requires a real model-ready acknowledgement before exposing microphone output', async () => {
  const requested = harness(undefined, false);
  const opening = requested.processor.init(requested.options);
  await new Promise((resolve) => setImmediate(resolve));
  requested.port.onmessage?.({ data: { type: 'ready', neuralNoise: false, sampleRate: 48000 } });
  await assert.rejects(opening, /обработк/iu);
  assert.equal(requested.processor.processedTrack, undefined);
  assert.equal(requested.source.connects, 0);
  assert.equal(requested.port.closed, true);
  const disabled = harness(undefined, false);
  disabled.processor.update({ ...defaultVoiceSettings, noiseSuppression: false });
  const plain = disabled.processor.init(disabled.options);
  await new Promise((resolve) => setImmediate(resolve));
  disabled.port.onmessage?.({ data: { type: 'ready', neuralNoise: false, sampleRate: 48000 } });
  await plain;
  assert.equal(disabled.processor.processedTrack, disabled.output);
  await disabled.processor.destroy();
});
test('a late worklet error is sticky, reports once and cannot be unmuted by later audio preferences', async () => {
  const { processor, options, node, output, failures } = harness();
  await processor.init(options);
  processor.setMuted(false);
  (node as any).onprocessorerror();
  (node as any).onprocessorerror();
  processor.setMuted(false);
  assert.equal(output.enabled, false);
  assert.equal(processor.failed, true);
  assert.equal(failures(), 1);
  await processor.destroy();
});
test('processor cannot expose an output before the neural worklet is ready, and leaving cancels its handshake', async () => {
  const { processor, options, output } = harness(undefined, false);
  const opening = processor.init(options);
  const cancelled = assert.rejects(opening, /обработк/iu);
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(processor.processedTrack, undefined);
  assert.equal(output.enabled, true, 'No destination should have been acquired yet.');
  await processor.destroy();
  await cancelled;
});
test('processor bounds meter input and forwards settings once without duplicate gain nodes', async () => {
  const { processor, options, port, readings } = harness();
  await processor.init(options);
  port.onmessage?.({ data: { type: 'level', level: 0.25, active: true } });
  assert.deepEqual(readings, [{ level: 0.25, active: true }]);
  for (const data of [
    { type: 'level', level: NaN, active: true },
    { type: 'level', level: 100, active: true },
    { type: 'wrong', level: 0.5, active: true },
  ])
    port.onmessage?.({ data });
  assert.equal(readings.length, 1);
  processor.update({ ...defaultVoiceSettings, inputGain: 1.5 });
  assert.equal(port.messages.length, 1);
  await processor.destroy();
});
test('leaving during worklet loading allocates no graph, while restarts reuse loaded module', async () => {
  let resolve!: () => void;
  const pending = new Promise<void>((yes) => {
    resolve = yes;
  });
  const cancelled = harness(pending);
  const opening = cancelled.processor.init(cancelled.options);
  await cancelled.processor.destroy();
  resolve();
  await assert.rejects(opening);
  assert.equal(cancelled.nodes(), 0);
  const live = harness();
  await live.processor.init(live.options);
  await live.processor.restart(live.options);
  assert.equal(live.modules(), 1);
  assert.equal(live.nodes(), 2);
  await live.processor.destroy();
});
test('SDK device restart omitting audioContext retains shared context and explicit mute', async () => {
  const { processor, options, output } = harness();
  await processor.init(options);
  processor.setMuted(true);
  const { audioContext: _context, ...restart } = options;
  await processor.restart({ ...restart, track: { enabled: true } });
  assert.equal(output.enabled, false);
  processor.setMuted(false);
  assert.equal(output.enabled, true);
  await processor.destroy();
});
test('cancelled or failed SDK restart stops the new raw capture that the SDK has not adopted yet', async () => {
  for (const failure of ['cancel', 'error']) {
    const live = harness();
    await live.processor.init(live.options);
    live.pauseReadiness();
    let stops = 0;
    const raw = {
      enabled: false,
      stop() {
        stops++;
      },
    };
    const restarting = live.processor.restart({ ...live.options, track: raw });
    const rejected = assert.rejects(restarting, /обработк/iu);
    await new Promise((resolve) => setImmediate(resolve));
    if (failure === 'cancel') await live.processor.destroy();
    else (live.node as any).onprocessorerror();
    await rejected;
    assert.equal(stops, 1, failure);
  }
});
test('initial processor failure preserves the raw Chromium fallback, and successful restart leaves new raw live', async () => {
  const failing = harness(undefined, false);
  let stops = 0;
  const raw = {
    enabled: false,
    stop() {
      stops++;
    },
  };
  const opening = failing.processor.init({ ...failing.options, track: raw });
  const rejected = assert.rejects(opening, /обработк/iu);
  await new Promise((resolve) => setImmediate(resolve));
  (failing.node as any).onprocessorerror();
  await rejected;
  assert.equal(stops, 0);
  const live = harness();
  await live.processor.init({ ...live.options, track: raw });
  const newRaw = {
    enabled: true,
    stop() {
      stops++;
    },
  };
  await live.processor.restart({ ...live.options, track: newRaw });
  assert.equal(stops, 0);
  live.pauseReadiness();
  const restarting = live.processor.restart({ ...live.options, track: newRaw });
  const failed = assert.rejects(restarting, /обработк/iu);
  await new Promise((resolve) => setImmediate(resolve));
  (live.node as any).onprocessorerror();
  await failed;
  assert.equal(stops, 0, 'A failed restart of the already adopted raw must preserve fallback.');
});
