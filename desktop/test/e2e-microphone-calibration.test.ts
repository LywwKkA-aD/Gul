import assert from 'node:assert/strict';
import test from 'node:test';
import { runInNewContext } from 'node:vm';
import { installMicrophoneCalibration } from '../e2e/microphone-calibration.ts';

function browser(linux: boolean) {
  let selected = { label: 'Fake default microphone', getSettings: () => ({ deviceId: 'native' }) };
  let oscillators = 0;
  let oscillatorStops = 0;
  const generated = { label: 'MediaStreamAudioDestinationNode', getSettings: () => ({ deviceId: 'native' }) };
  const stream = (track: object) => ({ getAudioTracks: () => [track], getTracks: () => [{ stop() {} }] });
  class AudioContext {
    createMediaStreamSource(input: object) {
      return { input, disconnect() {} };
    }
    createMediaStreamDestination() {
      return { stream: stream(generated) };
    }
    createOscillator() {
      oscillators++;
      return {
        frequency: { value: 0 },
        start() {},
        stop() {
          oscillatorStops++;
        },
        disconnect() {},
        connect(gain: object) {
          return gain;
        },
      };
    }
    createGain() {
      return { gain: { value: 0 }, connect() {}, disconnect() {} };
    }
  }
  class MediaDevices {
    async getUserMedia() {
      return stream(selected);
    }
  }
  const mediaDevices = new MediaDevices();
  const guarded = mediaDevices.getUserMedia;
  Object.defineProperty(MediaDevices.prototype, 'getUserMedia', {
    value: guarded,
    writable: false,
    configurable: false,
  });
  Object.defineProperty(mediaDevices, 'getUserMedia', {
    value: guarded,
    writable: false,
    configurable: false,
  });
  runInNewContext(`(${installMicrophoneCalibration.toString()})(${linux})`, {
    AudioContext,
    navigator: { mediaDevices },
  });
  return {
    setTrack: (label: string) => {
      selected = { label, getSettings: () => ({ deviceId: 'native' }) };
    },
    capture: () => mediaDevices.getUserMedia(),
    context: () => new AudioContext(),
    derived: () => stream(generated),
    remote: () => stream({ label: 'remote audio', getSettings: () => ({ deviceId: 'native' }) }),
    oscillators: () => oscillators,
    stops: () => oscillatorStops,
    guard: () => mediaDevices.getUserMedia === guarded,
  };
}

test('non-Linux calibration leaves locked GUM guards intact and only injects into Fake microphone tracks', async () => {
  const fixture = browser(false);
  const context = fixture.context();
  const microphone = await fixture.capture();
  const source = context.createMediaStreamSource(microphone);
  assert.notEqual(source.input, microphone);
  assert.equal(fixture.oscillators(), 1);
  assert.equal(fixture.guard(), true);
  source.disconnect();
  source.disconnect();
  assert.equal(fixture.stops(), 1, 'cleanup must stop a calibration oscillator exactly once');
  fixture.setTrack('Native microphone');
  const native = await fixture.capture();
  assert.equal(context.createMediaStreamSource(native).input, native);
  assert.equal(fixture.oscillators(), 1);
});

test('Linux only calibrates the isolated native test microphone, never a processor output, remote or private screen track', async () => {
  const fixture = browser(true);
  fixture.setTrack('Gul-Test-Microphone-0');
  const context = fixture.context();
  const microphone = await fixture.capture();
  assert.notEqual(context.createMediaStreamSource(microphone).input, microphone);
  const derived = fixture.derived();
  assert.equal(
    context.createMediaStreamSource(derived).input,
    derived,
    'a derived track can have a deviceId',
  );
  const remote = fixture.remote();
  assert.equal(context.createMediaStreamSource(remote).input, remote);
  fixture.setTrack('Gul-Screen-Audio-0123456789abcdef');
  const screen = await fixture.capture();
  assert.equal(context.createMediaStreamSource(screen).input, screen);
  assert.equal(fixture.oscillators(), 1, 'screen/remote/processor graphs must retain their actual audio');
  assert.equal(fixture.guard(), true);
});
