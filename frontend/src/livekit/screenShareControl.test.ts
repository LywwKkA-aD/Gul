import test from 'node:test';
import assert from 'node:assert/strict';
import { screenShareControl, toggleScreenShare } from './screenShareControl.ts';

const connected = { status: 'connected', sharing: false, pendingShare: false };

test('screen button is a compact accessible toggle that stays usable to cancel capture', () => {
  assert.deepEqual(screenShareControl(connected, true), { label: 'Показать экран', active: false, disabled: false });
  assert.deepEqual(screenShareControl({ ...connected, pendingShare: true }, true), {
    label: 'Отменить выбор экрана', active: true, disabled: false,
  });
  assert.deepEqual(screenShareControl({ ...connected, sharing: true }, true), {
    label: 'Остановить показ', active: true, disabled: false,
  });
  assert.equal(screenShareControl(null, true).disabled, true);
  assert.equal(screenShareControl({ ...connected, status: 'connecting' }, true).disabled, true);
  assert.equal(screenShareControl({ ...connected, status: 'reconnecting' }, true).disabled, true);
  assert.equal(screenShareControl(connected, false).disabled, true);
  assert.equal(screenShareControl({ ...connected, pendingShare: true }, false).disabled, false);
});

test('click starts capture synchronously and a second click cancels its current pending state', async () => {
  let snapshot = connected;
  const calls: string[] = [];
  const controller = {
    getSnapshot: () => snapshot,
    share() { calls.push('share'); snapshot = { ...snapshot, pendingShare: true }; return Promise.resolve(); },
    stopShare() { calls.push('stop'); snapshot = { ...snapshot, pendingShare: false }; return Promise.resolve(); },
  };
  const first = toggleScreenShare(controller);
  assert.deepEqual(calls, ['share'], 'the browser user gesture must not cross an await');
  const second = toggleScreenShare(controller);
  assert.deepEqual(calls, ['share', 'stop'], 'the click must read current state, not a stale React render');
  await Promise.all([first, second]);
});
