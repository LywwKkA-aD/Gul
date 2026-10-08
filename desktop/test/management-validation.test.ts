import assert from 'node:assert/strict';
import test from 'node:test';
import {
  serverInfo,
  managementContext,
  createChannelInput,
  updateChannelInput,
  deleteChannelInput,
  memberList,
  channelPermissions,
  invitation,
  managedMetadata,
} from '../src/main/management-validation.ts';

const serverId = 'a'.repeat(32),
  ownerId = 'b'.repeat(32),
  memberId = 'c'.repeat(32);
const context = { epoch: 7, serverId };
const info = {
  protocolVersion: 2,
  serverId,
  channelManagement: true,
  memberAuthentication: true,
  maxChannels: 64,
};
const meta = { serverId, member: { id: ownerId, role: 'owner' as const }, catalogVersion: 1 };

test('managed protocol negotiation cannot downgrade malformed or foreign metadata to a guest', () => {
  assert.deepEqual(serverInfo(info), info);
  assert.deepEqual(
    serverInfo({ ...info, serverId: null, channelManagement: false, memberAuthentication: false }),
    { ...info, serverId: null, channelManagement: false, memberAuthentication: false },
  );
  for (const value of [
    null,
    {},
    { ...info, serverId: null },
    { ...info, maxChannels: 65 },
    { ...info, memberAuthentication: false },
    { ...info, serverId: 'private-value' },
  ])
    assert.throws(() => serverInfo(value), /GUL_INFO_INVALID/u);
  assert.throws(() => serverInfo({ ...info, protocolVersion: 3 }), /GUL_UPGRADE_REQUIRED/u);
  assert.deepEqual(managedMetadata(meta), meta);
  for (const value of [
    { ...meta, member: { id: null, role: 'owner' as const } },
    { ...meta, member: { id: ownerId, role: 'administrator' } },
    { ...meta, catalogVersion: 0 },
    { ...meta, serverId: 'private' },
  ])
    assert.throws(() => managedMetadata(value), /GUL_STATE_INVALID/u);
  assert.throws(() => managedMetadata(meta, { ...meta, serverId: 'd'.repeat(32) }), /GUL_MEMBER_MISMATCH/u);
  assert.deepEqual(managedMetadata({}), {});
});

test('management inputs fence session context and reject forged roles, paths and oversized policies', () => {
  assert.deepEqual(managementContext(context), context);
  const value = {
    ...context,
    name: ' Новый канал ',
    access: 'restricted',
    allowedMemberIds: [memberId],
    catalogVersion: 4,
  };
  assert.deepEqual(createChannelInput(value), { ...value, name: 'Новый канал' });
  for (const patch of [
    { epoch: 0 },
    { serverId: 'x'.repeat(32) },
    { role: 'owner' as const },
    { path: '/api/private' },
    { name: 'x\0' },
    { name: 'x'.repeat(65) },
    { allowedMemberIds: [memberId, memberId] },
    { allowedMemberIds: Array(65).fill(memberId) },
    { allowedMemberIds: ['unknown'] },
    { access: 'private' },
    { access: 'open', allowedMemberIds: [memberId] },
    { catalogVersion: 1.5 },
  ])
    assert.throws(() => createChannelInput({ ...value, ...patch }), /GUL_INPUT_INVALID/u);
  assert.equal(
    updateChannelInput({
      ...context,
      channelId: 29,
      version: 1,
      name: 'Канал',
      access: 'open',
      allowedMemberIds: [],
    }).channelId,
    29,
  );
  assert.deepEqual(deleteChannelInput({ ...context, channelId: 29, version: 1 }), {
    ...context,
    channelId: 29,
    version: 1,
  });
  for (const channelId of [-1, 0, 1, 2 ** 31, 2.5])
    assert.throws(() => deleteChannelInput({ ...context, channelId, version: 1 }), /GUL_INPUT_INVALID/u);
});

test('owner member listings are bounded, unique and do not forward credentials or unknown fields', () => {
  const value = {
    members: [
      { id: ownerId, name: 'Владелец', role: 'owner' as const, revoked: false, credential: 'private' },
      { id: memberId, name: 'Участник', role: 'member', revoked: false },
    ],
    catalogVersion: 2,
  };
  const result = memberList(value, ownerId);
  assert.equal(result.members.length, 2);
  assert.equal(JSON.stringify(result).includes('private'), false);
  for (const members of [
    [...value.members, value.members[0]],
    [],
    Array(129).fill(value.members[1]),
    [{ ...value.members[0], revoked: true }],
    [{ ...value.members[0], role: 'guest' }],
  ])
    assert.throws(() => memberList({ ...value, members }, ownerId), /GUL_STATE_INVALID/u);
});

test('permissions and deliberate invitation DTOs are strict, bounded and reveal no member key', () => {
  const policy = { channelId: 29, version: 4, access: 'restricted', allowedMemberIds: [memberId] };
  assert.deepEqual(channelPermissions({ ...policy, credential: 'private' }, 29), policy);
  assert.throws(() => channelPermissions(policy, 30), /GUL_STATE_INVALID/u);
  assert.throws(
    () => channelPermissions({ ...policy, allowedMemberIds: [memberId, memberId] }, 29),
    /GUL_STATE_INVALID/u,
  );
  const token = Buffer.alloc(32, 2).toString('base64url');
  assert.deepEqual(invitation({ inviteToken: token, expiresAtUnixSeconds: 123, credential: 'private' }), {
    inviteToken: token,
    expiresAtUnixSeconds: 123,
  });
  for (const value of [
    null,
    { inviteToken: 'secret', expiresAtUnixSeconds: 123 },
    { inviteToken: token, expiresAtUnixSeconds: -1 },
    { inviteToken: token, expiresAtUnixSeconds: Infinity },
  ])
    assert.throws(() => invitation(value), /GUL_STATE_INVALID/u);
});
