import { randomBytes } from 'node:crypto';
import type { DisplayCaptureConsent, AudioCaptureConsent } from './capture-consent.ts';
import type { AudioHelperProcess } from './screen-audio.ts';
import { WindowsAudioFrames } from './windows-audio-protocol.ts';
import { WindowsAudioBridge } from './windows-audio-bridge.ts';
import {
  executableAvailable,
  launchWindowsAudio,
  stopWindowsAudio,
  type WindowsAudioProcessOptions,
} from './windows-audio-process.ts';

export interface WindowsScreenAudioLease {
  readonly leaseId: string;
  readonly url: string;
}
export interface WindowsScreenAudioOptions extends WindowsAudioProcessOptions {
  readonly consent: Pick<DisplayCaptureConsent, 'claimAudio'>;
  readonly platform?: string;
  readonly nonce?: (bytes: number) => string;
  readonly onEnded?: (leaseId: string) => void;
  readonly probeTimeoutMs?: number;
  readonly startupTimeoutMs?: number;
  readonly pollIntervalMs?: number;
}
interface Active {
  readonly child: AudioHelperProcess;
  readonly bridge: WindowsAudioBridge;
  readonly parser: WindowsAudioFrames;
  readonly lease: WindowsScreenAudioLease;
  readonly consent: AudioCaptureConsent;
  readonly generation: number;
  readonly timer: ReturnType<typeof setTimeout>;
  readonly heartbeat: ReturnType<typeof setInterval>;
  readonly resolve: (lease: WindowsScreenAudioLease) => void;
  readonly reject: (error: Error) => void;
  ready: boolean;
}
const unavailable = () => new Error('GUL_SCREEN_AUDIO_UNAVAILABLE');
const validId = (id: string) => /^[a-f0-9]{32}$/u.test(id);

/** Captures only EXCLUDE_PARENT_PROCESS_TREE. No full system mix fallback is permitted. */
export class WindowsScreenAudio {
  private readonly options: WindowsScreenAudioOptions;
  private generation = 0;
  private probeEpoch = 0;
  private probe?: Promise<boolean>;
  private probeChild?: AudioHelperProcess;
  private active?: Active;
  private readonly closing = new Set<Promise<void>>();
  constructor(options: WindowsScreenAudioOptions) {
    this.options = options;
  }
  async available(): Promise<boolean> {
    if ((this.options.platform ?? process.platform) !== 'win32') return false;
    if (!this.probe) this.probe = this.check(this.probeEpoch);
    return this.probe;
  }
  networkAllowed(url: string): boolean {
    const active = this.active;
    return Boolean(active?.ready && this.current(active) && url === active.lease.url);
  }
  async start(): Promise<WindowsScreenAudioLease> {
    const consent = this.options.consent.claimAudio();
    if (!consent?.valid()) throw unavailable();
    const generation = ++this.generation;
    await this.stopActive();
    await Promise.all([...this.closing]);
    if (!(await this.available()) || !this.valid(generation, consent)) throw unavailable();
    const leaseId = this.nonce(16);
    if (!validId(leaseId)) throw unavailable();
    const bridge = new WindowsAudioBridge(
      this.nonce(24),
      () => this.valid(generation, consent),
      () => {
        if (this.active?.generation === generation) this.failed(this.active);
      },
    );
    let url: string;
    try {
      url = await bridge.listen();
    } catch {
      await bridge.close();
      throw unavailable();
    }
    if (!this.valid(generation, consent)) {
      await bridge.close();
      throw unavailable();
    }
    let child: AudioHelperProcess;
    try {
      child = launchWindowsAudio(this.options, '--capture');
    } catch {
      await bridge.close();
      throw unavailable();
    }
    const lease = Object.freeze({ leaseId, url });
    let resolve!: (lease: WindowsScreenAudioLease) => void;
    let reject!: (error: Error) => void;
    const pending = new Promise<WindowsScreenAudioLease>((yes, no) => {
      resolve = yes;
      reject = no;
    });
    const parser = new WindowsAudioFrames(
      () => {
        if (!this.current(active)) {
          this.failed(active);
          return;
        }
        active.ready = true;
        clearTimeout(active.timer);
        active.resolve(lease);
      },
      (frame) => {
        if (!this.current(active)) this.failed(active);
        else bridge.send(frame);
      },
    );
    const active: Active = {
      child,
      bridge,
      parser,
      lease,
      consent,
      generation,
      resolve,
      reject,
      ready: false,
      timer: setTimeout(() => this.failed(active), this.options.startupTimeoutMs ?? 8000),
      heartbeat: setInterval(() => {
        if (!this.current(active) || child.stdin.writableLength > 512) {
          this.failed(active);
          return;
        }
        try {
          child.stdin.write('PING\n');
        } catch {
          this.failed(active);
        }
      }, this.options.pollIntervalMs ?? 500),
    };
    this.active = active;
    child.stdin.on('error', () => this.failed(active));
    child.once('error', () => this.failed(active));
    child.once('exit', () => this.failed(active));
    child.stdout.once('error', () => this.failed(active));
    child.stdout.on('data', (chunk: Buffer) => {
      if (this.active !== active) return;
      try {
        parser.push(chunk);
      } catch {
        this.failed(active);
      }
    });
    return pending;
  }
  async stop(leaseId: string): Promise<void> {
    if (!validId(leaseId)) throw unavailable();
    if (this.active?.lease.leaseId !== leaseId) return;
    ++this.generation;
    await this.stopActive();
  }
  async close(): Promise<void> {
    ++this.generation;
    ++this.probeEpoch;
    const probe = this.probeChild;
    this.probe = undefined;
    await this.stopActive();
    if (probe) await stopWindowsAudio(probe, this.options);
    await Promise.all([...this.closing]);
  }
  private nonce(bytes: number): string {
    return this.options.nonce?.(bytes) ?? randomBytes(bytes).toString('hex');
  }
  private valid(generation: number, consent: AudioCaptureConsent): boolean {
    try {
      return generation === this.generation && consent.valid();
    } catch {
      return false;
    }
  }
  private current(active: Active): boolean {
    return this.active === active && this.valid(active.generation, active.consent);
  }
  private failed(active: Active): void {
    if (this.active !== active) return;
    if (active.ready) {
      try {
        this.options.onEnded?.(active.lease.leaseId);
      } catch {
        /* Capture still closes. */
      }
    }
    ++this.generation;
    void this.stopActive();
  }
  private async stopActive(): Promise<void> {
    const active = this.active;
    this.active = undefined;
    if (!active) return;
    clearTimeout(active.timer);
    clearInterval(active.heartbeat);
    active.parser.close();
    if (!active.ready) active.reject(unavailable());
    const cleanup = Promise.all([active.bridge.close(), stopWindowsAudio(active.child, this.options)]).then(
      () => {},
    );
    this.closing.add(cleanup);
    try {
      await cleanup;
    } finally {
      this.closing.delete(cleanup);
    }
  }
  private async check(epoch: number): Promise<boolean> {
    if (!(await executableAvailable(this.options)) || epoch !== this.probeEpoch) return false;
    let child: AudioHelperProcess;
    try {
      child = launchWindowsAudio(this.options, '--probe');
    } catch {
      return false;
    }
    this.probeChild = child;
    return new Promise<boolean>((resolve) => {
      let buffer = '';
      let done = false;
      let exited = false;
      let exitCode: number | null = null;
      let drained = false;
      const finish = (supported: boolean) => {
        if (done) return;
        done = true;
        clearTimeout(timer);
        if (this.probeChild === child) this.probeChild = undefined;
        void stopWindowsAudio(child, this.options).then(() =>
          resolve(supported && epoch === this.probeEpoch),
        );
      };
      const timer = setTimeout(() => finish(false), this.options.probeTimeoutMs ?? 7000);
      const complete = () => {
        if (exited && drained) finish(exitCode === 0 && buffer === 'SUPPORTED\n');
      };
      child.stdin.on('error', () => finish(false));
      child.once('error', () => finish(false));
      child.once('exit', (code: number | null) => {
        exited = true;
        exitCode = code;
        complete();
      });
      child.stdout.once('end', () => {
        drained = true;
        complete();
      });
      child.stdout.once('error', () => finish(false));
      child.stdout.on('data', (chunk: Buffer) => {
        buffer += chunk.toString('latin1');
        if (buffer.length > 64 || !'SUPPORTED\n'.startsWith(buffer)) finish(false);
      });
    });
  }
}
