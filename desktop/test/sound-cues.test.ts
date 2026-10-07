import assert from 'node:assert/strict';
import test from 'node:test';
import { SoundCues } from '../src/renderer/sound-cues.ts';

function harness(suspended = false) {
  const parameter = () => ({
    values: [] as { method: string; value: number; time: number }[],
    setValueAtTime(value: number, time: number) {
      this.values.push({ method: 'set', value, time });
    },
    linearRampToValueAtTime(value: number, time: number) {
      this.values.push({ method: 'ramp', value, time });
    },
    cancelAndHoldAtTime(time: number) {
      this.values.push({ method: 'hold', value: 0, time });
    },
  });
  const oscillators: any[] = [];
  const gains: any[] = [];
  let contexts = 0;
  let closes = 0;
  let resumed!: () => void;
  const resuming = new Promise<void>((resolve) => {
    resumed = resolve;
  });
  const context = {
    state: suspended ? 'suspended' : 'running',
    currentTime: 1,
    destination: {},
    async resume() {
      await resuming;
      this.state = 'running';
    },
    async close() {
      closes++;
      this.state = 'closed';
    },
    createGain() {
      const gain = {
        gain: parameter(),
        disconnects: 0,
        connect() {},
        disconnect() {
          this.disconnects++;
        },
      };
      gains.push(gain);
      return gain;
    },
    createOscillator() {
      const oscillator = {
        frequency: parameter(),
        type: '',
        starts: [] as number[],
        stops: [] as number[],
        disconnects: 0,
        onended: null as (() => void) | null,
        connect() {},
        start(time: number) {
          this.starts.push(time);
        },
        stop(time: number) {
          this.stops.push(time);
          if (this.stops.length > 1) queueMicrotask(() => this.onended?.());
        },
        disconnect() {
          this.disconnects++;
        },
      };
      oscillators.push(oscillator);
      return oscillator;
    },
  };
  const cues = new SoundCues(() => {
    contexts++;
    return context as any;
  });
  return { cues, context, gains, oscillators, resumed, contexts: () => contexts, closes: () => closes };
}

test('notification tones are explicit opt-in and deafen never creates an audio context', async () => {
  const { cues, contexts } = harness();
  await cues.play('connect', false, false);
  await cues.play('connect', true, true);
  assert.equal(contexts(), 0);
  await cues.close();
});

test('quiet sine cues ramp at both edges and reuse one lazy context', async () => {
  const { cues, context, gains, oscillators, contexts } = harness();
  await cues.play('connect', true, false);
  assert.equal(contexts(), 1);
  assert.equal(oscillators[0].type, 'sine');
  assert.equal(gains[0].gain.values[0].value, 0);
  assert.equal(gains[0].gain.values.at(-1).value, 0);
  assert.ok(
    gains[0].gain.values.every((entry: { value: number }) => entry.value >= 0 && entry.value <= 0.025),
  );
  assert.ok(oscillators[0].stops[0] - context.currentTime <= 0.25);
  oscillators[0].onended();
  await cues.play('screen-start', true, false);
  assert.equal(contexts(), 1);
  assert.equal(oscillators.length, 2);
  assert.equal(oscillators[0].disconnects, 1);
  await cues.close();
});

test('rapid cues during resume only start the latest local intent', async () => {
  const { cues, oscillators, resumed, contexts } = harness(true);
  const old = cues.play('connect', true, false);
  const latest = cues.play('mute', true, false);
  assert.equal(contexts(), 1);
  assert.equal(oscillators.length, 0);
  resumed();
  await Promise.all([old, latest]);
  assert.equal(oscillators.length, 1);
  assert.ok(oscillators[0].frequency.values[1].value < oscillators[0].frequency.values[0].value);
  await cues.close();
});

test('deafen or disabled notifications fade an existing cue and release its graph', async () => {
  const { cues, gains, oscillators, contexts } = harness();
  await cues.play('unmute', true, false);
  await cues.play('deafen', true, true);
  assert.equal(oscillators.length, 1);
  assert.equal(oscillators[0].disconnects, 1);
  assert.equal(gains[0].disconnects, 1);
  assert.equal(gains[0].gain.values.at(-1).value, 0);
  await cues.play('reconnect', true, false);
  await cues.play('disconnect', false, false);
  assert.equal(contexts(), 1);
  assert.equal(oscillators[1].disconnects, 1);
  await cues.close();
});

test('closing while resume is pending never starts a late cue and closes the context once', async () => {
  const { cues, oscillators, resumed, closes } = harness(true);
  const pending = cues.play('connect', true, false);
  await cues.close();
  resumed();
  await pending;
  await cues.close();
  await cues.play('connect', true, false);
  assert.equal(oscillators.length, 0);
  assert.equal(closes(), 1);
});

test('missing or blocked audio cannot reject an app action', async () => {
  const unavailable = new SoundCues(() => {
    throw new Error('AudioContext unavailable');
  });
  await unavailable.play('connect', true, false);
  await unavailable.close();
  const { cues, context, oscillators } = harness(true);
  context.resume = async () => {
    throw new Error('Autoplay blocked');
  };
  await cues.play('connect', true, false);
  assert.equal(oscillators.length, 0);
  await cues.close();
});
