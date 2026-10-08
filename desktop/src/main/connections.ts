import type { ConnectInput, MediaSession, ServerList } from '../shared/contracts.ts';
import type { SessionAuthority } from './session.ts';
import type { MemberCredentialStore } from './member-credentials.ts';
import type { SavedServerStore } from './servers.ts';
import { connectInput, failure } from './validation.ts';

const record = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);

/** Consent and operation fences keep keyring writes outside the renderer and canceled logins. */
export class ConnectionManager {
  private operation = 0;
  private saveNotice: ServerList['lastSave'] = null;
  private readonly authority: Pick<SessionAuthority, 'connect' | 'disconnect' | 'mediaEpoch'>;
  private readonly store: Pick<SavedServerStore, 'remember' | 'resolve'>;
  private readonly identities?: Pick<MemberCredentialStore, 'resolve' | 'confirm'>;
  constructor(
    authority: Pick<SessionAuthority, 'connect' | 'disconnect' | 'mediaEpoch'>,
    store: Pick<SavedServerStore, 'remember' | 'resolve'>,
    identities?: Pick<MemberCredentialStore, 'resolve' | 'confirm'>,
  ) {
    this.authority = authority;
    this.store = store;
    this.identities = identities;
  }
  async connect(value: unknown): Promise<MediaSession> {
    if (
      !record(value) ||
      Object.keys(value).some((key) => !['input', 'rememberPassword'].includes(key)) ||
      typeof value.rememberPassword !== 'boolean'
    )
      throw failure('GUL_INPUT_INVALID');
    return this.accept(connectInput(value.input), value.rememberPassword);
  }
  async connectSaved(value: unknown): Promise<MediaSession> {
    if (
      !record(value) ||
      Object.keys(value).some((key) => !['address', 'username', 'rememberPassword'].includes(key)) ||
      (value.rememberPassword !== undefined && typeof value.rememberPassword !== 'boolean')
    )
      throw failure('GUL_INPUT_INVALID');
    const metadata = connectInput({ address: value.address, username: value.username, password: '' });
    const saved = this.store.resolve(metadata.address);
    if (saved.kind !== 'ready') throw failure('GUL_SAVED_PASSWORD_REQUIRED');
    return this.accept({ ...saved.input, username: metadata.username }, value.rememberPassword !== false);
  }
  lastSave(): ServerList['lastSave'] {
    return this.saveNotice;
  }
  async disconnect(): Promise<void> {
    ++this.operation;
    await this.authority.disconnect();
  }
  private async accept(input: ConnectInput, rememberPassword: boolean): Promise<MediaSession> {
    const operation = ++this.operation;
    const identity = this.identities?.resolve(input.address);
    if (identity?.kind === 'required') throw failure('GUL_MEMBER_KEY_REQUIRED');
    const session = await this.authority.connect(
      input,
      identity?.kind === 'ready' ? identity.key : undefined,
    );
    if (operation !== this.operation || this.authority.mediaEpoch() !== session.epoch)
      throw failure('GUL_SESSION_STALE');
    if (identity?.kind === 'ready') {
      if (!session.serverId || !session.member) throw failure('GUL_MEMBER_MISMATCH');
      await this.identities!.confirm(input.address, session.serverId, session.member);
      if (operation !== this.operation || this.authority.mediaEpoch() !== session.epoch)
        throw failure('GUL_SESSION_STALE');
    }
    const saved = await this.store
      .remember({ ...input, password: rememberPassword ? input.password : '' })
      .catch(() => ({
        passwordSaved: false,
        persisted: false,
        storage: 'unavailable' as const,
        status: 'write-failed' as const,
      }));
    if (operation !== this.operation || this.authority.mediaEpoch() !== session.epoch)
      throw failure('GUL_SESSION_STALE');
    this.saveNotice = Object.freeze({
      address: input.address,
      status: saved.status,
      persisted: saved.persisted,
    });
    return session;
  }
}
