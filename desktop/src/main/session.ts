import type {
  AudioState,
  BrokerState,
  ConnectInput,
  MediaGrant,
  MediaSession,
  ScreenRequest,
} from '../shared/contracts.ts';
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
  private readonly gatewayEpochs = new WeakMap<SessionGateway, number>();
  constructor(createGateway: GatewayFactory) {
    this.createGateway = createGateway;
  }

  async connect(value: ConnectInput): Promise<MediaSession> {
    const input = connectInput(value);
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
      const login = loginResponse(
        await gateway.request('POST', '/api/gul/login', undefined, {
          username: input.username,
          password: input.password,
        }),
        gateway.brokerOrigin,
      );
      if (epoch !== this.epoch) throw failure('GUL_SESSION_STALE');
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
      return brokerState(result, active.login);
    } catch (error) {
      throw safe(error, 'GUL_STATE_FAILED');
    }
  }
  async channel(id: number): Promise<MediaSession> {
    if (!channelId(id)) throw failure('GUL_INPUT_INVALID');
    const active = this.required();
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
]);
function safe(error: unknown, fallback: string): Error {
  return failure(error instanceof Error && safeCodes.has(error.message) ? error.message : fallback);
}
