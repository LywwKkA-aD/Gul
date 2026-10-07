export type SoundCue =
  'connect' | 'disconnect' | 'reconnect' | 'mute' | 'unmute' | 'deafen' | 'screen-start' | 'screen-stop';

interface Tone {
  readonly from: number;
  readonly to: number;
  readonly duration: number;
}
const tones: Readonly<Record<SoundCue, Tone>> = Object.freeze({
  connect: Object.freeze({ from: 523, to: 659, duration: 0.18 }),
  disconnect: Object.freeze({ from: 659, to: 392, duration: 0.18 }),
  reconnect: Object.freeze({ from: 440, to: 440, duration: 0.1 }),
  mute: Object.freeze({ from: 523, to: 392, duration: 0.12 }),
  unmute: Object.freeze({ from: 392, to: 523, duration: 0.12 }),
  deafen: Object.freeze({ from: 523, to: 329, duration: 0.14 }),
  'screen-start': Object.freeze({ from: 659, to: 783, duration: 0.16 }),
  'screen-stop': Object.freeze({ from: 783, to: 523, duration: 0.16 }),
});
interface ActiveTone {
  readonly oscillator: OscillatorNode;
  readonly gain: GainNode;
  readonly release: () => void;
}

/** Optional quiet UI tones own one context and one short oscillator, separate from media processing. */
export class SoundCues {
  private readonly factory: () => AudioContext;
  private context?: AudioContext;
  private active?: ActiveTone;
  private stopping?: Promise<void>;
  private closed = false;
  private revision = 0;
  constructor(factory: () => AudioContext = () => new AudioContext()) {
    this.factory = factory;
  }
  async play(cue: SoundCue, enabled: boolean, deafened: boolean): Promise<void> {
    const revision = ++this.revision;
    if (this.closed || !enabled || deafened || !Object.hasOwn(tones, cue)) {
      await this.stop();
      return;
    }
    try {
      const context = (this.context ??= this.factory());
      if (context.state === 'closed') return;
      if (context.state !== 'running') await context.resume();
      if (this.closed || this.revision !== revision) return;
      await this.stop();
      if (this.closed || this.revision !== revision) return;
      this.start(context, tones[cue]);
    } catch {
      // Autoplay restrictions or an unavailable device must not fail the user's app action.
    }
  }
  async close(): Promise<void> {
    if (this.closed) return;
    this.closed = true;
    ++this.revision;
    await this.stop();
    const context = this.context;
    this.context = undefined;
    try {
      await context?.close();
    } catch {
      /* A context already closed by the OS has no resources left to release. */
    }
  }
  private start(context: AudioContext, tone: Tone): void {
    const oscillator = context.createOscillator();
    const gain = context.createGain();
    let released = false;
    const active: ActiveTone = {
      oscillator,
      gain,
      release: () => {
        if (released) return;
        released = true;
        oscillator.onended = null;
        oscillator.disconnect();
        gain.disconnect();
        if (this.active === active) this.active = undefined;
      },
    };
    try {
      const now = context.currentTime;
      oscillator.type = 'sine';
      oscillator.frequency.setValueAtTime(tone.from, now);
      oscillator.frequency.linearRampToValueAtTime(tone.to, now + tone.duration * 0.65);
      gain.gain.setValueAtTime(0, now);
      gain.gain.linearRampToValueAtTime(0.025, now + 0.008);
      gain.gain.linearRampToValueAtTime(0.025, now + tone.duration * 0.55);
      gain.gain.linearRampToValueAtTime(0, now + tone.duration);
      oscillator.connect(gain);
      gain.connect(context.destination);
      oscillator.onended = active.release;
      this.active = active;
      oscillator.start(now);
      oscillator.stop(now + tone.duration + 0.005);
    } catch {
      active.release();
    }
  }
  private stop(): Promise<void> {
    if (this.stopping) return this.stopping;
    const active = this.active;
    const context = this.context;
    if (!active || !context) return Promise.resolve();
    this.active = undefined;
    let finish!: () => void;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const stopping = new Promise<void>((resolve) => {
      finish = () => {
        if (timer) clearTimeout(timer);
        active.release();
        resolve();
      };
    }).finally(() => {
      if (this.stopping === stopping) this.stopping = undefined;
    });
    this.stopping = stopping;
    active.oscillator.onended = finish;
    try {
      const now = context.currentTime;
      active.gain.gain.cancelAndHoldAtTime(now);
      active.gain.gain.linearRampToValueAtTime(0, now + 0.008);
      active.oscillator.stop(now + 0.01);
      timer = setTimeout(finish, 50);
    } catch {
      finish();
    }
    return stopping;
  }
}
