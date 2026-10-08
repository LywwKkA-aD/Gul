import type { ImportMemberCredential, MemberCredentialInfo, RedeemInvitation } from '../shared/management.ts';
import type { SessionAuthority } from './session.ts';
import { MemberCredentialStore, parseMemberKey } from './member-credentials.ts';
import { readBoundedJSON } from './storage.ts';
import { connectInput, failure } from './validation.ts';

/** OS file selection stays in main and is fenced against profile/session replacement. */
export class MemberAccess {
  private operation = 0;
  private readonly authority: Pick<
    SessionAuthority,
    'idle' | 'operationRevision' | 'redeemInvitation' | 'forgetIdentity'
  >;
  private readonly store: MemberCredentialStore;
  constructor(
    authority: Pick<SessionAuthority, 'idle' | 'operationRevision' | 'redeemInvitation' | 'forgetIdentity'>,
    store: MemberCredentialStore,
  ) {
    this.authority = authority;
    this.store = store;
  }
  describe(address: string): MemberCredentialInfo {
    return this.store.describe(address);
  }
  async import(
    value: ImportMemberCredential,
    choose: () => Promise<string | null>,
  ): Promise<MemberCredentialInfo | null> {
    if (
      !value ||
      typeof value !== 'object' ||
      Array.isArray(value) ||
      Object.keys(value).some((key) => !['address', 'rememberIdentity'].includes(key)) ||
      typeof value.rememberIdentity !== 'boolean'
    )
      throw failure('GUL_INPUT_INVALID');
    const address = connectInput({ address: value.address, username: 'identity', password: '' }).address;
    const revision = this.authority.operationRevision(),
      operation = ++this.operation;
    const assertCurrent = () => {
      if (
        operation !== this.operation ||
        revision !== this.authority.operationRevision() ||
        !this.authority.idle()
      )
        throw failure('GUL_SESSION_STALE');
    };
    assertCurrent();
    const path = await choose();
    assertCurrent();
    if (path === null) return null;
    const data = await readBoundedJSON(path, 4096);
    assertCurrent();
    let key;
    try {
      key = parseMemberKey(data);
    } catch {
      throw failure('GUL_MEMBER_IMPORT_FAILED');
    }
    this.store.stage(address, key, value.rememberIdentity);
    return this.store.describe(address);
  }
  async redeem(value: RedeemInvitation): Promise<MemberCredentialInfo> {
    const operation = ++this.operation,
      revision = this.authority.operationRevision() + 1;
    const key = await this.authority.redeemInvitation(value);
    if (
      operation !== this.operation ||
      revision !== this.authority.operationRevision() ||
      !this.authority.idle()
    )
      throw failure('GUL_SESSION_STALE');
    this.store.stage(value.input.address, key, value.rememberIdentity);
    return this.store.confirm(value.input.address, key.serverId, { id: key.memberId, role: 'member' });
  }
  consent(value: ImportMemberCredential): MemberCredentialInfo {
    if (
      !value ||
      typeof value !== 'object' ||
      Array.isArray(value) ||
      Object.keys(value).some((key) => !['address', 'rememberIdentity'].includes(key)) ||
      typeof value.rememberIdentity !== 'boolean'
    )
      throw failure('GUL_INPUT_INVALID');
    if (!this.authority.idle()) throw failure('GUL_SESSION_STALE');
    const identity = this.store.resolve(value.address);
    if (identity.kind !== 'ready') throw failure('GUL_MEMBER_KEY_REQUIRED');
    ++this.operation;
    this.store.stage(value.address, identity.key, value.rememberIdentity);
    return this.store.describe(value.address);
  }
  async clear(address: string): Promise<void> {
    if (!this.authority.idle()) throw failure('GUL_SESSION_STALE');
    ++this.operation;
    if (!(await this.store.forget(address)).persisted) throw failure('GUL_STORAGE_WRITE_FAILED');
    this.authority.forgetIdentity(address);
  }
}
