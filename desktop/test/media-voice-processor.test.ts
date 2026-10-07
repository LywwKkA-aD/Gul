import assert from 'node:assert/strict';
import test from 'node:test';
import { VoiceProcessor } from '../src/renderer/media/voice-processor.ts';
import { defaultVoiceSettings } from '../src/renderer/media/voice-gate.ts';

function harness(module?: Promise<void>) {
  const output = {
    enabled: true,
    stops: 0,
    stop() {
      this.stops++;
    },
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
  const destination = { stream: { getAudioTracks: () => [output] } };
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
      return node as any;
    },
    stream: () => ({}) as any,
    moduleURL: 'gul://app/voice-worklet.js',
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
