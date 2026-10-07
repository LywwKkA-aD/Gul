import {
  createLocalAudioTrack,
  createLocalScreenTracks,
  type LocalAudioTrack,
  type LocalVideoTrack,
} from 'livekit-client';
import type { ScreenCapture } from './model.ts';
import { defaultVoiceSettings, type VoiceSettings } from './voice-gate.ts';
import { screenResolution } from './screen-settings.ts';
import { captureFailureName } from './capture-diagnostics.ts';
import { attachLinuxScreenAudio, findPrivateAudioDevice } from './linux-screen-audio.ts';

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
export async function captureScreen(withAudio: boolean): Promise<ScreenCapture> {
  let display: ScreenCapture | undefined;
  try {
    const tracks = await createLocalScreenTracks({
      resolution: screenResolution,
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
    display = { tracks: tracks as (LocalVideoTrack | LocalAudioTrack)[] };
    if (!withAudio) return display;
    const capabilities = await window.gul.captureCapabilities();
    if (capabilities.platform !== 'linux') return display;
    // The Linux display handler never grants Chromium's total sink loopback. Defense in
    // depth stops any unexpected native audio before acquiring the private excluded mix.
    const video = tracks.filter((track): track is LocalVideoTrack => track.kind === 'video');
    tracks.filter((track) => track.kind === 'audio').forEach((track) => track.stop());
    const linuxDisplay: ScreenCapture = { tracks: video };
    display = linuxDisplay;
    if (video.length !== 1) throw new Error('GUL_SCREEN_AUDIO_UNAVAILABLE');
    if (!capabilities.systemAudio || !capabilities.ownAudioExcluded)
      throw new Error('GUL_SCREEN_AUDIO_UNAVAILABLE');
    return await attachLinuxScreenAudio(linuxDisplay, {
      start: () => window.gul.screenAudioStart(),
      stop: (leaseId) => window.gul.screenAudioStop(leaseId),
      onEnded: (listener) => window.gul.onScreenAudioEnded(listener),
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
