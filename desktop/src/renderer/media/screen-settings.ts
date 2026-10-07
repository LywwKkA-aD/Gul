import { AudioPresets, Track, type TrackPublishOptions } from 'livekit-client';

export const screenResolution = Object.freeze({ width: 1280, height: 720, frameRate: 30 });
export function screenPublishOptions(video: boolean, h264: boolean): TrackPublishOptions {
  return video
    ? {
        source: Track.Source.ScreenShare,
        videoCodec: h264 ? 'h264' : 'vp8',
        simulcast: false,
        screenShareEncoding: { maxBitrate: 2_000_000, maxFramerate: 30 },
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
