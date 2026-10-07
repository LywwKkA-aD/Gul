import assert from 'node:assert/strict';
import test from 'node:test';
import { captureRequestFacts } from '../src/main/capture-diagnostics.ts';
import { captureFailureName } from '../src/renderer/media/capture-diagnostics.ts';

test('capture diagnostics identify the failed consent boundary without recording URLs or session values', () => {
  const request = {
    securityOrigin: 'gul://app',
    videoRequested: true,
    audioRequested: true,
    userGesture: true,
  };
  const accepted = captureRequestFacts(request, 'gul://app/index.html', true, true);
  assert.deepEqual(accepted, {
    activeSession: true,
    mainFrame: true,
    appFrame: true,
    appOrigin: true,
    videoRequested: true,
    audioRequested: true,
    userGesture: true,
  });
  const denied = captureRequestFacts(
    { ...request, securityOrigin: 'https://private.example/?password=secret', userGesture: false },
    'https://private.example/?token=secret',
    false,
    false,
  );
  assert.deepEqual(denied, {
    ...accepted,
    activeSession: false,
    mainFrame: false,
    appFrame: false,
    appOrigin: false,
    userGesture: false,
  });
  assert.doesNotMatch(JSON.stringify(denied), /secret|private|token|password/);
  assert.equal(Object.isFrozen(denied), true);
});

test('renderer capture diagnostics expose only a bounded DOMException name, never an error message', () => {
  assert.equal(captureFailureName(new DOMException('secret', 'NotAllowedError')), 'NotAllowedError');
  assert.equal(captureFailureName(new DOMException('secret', 'NotReadableError')), 'NotReadableError');
  assert.equal(
    captureFailureName(new DOMException('secret', 'OverconstrainedError')),
    'OverconstrainedError',
  );
  for (const value of [
    new Error('secret'),
    new DOMException('secret', 'secret'),
    { name: 'NotAllowedError', message: 'secret' },
    undefined,
  ])
    assert.equal(captureFailureName(value), 'UnknownError');
});
