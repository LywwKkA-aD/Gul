import { spawn, type ChildProcess } from 'node:child_process';
import type { PasswordStorageInfo, PasswordStorageRecovery } from '../shared/contracts.ts';
import { protectedStorage, type SafeStorageAdapter } from './storage.ts';
import {
  runPasswordStorageProcess,
  waitForPasswordStorageExit,
  type NativePasswordStorageState,
} from './password-storage-process.ts';

export { parsePasswordStorageState, type NativePasswordStorageState } from './password-storage-process.ts';
type Mode = 'status' | 'unlock';
interface Options {
  readonly platform: string;
  readonly executable: string;
  readonly applicationName: string;
  readonly safeStorage: SafeStorageAdapter;
  readonly run?: (mode: Mode, signal: AbortSignal) => Promise<NativePasswordStorageState>;
  readonly open?: () => Promise<boolean>;
}
const unavailable = (): PasswordStorageInfo =>
  Object.freeze({ provider: 'unavailable', state: 'unavailable', restartRequired: false });

/** This helper reads only key metadata; passwords and keyring secrets never cross its protocol. */
export class PasswordStorage {
  private readonly options: Options;
  private readonly abort = new AbortController();
  private snapshot: PasswordStorageInfo = unavailable();
  private queue: Promise<unknown> = Promise.resolve();
  private opening?: Promise<PasswordStorageRecovery>;
  private readonly helpers = new Set<ChildProcess>();
  constructor(options: Options) {
    this.options = options;
  }
  getSnapshot(): PasswordStorageInfo {
    return this.snapshot;
  }
  status(): Promise<PasswordStorageInfo> {
    return this.serialize(async () => {
      if (this.otherProvider())
        return (this.snapshot = Object.freeze({
          provider: 'other',
          state: 'unavailable',
          restartRequired: false,
        }));
      const state = await this.run('status');
      return this.update(state);
    });
  }
  unlock(): Promise<PasswordStorageRecovery> {
    this.opening ??= this.serialize(async () => {
      if (this.otherProvider() || this.abort.signal.aborted)
        return { state: 'unavailable', restartRequired: false } as const;
      const state = await this.run('unlock');
      const snapshot = this.update(state);
      return Object.freeze({
        state: state === 'ready' ? 'unlocked' : state === 'locked' ? 'cancelled' : state,
        restartRequired: snapshot.restartRequired,
      });
    }).finally(() => {
      this.opening = undefined;
    });
    return this.opening;
  }
  async open(): Promise<boolean> {
    if (this.otherProvider() || this.abort.signal.aborted) return false;
    try {
      return await (
        this.options.open ??
        (() =>
          new Promise<boolean>((resolve) => {
            const child = spawn('/usr/bin/seahorse', [], { shell: false, stdio: 'ignore', detached: true });
            child.once('error', () => resolve(false));
            child.once('spawn', () => {
              child.unref();
              resolve(true);
            });
          }))
      )();
    } catch {
      return false;
    }
  }
  async close(): Promise<void> {
    this.abort.abort();
    this.snapshot = unavailable();
    await Promise.all([...this.helpers].map((child) => waitForPasswordStorageExit(child)));
  }
  private otherProvider(): boolean {
    if (this.options.platform !== 'linux') return true;
    try {
      return ['kwallet', 'kwallet5', 'kwallet6'].includes(
        this.options.safeStorage.getSelectedStorageBackend(),
      );
    } catch {
      return false;
    }
  }
  private update(state: NativePasswordStorageState): PasswordStorageInfo {
    if (this.abort.signal.aborted || state === 'unavailable') return (this.snapshot = unavailable());
    this.snapshot = Object.freeze({
      provider: 'gnome',
      state: state === 'cancelled' ? 'locked' : state,
      restartRequired:
        state === 'ready' && !protectedStorage(this.options.safeStorage, this.options.platform),
    });
    return this.snapshot;
  }
  private async run(mode: Mode): Promise<NativePasswordStorageState> {
    if (this.abort.signal.aborted) return 'unavailable';
    try {
      const state = this.options.run
        ? await this.options.run(mode, this.abort.signal)
        : await this.execute(mode);
      return this.abort.signal.aborted ? 'unavailable' : state;
    } catch {
      return 'unavailable';
    }
  }
  private async execute(mode: Mode): Promise<NativePasswordStorageState> {
    const { executable, applicationName } = this.options;
    return runPasswordStorageProcess(executable, applicationName, mode, this.abort.signal, this.helpers);
  }
  private serialize<T>(operation: () => Promise<T>): Promise<T> {
    const result = this.queue.catch(() => {}).then(operation);
    this.queue = result;
    return result;
  }
}
