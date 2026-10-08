import { AudioPresets, Track, type TrackPublishOptions } from 'livekit-client';

export type ScreenQuality = '720p30' | '720p60' | '1080p30' | '1080p60';
export interface ScreenPreset {
  readonly id: ScreenQuality;
  readonly label: string;
  readonly width: number;
  readonly height: number;
  readonly frameRate: number;
  readonly maxBitrate: number;
}
export const defaultScreenQuality: ScreenQuality = '720p30';
export const screenPresets: readonly ScreenPreset[] = Object.freeze([
  Object.freeze({
    id: '720p30',
    label: '720p · 30 FPS',
    width: 1280,
    height: 720,
    frameRate: 30,
    maxBitrate: 2_000_000,
  }),
  Object.freeze({
    id: '720p60',
    label: '720p · 60 FPS',
    width: 1280,
    height: 720,
    frameRate: 60,
    maxBitrate: 4_000_000,
  }),
  Object.freeze({
    id: '1080p30',
    label: '1080p · 30 FPS',
    width: 1920,
    height: 1080,
    frameRate: 30,
    maxBitrate: 5_000_000,
  }),
  Object.freeze({
    id: '1080p60',
    label: '1080p · 60 FPS',
    width: 1920,
    height: 1080,
    frameRate: 60,
    maxBitrate: 8_000_000,
  }),
]);
export function parseScreenQuality(value: unknown): ScreenQuality {
  return typeof value === 'string' && screenPresets.some((preset) => preset.id === value)
    ? (value as ScreenQuality)
    : defaultScreenQuality;
}
export function screenPreset(quality: ScreenQuality): ScreenPreset {
  return screenPresets.find((preset) => preset.id === parseScreenQuality(quality))!;
}
export function screenVideoConstraints(quality: ScreenQuality): MediaTrackConstraints {
  const preset = screenPreset(quality);
  return {
    width: { max: preset.width },
    height: { max: preset.height },
    frameRate: { max: preset.frameRate },
  };
}
export function screenPublishOptions(
  video: boolean,
  h264: boolean,
  quality: ScreenQuality = defaultScreenQuality,
): TrackPublishOptions {
  const preset = screenPreset(quality);
  return video
    ? {
        source: Track.Source.ScreenShare,
        videoCodec: h264 ? 'h264' : 'vp8',
        simulcast: false,
        screenShareEncoding: { maxBitrate: preset.maxBitrate, maxFramerate: preset.frameRate },
        degradationPreference: 'maintain-framerate',
      }
    : {
        source: Track.Source.ScreenShareAudio,
        audioPreset: AudioPresets.musicHighQualityStereo,
        forceStereo: true,
        dtx: false,
        red: true,
      };
}
