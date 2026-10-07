import type { ConnectInput, MediaSession } from '../shared/contracts.ts';
import type { SessionAuthority } from './session.ts';
import type { SavedServerStore } from './servers.ts';
import { connectInput, failure } from './validation.ts';

const record = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);

/** Consent and operation fences keep keyring writes outside the renderer and canceled logins. */
export class ConnectionManager {
  private operation = 0;
  private readonly authority: Pick<SessionAuthority, 'connect' | 'disconnect' | 'mediaEpoch'>;
  private readonly store: Pick<SavedServerStore, 'remember' | 'resolve'>;
  constructor(
    authority: Pick<SessionAuthority, 'connect' | 'disconnect' | 'mediaEpoch'>,
    store: Pick<SavedServerStore, 'remember' | 'resolve'>,
  ) {
    this.authority = authority;
    this.store = store;
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
    if (!record(value) || Object.keys(value).some((key) => !['address', 'username'].includes(key)))
      throw failure('GUL_INPUT_INVALID');
    const metadata = connectInput({ address: value.address, username: value.username, password: '' });
    const saved = this.store.resolve(metadata.address);
    if (saved.kind !== 'ready') throw failure('GUL_SAVED_PASSWORD_REQUIRED');
    return this.accept({ ...saved.input, username: metadata.username }, true);
  }
  async disconnect(): Promise<void> {
    ++this.operation;
    await this.authority.disconnect();
  }
  private async accept(input: ConnectInput, rememberPassword: boolean): Promise<MediaSession> {
    const operation = ++this.operation;
    const session = await this.authority.connect(input);
    if (operation !== this.operation || this.authority.mediaEpoch() !== session.epoch)
      throw failure('GUL_SESSION_STALE');
    await this.store.remember({ ...input, password: rememberPassword ? input.password : '' }).catch(() => {});
    if (operation !== this.operation || this.authority.mediaEpoch() !== session.epoch)
      throw failure('GUL_SESSION_STALE');
    return session;
  }
}
