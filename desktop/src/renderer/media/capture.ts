import {
  createLocalAudioTrack,
  createLocalScreenTracks,
  type LocalAudioTrack,
  type LocalVideoTrack,
} from 'livekit-client';
import type { ScreenCapture } from './model.ts';

export function microphone(deviceId?: string): Promise<LocalAudioTrack> {
  return createLocalAudioTrack({
    ...(deviceId ? { deviceId: { exact: deviceId } } : {}),
    channelCount: 1,
    sampleRate: 48000,
    echoCancellation: true,
    noiseSuppression: true,
    autoGainControl: true,
  });
}
export async function captureScreen(withAudio: boolean): Promise<ScreenCapture> {
  const tracks = await createLocalScreenTracks({
    resolution: { width: 1280, height: 720, frameRate: 30 },
    audio: withAudio
      ? {
          channelCount: 2,
          sampleRate: 48000,
          echoCancellation: false,
          noiseSuppression: false,
          autoGainControl: false,
          restrictOwnAudio: true,
        }
      : false,
    systemAudio: withAudio ? 'include' : 'exclude',
    selfBrowserSurface: 'exclude',
    contentHint: 'motion',
  });
  return { tracks: tracks as (LocalVideoTrack | LocalAudioTrack)[] };
}
export function audioElement(): HTMLAudioElement {
  const element = document.createElement('audio');
  element.autoplay = true;
  element.hidden = true;
  document.body.append(element);
  return element;
}
