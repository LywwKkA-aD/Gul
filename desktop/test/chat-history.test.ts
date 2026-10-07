import assert from 'node:assert/strict';
import test from 'node:test';
import {
  CHAT_HISTORY_CHANNEL_LIMIT,
  CHAT_HISTORY_LIMIT,
  EphemeralChatHistory,
} from '../src/renderer/chat-history.ts';
import type { ChatEntry } from '../src/renderer/media/model.ts';

const scope = { server: 'livekit+vless://example.test', sessionId: 7 };
const message = (id: number, text = `Message ${id}`): ChatEntry => ({
  id,
  identity: 'voice.8',
  name: 'Participant',
  text,
  local: false,
  time: 1000 + id,
});
const ids = (entries: readonly ChatEntry[]) => entries.map((entry) => entry.id);

test('chat history is empty and cannot store messages before an authenticated bind', () => {
  const history = new EphemeralChatHistory();
  history.write(0, [message(1)]);
  assert.deepEqual(history.read(0), []);
  history.bind(scope);
  assert.deepEqual(history.read(0), []);
});

test('channel transcripts stay separate, preserve arrival order and deduplicate IDs', () => {
  const history = new EphemeralChatHistory();
  history.bind(scope);
  history.write(0, [message(3), message(1), message(3, 'Replacement'), message(2)]);
  history.write(0, [message(1), message(2), message(3), message(4)]);
  history.write(1, [message(1, 'Other channel')]);
  assert.deepEqual(ids(history.read(0)), [3, 1, 2, 4]);
  assert.equal(history.read(0)[0].text, 'Message 3');
  assert.equal(history.read(1)[0].text, 'Other channel');
  assert.deepEqual(history.read(2), []);
});

test('writes copy and freeze entries, while repeated snapshots keep the same reference', () => {
  const history = new EphemeralChatHistory();
  history.bind(scope);
  const original = [message(1)];
  history.write(0, original);
  const stored = history.read(0);
  assert.notEqual(stored, original);
  assert.notEqual(stored[0], original[0]);
  assert.ok(Object.isFrozen(stored));
  assert.ok(Object.isFrozen(stored[0]));
  original[0] = message(1, 'Changed after write');
  original.push(message(2));
  assert.equal(stored[0].text, 'Message 1');
  history.write(0, [message(1, 'Duplicate')]);
  assert.equal(history.read(0), stored);
});

test('the latest 500 messages survive and evicted snapshots cannot resurrect old messages', () => {
  const history = new EphemeralChatHistory();
  history.bind(scope);
  const messages = Array.from({ length: CHAT_HISTORY_LIMIT + 12 }, (_, index) => message(index + 1));
  history.write(0, messages);
  const stored = history.read(0);
  assert.equal(stored.length, 500);
  assert.equal(stored[0].id, 13);
  assert.equal(stored.at(-1)?.id, 512);
  history.write(0, messages);
  assert.equal(history.read(0), stored);
  history.write(0, [message(1), message(513)]);
  assert.equal(history.read(0)[0].id, 14);
  assert.equal(history.read(0).at(-1)?.id, 513);
});

test('binding the same authenticated server/session preserves history; changing either clears it', () => {
  const history = new EphemeralChatHistory();
  history.bind(scope);
  history.write(0, [message(1)]);
  const stored = history.read(0);
  history.bind({ ...scope });
  assert.equal(history.read(0), stored);
  history.bind({ ...scope, sessionId: 8 });
  assert.deepEqual(history.read(0), []);
  history.write(0, [message(1)]);
  history.bind({ ...scope, server: 'livekit+vless://other.test' });
  assert.deepEqual(history.read(0), []);
});

test('disconnect reset removes scope, history and ID high-water marks', () => {
  const history = new EphemeralChatHistory();
  history.bind(scope);
  history.write(0, [message(99)]);
  history.reset();
  history.write(0, [message(100)]);
  assert.deepEqual(history.read(0), []);
  history.bind(scope);
  history.write(0, [message(1)]);
  assert.deepEqual(ids(history.read(0)), [1]);
});

test('channel memory is bounded and evicts the least recently read or written channel', () => {
  const history = new EphemeralChatHistory();
  history.bind(scope);
  for (let channel = 0; channel < CHAT_HISTORY_CHANNEL_LIMIT; channel++)
    history.write(channel, [message(channel + 1)]);
  assert.deepEqual(ids(history.read(0)), [1]);
  history.write(CHAT_HISTORY_CHANNEL_LIMIT, [message(100)]);
  assert.deepEqual(history.read(1), []);
  assert.deepEqual(ids(history.read(0)), [1]);
  history.write(2, [message(3)]);
  history.write(CHAT_HISTORY_CHANNEL_LIMIT + 1, [message(101)]);
  assert.deepEqual(history.read(3), []);
  assert.deepEqual(ids(history.read(2)), [3]);
});

test('invalid scope clears prior data and invalid channels or IDs cannot allocate history', () => {
  const history = new EphemeralChatHistory();
  history.bind(scope);
  history.write(0, [message(1)]);
  for (const channel of [-1, 1.5, NaN, Infinity, Number.MAX_SAFE_INTEGER + 1]) {
    history.write(channel, [message(2)]);
    assert.deepEqual(history.read(channel), []);
  }
  history.write(1, [message(0), message(-1), message(NaN), message(1.5)]);
  assert.deepEqual(history.read(1), []);
  history.bind({ ...scope, server: '' });
  assert.deepEqual(history.read(0), []);
  for (const sessionId of [0, -1, 1.5, Infinity, NaN]) {
    history.bind({ ...scope, sessionId });
    history.write(0, [message(1)]);
    assert.deepEqual(history.read(0), []);
  }
});

test('an empty snapshot neither erases messages nor reserves a channel', () => {
  const history = new EphemeralChatHistory();
  history.bind(scope);
  history.write(0, [message(1)]);
  for (let channel = 1; channel <= CHAT_HISTORY_CHANNEL_LIMIT; channel++) history.write(channel, []);
  history.write(0, []);
  assert.deepEqual(ids(history.read(0)), [1]);
});
