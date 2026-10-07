import type {
  AudioState,
  BrokerState,
  ChannelNode,
  ConnectInput,
  MediaGrant,
  ScreenRequest,
  UserInfo,
} from '../shared/contracts.ts';

export function failure(code: string): Error {
  return new Error(code);
}
function record(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}
function integer(value: unknown, minimum: number, maximum = Number.MAX_SAFE_INTEGER): value is number {
  return Number.isSafeInteger(value) && (value as number) >= minimum && (value as number) <= maximum;
}
function text(value: unknown, maximum: number): value is string {
  return (
    typeof value === 'string' &&
    value.length > 0 &&
    value.length <= maximum &&
    !/[\u0000-\u001f\u007f]/u.test(value)
  );
}
export function channelId(value: unknown): value is number {
  return integer(value, 0, 3);
}
function only(value: Record<string, unknown>, keys: readonly string[]): boolean {
  return Object.keys(value).every((key) => keys.includes(key));
}

export function connectInput(value: unknown): ConnectInput {
  if (
    !record(value) ||
    !only(value, ['address', 'username', 'password']) ||
    !text(value.address, 4096) ||
    !text(value.username, 256) ||
    !value.username.trim() ||
    [...value.username.trim()].length > 64 ||
    typeof value.password !== 'string' ||
    value.password.length > 1024
  )
    throw failure('GUL_INPUT_INVALID');
  return { address: value.address.trim(), username: value.username.trim(), password: value.password };
}
export function audioInput(value: unknown): AudioState {
  if (
    !record(value) ||
    !only(value, ['muted', 'deafened']) ||
    typeof value.muted !== 'boolean' ||
    typeof value.deafened !== 'boolean'
  )
    throw failure('GUL_INPUT_INVALID');
  return { muted: value.muted || value.deafened, deafened: value.deafened };
}
export function screenInput(value: unknown): ScreenRequest {
  if (
    !record(value) ||
    !only(value, ['channelId', 'revision']) ||
    !channelId(value.channelId) ||
    !integer(value.revision, 1)
  )
    throw failure('GUL_INPUT_INVALID');
  return { channelId: value.channelId, revision: value.revision };
}
export interface Login {
  readonly sessionToken: string;
  readonly sessionId: number;
  readonly identity: string;
  readonly name: string;
  readonly channelId: number;
  readonly revision: number;
  readonly grant: MediaGrant;
}
interface Scope {
  sessionId: number;
  channelId: number;
  revision: number;
}
export function mediaGrant(
  value: unknown,
  scope: Scope,
  role: 'voice' | 'screen',
  brokerOrigin: string,
): MediaGrant {
  if (
    !record(value) ||
    !text(value.token, 32768) ||
    value.identity !== `${role}.${scope.sessionId}` ||
    value.ownerIdentity !== `voice.${scope.sessionId}` ||
    value.room !== `gul-channel-${scope.channelId}` ||
    value.sessionId !== scope.sessionId ||
    value.channelId !== scope.channelId ||
    value.revision !== scope.revision ||
    typeof value.url !== 'string'
  )
    throw failure('GUL_GRANT_INVALID');
  try {
    const url = new URL(value.url),
      broker = new URL(brokerOrigin);
    if (
      url.protocol !== 'wss:' ||
      broker.protocol !== 'https:' ||
      url.host !== broker.host ||
      url.username ||
      url.password ||
      url.search ||
      url.hash ||
      (url.pathname !== '' && url.pathname !== '/')
    )
      throw failure('GUL_GRANT_INVALID');
  } catch {
    throw failure('GUL_GRANT_INVALID');
  }
  return {
    url: value.url,
    token: value.token,
    identity: value.identity,
    ownerIdentity: value.ownerIdentity,
    room: value.room,
    sessionId: scope.sessionId,
    channelId: scope.channelId,
    revision: scope.revision,
  };
}
export function loginResponse(
  value: unknown,
  brokerOrigin: string,
  previous?: Login,
  expectedChannel?: number,
): Login {
  if (
    !record(value) ||
    !text(value.sessionToken, 256) ||
    !integer(value.sessionId, 1, 0x7fffffff) ||
    value.identity !== `voice.${value.sessionId}` ||
    !text(value.name, 256) ||
    !channelId(value.channelId) ||
    !integer(value.revision, 1)
  )
    throw failure('GUL_GRANT_INVALID');
  if (
    previous &&
    (value.sessionToken !== previous.sessionToken ||
      value.sessionId !== previous.sessionId ||
      value.identity !== previous.identity ||
      value.channelId !== expectedChannel ||
      value.revision <= previous.revision)
  )
    throw failure('GUL_GRANT_INVALID');
  const scope = { sessionId: value.sessionId, channelId: value.channelId, revision: value.revision };
  return {
    ...scope,
    sessionToken: value.sessionToken,
    identity: value.identity,
    name: value.name,
    grant: mediaGrant(value.grant, scope, 'voice', brokerOrigin),
  };
}
function user(value: unknown): UserInfo {
  if (
    !record(value) ||
    !integer(value.session, 1, 0x7fffffff) ||
    !text(value.name, 256) ||
    !channelId(value.channelId) ||
    typeof value.selfMute !== 'boolean' ||
    typeof value.selfDeaf !== 'boolean' ||
    typeof value.isSelf !== 'boolean'
  )
    throw failure('GUL_STATE_INVALID');
  return {
    session: value.session,
    key: `s:livekit:${value.session}`,
    name: value.name,
    channelId: value.channelId,
    selfMute: value.selfMute,
    selfDeaf: value.selfDeaf,
    isSelf: value.isSelf,
  };
}
function tree(value: unknown, seen: Set<number>, depth = 0): ChannelNode {
  if (
    depth > 4 ||
    !record(value) ||
    !channelId(value.id) ||
    seen.has(value.id) ||
    !text(value.name, 256) ||
    !integer(value.position, -2147483648, 2147483647) ||
    (value.users !== null && (!Array.isArray(value.users) || value.users.length > 128)) ||
    (value.children !== null && (!Array.isArray(value.children) || value.children.length > 4))
  )
    throw failure('GUL_STATE_INVALID');
  seen.add(value.id);
  return {
    id: value.id,
    name: value.name,
    position: value.position,
    users: value.users === null ? null : (value.users as unknown[]).map(user),
    children:
      value.children === null
        ? null
        : (value.children as unknown[]).map((node) => tree(node, seen, depth + 1)),
  };
}
export function brokerState(value: unknown, scope: Scope): BrokerState {
  if (
    !record(value) ||
    value.selfSession !== scope.sessionId ||
    value.selfChannel !== scope.channelId ||
    value.revision !== scope.revision
  )
    throw failure('GUL_STATE_INVALID');
  return {
    selfSession: scope.sessionId,
    selfChannel: scope.channelId,
    revision: scope.revision,
    tree: tree(value.tree, new Set()),
  };
}
