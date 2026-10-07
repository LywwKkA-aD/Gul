import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import test from 'node:test';
import type { Room } from 'livekit-client';
import { MediaController } from '../src/renderer/media/controller.ts';
import type { AudioState, MediaSession } from '../src/shared/contracts.ts';

const session: MediaSession = {
  epoch: 1,
  sessionId: 7,
  identity: 'voice.7',
  name: 'Alice',
  channelId: 0,
  revision: 1,
  grant: {
    url: 'ws://127.0.0.1:5000/capability/rtc',
    token: 'fixture',
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
  readonly localParticipant = { publishTrack: async () => ({}), unpublishTrack: async () => {} };
  async connect() {}
  async disconnect() {}
  async startAudio() {}
}
test('microphone failure fences pending unmute metadata, persists local mute and permits a fresh explicit retry', async () => {
  let confirm!: (state: AudioState) => void;
  const pending = new Promise<AudioState>((resolve) => {
    confirm = resolve;
  });
  let confirmations = 0;
  let failure!: () => void;
  const captures: { mediaStreamTrack: { enabled: boolean }; stopped: boolean }[] = [];
  const controller = new MediaController({
    screenGrant: async () => ({ ...session.grant, identity: 'screen.7' }),
    audioState: async (state) => (++confirmations === 1 ? pending : state),
    roomFactory: () => new FakeRoom() as unknown as Room,
    micFactory: async () => {
      const track = {
        mediaStreamTrack: { enabled: true },
        stopped: false,
        stop() {
          this.stopped = true;
          this.mediaStreamTrack.enabled = false;
        },
        async mute() {
          this.mediaStreamTrack.enabled = false;
        },
        async unmute() {
          this.mediaStreamTrack.enabled = true;
        },
      };
      captures.push(track);
      return track as any;
    },
    voiceProcessorFactory: async (_track, _settings, _reading, onFailure) => {
      failure = onFailure;
      return { update() {}, async destroy() {} };
    },
  });
  await controller.join(session);
  const opening = controller.setAudio({ muted: false, deafened: false });
  await new Promise((resolve) => setImmediate(resolve));
  failure();
  assert.equal(controller.getSnapshot().muted, true);
  confirm({ muted: false, deafened: false });
  await opening;
  assert.equal(controller.getSnapshot().muted, true);
  assert.equal(captures.length, 1, 'Stale broker metadata must not recapture after a processor failure.');
  assert.equal(captures[0].stopped, true);
  await controller.setAudio({ muted: false, deafened: false });
  assert.equal(captures.length, 2, 'A fresh explicit retry may acquire a healthy processor.');
  assert.equal(controller.getSnapshot().muted, false);
  assert.equal(captures[1].mediaStreamTrack.enabled, true);
  failure();
  await controller.leave();
  await controller.join(session);
  assert.equal(controller.getSnapshot().muted, true, 'Failure mute must survive changing channels.');
  assert.equal(captures[2].mediaStreamTrack.enabled, false);
  await controller.leave();
});
