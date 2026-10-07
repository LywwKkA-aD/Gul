import {
  Room,
  RoomEvent,
  type RoomOptions,
  type LocalAudioTrack,
  type LocalVideoTrack,
} from 'livekit-client';
import { installBundleWorkaround } from './sdp-bundle.ts';

interface RoomDependencies {
  readonly context?: () => AudioContext;
  readonly room?: (options: RoomOptions) => Room;
}
const contexts = new WeakMap<Room, { readonly context: AudioContext; closing?: Promise<void> }>();
async function closeContext(room: Room): Promise<void> {
  const owned = contexts.get(room);
  if (!owned) return;
  owned.closing ??= Promise.resolve()
    .then(() => owned.context.close())
    .catch(() => {});
  await owned.closing;
}
export function createRoom(kind: 'voice' | 'screen'): Room;
export function createRoom(dependencies?: RoomDependencies): Room;
export function createRoom(input: RoomDependencies | 'voice' | 'screen' = {}): Room {
  const dependencies = typeof input === 'string' ? {} : input;
  const context = (
    dependencies.context ?? (() => new AudioContext({ sampleRate: 48000, latencyHint: 'interactive' }))
  )();
  let room: Room | undefined;
  try {
    room = (dependencies.room ?? ((options) => new Room(options)))({
      webAudioMix: { audioContext: context },
      adaptiveStream: true,
      dynacast: true,
      stopLocalTrackOnUnpublish: true,
      audioCaptureDefaults: {
        sampleRate: 48000,
        channelCount: 1,
        echoCancellation: true,
        noiseSuppression: true,
        autoGainControl: true,
      },
    });
    contexts.set(room, { context });
    const created = room;
    room.on(RoomEvent.Disconnected, () => void closeContext(created));
    installBundleWorkaround(room);
    return room;
  } catch {
    if (room) void closeContext(room);
    else
      void Promise.resolve()
        .then(() => context.close())
        .catch(() => {});
    throw new Error('Не удалось запустить звук канала.');
  }
}

export async function disconnect(room?: Room): Promise<void> {
  try {
    await room?.disconnect(true);
  } catch {
    /* Raw SDK errors may contain grants. */
  } finally {
    if (room) await closeContext(room);
  }
}

export async function unpublish(room: Room, track: LocalAudioTrack | LocalVideoTrack): Promise<void> {
  try {
    await room.localParticipant.unpublishTrack(track, false);
  } catch {
    /* Closing room already removes publications. */
  }
}
