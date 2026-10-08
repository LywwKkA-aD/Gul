import { Track, type TrackPublication } from 'livekit-client';
import type { ScreenInfo } from './model.ts';

/** Voice-observer metadata can lead the independent media signal flow. Known
 * replacement SIDs must survive late events; absent metadata keeps discovery.
 */
export function screenEventCurrent(
  screen: ScreenInfo | undefined,
  publication: Pick<TrackPublication, 'source' | 'trackSid'>,
): boolean {
  if (!screen) return true;
  if (publication.source === Track.Source.ScreenShare) return screen.videoSid === publication.trackSid;
  if (publication.source === Track.Source.ScreenShareAudio) return screen.audioSid === publication.trackSid;
  return true;
}
