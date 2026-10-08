import assert from 'node:assert/strict';
import test from 'node:test';
import { brokerError } from '../src/transport/http.ts';

test('broker management errors expose only status-matched fixed codes and never raw key or URL content', () => {
  for (const [status, code, expected] of [
    [403, 'owner_required', 'owner-required'],
    [403, 'access_denied', 'access-denied'],
    [409, 'conflict', 'stale'],
    [409, 'channel_busy', 'channel-busy'],
    [426, 'upgrade_required', 'upgrade-required'],
    [503, 'media_cleanup_pending', 'cleanup-pending'],
    [503, 'storage_unavailable', 'storage-unavailable'],
  ] as const) {
    const result = brokerError(status, {
      code,
      credential: 'fixture-private',
      url: 'https://private.invalid?token=fixture',
    });
    assert.equal(result.code, expected);
    assert.equal(JSON.stringify(result).includes('fixture-private'), false);
    assert.equal(result.message.includes('private.invalid'), false);
  }
  assert.equal(brokerError(404, undefined).code, 'not-found');
  assert.equal(brokerError(401, { code: 'owner_required' }).code, 'authentication');
  assert.equal(brokerError(403, { code: 'storage_unavailable' }).code, 'authentication');
  assert.equal(brokerError(409, 'private-key').code, 'stale');
  assert.equal(brokerError(500, { code: 'owner_required' }).code, 'server');
});
