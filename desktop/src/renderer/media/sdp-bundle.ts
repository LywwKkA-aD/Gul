import type { Room } from 'livekit-client';

interface Section {
  readonly lines: readonly string[];
  readonly mid?: string;
  readonly vp8: readonly number[];
}
const fmtp = (section: Section, payload: number): string =>
  section.lines.find((line) => line.startsWith(`a=fmtp:${payload} `))?.slice(`a=fmtp:${payload} `.length) ??
  '';
const withoutBitrate = (config: string): string =>
  config
    .split(';')
    .map((value) => value.trim())
    .filter((value) => value && !/^x-google-start-bitrate\s*=/i.test(value))
    .sort()
    .join(';');

/** LiveKit 2.22.3 omits missing fmtp entries when conforming its unused video transceivers.
 * Repair only VP8's encoder hint in a local BUNDLE, preserving codec profiles and media fields.
 * Remove this workaround when the pinned SDK handles absent fmtp (including the regression tests).
 */
export function conformVp8Placeholders(sdp: string, placeholders: ReadonlySet<string>): string {
  if (!placeholders.size) return sdp;
  const newline = sdp.includes('\r\n') ? '\r\n' : '\n';
  const lines = sdp.split(newline);
  const session: string[] = [];
  const media: string[][] = [];
  for (const line of lines) {
    if (line.startsWith('m=')) media.push([line]);
    else if (media.length) media[media.length - 1].push(line);
    else session.push(line);
  }
  const groups = session
    .filter((line) => line.startsWith('a=group:BUNDLE '))
    .map((line) => new Set(line.slice('a=group:BUNDLE '.length).trim().split(/\s+/)));
  const sections: Section[] = media.map((values) => ({
    lines: values,
    mid: values.find((line) => line.startsWith('a=mid:'))?.slice(6),
    vp8:
      /^m=video /.test(values[0]) && (!/^m=video 0\b/.test(values[0]) || values.includes('a=bundle-only'))
        ? values.flatMap((line) => {
            const match = /^a=rtpmap:(\d+) VP8\/90000$/i.exec(line);
            return match ? [Number(match[1])] : [];
          })
        : [],
  }));
  const replacements = new Map<Section, readonly string[]>();
  for (const group of groups) {
    const bundled = sections.filter((section) => section.mid && group.has(section.mid));
    for (const placeholder of bundled.filter((section) => section.mid && placeholders.has(section.mid))) {
      let values = placeholder.lines;
      for (const payload of placeholder.vp8) {
        const candidates = bundled.filter((section) => section.vp8.includes(payload));
        const real = candidates.filter((section) => section.mid && !placeholders.has(section.mid));
        const canonical = fmtp(real[0] ?? candidates[0], payload);
        // Different real codec constraints require negotiation, never arbitrary SDP rewriting.
        if (real.some((section) => fmtp(section, payload) !== canonical)) continue;
        const current = fmtp(placeholder, payload);
        if (current === canonical || withoutBitrate(current) !== withoutBitrate(canonical)) continue;
        const prefix = `a=fmtp:${payload} `;
        const existing = values.some((line) => line.startsWith(prefix));
        values = values.flatMap((line) =>
          line.startsWith(prefix) ? (canonical ? [prefix + canonical] : []) : [line],
        );
        if (canonical && !existing) {
          const index = values.findIndex((line) => line.startsWith(`a=rtpmap:${payload} `));
          values = [...values.slice(0, index + 1), prefix + canonical, ...values.slice(index + 1)];
        }
      }
      if (values !== placeholder.lines) replacements.set(placeholder, values);
    }
  }
  return replacements.size
    ? [...session, ...sections.flatMap((section) => replacements.get(section) ?? section.lines)].join(newline)
    : sdp;
}

interface Transport {
  getTransceivers(): readonly {
    readonly mid: string | null;
    readonly direction?: string;
    readonly sender: { readonly track: { readonly readyState?: string } | null };
  }[];
  setMungedSDP(sd: RTCSessionDescriptionInit, munged?: string, remote?: boolean): Promise<void>;
}
const guarded = new WeakSet<object>();
const ownedRooms = new WeakSet<Room>();
const ownedEngines = new WeakSet<Engine>();
interface Engine {
  on(event: string, listener: (publisher: Transport) => void): unknown;
}

function placeholderMids(transport: Transport, sdp: string, localOffer: boolean): ReadonlySet<string> {
  const transceivers = transport.getTransceivers();
  const assigned = new Map(transceivers.filter((t) => t.mid !== null).map((t) => [t.mid!, t]));
  const unassigned = transceivers.some((t) => t.mid === null);
  return new Set(
    sdp.split(/(?=m=)/).flatMap((section) => {
      const mid = /^a=mid:([^\r\n]+)$/m.exec(section)?.[1];
      if (!mid) return [];
      const transceiver = assigned.get(mid);
      // Initial createOffer has SDP mids before getTransceivers assigns them. An
      // explicit non-sending section is safe to conform without positional mapping.
      const placeholder = transceiver
        ? !transceiver.sender.track ||
          transceiver.direction === 'inactive' ||
          transceiver.sender.track.readyState === 'ended'
        : localOffer && unassigned && /^a=(recvonly|inactive)\r?$/m.test(section);
      return placeholder ? [mid] : [];
    }),
  );
}

/** Instance-local integration for the pinned SDK's publisher offers and munged answers.
 * Remote offers and original server descriptions remain untouched.
 */
export function installBundleWorkaround(room: Room): void {
  if (ownedRooms.has(room)) return;
  const watch = (next?: Engine) => {
    if (!next || ownedEngines.has(next)) return;
    ownedEngines.add(next);
    next.on('transportsCreated', (publisher) => {
      if (guarded.has(publisher)) return;
      if (typeof publisher.setMungedSDP !== 'function' || typeof publisher.getTransceivers !== 'function')
        throw new Error('Версия WebRTC транспорта несовместима с демонстрацией.');
      const original = publisher.setMungedSDP;
      publisher.setMungedSDP = function (sd, munged, remote) {
        const localOffer = !remote && sd.type === 'offer';
        const publisherAnswer = remote && sd.type === 'answer';
        const outgoing =
          munged && (localOffer || publisherAnswer)
            ? conformVp8Placeholders(munged, placeholderMids(this, munged, localOffer))
            : munged;
        return original.call(this, sd, outgoing, remote);
      };
      guarded.add(publisher);
    });
  };
  let engine = room.engine as unknown as Engine | undefined;
  watch(engine);
  // Full reconnect replaces Room.engine. Bind before the new engine configures its transports.
  Object.defineProperty(room, 'engine', {
    configurable: true,
    enumerable: true,
    get: () => engine,
    set: (next: Engine | undefined) => {
      engine = next;
      watch(next);
    },
  });
  ownedRooms.add(room);
}
