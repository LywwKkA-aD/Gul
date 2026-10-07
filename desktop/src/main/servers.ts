import type { ConnectInput } from '../shared/contracts.ts';
import { parseRealityProfile } from '../transport/profile.ts';
import { connectInput, failure } from './validation.ts';
import { protectedStorage, readBoundedJSON, writePrivateJSON, type SafeStorageAdapter } from './storage.ts';

export const SAVED_SERVER_LIMIT = 8;
export interface SavedServer {
  readonly address: string;
  readonly username: string;
  readonly lastUsed: number;
  readonly hasPassword: boolean;
}
interface StoredServer extends Omit<SavedServer, 'hasPassword'> {
  readonly encryptedPassword?: string;
}
export type SavedConnection =
  | { readonly kind: 'ready'; readonly input: ConnectInput }
  | {
      readonly kind: 'password-required';
      readonly address: string;
      readonly username: string;
      readonly reason: 'unavailable' | 'locked' | 'missing';
    }
  | { readonly kind: 'missing' };
export interface RememberResult {
  readonly passwordSaved: boolean;
  readonly persisted: boolean;
  readonly storage: 'protected' | 'unavailable';
}
interface Options {
  readonly file: string;
  readonly safeStorage: SafeStorageAdapter;
  readonly platform: string;
  readonly now?: () => number;
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
      this.records.map(({ address, username, lastUsed }) =>
        Object.freeze({ address, username, lastUsed, hasPassword: this.resolve(address).kind === 'ready' }),
      ),
    );
  }
  storageStatus(): 'protected' | 'unavailable' {
    return protectedStorage(this.options.safeStorage, this.options.platform) ? 'protected' : 'unavailable';
  }
  /** Call only after the server accepts a login; renderer never calls this directly. */
  remember(value: ConnectInput): Promise<RememberResult> {
    return this.serialize(async () => {
      const input = connectInput(value);
      if (!metadata(input)) throw failure('GUL_INPUT_INVALID');
      const storage = this.storageStatus();
      let encryptedPassword: string | undefined;
      if (input.password && storage === 'protected') {
        try {
          encryptedPassword = ciphertext(
            this.options.safeStorage
              .encryptString(JSON.stringify({ address: input.address, password: input.password }))
              .toString('base64'),
          );
        } catch {
          /* Keyring refusal costs saved credentials, never the accepted login. */
        }
      }
      const lastUsed = Math.max(
        0,
        (this.options.now ?? Date.now)(),
        ...this.records.map((entry) => entry.lastUsed),
      );
      this.records = Object.freeze(
        [
          Object.freeze({
            address: input.address,
            username: input.username,
            lastUsed,
            ...(encryptedPassword ? { encryptedPassword } : {}),
          }),
          ...this.records.filter((entry) => entry.address !== input.address),
        ].slice(0, SAVED_SERVER_LIMIT),
      );
      return { passwordSaved: Boolean(encryptedPassword), persisted: await this.save(), storage };
    });
  }
  forget(address: string): Promise<{ readonly persisted: boolean }> {
    return this.serialize(async () => {
      if (typeof address !== 'string' || address.length > 4096) throw failure('GUL_INPUT_INVALID');
      this.records = Object.freeze(this.records.filter((entry) => entry.address !== address.trim()));
      return { persisted: await this.save() };
    });
  }
  /** The ready branch contains a secret and must be consumed in main, never sent over IPC. */
  resolve(address: string): SavedConnection {
    const entry = this.records.find((candidate) => candidate.address === address);
    if (!entry) return { kind: 'missing' };
    const required = (reason: 'unavailable' | 'locked' | 'missing'): SavedConnection => ({
      kind: 'password-required',
      address: entry.address,
      username: entry.username,
      reason,
    });
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
        return required('locked');
      return {
        kind: 'ready',
        input: { address: entry.address, username: entry.username, password: decrypted.password },
      };
    } catch {
      return required('locked');
    }
  }
  /** Only non-secret metadata is migrated; legacy keyring entries are never read. */
  importLegacy(document: unknown): Promise<{ readonly persisted: boolean }> {
    return this.serialize(async () => {
      if (this.records.length || !record(document) || !Array.isArray(document.servers))
        return { persisted: true };
      this.records = this.decode(document.servers, true);
      return { persisted: await this.save() };
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
      const encryptedPassword = legacy ? undefined : ciphertext(value.encryptedPassword);
      return [Object.freeze({ ...info, lastUsed, ...(encryptedPassword ? { encryptedPassword } : {}) })];
    });
    return Object.freeze(entries.sort((a, b) => b.lastUsed - a.lastUsed).slice(0, SAVED_SERVER_LIMIT));
  }
  private save(): Promise<boolean> {
    return writePrivateJSON(this.options.file, { version: 1, servers: this.records });
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
