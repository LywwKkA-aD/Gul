import { memberId, parseMemberKey, type MemberKey } from './member-key.ts';
export {
  memberId,
  memberCredential,
  parseMemberKey,
  createMemberCredential,
  type MemberKey,
} from './member-key.ts';
import type { PasswordStorageInfo } from '../shared/contracts.ts';
import type { MemberCredentialInfo, MemberIdentity } from '../shared/management.ts';
import { protectedStorage, readBoundedJSON, writePrivateJSON, type SafeStorageAdapter } from './storage.ts';
import { connectInput, failure } from './validation.ts';

const record = (value: unknown): value is Record<string, unknown> =>
  !!value && typeof value === 'object' && !Array.isArray(value);
interface StoredIdentity {
  readonly address: string;
  readonly serverId: string;
  readonly memberId: string;
  readonly encryptedCredential: string;
}
interface LoadedIdentity {
  readonly key: MemberKey;
  readonly rememberIdentity: boolean;
  readonly saveError?: 'unavailable' | 'write-failed';
}
export type ResolvedMemberCredential =
  | { readonly kind: 'ready'; readonly key: MemberKey; readonly rememberIdentity: boolean }
  | {
      readonly kind: 'required';
      readonly reason: 'unavailable' | 'unreadable';
      readonly serverId: string;
      readonly memberId: string;
    }
  | { readonly kind: 'missing' };
interface Options {
  readonly file: string;
  readonly safeStorage: SafeStorageAdapter;
  readonly platform: string;
  readonly passwordStorage?: () => PasswordStorageInfo;
}
function profile(value: unknown): string {
  return connectInput({ address: value, username: 'identity', password: '' }).address;
}
function cipher(value: unknown): value is string {
  return (
    typeof value === 'string' &&
    value.length > 0 &&
    value.length <= 16384 &&
    /^[A-Za-z0-9+/]+={0,2}$/u.test(value) &&
    Buffer.from(value, 'base64').toString('base64') === value
  );
}

/** Permanent keys never cross IPC. Persistent secrets require independent, explicit identity consent. */
export class MemberCredentialStore {
  private readonly options: Options;
  private records: readonly StoredIdentity[] = Object.freeze([]);
  private memory: ReadonlyMap<string, LoadedIdentity> = new Map();
  private loaded?: Promise<void>;
  private pending = Promise.resolve();
  constructor(options: Options) {
    this.options = options;
  }
  load(): Promise<void> {
    return (this.loaded ??= this.read());
  }
  private async read(): Promise<void> {
    const data = await readBoundedJSON(this.options.file);
    if (!record(data) || data.version !== 1 || !Array.isArray(data.identities)) return;
    const seen = new Set<string>();
    this.records = Object.freeze(
      data.identities.slice(0, 8).flatMap((entry) => {
        if (
          !record(entry) ||
          !memberId(entry.serverId) ||
          !memberId(entry.memberId) ||
          !cipher(entry.encryptedCredential)
        )
          return [];
        try {
          const address = profile(entry.address);
          if (seen.has(address)) return [];
          seen.add(address);
          return [
            Object.freeze({
              address,
              serverId: entry.serverId,
              memberId: entry.memberId,
              encryptedCredential: entry.encryptedCredential,
            }),
          ];
        } catch {
          return [];
        }
      }),
    );
  }
  stage(address: string, value: unknown, rememberIdentity: boolean): void {
    const key = parseMemberKey(value),
      target = profile(address);
    if (typeof rememberIdentity !== 'boolean') throw failure('GUL_INPUT_INVALID');
    this.memory = new Map([
      [target, Object.freeze({ key, rememberIdentity })],
      ...[...this.memory].filter(([name]) => name !== target).slice(0, 7),
    ]);
  }
  resolve(address: string): ResolvedMemberCredential {
    const target = profile(address),
      loaded = this.memory.get(target);
    if (loaded) return { kind: 'ready', key: loaded.key, rememberIdentity: loaded.rememberIdentity };
    const stored = this.records.find((entry) => entry.address === target);
    if (!stored) return { kind: 'missing' };
    const required = (reason: 'unavailable' | 'unreadable'): ResolvedMemberCredential => ({
      kind: 'required',
      reason,
      serverId: stored.serverId,
      memberId: stored.memberId,
    });
    if (!this.protected()) return required('unavailable');
    try {
      const data: unknown = JSON.parse(
        this.options.safeStorage.decryptString(Buffer.from(stored.encryptedCredential, 'base64')),
      );
      if (!record(data) || data.address !== target) throw Error();
      const key = parseMemberKey(data.key);
      if (key.serverId !== stored.serverId || key.memberId !== stored.memberId) throw Error();
      return { kind: 'ready', key, rememberIdentity: true };
    } catch {
      return required('unreadable');
    }
  }
  describe(address: string): MemberCredentialInfo {
    const resolved = this.resolve(address),
      loaded = this.memory.get(profile(address));
    if (resolved.kind === 'missing')
      return { state: 'none', serverId: null, memberId: null, rememberIdentity: false, usable: false };
    if (resolved.kind === 'required')
      return {
        state: resolved.reason,
        serverId: resolved.serverId,
        memberId: resolved.memberId,
        rememberIdentity: true,
        usable: false,
      };
    return {
      state: loaded
        ? loaded.saveError || !loaded.rememberIdentity
          ? 'loaded'
          : this.records.some((entry) => entry.address === profile(address))
            ? 'saved'
            : 'loaded'
        : 'saved',
      serverId: resolved.key.serverId,
      memberId: resolved.key.memberId,
      rememberIdentity: resolved.rememberIdentity,
      usable: true,
      ...(loaded?.saveError ? { saveError: loaded.saveError } : {}),
    };
  }
  confirm(address: string, serverId: string, member: MemberIdentity): Promise<MemberCredentialInfo> {
    return this.serialize(async () => {
      const target = profile(address),
        resolved = this.resolve(target);
      if (
        resolved.kind !== 'ready' ||
        serverId !== resolved.key.serverId ||
        member.id !== resolved.key.memberId ||
        !['owner', 'member'].includes(member.role)
      )
        throw failure('GUL_MEMBER_MISMATCH');
      let encryptedCredential: string | undefined, saveError: LoadedIdentity['saveError'];
      if (resolved.rememberIdentity) {
        if (!this.protected()) saveError = 'unavailable';
        else
          try {
            encryptedCredential = this.options.safeStorage
              .encryptString(JSON.stringify({ address: target, key: resolved.key }))
              .toString('base64');
            if (!cipher(encryptedCredential)) throw Error();
          } catch {
            saveError = 'unavailable';
          }
      }
      if (!saveError) {
        const next = Object.freeze(
          [
            ...(encryptedCredential
              ? [
                  Object.freeze({
                    address: target,
                    serverId,
                    memberId: resolved.key.memberId,
                    encryptedCredential,
                  }),
                ]
              : []),
            ...this.records.filter((entry) => entry.address !== target),
          ].slice(0, 8),
        );
        if (await this.save(next)) this.records = next;
        else saveError = 'write-failed';
      }
      this.memory = new Map(this.memory).set(
        target,
        Object.freeze({
          key: resolved.key,
          rememberIdentity: resolved.rememberIdentity,
          ...(saveError ? { saveError } : {}),
        }),
      );
      return this.describe(target);
    });
  }
  forget(address: string): Promise<{ readonly persisted: boolean }> {
    return this.serialize(async () => {
      const target = profile(address),
        next = Object.freeze(this.records.filter((entry) => entry.address !== target));
      if (!(await this.save(next))) return { persisted: false };
      this.records = next;
      this.memory = new Map([...this.memory].filter(([name]) => name !== target));
      return { persisted: true };
    });
  }
  private protected(): boolean {
    const health = this.options.passwordStorage?.();
    if (
      this.options.platform === 'linux' &&
      health &&
      health.provider !== 'other' &&
      health.state !== 'ready'
    )
      return false;
    return protectedStorage(this.options.safeStorage, this.options.platform);
  }
  private save(records: readonly StoredIdentity[]): Promise<boolean> {
    return writePrivateJSON(this.options.file, { version: 1, identities: records });
  }
  private serialize<T>(action: () => Promise<T>): Promise<T> {
    const operation = this.pending.then(async () => {
      await this.load();
      return action();
    });
    this.pending = operation.then(
      () => {},
      () => {},
    );
    return operation;
  }
}
