import test from 'node:test';
import assert from 'node:assert/strict';

import { collectPeerKeys, forgetAbsentPeers, isMortalPeerKey } from './peerKeys.ts';
import type { ChannelNode, UserInfo } from './types';

function user(key: string): UserInfo {
  return {
    session: 0,
    name: 'someone',
    key,
    channelId: 0,
    selfMute: false,
    selfDeaf: false,
    isSelf: false,
  };
}

function room(users: UserInfo[], children: ChannelNode[] = []): ChannelNode {
  return { id: 0, name: 'root', position: 0, users, children };
}

// The mortality rule is the Go side's, and the two copies have to agree about
// it or they diverge in exactly the case it exists for.
test('only a session key dies with the connection', () => {
  assert.equal(isMortalPeerKey('s:7'), true);
  assert.equal(isMortalPeerKey('h:abc123'), false);
  assert.equal(isMortalPeerKey('u:42'), false);
});

// The bug, stated as what a user would see: someone leaves, Murmur gives their
// session number to the next person through the door, and this copy shows the
// newcomer already muted while the engine - which swept - plays them.
test('a session-keyed setting does not outlive its peer', () => {
  const present = collectPeerKeys(room([user('s:9')]));
  const muted = forgetAbsentPeers({ 's:7': true, 's:9': true }, present);

  assert.deepEqual(muted, { 's:9': true });
});

// A certificate hash names the same person next week. Dropping it because they
// stepped out of the room would lose a setting the user meant to keep.
test('a hash-keyed setting survives the peer leaving', () => {
  const volumes = forgetAbsentPeers({ 'h:abc': 0.4, 'u:42': 0.6 }, new Set<string>());

  assert.deepEqual(volumes, { 'h:abc': 0.4, 'u:42': 0.6 });
});

// This runs on every tree update, several a second in a busy room. A new
// object each time would re-render every member row that reads it.
test('nothing to drop returns the same object', () => {
  const volumes = { 'h:abc': 0.4, 's:9': 0.8 };
  const present = collectPeerKeys(room([user('s:9')]));

  assert.equal(forgetAbsentPeers(volumes, present), volumes);
});

// The roster is a tree, and a peer in a subchannel is as present as one in the
// root. Walking only the top would sweep everybody who is not in the lobby.
test('a peer in a subchannel counts as present', () => {
  const tree = room([user('s:1')], [room([user('s:2')], [room([user('s:3')])])]);

  assert.deepEqual([...collectPeerKeys(tree)].sort(), ['s:1', 's:2', 's:3']);
});

// A peer this client could not name at all is not a person it can remember.
test('an empty key is not collected', () => {
  assert.deepEqual([...collectPeerKeys(room([user(''), user('s:4')]))], ['s:4']);
});

// The sweep has to actually be wired to the roster, not merely exist.
//
// The pure function above is well covered, and that is exactly how this hole
// stays open: every one of those tests passes with setTree never calling it.
// Mutation testing said so - deleting the call from the store broke nothing.
test('the roster update sweeps the store', async () => {
  const { useGulStore } = await import('./store.ts');

  useGulStore.getState().setUserVolume('s:7', 0.3);
  useGulStore.getState().setUserMuted('s:7', true);
  useGulStore.getState().setUserVolume('h:abc', 0.4);
  useGulStore.getState().setTree(room([user('s:7')]));

  assert.equal(useGulStore.getState().userVolumes['s:7'], 0.3, 'a peer still here keeps their volume');

  // The peer leaves; Murmur will hand their session number to somebody else.
  useGulStore.getState().setTree(room([user('s:9')]));

  const state = useGulStore.getState();
  assert.equal(state.userVolumes['s:7'], undefined, 'a departed session key kept its volume');
  assert.equal(state.mutedUsers['s:7'], undefined, 'a departed session key kept its mute');
  assert.equal(state.userVolumes['h:abc'], 0.4, 'a certificate hash was swept with the mortals');
});
