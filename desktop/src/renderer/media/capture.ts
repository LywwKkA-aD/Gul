import {
  createLocalAudioTrack,
  createLocalScreenTracks,
  type LocalAudioTrack,
  type LocalVideoTrack,
  type ScreenShareCaptureOptions,
} from 'livekit-client';
import type { ScreenCapture } from './model.ts';
import { defaultVoiceSettings, type VoiceSettings } from './voice-gate.ts';
import { defaultScreenQuality, screenPreset, type ScreenQuality } from './screen-settings.ts';
import { captureFailureName } from './capture-diagnostics.ts';
import { attachLinuxScreenAudio, findPrivateAudioDevice } from './linux-screen-audio.ts';
import { attachWindowsScreenAudio } from './windows-screen-audio.ts';

export function voiceCaptureOptions(settings: VoiceSettings, deviceId?: string) {
  return {
    ...(deviceId ? { deviceId: { exact: deviceId } } : {}),
    channelCount: 1,
    sampleRate: 48000,
    echoCancellation: settings.echoCancellation,
    noiseSuppression: settings.noiseSuppression,
    autoGainControl: settings.autoGainControl,
  };
}
export function microphone(
  deviceId?: string,
  settings: VoiceSettings = defaultVoiceSettings,
): Promise<LocalAudioTrack> {
  return createLocalAudioTrack(voiceCaptureOptions(settings, deviceId));
}
export function screenCaptureOptions(
  withAudio: boolean,
  quality: ScreenQuality = defaultScreenQuality,
): ScreenShareCaptureOptions {
  const { width, height, frameRate } = screenPreset(quality);
  return {
    resolution: { width, height, frameRate },
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
  };
}
export async function captureScreen(
  withAudio: boolean,
  quality: ScreenQuality = defaultScreenQuality,
): Promise<ScreenCapture> {
  let display: ScreenCapture | undefined;
  try {
    const tracks = await createLocalScreenTracks(screenCaptureOptions(withAudio, quality));
    display = { tracks: tracks as (LocalVideoTrack | LocalAudioTrack)[] };
    if (!withAudio) return display;
    const capabilities = await window.gul.captureCapabilities();
    if (!['linux', 'win32'].includes(capabilities.platform)) return display;
    // Native helpers exclude Gul. Stop any unexpected Chromium full mix before
    // acquiring the consent-bound source so a voice cannot return through a share.
    const video = tracks.filter((track): track is LocalVideoTrack => track.kind === 'video');
    tracks.filter((track) => track.kind === 'audio').forEach((track) => track.stop());
    const nativeDisplay: ScreenCapture = { tracks: video };
    display = nativeDisplay;
    if (video.length !== 1) throw new Error('GUL_SCREEN_AUDIO_UNAVAILABLE');
    if (!capabilities.systemAudio || !capabilities.ownAudioExcluded) return nativeDisplay;
    const nativeAudio = {
      start: () => window.gul.screenAudioStart(),
      stop: (leaseId) => window.gul.screenAudioStop(leaseId),
      onEnded: (listener) => window.gul.onScreenAudioEnded(listener),
    } satisfies Pick<
      import('./linux-screen-audio.ts').LinuxScreenAudioDependencies,
      'start' | 'stop' | 'onEnded'
    >;
    if (capabilities.platform === 'win32') return await attachWindowsScreenAudio(nativeDisplay, nativeAudio);
    return await attachLinuxScreenAudio(nativeDisplay, {
      ...nativeAudio,
      findDevice: (label) => findPrivateAudioDevice(label),
      capture: (deviceId) =>
        createLocalAudioTrack({
          deviceId: { exact: deviceId },
          channelCount: 2,
          sampleRate: 48000,
          echoCancellation: false,
          noiseSuppression: false,
          autoGainControl: false,
        }),
    });
  } catch (error) {
    display?.tracks.forEach((track) => track.stop());
    display?.cleanup?.();
    console.debug('GUL_CAPTURE_FAILURE', captureFailureName(error));
    throw error;
  }
}
export function audioElement(): HTMLAudioElement {
  const element = document.createElement('audio');
  element.autoplay = true;
  element.hidden = true;
  document.body.append(element);
  return element;
}
