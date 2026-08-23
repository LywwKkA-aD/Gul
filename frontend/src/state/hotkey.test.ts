import test from 'node:test';
import assert from 'node:assert/strict';
import {
  DEFAULT_HOTKEY,
  globalPTTRunning,
  localPTTActive,
  normalizeHotkey,
} from './hotkey.ts';
import type { HotkeyStatus } from './hotkey.ts';

const hold: HotkeyStatus = { mode: 'hold', reason: '', error: '' };
const toggle: HotkeyStatus = { mode: 'toggle', reason: 'На Wayland…', error: '' };
const unsupported: HotkeyStatus = { mode: 'unsupported', reason: 'Нет доступа…', error: '' };

test('a complete status is taken as it is', () => {
  assert.deepEqual(normalizeHotkey(toggle), toggle);
});

test('a status that never arrived reads as no global key', () => {
  for (const raw of [undefined, null, 'hold', 42, []]) {
    assert.deepEqual(normalizeHotkey(raw), DEFAULT_HOTKEY);
  }
});

// The window listener is the fallback that always works, so an unreadable
// mode must land on it rather than on a key that may drive nothing.
test('a mode this build does not know reads as unsupported', () => {
  for (const mode of ['latch', '', undefined, 7]) {
    assert.equal(normalizeHotkey({ mode, reason: '', error: '' }).mode, 'unsupported');
  }
});

test('non-string messages read as nothing to say', () => {
  assert.deepEqual(normalizeHotkey({ mode: 'hold', reason: 12, error: {} }), hold);
});

// One source drives the gate. Both would double every press in hold mode, and
// in toggle mode the window listener would close what the global key opened.
test('the global key takes the gate when it is watching', () => {
  assert.equal(localPTTActive(true, hold), false);
  assert.equal(localPTTActive(true, toggle), false);
  assert.equal(globalPTTRunning(true, hold), true);
});

test('the window keeps the gate while no global key is asked for', () => {
  assert.equal(localPTTActive(false, hold), true);
  assert.equal(localPTTActive(false, toggle), true);
  assert.equal(globalPTTRunning(false, hold), false);
});

test('the window keeps the gate on a machine that cannot watch keys', () => {
  assert.equal(localPTTActive(true, unsupported), true);
  assert.equal(globalPTTRunning(true, unsupported), false);
});

// A watch that failed to bind delivers nothing at all - the key would be dead
// in both places if the window listener stood down for it.
test('the window keeps the gate when the key could not be bound', () => {
  const failed: HotkeyStatus = { ...hold, error: 'Клавиша Space недоступна…' };

  assert.equal(localPTTActive(true, failed), true);
  assert.equal(globalPTTRunning(true, failed), false);
});
