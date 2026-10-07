import { Room, type LocalAudioTrack, type LocalVideoTrack } from 'livekit-client';
import { installBundleWorkaround } from './sdp-bundle.ts';

export function createRoom(): Room {
  const room = new Room({
    webAudioMix: true,
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
  installBundleWorkaround(room);
  return room;
}

export async function disconnect(room?: Room): Promise<void> {
  try {
    await room?.disconnect(true);
  } catch {
    /* Raw SDK errors may contain grants. */
  }
}

export async function unpublish(room: Room, track: LocalAudioTrack | LocalVideoTrack): Promise<void> {
  try {
    await room.localParticipant.unpublishTrack(track, false);
  } catch {
    /* Closing room already removes publications. */
  }
}
