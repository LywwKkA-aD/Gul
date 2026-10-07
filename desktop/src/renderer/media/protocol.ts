import type { MediaGrant, MediaSession } from '../../shared/contracts.ts';

export function participantId(identity: string, role: 'voice' | 'screen'): number | undefined {
  const found = /^(voice|screen)\.([1-9][0-9]{0,9})$/.exec(identity);
  if (!found || found[1] !== role) return;
  const value = Number(found[2]);
  return value <= 0x7fffffff ? value : undefined;
}
export function validGrant(grant: MediaGrant, session: MediaSession, role: 'voice' | 'screen'): boolean {
  let url: URL;
  try {
    url = new URL(grant.url);
  } catch {
    return false;
  }
  return (
    url.protocol === 'ws:' &&
    url.hostname === '127.0.0.1' &&
    !url.username &&
    !url.password &&
    Boolean(url.port) &&
    Boolean(grant.token) &&
    participantId(grant.identity, role) === session.sessionId &&
    grant.ownerIdentity === `voice.${session.sessionId}` &&
    grant.sessionId === session.sessionId &&
    grant.channelId === session.channelId &&
    grant.room === session.grant.room &&
    grant.revision === session.revision
  );
}
export function validText(value: unknown): value is string {
  return (
    typeof value === 'string' &&
    value.trim().length > 0 &&
    [...value].length <= 5000 &&
    !/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/u.test(value)
  );
}
export function chatText(data: Uint8Array, identity: string, topic: string | undefined): string | undefined {
  if (topic !== 'gul.chat.v1' || !participantId(identity, 'voice') || data.byteLength > 24000) return;
  try {
    const payload: unknown = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(data));
    if (!payload || typeof payload !== 'object') return;
    const text = (payload as { text?: unknown }).text;
    return validText(text) ? text : undefined;
  } catch {
    return;
  }
}
/** Read an actual selected and responsive ICE pair; absent statistics remain unknown. */
export function latency(report: RTCStatsReport): number | undefined {
  const selected = new Set<string>();
  report.forEach((entry) => {
    if (entry.type === 'transport' && typeof entry.selectedCandidatePairId === 'string')
      selected.add(entry.selectedCandidatePairId);
  });
  let worst: number | undefined;
  report.forEach((entry) => {
    const seconds = entry.currentRoundTripTime;
    if (
      entry.type !== 'candidate-pair' ||
      entry.state !== 'succeeded' ||
      !(selected.has(entry.id) || entry.nominated === true) ||
      !(entry.responsesReceived > 0) ||
      typeof seconds !== 'number' ||
      !Number.isFinite(seconds) ||
      seconds < 0
    )
      return;
    worst = Math.max(worst ?? 0, seconds * 1000);
  });
  return worst;
}
