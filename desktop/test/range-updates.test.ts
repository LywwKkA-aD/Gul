import assert from 'node:assert/strict';
import test from 'node:test';
import { RangeUpdates } from '../src/renderer/range-updates.ts';
function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
test('range updates apply immediately and coalesce pending gain, threshold and hold to the latest intent', async () => {
  const updates = new RangeUpdates();
  const pending = deferred();
  const applied: unknown[] = [];
  const apply = async (patch: unknown) => {
    applied.push(patch);
    await pending.promise;
  };
  const first = updates.run({ inputGain: 1.5 }, apply);
  const second = updates.run({ inputGain: 0.5, holdMs: 500 }, apply);
  const third = updates.run({ inputGain: 1, thresholdDb: -50 }, apply);
  assert.deepEqual(applied, [{ inputGain: 1.5 }]);
  pending.resolve();
  await Promise.all([first, second, third]);
  assert.deepEqual(applied, [{ inputGain: 1.5 }, { inputGain: 1, holdMs: 500, thresholdDb: -50 }]);
});
test('a failed apply rejects its callers while the latest queued values still apply', async () => {
  const updates = new RangeUpdates();
  const pending = deferred();
  let attempts = 0;
  const applied: unknown[] = [];
  const apply = async (patch: unknown) => {
    await pending.promise;
    if (++attempts === 1) throw new Error('Unavailable.');
    applied.push(patch);
  };
  const first = updates.run({ inputGain: 1.5 }, apply);
  const failed = assert.rejects(first, /Unavailable/u);
  const second = updates.run({ inputGain: 1 }, apply);
  pending.resolve();
  await Promise.all([failed, second]);
  await updates.run({ holdMs: 50 }, apply);
  assert.deepEqual(applied, [{ inputGain: 1 }, { holdMs: 50 }]);
});
