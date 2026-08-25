/** How a round-trip time reads to someone on a call.
 *
 * ITU-T G.114 puts the transparent limit at 150 ms one way, mouth to ear.
 * Our own path already spends 60-80 ms of that on capture, APM, the jitter
 * buffer, decode and playback, and the readout is a round trip - so the
 * budget left for the network, doubled, lands near 100 ms. Past 250 ms the
 * pauses get long enough that two people start talking over each other, the
 * way they do on a bad phone line. */
export type PingTone = 'none' | 'good' | 'usable' | 'bad';

export function pingTone(roundTripMs: number | null): PingTone {
  if (roundTripMs === null) return 'none';
  if (roundTripMs <= 100) return 'good';
  if (roundTripMs <= 250) return 'usable';
  return 'bad';
}
