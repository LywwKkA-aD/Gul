import assert from 'node:assert/strict';
import test from 'node:test';
import type { RemoteAudioTrack, Room } from 'livekit-client';
import { Devices } from '../src/renderer/media/devices.ts';
import { Playback } from '../src/renderer/media/playback.ts';
import { createRoom, disconnect } from '../src/renderer/media/rooms.ts';

const tick = () => new Promise((resolve) => setImmediate(resolve));
function fakeTrack() {
  let attached = false;
  const track = {
    receiver: {} as RTCRtpReceiver,
    volume: 1,
    attach: (_element: HTMLAudioElement) => {
      attached = true;
    },
    detach: (_element: HTMLAudioElement) => {
      attached = false;
    },
    setVolume: (value: number) => {
      track.volume = value;
    },
  };
  return { track, attached: () => attached, remote: track as unknown as RemoteAudioTrack };
}
function element(play: () => Promise<void> = async () => undefined) {
  const audio = {
    muted: false,
    volume: 1,
    removed: false,
    dataset: {},
    play,
    remove: () => {
      audio.removed = true;
    },
  };
  return audio as unknown as HTMLAudioElement;
}
function fakeRoom(switcher?: (id: string) => boolean | Promise<boolean>) {
  const calls: string[] = [];
  const room = {
    switchActiveDevice: async (_kind: string, id: string) => {
      calls.push(id);
      return switcher ? switcher(id) : true;
    },
  } as unknown as Room;
  return { room, calls };
}

test('both production room kinds enable the SDK WebAudio mixer for gain above unity', async () => {
  const room = createRoom();
  try {
    assert.equal(room.options.webAudioMix, true);
  } finally {
    await disconnect(room);
  }
});

test('HTML fallback clamps playback to unity while preserving the preference for WebAudio reattach', () => {
  const audio: HTMLAudioElement[] = [];
  const playback = new Playback(
    () => {
      const value = element();
      audio.push(value);
      return value;
    },
    () => undefined,
  );
  const voice = fakeTrack(),
    screen = fakeTrack();
  playback.setUserVolume('voice.8', 1.75);
  playback.attach('voice', 'voice.8', voice.remote, 'voice', false);
  playback.attach('screen', 'screen.8', screen.remote, 'screen', true);
  assert.equal(voice.track.volume, 1);
  assert.equal(audio[0].muted, false);
  assert.deepEqual(playback.receivers('voice'), [voice.track.receiver]);
  playback.setUserMuted('voice.8', true);
  assert.equal(voice.track.volume, 0);
  assert.equal(audio[0].muted, true);
  assert.equal(screen.track.volume, 1);
  playback.setUserMuted('voice.8', false);
  assert.equal(voice.track.volume, 1);
  playback.remove('voice');
  assert.equal(voice.attached(), false);
  playback.attach('voice', 'voice.8', voice.remote, 'voice', true);
  assert.equal(voice.track.volume, 1.75);
  assert.equal(audio.at(-1)!.muted, true);
  assert.equal(audio.at(-1)!.volume, 0);
  playback.removeParticipant('voice.8');
  assert.equal(voice.attached(), false);
  assert.equal(screen.attached(), true);
  playback.reset();
  assert.equal(screen.attached(), false);
  playback.attach('voice', 'voice.8', voice.remote, 'voice', true);
  assert.equal(voice.track.volume, 1);
  playback.clear();
});

test('playback warnings belong only to the currently attached element', async () => {
  let reject!: (error: Error) => void;
  let warnings = 0;
  const pending = new Promise<void>((_resolve, no) => {
    reject = no;
  });
  const playback = new Playback(
    () => element(() => pending),
    () => {
      warnings++;
    },
  );
  const voice = fakeTrack();
  playback.attach('voice', 'voice.8', voice.remote, 'voice', true);
  reject(new Error('private playback failure'));
  await tick();
  assert.equal(warnings, 1);
  playback.clear();
  const stale = new Playback(
    () => element(() => Promise.reject(new Error('private'))),
    () => {
      warnings++;
    },
  );
  stale.attach('voice', 'voice.8', voice.remote, 'voice', true);
  stale.clear();
  await tick();
  assert.equal(warnings, 1);
});

test('unconfirmed output changes restore the prior room output and do not overwrite preferences', async () => {
  const devices = new Devices();
  await devices.set(
    'audiooutput',
    'headphones',
    undefined,
    () => undefined,
    () => true,
  );
  const voice = fakeRoom(),
    screen = fakeRoom((id) => id !== 'rejected');
  await assert.rejects(
    devices.set(
      'audiooutput',
      'rejected',
      voice.room,
      () => screen.room,
      () => true,
    ),
  );
  assert.deepEqual(voice.calls, ['rejected', 'headphones']);
  assert.deepEqual(screen.calls, ['rejected', 'headphones']);
  const next = fakeRoom();
  await devices.applyOutput(next.room);
  assert.deepEqual(next.calls, ['headphones']);
});

test('a stale device completion cannot replace a stored input preference', async () => {
  const devices = new Devices();
  await devices.set(
    'audioinput',
    'microphone',
    undefined,
    () => undefined,
    () => true,
  );
  const voice = fakeRoom();
  await assert.rejects(
    devices.set(
      'audioinput',
      'late-microphone',
      voice.room,
      () => undefined,
      () => false,
    ),
  );
  assert.equal(devices.microphoneDevice, 'microphone');
  await devices.set(
    'audioinput',
    'microphone',
    voice.room,
    () => undefined,
    () => true,
  );
  assert.deepEqual(voice.calls, ['late-microphone']);
});
