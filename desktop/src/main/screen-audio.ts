import { spawn, type SpawnOptions } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { access, lstat } from 'node:fs/promises';
import { constants } from 'node:fs';
import { isAbsolute } from 'node:path';
import type { EventEmitter } from 'node:events';
import type { Readable, Writable } from 'node:stream';
import type { DisplayCaptureConsent, AudioCaptureConsent } from './capture-consent.ts';
import { resolvePulseEndpoint, localPulseEndpoint } from './screen-audio-endpoint.ts';

export interface ScreenAudioLease {
  readonly leaseId: string;
  readonly deviceLabel: string;
}
export interface AudioHelperProcess extends Pick<EventEmitter, 'on' | 'once' | 'off'> {
  readonly stdin: Writable;
  readonly stdout: Readable;
  readonly exitCode: number | null;
  readonly signalCode: NodeJS.Signals | null;
  kill(signal?: NodeJS.Signals): boolean;
}
export interface NativeScreenAudioOptions {
  readonly executable: string;
  readonly consent: Pick<DisplayCaptureConsent, 'claimAudio'>;
  readonly onEnded?: (leaseId: string) => void;
  readonly platform?: string;
  readonly checkExecutable?: () => Promise<boolean>;
  readonly resolveEndpoint?: () => Promise<string | null>;
  readonly nonce?: () => string;
  readonly spawn?: (file: string, args: readonly string[], options: SpawnOptions) => AudioHelperProcess;
  readonly startupTimeoutMs?: number;
  readonly stopTimeoutMs?: number;
  readonly killGraceTimeoutMs?: number;
  readonly pollIntervalMs?: number;
}
interface Active {
  readonly child: AudioHelperProcess;
  readonly consent: AudioCaptureConsent;
  readonly lease: ScreenAudioLease;
  readonly generation: number;
  readonly endpoint: string;
  readonly resolve: (lease: ScreenAudioLease) => void;
  readonly reject: (error: Error) => void;
  readonly readyTimeout: ReturnType<typeof setTimeout>;
  readonly heartbeat: ReturnType<typeof setInterval>;
  ready: boolean;
  buffer: string;
}
const unavailable = () => new Error('GUL_SCREEN_AUDIO_UNAVAILABLE');
const validNonce = (value: string) => /^[a-f0-9]{32}$/u.test(value);

/** No PCM, process names, device IDs or command options are accepted from the renderer. */
export class NativeScreenAudio {
  private readonly options: NativeScreenAudioOptions;
  private active?: Active;
  private generation = 0;
  private readonly closing = new Set<Promise<void>>();
  constructor(options: NativeScreenAudioOptions) {
    this.options = options;
  }
  async available(): Promise<boolean> {
    if ((this.options.platform ?? process.platform) !== 'linux' || !isAbsolute(this.options.executable))
      return false;
    try {
      if (this.options.checkExecutable) {
        if (!(await this.options.checkExecutable())) return false;
      } else {
        const properties = await lstat(this.options.executable);
        if (!properties.isFile() || properties.isSymbolicLink()) return false;
        await access(this.options.executable, constants.X_OK);
      }
      return Boolean(await this.endpoint());
    } catch {
      return false;
    }
  }
  async start(): Promise<ScreenAudioLease> {
    const consent = this.options.consent.claimAudio();
    if (!consent?.valid()) throw unavailable();
    const generation = ++this.generation;
    await this.stopActive();
    if (!(await this.available()) || generation !== this.generation || !consent.valid()) throw unavailable();
    let endpoint: string | null;
    try {
      endpoint = await this.endpoint();
    } catch {
      throw unavailable();
    }
    if (!endpoint || generation !== this.generation || !consent.valid()) throw unavailable();
    const leaseId = this.options.nonce?.() ?? randomBytes(16).toString('hex');
    if (!validNonce(leaseId)) throw unavailable();
    const lease = Object.freeze({ leaseId, deviceLabel: `Gul-Screen-Audio-${leaseId}` });
    let child: AudioHelperProcess;
    try {
      child = this.launch(['--capture', leaseId], endpoint);
    } catch {
      throw unavailable();
    }
    let resolve!: (lease: ScreenAudioLease) => void;
    let reject!: (error: Error) => void;
    const pending = new Promise<ScreenAudioLease>((yes, no) => {
      resolve = yes;
      reject = no;
    });
    const active: Active = {
      child,
      consent,
      lease,
      generation,
      endpoint,
      resolve,
      reject,
      ready: false,
      buffer: '',
      readyTimeout: setTimeout(() => this.failed(active), this.options.startupTimeoutMs ?? 8000),
      heartbeat: setInterval(() => {
        if (!this.current(active)) {
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
    child.on('error', () => this.failed(active));
    child.once('exit', () => this.failed(active));
    child.stdout.on('data', (chunk: Buffer | string) => {
      if (this.active !== active) return;
      active.buffer += chunk.toString();
      if (active.buffer.length > 4096) {
        this.failed(active);
        return;
      }
      let line = active.buffer.indexOf('\n');
      while (line >= 0) {
        const value = active.buffer.slice(0, line);
        active.buffer = active.buffer.slice(line + 1);
        if (value !== 'READY' || active.ready || !this.current(active)) {
          this.failed(active);
          return;
        }
        active.ready = true;
        clearTimeout(active.readyTimeout);
        active.resolve(lease);
        line = active.buffer.indexOf('\n');
      }
    });
    return pending;
  }
  async stop(leaseId: string): Promise<void> {
    if (!validNonce(leaseId)) throw unavailable();
    if (this.active?.lease.leaseId !== leaseId) return;
    ++this.generation;
    await this.stopActive();
  }
  async close(): Promise<void> {
    ++this.generation;
    await this.stopActive();
    await Promise.all([...this.closing]);
  }
  private current(active: Active): boolean {
    try {
      return this.active === active && active.generation === this.generation && active.consent.valid();
    } catch {
      return false;
    }
  }
  private failed(active: Active): void {
    if (this.active !== active) return;
    if (active.ready) {
      try {
        this.options.onEnded?.(active.lease.leaseId);
      } catch {
        /* Capture still stops. */
      }
    }
    ++this.generation;
    void this.stopActive();
  }
  private async stopActive(): Promise<void> {
    const active = this.active;
    this.active = undefined;
    if (!active) return;
    clearTimeout(active.readyTimeout);
    clearInterval(active.heartbeat);
    if (!active.ready) active.reject(unavailable());
    const closing = (async () => {
      const closed = this.waitForExit(active.child, this.options.stopTimeoutMs ?? 2000);
      try {
        active.child.stdin.end();
      } catch {
        active.child.kill('SIGTERM');
      }
      if (!(await closed)) return;
      // PA modules survive an abnormal client exit; remove only this generated private pair.
      try {
        const cleanup = this.launch(['--cleanup', active.lease.leaseId], active.endpoint);
        cleanup.stdin.on('error', () => {});
        cleanup.on('error', () => {});
        await this.waitForExit(cleanup, this.options.stopTimeoutMs ?? 2000);
      } catch {
        /* Missing audio servers cannot keep live record streams after the helper exits. */
      }
    })();
    this.closing.add(closing);
    try {
      await closing;
    } finally {
      this.closing.delete(closing);
    }
  }
  private waitForExit(child: AudioHelperProcess, timeout: number): Promise<boolean> {
    if (child.exitCode !== null || child.signalCode !== null) return Promise.resolve(true);
    return new Promise((resolve) => {
      let complete = false;
      let grace: ReturnType<typeof setTimeout> | undefined;
      const finish = (exited: boolean) => {
        if (complete) return;
        complete = true;
        clearTimeout(timer);
        clearTimeout(grace);
        child.off('exit', exitedHandler);
        resolve(exited);
      };
      const exitedHandler = () => finish(true);
      const timer = setTimeout(() => {
        grace = setTimeout(() => finish(false), this.options.killGraceTimeoutMs ?? 1000);
        try {
          child.kill('SIGKILL');
        } catch {
          /* Wait for observed exit; a failed kill cannot establish that the process is closed. */
        }
      }, timeout);
      child.once('exit', exitedHandler);
    });
  }
  private async endpoint(): Promise<string | null> {
    return localPulseEndpoint(await (this.options.resolveEndpoint?.() ?? resolvePulseEndpoint()));
  }
  private launch(args: readonly string[], endpoint: string): AudioHelperProcess {
    const options: SpawnOptions = {
      stdio: ['pipe', 'pipe', 'ignore'],
      shell: false,
      env: {
        ...Object.fromEntries(
          ['HOME', 'XDG_RUNTIME_DIR', 'PULSE_RUNTIME_PATH', 'LANG', 'LC_ALL'].flatMap((name) =>
            process.env[name] ? [[name, process.env[name]]] : [],
          ),
        ),
        PULSE_SERVER: endpoint,
      },
    };
    if (this.options.spawn) return this.options.spawn(this.options.executable, args, options);
    const child = spawn(this.options.executable, [...args], options);
    if (!child.stdin || !child.stdout) {
      child.kill();
      throw unavailable();
    }
    return child as AudioHelperProcess;
  }
}
