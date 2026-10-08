import type {
  AudioState,
  BrokerState,
  ConnectInput,
  MediaGrant,
  MediaSession,
  ScreenRequest,
} from '../shared/contracts.ts';
import type {
  ManagementContext,
  ChannelCreate,
  ChannelUpdate,
  ChannelDelete,
  ChannelPermissions,
  MemberList,
  Invitation,
  RedeemInvitation,
} from '../shared/management.ts';
import { GatewayError } from '../transport/errors.ts';
import {
  parseMemberKey,
  createMemberCredential,
  memberCredential,
  memberId,
  type MemberKey,
} from './member-credentials.ts';
import {
  serverInfo,
  managedMetadata,
  managementContext,
  createChannelInput,
  updateChannelInput,
  deleteChannelInput,
  channelPermissions,
  memberList,
  invitation,
} from './management-validation.ts';
import { allowedNetwork } from './security.ts';
import {
  audioInput,
  brokerState,
  channelId,
  connectInput,
  failure,
  loginResponse,
  mediaGrant,
  screenInput,
  type Login,
} from './validation.ts';

export interface SessionGateway {
  readonly brokerOrigin: string;
  request<T>(method: 'GET' | 'POST', path: string, token?: string, body?: unknown): Promise<T>;
  beginEpoch(epoch: number): void;
  signalURL(epoch: number, token: string): string;
  close(): Promise<void>;
}
export type GatewayFactory = (input: Pick<ConnectInput, 'address' | 'password'>) => Promise<SessionGateway>;
interface Active {
  readonly gateway: SessionGateway;
  readonly login: Login;
  readonly epoch: number;
}

/** Broker credentials are owned by main; every renderer result is epoch fenced. */
export class SessionAuthority {
  private readonly createGateway: GatewayFactory;
  private active?: Active;
  private transitioning?: Active;
  private epoch = 0;
  private endpoints: readonly string[] = [];
  private mutations: Promise<void> = Promise.resolve();
  private readonly closing = new WeakMap<SessionGateway, Promise<void>>();
  private readonly gateways = new Set<SessionGateway>();
  private readonly creations = new Set<Promise<SessionGateway>>();
  private readonly cleanupTasks = new Set<Promise<void>>();
  private readonly catalogVersions = new WeakMap<Active, number>();
  private readonly redemptionCredentials = new Map<
    string,
    { readonly serverId: string; readonly credential: string }
  >();
  private readonly gatewayEpochs = new WeakMap<SessionGateway, number>();
  constructor(createGateway: GatewayFactory) {
    this.createGateway = createGateway;
  }

  async connect(value: ConnectInput, memberKey?: MemberKey): Promise<MediaSession> {
    const input = connectInput(value);
    const key = memberKey === undefined ? undefined : parseMemberKey(memberKey);
    const epoch = ++this.epoch;
    this.endpoints = [];
    const previous = this.active ?? this.transitioning;
    this.active = undefined;
    this.transitioning = undefined;
    if (previous) void this.release(previous);
    for (const gateway of this.gateways) if (gateway !== previous?.gateway) void this.close(gateway);
    let gateway: SessionGateway | undefined;
    try {
      gateway = await this.makeGateway({ address: input.address, password: input.password });
      if (epoch !== this.epoch) throw failure('GUL_SESSION_STALE');
      const info = await this.info(gateway);
      if (epoch !== this.epoch) throw failure('GUL_SESSION_STALE');
      if (key && !info?.memberAuthentication) throw failure('GUL_MEMBER_UNSUPPORTED');
      if (key && key.serverId !== info?.serverId) throw failure('GUL_MEMBER_MISMATCH');
      const login = loginResponse(
        await gateway.request('POST', '/api/gul/login', undefined, {
          username: input.username,
          password: input.password,
          ...(info ? { protocolVersion: 2 } : {}),
          ...(key ? { memberCredential: key.credential } : {}),
        }),
        gateway.brokerOrigin,
      );
      if (epoch !== this.epoch) throw failure('GUL_SESSION_STALE');
      if (info?.serverId && login.serverId !== info.serverId) throw failure('GUL_MEMBER_MISMATCH');
      if (key && (login.member?.id !== key.memberId || !['owner', 'member'].includes(login.member.role)))
        throw failure('GUL_MEMBER_MISMATCH');
      if (!key && login.member && login.member.role !== 'guest') throw failure('GUL_MEMBER_MISMATCH');
      this.advance(gateway, epoch);
      const active = { gateway, login, epoch };
      const session = this.rendererSession(active);
      this.active = active;
      return session;
    } catch (error) {
      if (gateway) await this.close(gateway);
      throw epoch !== this.epoch ? failure('GUL_SESSION_STALE') : safe(error, 'GUL_CONNECT_FAILED');
    }
  }
  async disconnect(): Promise<void> {
    ++this.epoch;
    this.endpoints = [];
    const previous = this.active ?? this.transitioning;
    this.active = undefined;
    this.transitioning = undefined;
    const creations = [...this.creations];
    const gateways = [...this.gateways];
    const cleanup = [...this.cleanupTasks];
    if (previous) cleanup.push(this.release(previous));
    const created = await Promise.allSettled(creations);
    for (const result of created) if (result.status === 'fulfilled') cleanup.push(this.close(result.value));
    for (const gateway of gateways) cleanup.push(this.close(gateway));
    await Promise.all(cleanup);
  }
  async state(): Promise<BrokerState | null> {
    const active = this.active;
    if (!active) return null;
    try {
      const result = await active.gateway.request('GET', '/api/gul/state', active.login.sessionToken);
      this.assertActive(active);
      return this.acceptState(result, active);
    } catch (error) {
      if (
        this.active === active &&
        active.login.serverId &&
        error instanceof GatewayError &&
        error.code === 'authentication'
      ) {
        await this.disconnect();
        return null;
      }
      throw safe(error, 'GUL_STATE_FAILED');
    }
  }
  async channel(id: number): Promise<MediaSession> {
    if (!channelId(id)) throw failure('GUL_INPUT_INVALID');
    const active = this.required();
    if (!active.login.serverId && id > 3) throw failure('GUL_INPUT_INVALID');
    return this.mutate(async () => {
      this.assertActive(active);
      const refreshing = active.login.channelId === id;
      const epoch = ++this.epoch;
      this.endpoints = [];
      this.active = undefined;
      this.transitioning = active;
      try {
        this.advance(active.gateway, epoch);
        const login = loginResponse(
          await active.gateway.request('POST', '/api/gul/channel', active.login.sessionToken, {
            channelId: id,
          }),
          active.gateway.brokerOrigin,
          refreshing ? undefined : active.login,
          id,
        );
        if (active.login.serverId)
          managedMetadata(login, {
            ...active.login,
            catalogVersion: this.catalogVersions.get(active) ?? active.login.catalogVersion,
          });
        if (
          refreshing &&
          (login.sessionToken !== active.login.sessionToken ||
            login.sessionId !== active.login.sessionId ||
            login.identity !== active.login.identity ||
            login.channelId !== id ||
            login.revision !== active.login.revision)
        )
          throw failure('GUL_GRANT_INVALID');
        if (epoch !== this.epoch) throw failure('GUL_SESSION_STALE');
        const next = { gateway: active.gateway, login, epoch };
        const session = this.rendererSession(next);
        this.active = next;
        return session;
      } catch (error) {
        await this.release(active);
        throw safe(error, 'GUL_CHANNEL_FAILED');
      } finally {
        if (this.transitioning === active) this.transitioning = undefined;
      }
    });
  }
  async audio(value: AudioState): Promise<AudioState> {
    const state = audioInput(value);
    const active = this.required();
    return this.mutate(async () => {
      this.assertActive(active);
      try {
        const result = await active.gateway.request(
          'POST',
          '/api/gul/audio',
          active.login.sessionToken,
          state,
        );
        this.assertActive(active);
        return audioInput(result);
      } catch (error) {
        throw safe(error, 'GUL_AUDIO_FAILED');
      }
    });
  }
  async screen(value: ScreenRequest): Promise<MediaGrant> {
    const request = screenInput(value);
    const active = this.required();
    if (request.channelId !== active.login.channelId || request.revision !== active.login.revision)
      throw failure('GUL_SESSION_STALE');
    try {
      const result = await active.gateway.request(
        'POST',
        '/api/gul/screen',
        active.login.sessionToken,
        request,
      );
      this.assertActive(active);
      return this.rendererGrant(
        active,
        mediaGrant(result, active.login, 'screen', active.gateway.brokerOrigin),
      );
    } catch (error) {
      throw safe(error, 'GUL_SCREEN_FAILED');
    }
  }
  forgetIdentity(address: string): void {
    const target = connectInput({ address, username: 'identity', password: '' }).address;
    if (!this.idle()) throw failure('GUL_SESSION_STALE');
    for (const name of this.redemptionCredentials.keys())
      if (name.startsWith(target + '\0')) this.redemptionCredentials.delete(name);
  }
  operationRevision(): number {
    return this.epoch;
  }
  idle(): boolean {
    return !this.active && !this.transitioning && this.creations.size === 0 && this.gateways.size === 0;
  }
  async members(value: ManagementContext): Promise<MemberList> {
    const context = managementContext(value),
      active = this.owner(context);
    try {
      const result = await active.gateway.request('GET', '/api/gul/members', active.login.sessionToken);
      this.assertActive(active);
      return memberList(result, active.login.member!.id!);
    } catch (error) {
      throw safe(error, 'GUL_MANAGEMENT_FAILED');
    }
  }
  async channelPermissions(value: ManagementContext & { channelId: number }): Promise<ChannelPermissions> {
    const context = managementContext(value, ['channelId']),
      active = this.owner(context);
    if (!channelId(value.channelId)) throw failure('GUL_INPUT_INVALID');
    try {
      const result = await active.gateway.request(
        'POST',
        '/api/gul/channels/permissions',
        active.login.sessionToken,
        { channelId: value.channelId },
      );
      this.assertActive(active);
      return channelPermissions(result, value.channelId);
    } catch (error) {
      throw safe(error, 'GUL_MANAGEMENT_FAILED');
    }
  }
  async createChannel(value: ChannelCreate): Promise<BrokerState> {
    const input = createChannelInput(value);
    const { epoch, serverId, ...body } = input;
    return this.catalog({ epoch, serverId }, '/api/gul/channels/create', body);
  }
  async updateChannel(value: ChannelUpdate): Promise<BrokerState> {
    const input = updateChannelInput(value);
    const { epoch, serverId, ...body } = input;
    return this.catalog({ epoch, serverId }, '/api/gul/channels/update', body);
  }
  async deleteChannel(value: ChannelDelete): Promise<BrokerState> {
    const input = deleteChannelInput(value);
    const { epoch, serverId, ...body } = input;
    return this.catalog({ epoch, serverId }, '/api/gul/channels/delete', body);
  }
  async createInvitation(value: ManagementContext): Promise<Invitation> {
    const context = managementContext(value),
      active = this.owner(context);
    try {
      const result = await active.gateway.request(
        'POST',
        '/api/gul/invites/create',
        active.login.sessionToken,
        {},
      );
      this.assertActive(active);
      return invitation(result);
    } catch (error) {
      throw safe(error, 'GUL_MANAGEMENT_FAILED');
    }
  }
  async redeemInvitation(value: RedeemInvitation): Promise<MemberKey> {
    if (
      !value ||
      typeof value !== 'object' ||
      Array.isArray(value) ||
      Object.keys(value).some((key) => !['input', 'inviteToken', 'rememberIdentity'].includes(key)) ||
      !memberCredential(value.inviteToken) ||
      typeof value.rememberIdentity !== 'boolean'
    )
      throw failure('GUL_INPUT_INVALID');
    const input = connectInput(value.input);
    if (!this.idle()) throw failure('GUL_SESSION_STALE');
    const epoch = ++this.epoch,
      retryKey = input.address + '\0' + value.inviteToken;
    let gateway: SessionGateway | undefined;
    try {
      gateway = await this.makeGateway(input);
      const info = await this.info(gateway);
      if (epoch !== this.epoch) throw failure('GUL_SESSION_STALE');
      if (!info?.memberAuthentication || !info.serverId) throw failure('GUL_MEMBER_UNSUPPORTED');
      const previous = this.redemptionCredentials.get(retryKey);
      if (previous && previous.serverId !== info.serverId) throw failure('GUL_MEMBER_MISMATCH');
      const credential = previous?.credential ?? createMemberCredential();
      if (!previous) {
        this.redemptionCredentials.set(retryKey, Object.freeze({ serverId: info.serverId, credential }));
        if (this.redemptionCredentials.size > 8)
          this.redemptionCredentials.delete(this.redemptionCredentials.keys().next().value!);
      }
      const result = await gateway.request<unknown>('POST', '/api/gul/invites/redeem', undefined, {
        protocolVersion: 2,
        username: input.username,
        password: input.password,
        inviteToken: value.inviteToken,
        memberCredential: credential,
      });
      if (epoch !== this.epoch) throw failure('GUL_SESSION_STALE');
      const data = result as { serverId?: unknown; member?: { id?: unknown; role?: unknown } };
      if (
        !data ||
        data.serverId !== info.serverId ||
        !memberId(data.member?.id) ||
        data.member?.role !== 'member'
      )
        throw failure('GUL_MEMBER_MISMATCH');
      return parseMemberKey({
        format: 'gul-member-key-v1',
        serverId: info.serverId,
        memberId: data.member.id,
        credential,
      });
    } catch (error) {
      throw epoch !== this.epoch ? failure('GUL_SESSION_STALE') : safe(error, 'GUL_REDEEM_FAILED');
    } finally {
      if (gateway) await this.close(gateway);
    }
  }
  private acceptState(value: unknown, active: Active): BrokerState {
    const result = brokerState(value, {
      ...active.login,
      catalogVersion: this.catalogVersions.get(active) ?? active.login.catalogVersion,
    });
    if (result.catalogVersion !== undefined) this.catalogVersions.set(active, result.catalogVersion);
    return result;
  }
  private owner(context: ManagementContext): Active {
    const active = this.required();
    if (context.epoch !== active.epoch || context.serverId !== active.login.serverId)
      throw failure('GUL_SESSION_STALE');
    if (active.login.member?.role !== 'owner') throw failure('GUL_OWNER_REQUIRED');
    return active;
  }
  private catalog(context: ManagementContext, path: string, body: unknown): Promise<BrokerState> {
    const active = this.owner(context);
    return this.mutate(async () => {
      this.assertActive(active);
      try {
        const result = await active.gateway.request('POST', path, active.login.sessionToken, body);
        this.assertActive(active);
        return this.acceptState(result, active);
      } catch (error) {
        throw safe(error, 'GUL_MANAGEMENT_FAILED');
      }
    });
  }
  private async info(gateway: SessionGateway) {
    try {
      return serverInfo(await gateway.request('GET', '/api/gul/info'));
    } catch (error) {
      if (error instanceof GatewayError && error.code === 'not-found') return null;
      throw error;
    }
  }
  private rendererGrant(active: Active, grant: MediaGrant): MediaGrant {
    const url = active.gateway.signalURL(active.epoch, grant.token);
    this.endpoints = [...new Set([...this.endpoints, url])];
    return { ...grant, url };
  }
  networkAllowed(url: string): boolean {
    return allowedNetwork(url, this.endpoints);
  }
  connected(): boolean {
    return this.active !== undefined;
  }
  mediaEpoch(): number | null {
    return this.active?.epoch ?? null;
  }
  private rendererSession(active: Active): MediaSession {
    const { login, epoch } = active;
    return {
      epoch,
      sessionId: login.sessionId,
      identity: login.identity,
      name: login.name,
      channelId: login.channelId,
      revision: login.revision,
      ...(login.serverId !== undefined
        ? { serverId: login.serverId, member: login.member, catalogVersion: login.catalogVersion }
        : {}),
      grant: this.rendererGrant(active, login.grant),
    };
  }
  private required(): Active {
    if (!this.active) throw failure('GUL_NOT_CONNECTED');
    return this.active;
  }
  private assertActive(active: Active): void {
    if (this.active !== active || this.epoch !== active.epoch) throw failure('GUL_SESSION_STALE');
  }
  private mutate<T>(operation: () => Promise<T>): Promise<T> {
    const result = this.mutations.then(operation);
    this.mutations = result.then(
      () => {},
      () => {},
    );
    return result;
  }
  private release(active: Active): Promise<void> {
    const existing = this.closing.get(active.gateway);
    if (existing) return existing;
    return this.clean(active.gateway, async () => {
      try {
        this.advance(active.gateway, this.epoch);
        await active.gateway.request('POST', '/api/gul/logout', active.login.sessionToken);
      } catch {
        /* A closed transport cannot send logout. */
      }
    });
  }
  private async makeGateway(input: Pick<ConnectInput, 'address' | 'password'>): Promise<SessionGateway> {
    const creating = this.createGateway(input).then((gateway) => {
      this.gateways.add(gateway);
      return gateway;
    });
    this.creations.add(creating);
    try {
      return await creating;
    } finally {
      this.creations.delete(creating);
    }
  }
  private clean(gateway: SessionGateway, before: () => Promise<void>): Promise<void> {
    const task = (async () => {
      await before();
      try {
        await gateway.close();
      } catch {
        /* Closing is terminal. */
      }
      this.gateways.delete(gateway);
    })();
    this.closing.set(gateway, task);
    this.cleanupTasks.add(task);
    void task.finally(() => {
      this.cleanupTasks.delete(task);
    });
    return task;
  }
  private advance(gateway: SessionGateway, epoch: number): void {
    if ((this.gatewayEpochs.get(gateway) ?? 0) >= epoch) return;
    gateway.beginEpoch(epoch);
    this.gatewayEpochs.set(gateway, epoch);
  }
  private close(gateway: SessionGateway): Promise<void> {
    return this.closing.get(gateway) ?? this.clean(gateway, async () => {});
  }
}
const safeCodes = new Set([
  'GUL_INPUT_INVALID',
  'GUL_GRANT_INVALID',
  'GUL_STATE_INVALID',
  'GUL_SESSION_STALE',
  'GUL_NOT_CONNECTED',
  'GUL_MEMBER_MISMATCH',
  'GUL_MEMBER_UNSUPPORTED',
  'GUL_INFO_INVALID',
  'GUL_UPGRADE_REQUIRED',
  'GUL_OWNER_REQUIRED',
]);
function safe(error: unknown, fallback: string): Error {
  const codes: Readonly<Record<string, string>> = {
    'owner-required': 'GUL_OWNER_REQUIRED',
    'access-denied': 'GUL_ACCESS_DENIED',
    'channel-busy': 'GUL_CHANNEL_BUSY',
    stale: 'GUL_CATALOG_CONFLICT',
    'upgrade-required': 'GUL_UPGRADE_REQUIRED',
    'cleanup-pending': 'GUL_CLEANUP_PENDING',
    'storage-unavailable': 'GUL_SERVER_STORAGE_UNAVAILABLE',
  };
  if (error instanceof GatewayError && codes[error.code]) return failure(codes[error.code]);
  return failure(error instanceof Error && safeCodes.has(error.message) ? error.message : fallback);
}
