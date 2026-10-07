import type { Snapshot } from './model.ts';
import { participantId } from './protocol.ts';

/** Local speech uses Chromium's worklet; remote speech retains LiveKit's normal cadence. */
export function speakingIdentities(
  snapshot: Snapshot,
  sdkSpeakers: readonly string[],
  identity: string | undefined,
  previous: readonly string[],
): readonly string[] {
  const remote = [
    ...new Set(sdkSpeakers.filter((value) => value !== identity && participantId(value, 'voice'))),
  ];
  const local =
    identity &&
    snapshot.state === 'connected' &&
    !snapshot.muted &&
    !snapshot.deafened &&
    snapshot.voiceSettings.inputGain > 0 &&
    (snapshot.voiceProcessingAvailable ? snapshot.voiceActive : sdkSpeakers.includes(identity));
  const next = local ? [...remote, identity] : remote;
  return next.length === previous.length && next.every((value, index) => value === previous[index])
    ? previous
    : Object.freeze(next);
}
