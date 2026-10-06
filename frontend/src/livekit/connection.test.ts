import test from 'node:test';
import assert from 'node:assert/strict';
import { ConnectionError } from 'livekit-client';
import { connectionFailure, runtimeIssue, waitForRetry } from './connection.ts';

test('runtime preflight distinguishes unsupported RTC from insecure content without device access', () => {
  const supported = { isSecureContext: true, RTCPeerConnection: { prototype: { addTransceiver() {} } } };
  assert.equal(runtimeIssue(supported), undefined);
  assert.equal(runtimeIssue({ ...supported, isSecureContext: false }), 'SCREEN_CONTEXT');
  assert.equal(runtimeIssue({ isSecureContext: true }), 'SCREEN_RTC_UNAVAILABLE');
  assert.equal(runtimeIssue({ ...supported, RTCPeerConnection: { prototype: {} } }), 'SCREEN_RTC_UNAVAILABLE');
  assert.equal(runtimeIssue({ ...supported, RTCPeerConnection: { prototype: { addTrack() {} } } }), undefined);
});

test('failure codes use only known SDK reasons and never expose arbitrary error data', () => {
  const secret = 'private-token-and-url';
  assert.deepEqual(connectionFailure('connect', ConnectionError.timeout(secret)), { code: 'SCREEN_TIMEOUT', retryable: true });
  assert.deepEqual(connectionFailure('connect', ConnectionError.websocket(secret, 503, secret)), { code: 'SCREEN_SIGNAL', retryable: true });
  assert.deepEqual(connectionFailure('connect', ConnectionError.notAllowed(secret, 403, secret)), { code: 'SCREEN_AUTH', retryable: false });
  assert.deepEqual(connectionFailure('runtime', new Error(secret)), { code: 'SCREEN_RUNTIME', retryable: false });
  assert.deepEqual(connectionFailure('connect', { reason: secret, message: secret }), { code: 'SCREEN_CONNECT', retryable: true });
});

test('retry delay is cancellable and does not keep a disposed session waiting', async () => {
  const abort = new AbortController();
  const waiting = waitForRetry(60_000, abort.signal);
  abort.abort();
  assert.equal(await waiting, false);
  assert.equal(await waitForRetry(60_000, abort.signal), false);
});
