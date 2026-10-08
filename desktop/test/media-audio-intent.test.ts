import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import test from 'node:test';
import type { Room } from 'livekit-client';
import type { AudioState, MediaSession } from '../src/shared/contracts.ts';
import { MediaController } from '../src/renderer/media/controller.ts';

const session: MediaSession = {
  epoch: 1,
  sessionId: 7,
  identity: 'voice.7',
  name: 'Fixture',
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
class RoomFixture extends EventEmitter {
  readonly options = { webAudioMix: true };
  readonly remoteParticipants = new Map();
  readonly localParticipant = {
    async publishTrack() {
      return { trackSid: 'fixture-microphone' };
    },
    async unpublishTrack() {},
  };
  async connect() {}
  async disconnect() {}
  async startAudio() {}
}
const normalized = (state: AudioState): AudioState => ({
  muted: state.muted || state.deafened,
  deafened: state.deafened,
});
function fixture(
  audioState: (state: AudioState) => Promise<AudioState> = async (state) => normalized(state),
) {
  const requests: AudioState[] = [];
  const raw = { enabled: true };
  const track = {
    mediaStreamTrack: raw,
    stop() {
      raw.enabled = false;
    },
    async mute() {
      raw.enabled = false;
    },
    async unmute() {
      raw.enabled = true;
    },
  };
  const controller = new MediaController({
    roomFactory: () => new RoomFixture() as unknown as Room,
    micFactory: async () => track as any,
    voiceProcessorFactory: async () => ({ update() {}, async destroy() {} }),
    screenGrant: async () => ({ ...session.grant, identity: 'screen.7' }),
    audioState: async (state) => {
      requests.push(state);
      return audioState(state);
    },
  });
  return { controller, raw, requests };
}

test('undeafen restores an originally open microphone despite the broker effective muted bit', async () => {
  const f = fixture();
  try {
    await f.controller.join(session);
    assert.equal(f.raw.enabled, true);
    await f.controller.setAudio({ muted: false, deafened: true });
    assert.equal(f.controller.getSnapshot().muted, false, 'deafen cannot become a new manual mute');
    assert.equal(f.controller.getSnapshot().deafened, true);
    assert.equal(f.raw.enabled, false, 'deafened still silences actual capture');
    await f.controller.setAudio({ muted: f.controller.getSnapshot().muted, deafened: false });
    assert.equal(f.controller.getSnapshot().muted, false);
    assert.equal(f.controller.getSnapshot().deafened, false);
    assert.equal(f.raw.enabled, true);
    assert.deepEqual(f.requests, [
      { muted: false, deafened: true },
      { muted: false, deafened: false },
    ]);
  } finally {
    await f.controller.leave();
  }
});

test('undeafen preserves a microphone that the user manually muted before deafen', async () => {
  const f = fixture();
  try {
    await f.controller.join(session);
    await f.controller.setAudio({ muted: true, deafened: false });
    await f.controller.setAudio({ muted: true, deafened: true });
    assert.equal(f.controller.getSnapshot().muted, true);
    assert.equal(f.raw.enabled, false);
    await f.controller.setAudio({ muted: f.controller.getSnapshot().muted, deafened: false });
    assert.equal(f.controller.getSnapshot().muted, true);
    assert.equal(f.controller.getSnapshot().deafened, false);
    assert.equal(f.raw.enabled, false);
  } finally {
    await f.controller.leave();
  }
});

test('a stale normalized deafen confirmation cannot replace the newest unmuted intent', async () => {
  let confirm!: (state: AudioState) => void;
  const older = new Promise<AudioState>((resolve) => {
    confirm = resolve;
  });
  let calls = 0;
  const f = fixture((state) => (++calls === 1 ? older : Promise.resolve(normalized(state))));
  try {
    await f.controller.join(session);
    const closing = f.controller.setAudio({ muted: false, deafened: true });
    await new Promise((done) => setImmediate(done));
    const opening = f.controller.setAudio({ muted: false, deafened: false });
    assert.equal(f.raw.enabled, true);
    confirm({ muted: true, deafened: true });
    await Promise.all([closing, opening]);
    assert.equal(f.controller.getSnapshot().muted, false);
    assert.equal(f.controller.getSnapshot().deafened, false);
    assert.equal(f.raw.enabled, true);
  } finally {
    await f.controller.leave();
  }
});
