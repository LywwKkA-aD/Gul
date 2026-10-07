import assert from 'node:assert/strict';
import test from 'node:test';
import { chatText, latency, participantId, validGrant, validText } from '../src/renderer/media/protocol.ts';
import type { MediaGrant, MediaSession } from '../src/shared/contracts.ts';

test('chat parsing rejects malformed encoding, oversized JSON and noncanonical identities', () => {
  const encode = (text: string) => new TextEncoder().encode(JSON.stringify({ text }));
  assert.equal(chatText(new Uint8Array([0xff]), 'voice.8', 'gul.chat.v1'), undefined);
  assert.equal(chatText(encode('x'.repeat(5001)), 'voice.8', 'gul.chat.v1'), undefined);
  assert.equal(chatText(new Uint8Array(24001), 'voice.8', 'gul.chat.v1'), undefined);
  for (const value of ['voice.0', 'voice.08', 'voice.2147483648', 'screen.8', 'voice.1.extra'])
    assert.equal(participantId(value, 'voice'), undefined);
  assert.equal(chatText(encode('Привет 😀'), 'voice.8', 'gul.chat.v1'), 'Привет 😀');
  assert.equal(validText('\uD800'), false);
  assert.equal(validText('\uDC00'), false);
  assert.equal(validText('😀'.repeat(5000)), true);
});

test('grants bind role, owner, room, revision and local REALITY gateway', () => {
  const grant: MediaGrant = {
    url: 'ws://127.0.0.1:5000/cap/rtc',
    token: 'short-lived',
    identity: 'voice.7',
    ownerIdentity: 'voice.7',
    room: 'gul.0',
    sessionId: 7,
    channelId: 0,
    revision: 1,
  };
  const session: MediaSession = {
    epoch: 1,
    sessionId: 7,
    identity: 'voice.7',
    name: 'Alice',
    channelId: 0,
    revision: 1,
    grant,
  };
  assert.equal(validGrant(grant, session, 'voice'), true);
  for (const patch of [
    { url: 'bad' },
    { url: 'wss://external.example/rtc' },
    { url: 'ws://user@127.0.0.1:5000/rtc' },
    { url: 'ws://127.0.0.1/rtc' },
    { identity: 'screen.7' },
    { ownerIdentity: 'voice.8' },
    { room: 'other' },
    { revision: 2 },
    { channelId: 1 },
    { token: '' },
  ])
    assert.equal(validGrant({ ...grant, ...patch }, session, 'voice'), false);
});

test('ping requires an actual selected responsive candidate pair and remains unknown otherwise', () => {
  const report = (entries: any[]) =>
    new Map(entries.map((entry) => [entry.id, entry])) as unknown as RTCStatsReport;
  assert.equal(
    latency(
      report([
        {
          id: 'pair',
          type: 'candidate-pair',
          state: 'succeeded',
          nominated: true,
          responsesReceived: 1,
          currentRoundTripTime: 0.052,
        },
      ]),
    ),
    52,
  );
  assert.equal(
    latency(
      report([
        { id: 'transport', type: 'transport', selectedCandidatePairId: 'pair' },
        {
          id: 'pair',
          type: 'candidate-pair',
          state: 'succeeded',
          responsesReceived: 5,
          currentRoundTripTime: 0.101,
        },
      ]),
    ),
    101,
  );
  for (const patch of [
    { state: 'failed' },
    { nominated: false },
    { responsesReceived: 0 },
    { currentRoundTripTime: NaN },
    { currentRoundTripTime: -1 },
  ])
    assert.equal(
      latency(
        report([
          {
            id: 'pair',
            type: 'candidate-pair',
            state: 'succeeded',
            nominated: true,
            responsesReceived: 1,
            currentRoundTripTime: 0.05,
            ...patch,
          },
        ]),
      ),
      undefined,
    );
});
