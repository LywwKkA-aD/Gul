import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import test from 'node:test';
import { RoomEvent, type Room, type LocalAudioTrack } from 'livekit-client';
import { MediaController } from '../src/renderer/media/controller.ts';
import { presentationSnapshot } from '../src/renderer/presentation-snapshot.ts';
import type { MediaSession } from '../src/shared/contracts.ts';
import type { VoiceReading } from '../src/renderer/media/voice-gate.ts';

const session: MediaSession = {
  epoch: 1,
  sessionId: 7,
  identity: 'voice.7',
  name: 'Alice',
  channelId: 0,
  revision: 1,
  grant: {
    url: 'ws://127.0.0.1:5000/capability/rtc',
    token: 'short-lived',
    identity: 'voice.7',
    ownerIdentity: 'voice.7',
    room: 'gul.0',
    sessionId: 7,
    channelId: 0,
    revision: 1,
  },
};
class FakeRoom extends EventEmitter {
  readonly options = { webAudioMix: true };
  readonly remoteParticipants = new Map();
  localParticipant = {
    publishTrack: async () => ({ trackSid: 'mic' }),
    unpublishTrack: async () => {},
  };
  async connect() {}
  async disconnect() {}
  async startAudio() {}
}
function harness(processorAvailable = true) {
  const rooms: FakeRoom[] = [];
  const readings: ((value: Pick<VoiceReading, 'level' | 'active'>) => void)[] = [];
  const track = {
    mediaStreamTrack: { enabled: true },
    stop() {},
    async mute() {},
    async unmute() {},
  } as unknown as LocalAudioTrack;
  const controller = new MediaController({
    screenGrant: async () => ({ ...session.grant, identity: 'screen.7' }),
    audioState: async (state) => state,
    micFactory: async () => track,
    voiceProcessorFactory: async (_track, _settings, report) => {
      readings.push(report);
      return processorAvailable ? { update() {}, async destroy() {} } : undefined;
    },
    roomFactory: () => {
      const room = new FakeRoom();
      rooms.push(room);
      return room as unknown as Room;
    },
  });
  return { controller, rooms, readings };
}

test('local worklet lights the avatar before a server event and only speech edges update the main view', async () => {
  const { controller, rooms, readings } = harness();
  await controller.join(session);
  const presentation = presentationSnapshot(controller.getSnapshot);
  const quiet = presentation();
  readings[0]({ level: 0.3, active: true });
  assert.deepEqual(controller.getSnapshot().speakers, ['voice.7']);
  const speaking = presentation();
  assert.notEqual(speaking, quiet);
  readings[0]({ level: 0.8, active: true });
  assert.equal(presentation(), speaking, '20Hz meter changes must not re-render the main view');
  rooms[0].emit(RoomEvent.ActiveSpeakersChanged, [{ identity: 'voice.8' }, { identity: 'screen.8' }]);
  assert.deepEqual(controller.getSnapshot().speakers, ['voice.8', 'voice.7']);
  readings[0]({ level: 0, active: false });
  assert.deepEqual(controller.getSnapshot().speakers, ['voice.8']);
  rooms[0].emit(RoomEvent.ActiveSpeakersChanged, [{ identity: 'voice.7' }, { identity: 'voice.8' }]);
  assert.deepEqual(
    controller.getSnapshot().speakers,
    ['voice.8'],
    'late server self activity must not relight local silence',
  );
  await controller.leave();
});

test('local activity obeys immediate mute, deafen, reconnection and session cancellation', async () => {
  const { controller, rooms, readings } = harness();
  await controller.join(session);
  readings[0]({ level: 0.4, active: true });
  const muted = controller.setAudio({ muted: true, deafened: false });
  assert.deepEqual(controller.getSnapshot().speakers, []);
  readings[0]({ level: 0.8, active: true });
  assert.deepEqual(controller.getSnapshot().speakers, []);
  await muted;
  await controller.setAudio({ muted: false, deafened: false });
  readings[0]({ level: 0.4, active: true });
  assert.deepEqual(controller.getSnapshot().speakers, ['voice.7']);
  rooms[0].emit(RoomEvent.Reconnecting);
  assert.deepEqual(controller.getSnapshot().speakers, []);
  rooms[0].emit(RoomEvent.Reconnected);
  await controller.setAudio({ muted: false, deafened: true });
  assert.deepEqual(controller.getSnapshot().speakers, []);
  await controller.leave();
  await controller.join(session);
  readings[0]({ level: 0.5, active: true });
  assert.deepEqual(controller.getSnapshot().speakers, [], 'old processor reports cannot enter a new session');
  await controller.leave();
});

test('processor unavailable retains the SDK local indicator and remote identities are deduplicated', async () => {
  const { controller, rooms } = harness(false);
  await controller.join(session);
  rooms[0].emit(RoomEvent.ActiveSpeakersChanged, [
    { identity: 'voice.7' },
    { identity: 'voice.8' },
    { identity: 'voice.8' },
    { identity: 'invalid' },
  ]);
  assert.deepEqual(controller.getSnapshot().speakers, ['voice.8', 'voice.7']);
  await controller.setAudio({ muted: true, deafened: false });
  assert.deepEqual(controller.getSnapshot().speakers, ['voice.8']);
  await controller.leave();
});

test('zero input gain cannot light the local avatar and VAD reads remain authoritative over server self activity', async () => {
  const { controller, rooms, readings } = harness();
  await controller.join(session);
  readings[0]({ level: 0.4, active: true });
  await controller.setVoiceSettings({ inputGain: 0 });
  assert.deepEqual(
    controller.getSnapshot().speakers,
    [],
    'zero gain sends no local speech even during a VAD hold',
  );
  readings[0]({ level: 0, active: true });
  assert.deepEqual(controller.getSnapshot().speakers, []);
  await controller.setVoiceSettings({ inputGain: 1, mode: 'vad' });
  readings[0]({ level: 0.01, active: false });
  rooms[0].emit(RoomEvent.ActiveSpeakersChanged, [{ identity: 'voice.7' }]);
  assert.deepEqual(controller.getSnapshot().speakers, []);
  readings[0]({ level: 0.5, active: true });
  assert.deepEqual(controller.getSnapshot().speakers, ['voice.7']);
  await controller.leave();
});
