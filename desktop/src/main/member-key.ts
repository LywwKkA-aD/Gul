import { randomBytes } from 'node:crypto';

export interface MemberKey {
  readonly format: 'gul-member-key-v1';
  readonly serverId: string;
  readonly memberId: string;
  readonly credential: string;
}
export const memberId = (value: unknown): value is string =>
  typeof value === 'string' && /^[a-f0-9]{32}$/u.test(value);
export function memberCredential(value: unknown): value is string {
  return (
    typeof value === 'string' &&
    /^[A-Za-z0-9_-]{43}$/u.test(value) &&
    Buffer.from(value, 'base64url').length === 32 &&
    Buffer.from(value, 'base64url').toString('base64url') === value
  );
}
export function createMemberCredential(): string {
  return randomBytes(32).toString('base64url');
}
const record = (value: unknown): value is Record<string, unknown> =>
  !!value && typeof value === 'object' && !Array.isArray(value);
export function parseMemberKey(value: unknown): MemberKey {
  if (
    !record(value) ||
    Object.keys(value).length !== 4 ||
    value.format !== 'gul-member-key-v1' ||
    !memberId(value.serverId) ||
    !memberId(value.memberId) ||
    !memberCredential(value.credential)
  )
    throw new Error('GUL_INPUT_INVALID');
  return Object.freeze({
    format: 'gul-member-key-v1',
    serverId: value.serverId,
    memberId: value.memberId,
    credential: value.credential,
  });
}
