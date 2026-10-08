import type { ConnectInput, SavedServerInfo, ServerList, PasswordStorageInfo } from '../shared/contracts.ts';
import { parseRealityProfile } from '../transport/profile.ts';
import { connectInput, failure } from './validation.ts';
import { protectedStorage, readBoundedJSON, writePrivateJSON, type SafeStorageAdapter } from './storage.ts';

export const SAVED_SERVER_LIMIT = 8;
export type SavedServer = SavedServerInfo;
export type SaveStatus = NonNullable<ServerList['lastSave']>['status'];
interface StoredServer extends Omit<SavedServer, 'hasPassword' | 'passwordStatus'> {
  readonly encryptedPassword?: string;
  readonly passwordBlocked?: true;
  readonly passwordFailure?: 'unavailable' | 'encrypt-failed';
}
export type SavedConnection =
  | { readonly kind: 'ready'; readonly input: ConnectInput }
  | {
      readonly kind: 'password-required';
      readonly address: string;
      readonly username: string;
      readonly reason: 'unavailable' | 'locked' | 'unreadable' | 'missing' | 'save-failed';
    }
  | { readonly kind: 'missing' };
export interface RememberResult {
  readonly passwordSaved: boolean;
  readonly persisted: boolean;
  readonly storage: 'protected' | 'unavailable';
  readonly status: SaveStatus;
}
interface Options {
  readonly file: string;
  readonly safeStorage: SafeStorageAdapter;
  readonly platform: string;
  readonly now?: () => number;
  readonly passwordStorage?: () => PasswordStorageInfo;
}
interface FailedWrite {
  readonly address: string;
  readonly usablePreviousPassword: boolean;
  readonly rememberPassword: boolean;
}
const record = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);
function metadata(value: unknown): { address: string; username: string } | undefined {
  if (!record(value)) return;
  try {
    const input = connectInput({ address: value.address, username: value.username, password: '' });
    parseRealityProfile(input.address);
    return { address: input.address, username: input.username };
  } catch {
    return;
  }
}
function ciphertext(value: unknown): string | undefined {
  if (typeof value !== 'string' || !value || value.length > 16384 || !/^[A-Za-z0-9+/]+={0,2}$/u.test(value))
    return;
  return Buffer.from(value, 'base64').toString('base64') === value ? value : undefined;
}

/** Main-only picker. Plain passwords never become renderer data or persisted JSON. */
export class SavedServerStore {
  private readonly options: Options;
  private records: readonly StoredServer[] = Object.freeze([]);
  private loaded?: Promise<void>;
  private queue: Promise<unknown> = Promise.resolve();
  private failedWrites: readonly FailedWrite[] = Object.freeze([]);
  constructor(options: Options) {
    this.options = options;
  }

  load(): Promise<void> {
    this.loaded ??= this.read();
    return this.loaded;
  }
  private async read(): Promise<void> {
    const document = await readBoundedJSON(this.options.file);
    if (!record(document) || document.version !== 1 || !Array.isArray(document.servers)) return;
    this.records = this.decode(document.servers, false);
  }
  list(): readonly SavedServer[] {
    return Object.freeze(
      this.records.map((entry) => {
        const resolved = this.resolve(entry.address);
        const failedWrite = this.failedWrites.find((failure) => failure.address === entry.address);
        const passwordStatus =
          failedWrite || entry.passwordFailure === 'encrypt-failed' || entry.passwordBlocked
            ? 'save-failed'
            : resolved.kind === 'ready'
              ? 'saved'
              : resolved.kind === 'password-required' && resolved.reason === 'unavailable'
                ? 'unavailable'
                : resolved.kind === 'password-required' && resolved.reason === 'locked'
                  ? 'locked'
                  : resolved.kind === 'password-required' && resolved.reason === 'unreadable'
                    ? 'unreadable'
                    : 'missing';
        return Object.freeze({
          address: entry.address,
          username: entry.username,
          lastUsed: entry.lastUsed,
          rememberPassword: failedWrite?.rememberPassword ?? entry.rememberPassword,
          hasPassword: resolved.kind === 'ready',
          passwordStatus,
        });
      }),
    );
  }
  storageStatus(): 'protected' | 'unavailable' {
    const health = this.options.passwordStorage?.();
    if (
      this.options.platform === 'linux' &&
      health &&
      health.provider !== 'other' &&
      health.state !== 'ready'
    )
      return 'unavailable';
    return protectedStorage(this.options.safeStorage, this.options.platform) ? 'protected' : 'unavailable';
  }
  /** Call only after the server accepts a login; renderer never calls this directly. */
  remember(value: ConnectInput): Promise<RememberResult> {
    return this.serialize(async () => {
      const input = connectInput(value);
      if (!metadata(input)) throw failure('GUL_INPUT_INVALID');
      const storage = this.storageStatus();
      const previous = this.records.find((entry) => entry.address === input.address);
      const previousPassword = this.resolve(input.address);
      const usablePreviousPassword =
        !!input.password &&
        previousPassword.kind === 'ready' &&
        previousPassword.input.password === input.password;
      const rememberPassword = !!input.password;
      let encryptedPassword: string | undefined;
      let passwordFailure: StoredServer['passwordFailure'];
      if (input.password && storage === 'protected') {
        try {
          encryptedPassword = ciphertext(
            this.options.safeStorage
              .encryptString(JSON.stringify({ address: input.address, password: input.password }))
              .toString('base64'),
          );
          if (!encryptedPassword) passwordFailure = 'encrypt-failed';
        } catch {
          passwordFailure = 'encrypt-failed';
        }
      } else if (rememberPassword) passwordFailure = 'unavailable';
      if (passwordFailure) encryptedPassword = previous?.encryptedPassword;
      const lastUsed = Math.max(
        0,
        (this.options.now ?? Date.now)(),
        ...this.records.map((entry) => entry.lastUsed),
      );
      const next = Object.freeze(
        [
          Object.freeze({
            address: input.address,
            username: input.username,
            lastUsed,
            rememberPassword,
            ...(encryptedPassword ? { encryptedPassword } : {}),
            ...(passwordFailure ? { passwordFailure } : {}),
            ...(passwordFailure && encryptedPassword && !usablePreviousPassword
              ? { passwordBlocked: true as const }
              : {}),
          }),
          ...this.records.filter((entry) => entry.address !== input.address),
        ].slice(0, SAVED_SERVER_LIMIT),
      );
      const persisted = await this.save(next);
      if (persisted) {
        this.records = next;
        this.failedWrites = Object.freeze(
          this.failedWrites.filter(
            (failure) =>
              failure.address !== input.address && next.some((entry) => entry.address === failure.address),
          ),
        );
      } else if (previous)
        this.failedWrites = Object.freeze(
          [
            Object.freeze({ address: input.address, usablePreviousPassword, rememberPassword }),
            ...this.failedWrites.filter((failure) => failure.address !== input.address),
          ].slice(0, SAVED_SERVER_LIMIT),
        );
      return {
        passwordSaved: persisted && !passwordFailure && Boolean(encryptedPassword),
        persisted,
        storage,
        status: !persisted
          ? 'write-failed'
          : (passwordFailure ?? (rememberPassword ? 'saved' : 'not-requested')),
      };
    });
  }
  forget(address: string): Promise<{ readonly persisted: boolean }> {
    return this.serialize(async () => {
      if (typeof address !== 'string' || address.length > 4096) throw failure('GUL_INPUT_INVALID');
      const next = Object.freeze(this.records.filter((entry) => entry.address !== address.trim()));
      const persisted = await this.save(next);
      if (persisted) {
        this.records = next;
        this.failedWrites = Object.freeze(
          this.failedWrites.filter((failure) => failure.address !== address.trim()),
        );
      }
      return { persisted };
    });
  }
  /** The ready branch contains a secret and must be consumed in main, never sent over IPC. */
  resolve(address: string): SavedConnection {
    const entry = this.records.find((candidate) => candidate.address === address);
    if (!entry) return { kind: 'missing' };
    const required = (
      reason: 'unavailable' | 'locked' | 'unreadable' | 'missing' | 'save-failed',
    ): SavedConnection => ({
      kind: 'password-required',
      address: entry.address,
      username: entry.username,
      reason,
    });
    const failedWrite = this.failedWrites.find((failure) => failure.address === address);
    if (entry.passwordBlocked || (failedWrite && !failedWrite.usablePreviousPassword))
      return required('save-failed');
    if (this.options.platform === 'linux' && this.options.passwordStorage?.().state === 'locked')
      return required('locked');
    if (this.storageStatus() !== 'protected') return required('unavailable');
    if (!entry.encryptedPassword) return required('missing');
    try {
      const decrypted: unknown = JSON.parse(
        this.options.safeStorage.decryptString(Buffer.from(entry.encryptedPassword, 'base64')),
      );
      if (
        !record(decrypted) ||
        decrypted.address !== entry.address ||
        typeof decrypted.password !== 'string' ||
        !decrypted.password ||
        decrypted.password.length > 1024
      )
        return required('unreadable');
      return {
        kind: 'ready',
        input: { address: entry.address, username: entry.username, password: decrypted.password },
      };
    } catch {
      return required('unreadable');
    }
  }
  /** Only non-secret metadata is migrated; legacy keyring entries are never read. */
  importLegacy(document: unknown): Promise<{ readonly persisted: boolean }> {
    return this.serialize(async () => {
      if (this.records.length || !record(document) || !Array.isArray(document.servers))
        return { persisted: true };
      const next = this.decode(document.servers, true);
      const persisted = await this.save(next);
      if (persisted) this.records = next;
      return { persisted };
    });
  }
  private decode(values: readonly unknown[], legacy: boolean): readonly StoredServer[] {
    const seen = new Set<string>();
    const entries = values.slice(0, 128).flatMap((value) => {
      const info = metadata(value);
      if (!info || !record(value) || seen.has(info.address)) return [];
      seen.add(info.address);
      const timestamp =
        legacy && typeof value.last_used === 'number' ? value.last_used * 1000 : value.lastUsed;
      const lastUsed =
        typeof timestamp === 'number' && Number.isSafeInteger(timestamp) && timestamp >= 0 ? timestamp : 0;
      const encryptedPassword =
        legacy || value.rememberPassword === false ? undefined : ciphertext(value.encryptedPassword);
      const rememberPassword = !legacy && (value.rememberPassword === true || !!encryptedPassword);
      const passwordFailure =
        rememberPassword && ['unavailable', 'encrypt-failed'].includes(value.passwordFailure as string)
          ? (value.passwordFailure as StoredServer['passwordFailure'])
          : undefined;
      return [
        Object.freeze({
          ...info,
          lastUsed,
          rememberPassword,
          ...(encryptedPassword ? { encryptedPassword } : {}),
          ...(passwordFailure ? { passwordFailure } : {}),
          ...(rememberPassword && value.passwordBlocked === true ? { passwordBlocked: true as const } : {}),
        }),
      ];
    });
    return Object.freeze(entries.sort((a, b) => b.lastUsed - a.lastUsed).slice(0, SAVED_SERVER_LIMIT));
  }
  private save(records: readonly StoredServer[]): Promise<boolean> {
    return writePrivateJSON(this.options.file, { version: 1, servers: records });
  }
  private serialize<T>(operation: () => Promise<T>): Promise<T> {
    const result = this.queue
      .catch(() => {})
      .then(async () => {
        await this.load();
        return operation();
      });
    this.queue = result;
    return result;
  }
}
