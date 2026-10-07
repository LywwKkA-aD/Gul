import assert from 'node:assert/strict';
import test from 'node:test';
import { capturePickerInput } from '../src/main/validation.ts';

const requestId = 'a'.repeat(32);
const sourceKey = 'b'.repeat(32);

test('picker IPC accepts only an opaque selection or explicit cancellation', () => {
  assert.deepEqual(capturePickerInput({ requestId, sourceKey }), { requestId, sourceKey });
  assert.deepEqual(capturePickerInput({ requestId, sourceKey: null }), { requestId, sourceKey: null });
});

test('picker IPC rejects native source IDs, audio flags, malformed keys and additional fields', () => {
  for (const input of [
    null,
    [],
    { requestId, sourceKey: 'window:42:0' },
    { requestId, sourceKey, withAudio: true },
    { requestId, sourceKey, path: 'file:///private' },
    { requestId: '', sourceKey },
    { requestId: 'z'.repeat(32), sourceKey },
    { requestId, sourceKey: undefined },
    { requestId, sourceKey: 0 },
    { requestId, sourceKey: '' },
    JSON.parse(`{"requestId":"${requestId}","sourceKey":null,"__proto__":{}}`),
  ])
    assert.throws(() => capturePickerInput(input), /GUL_INPUT_INVALID/);
});
