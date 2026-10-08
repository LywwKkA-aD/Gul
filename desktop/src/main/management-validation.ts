import type {
  ServerInfo,
  MemberIdentity,
  ManagementContext,
  ChannelCreate,
  ChannelUpdate,
  ChannelDelete,
  ChannelPermissions,
  MemberList,
  Invitation,
} from '../shared/management.ts';
import { failure } from './validation.ts';
import { memberId, memberCredential } from './member-key.ts';

const record = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);
const integer = (value: unknown, min = 1, max = Number.MAX_SAFE_INTEGER): value is number =>
  Number.isSafeInteger(value) && (value as number) >= min && (value as number) <= max;
const only = (value: Record<string, unknown>, keys: readonly string[]) =>
  Object.keys(value).every((key) => keys.includes(key));
function name(value: unknown): value is string {
  return (
    typeof value === 'string' &&
    value.trim().length > 0 &&
    [...value.trim()].length <= 64 &&
    Buffer.byteLength(value.trim()) <= 256 &&
    !/[\u0000-\u001f\u007f-\u009f]/u.test(value)
  );
}
export function serverInfo(value: unknown): ServerInfo {
  if (record(value) && typeof value.protocolVersion === 'number' && value.protocolVersion !== 2)
    throw failure('GUL_UPGRADE_REQUIRED');
  if (
    !record(value) ||
    value.protocolVersion !== 2 ||
    value.maxChannels !== 64 ||
    typeof value.channelManagement !== 'boolean' ||
    value.memberAuthentication !== value.channelManagement ||
    (value.channelManagement ? !memberId(value.serverId) : value.serverId !== null)
  )
    throw failure('GUL_INFO_INVALID');
  return {
    protocolVersion: 2,
    serverId: value.serverId as string | null,
    channelManagement: value.channelManagement,
    memberAuthentication: value.memberAuthentication as boolean,
    maxChannels: 64,
  };
}
export interface ManagementMetadata {
  readonly serverId?: string | null;
  readonly member?: MemberIdentity;
  readonly catalogVersion?: number;
}
export function managedMetadata(value: unknown, expected?: ManagementMetadata): ManagementMetadata {
  if (!record(value)) throw failure('GUL_STATE_INVALID');
  if (!['serverId', 'member', 'catalogVersion'].some((key) => Object.hasOwn(value, key))) {
    if (expected?.serverId) throw failure('GUL_STATE_INVALID');
    return {};
  }
  const member = value.member;
  if (
    !record(member) ||
    !integer(value.catalogVersion) ||
    !['owner', 'member', 'guest'].includes(member.role as string) ||
    (member.role === 'guest' ? member.id !== null : !memberId(member.id)) ||
    (value.serverId === null ? member.role !== 'guest' : !memberId(value.serverId))
  )
    throw failure('GUL_STATE_INVALID');
  const result = {
    serverId: value.serverId as string | null,
    member: { id: member.id as string | null, role: member.role as MemberIdentity['role'] },
    catalogVersion: value.catalogVersion,
  };
  if (
    expected &&
    (result.serverId !== expected.serverId ||
      result.member.id !== expected.member?.id ||
      result.member.role !== expected.member?.role)
  )
    throw failure('GUL_MEMBER_MISMATCH');
  if (expected?.catalogVersion !== undefined && result.catalogVersion < expected.catalogVersion)
    throw failure('GUL_STATE_INVALID');
  return result;
}
export function managementContext(value: unknown, fields: readonly string[] = []): ManagementContext {
  if (
    !record(value) ||
    !only(value, ['epoch', 'serverId', ...fields]) ||
    !integer(value.epoch) ||
    !memberId(value.serverId)
  )
    throw failure('GUL_INPUT_INVALID');
  return { epoch: value.epoch, serverId: value.serverId };
}
function policy(
  value: Record<string, unknown>,
  error = 'GUL_INPUT_INVALID',
): { access: 'open' | 'restricted'; allowedMemberIds: readonly string[] } {
  if (
    !['open', 'restricted'].includes(value.access as string) ||
    !Array.isArray(value.allowedMemberIds) ||
    value.allowedMemberIds.length > 64 ||
    (value.access === 'open' && value.allowedMemberIds.length !== 0) ||
    !value.allowedMemberIds.every(memberId) ||
    new Set(value.allowedMemberIds).size !== value.allowedMemberIds.length
  )
    throw failure(error);
  return {
    access: value.access as 'open' | 'restricted',
    allowedMemberIds: Object.freeze([...value.allowedMemberIds]),
  };
}
export function createChannelInput(value: unknown): ChannelCreate {
  const context = managementContext(value, ['name', 'access', 'allowedMemberIds', 'catalogVersion']),
    input = value as Record<string, unknown>;
  if (!name(input.name) || !integer(input.catalogVersion)) throw failure('GUL_INPUT_INVALID');
  return { ...context, ...policy(input), name: input.name.trim(), catalogVersion: input.catalogVersion };
}
export function updateChannelInput(value: unknown): ChannelUpdate {
  const context = managementContext(value, ['channelId', 'version', 'name', 'access', 'allowedMemberIds']),
    input = value as Record<string, unknown>;
  if (!integer(input.channelId, 0, 0x7fffffff) || !integer(input.version) || !name(input.name))
    throw failure('GUL_INPUT_INVALID');
  if (input.channelId <= 1 && input.access !== 'open') throw failure('GUL_INPUT_INVALID');
  return {
    ...context,
    ...policy(input),
    channelId: input.channelId,
    version: input.version,
    name: input.name.trim(),
  };
}
export function deleteChannelInput(value: unknown): ChannelDelete {
  const context = managementContext(value, ['channelId', 'version']),
    input = value as Record<string, unknown>;
  if (!integer(input.channelId, 2, 0x7fffffff) || !integer(input.version)) throw failure('GUL_INPUT_INVALID');
  return { ...context, channelId: input.channelId, version: input.version };
}
export function channelPermissions(value: unknown, expected: number): ChannelPermissions {
  if (!record(value) || value.channelId !== expected || !integer(value.version))
    throw failure('GUL_STATE_INVALID');
  return { channelId: expected, version: value.version, ...policy(value, 'GUL_STATE_INVALID') };
}
export function memberList(value: unknown, ownerId: string): MemberList {
  if (
    !record(value) ||
    !integer(value.catalogVersion) ||
    !Array.isArray(value.members) ||
    value.members.length > 128
  )
    throw failure('GUL_STATE_INVALID');
  const seen = new Set<string>();
  const members = value.members.map((input) => {
    if (
      !record(input) ||
      !memberId(input.id) ||
      seen.has(input.id) ||
      !name(input.name) ||
      !['owner', 'member'].includes(input.role as string) ||
      typeof input.revoked !== 'boolean'
    )
      throw failure('GUL_STATE_INVALID');
    seen.add(input.id);
    return { id: input.id, name: input.name, role: input.role as 'owner' | 'member', revoked: input.revoked };
  });
  const owners = members.filter((value) => value.role === 'owner');
  if (owners.length !== 1 || owners[0].id !== ownerId || owners[0].revoked)
    throw failure('GUL_STATE_INVALID');
  return { members, catalogVersion: value.catalogVersion };
}
export function invitation(value: unknown): Invitation {
  if (
    !record(value) ||
    !memberCredential(value.inviteToken) ||
    !integer(value.expiresAtUnixSeconds, 1, 8_640_000_000_000)
  )
    throw failure('GUL_STATE_INVALID');
  return { inviteToken: value.inviteToken, expiresAtUnixSeconds: value.expiresAtUnixSeconds };
}
