import type { RemoteAudioTrack } from 'livekit-client';
import { participantId } from './protocol.ts';

type MediaKind = 'voice' | 'screen';
interface Binding {
  readonly identity: string;
  readonly track: RemoteAudioTrack;
  readonly element: HTMLAudioElement;
  readonly kind: MediaKind;
  readonly webAudioMix: boolean;
}

/** Participant preferences are separate from the effective gain while locally muted or deafened. */
export class Playback {
  private readonly bindings = new Map<string, Binding>();
  private readonly volumes = new Map<string, number>();
  private readonly muted = new Set<string>();
  private deafened = false;
  private readonly elementFactory: () => HTMLAudioElement;
  private readonly warn: () => void;

  constructor(elementFactory: () => HTMLAudioElement, warn: () => void) {
    this.elementFactory = elementFactory;
    this.warn = warn;
  }

  attach(
    sid: string,
    identity: string,
    track: RemoteAudioTrack,
    kind: MediaKind,
    webAudioMix: boolean,
  ): void {
    this.remove(sid);
    const element = this.elementFactory();
    if (element.dataset) {
      element.dataset.source = kind;
      element.dataset.participant = identity;
    }
    track.attach(element);
    const binding = Object.freeze({ identity, track, element, kind, webAudioMix });
    this.bindings.set(sid, binding);
    this.applyBinding(binding);
    void element.play().catch(() => {
      if (this.bindings.get(sid) === binding) this.warn();
    });
  }

  setUserVolume(identity: string, gain: number): void {
    if (!this.validIdentity(identity) || !Number.isFinite(gain)) return;
    this.volumes.set(identity, Math.max(0, Math.min(2, gain)));
    this.apply();
  }

  setUserMuted(identity: string, muted: boolean): void {
    if (!this.validIdentity(identity)) return;
    if (muted) this.muted.add(identity);
    else this.muted.delete(identity);
    this.apply();
  }

  apply(deafened = this.deafened): void {
    this.deafened = deafened;
    this.bindings.forEach((binding) => this.applyBinding(binding));
  }

  remove(sid: string): void {
    const binding = this.bindings.get(sid);
    if (!binding) return;
    this.bindings.delete(sid);
    binding.track.detach(binding.element);
    binding.element.remove();
  }

  removeParticipant(identity: string): void {
    this.bindings.forEach((binding, sid) => {
      if (binding.identity === identity) this.remove(sid);
    });
  }

  clear(kind?: MediaKind): void {
    this.bindings.forEach((binding, sid) => {
      if (kind === undefined || binding.kind === kind) this.remove(sid);
    });
  }

  reset(): void {
    this.clear();
    this.volumes.clear();
    this.muted.clear();
  }

  receivers(kind: MediaKind): readonly (RTCRtpReceiver | undefined)[] {
    return [...this.bindings.values()]
      .filter((binding) => binding.kind === kind)
      .map((binding) => binding.track.receiver);
  }

  private validIdentity(identity: string): boolean {
    return Boolean(participantId(identity, 'voice') || participantId(identity, 'screen'));
  }

  private applyBinding(binding: Binding): void {
    const silent = this.deafened || this.muted.has(binding.identity);
    const gain = silent ? 0 : (this.volumes.get(binding.identity) ?? 1);
    // SDK WebAudioMix owns the GainNode. Its HTML element must remain silent to avoid double playback.
    if (binding.webAudioMix) binding.element.volume = 0;
    binding.element.muted = binding.webAudioMix || silent;
    binding.track.setVolume(binding.webAudioMix ? gain : Math.min(1, gain));
  }
}
