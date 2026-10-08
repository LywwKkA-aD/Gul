import assert from 'node:assert/strict';
import test from 'node:test';
import { brokerState, loginResponse } from '../src/main/validation.ts';

const serverId = 'a'.repeat(32),
  member = { id: 'b'.repeat(32), role: 'owner' as const };
const scope = { sessionId: 7, channelId: 29, revision: 1, serverId, member, catalogVersion: 1 };
const own = { session: 7, name: 'Владелец', channelId: 29, selfMute: false, selfDeaf: false, isSelf: true };
const node = (id: number, users: unknown[] | null = []) => ({
  id,
  name: `Канал ${id}`,
  position: id,
  version: 1,
  access: 'open',
  canJoin: true,
  users,
  children: null,
});
const state = () => ({
  selfSession: 7,
  selfChannel: 29,
  revision: 1,
  serverId,
  member,
  catalogVersion: 2,
  tree: { ...node(0), children: [node(1), node(29, [own])] },
});
const login = () => ({
  ...scope,
  sessionToken: 'fixture-broker-token',
  identity: 'voice.7',
  name: 'Владелец',
  grant: {
    url: 'wss://fixture.invalid',
    token: 'fixture-media-token',
    identity: 'voice.7',
    ownerIdentity: 'voice.7',
    room: 'gul-channel-29',
    sessionId: 7,
    channelId: 29,
    revision: 1,
  },
});

test('dynamic channel catalogs preserve authoritative permissions without changing the media revision', () => {
  const result = brokerState(state(), scope);
  assert.equal(result.serverId, serverId);
  assert.deepEqual(result.member, member);
  assert.equal(result.catalogVersion, 2);
  assert.equal(result.revision, 1);
  assert.equal(result.tree.children?.[1].id, 29);
  assert.equal(result.tree.children?.[1].version, 1);
  assert.equal(result.tree.children?.[1].canJoin, true);
  const accepted = loginResponse(login(), 'https://fixture.invalid');
  assert.deepEqual(accepted.member, member);
  assert.equal(accepted.serverId, serverId);
});

test('foreign, missing or downgraded identity metadata is rejected, never accepted as guest capability', () => {
  for (const change of [
    { serverId: 'c'.repeat(32) },
    { member: { id: member.id, role: 'member' } },
    { member: { id: null, role: 'guest' } },
    { catalogVersion: 0 },
    { serverId: undefined, member: undefined, catalogVersion: undefined },
  ])
    assert.throws(
      () => brokerState({ ...state(), ...change }, scope),
      /GUL_(STATE_INVALID|MEMBER_MISMATCH)/u,
    );
  const value = state();
  delete (value as any).serverId;
  delete (value as any).member;
  delete (value as any).catalogVersion;
  assert.throws(() => brokerState(value, scope), /GUL_STATE_INVALID/u);
});

test('restricted channels cannot leak roster or allow the caller into a forbidden current room', () => {
  const value = state();
  (value.tree.children as any[]).push({ ...node(31), access: 'restricted', canJoin: false, users: null });
  assert.equal(brokerState(value, scope).tree.children?.[2].users, null);
  const forbidden = {
    ...node(31),
    access: 'restricted',
    canJoin: false,
    users: [{ ...own, session: 8, channelId: 31, isSelf: false }],
  };
  assert.throws(
    () =>
      brokerState(
        { ...value, tree: { ...value.tree, children: [node(1), node(29, [own]), forbidden] } },
        scope,
      ),
    /GUL_STATE_INVALID/u,
  );
  assert.throws(
    () =>
      brokerState(
        { ...value, tree: { ...value.tree, children: [node(1), { ...node(29), canJoin: false }] } },
        scope,
      ),
    /GUL_STATE_INVALID/u,
  );
});

test('catalog and roster bounds reject duplicate IDs, foreign user-channel bindings and deep managed trees', () => {
  for (const children of [
    [node(29, [own]), node(29)],
    [node(29, [{ ...own, channelId: 1 }])],
    [node(29, [own, own])],
    [{ ...node(29), children: [node(30)] }],
    Array.from({ length: 66 }, (_, index) => node(index + 1)),
  ])
    assert.throws(
      () => brokerState({ ...state(), tree: { ...node(0), children } }, scope),
      /GUL_STATE_INVALID/u,
    );
});
