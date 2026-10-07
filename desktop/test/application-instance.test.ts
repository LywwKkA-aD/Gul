import assert from 'node:assert/strict';
import test from 'node:test';
import { claimApplicationInstance } from '../src/main/application-instance.ts';

test('production and packaged launches always acquire the normal application lock', () => {
  let calls = 0;
  const app = {
    requestSingleInstanceLock: () => {
      calls++;
      return false;
    },
  };
  assert.equal(claimApplicationInstance(app, {}, false, []), false);
  assert.equal(claimApplicationInstance(app, { NODE_ENV: 'test' }, true, ['--gul-electron-test']), false);
  assert.equal(
    claimApplicationInstance(app, { NODE_ENV: 'production' }, false, ['--gul-electron-test']),
    false,
  );
  assert.equal(claimApplicationInstance(app, { NODE_ENV: 'test' }, false, []), false);
  assert.equal(calls, 4);
});

test('only explicit unpackaged test launches allow isolated multiple clients', () => {
  const app = {
    requestSingleInstanceLock: () => {
      throw new Error('Test fixture unexpectedly acquired production lock');
    },
  };
  assert.equal(claimApplicationInstance(app, { NODE_ENV: 'test' }, false, ['--gul-electron-test']), true);
});
