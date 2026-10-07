import assert from 'node:assert/strict';
import test from 'node:test';
import {
  readPreferences,
  savePreferences,
  saveConnection,
  readSavedString,
  shortcutFromKey,
} from '../src/renderer/preferences.ts';

function storage(value: string | null, unavailable = false) {
  const written = new Map<string, string>();
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem: () => {
        if (unavailable) throw new Error('storage unavailable');
        return value;
      },
      setItem: (key: string, next: string) => {
        if (unavailable) throw new Error('storage unavailable');
        written.set(key, next);
      },
    },
  });
  return written;
}

test('saved device and key preferences never persist a password or media token', () => {
  const written = storage(
    JSON.stringify({
      audioinput: 'chosen-input',
      audiooutput: 'chosen-output',
      shortcut: 'Control+F9',
      toggleEnabled: true,
      password: 'fixture-secret',
      token: 'fixture-token',
    }),
  );
  const preferences = readPreferences();
  assert.deepEqual(preferences, {
    audioinput: 'chosen-input',
    audiooutput: 'chosen-output',
    shortcut: 'Control+F9',
    toggleEnabled: true,
  });
  assert.equal(Object.isFrozen(preferences), true);
  savePreferences(preferences);
  saveConnection('public-profile', 'local-name');
  assert.deepEqual([...written.keys()], ['gul.preferences', 'gul.address', 'gul.username']);
  assert.equal(
    [...written.values()].some(
      (value) => value.includes('fixture-secret') || value.includes('fixture-token'),
    ),
    false,
  );
});

test('corrupt and unavailable storage defaults to an unregistered global key', () => {
  for (const raw of [
    null,
    '{broken',
    'null',
    '42',
    JSON.stringify({
      audioinput: '',
      audiooutput: 'x'.repeat(513),
      shortcut: '\r\ninvalid',
      toggleEnabled: 'true',
    }),
  ]) {
    storage(raw);
    assert.deepEqual(readPreferences(), {
      audioinput: 'default',
      audiooutput: 'default',
      shortcut: 'F8',
      toggleEnabled: false,
    });
  }
  storage(null, true);
  assert.equal(readPreferences().toggleEnabled, false);
  assert.equal(readSavedString('gul.username'), '');
  assert.doesNotThrow(() => savePreferences(readPreferences()));
  assert.doesNotThrow(() => saveConnection('public-profile', 'local-name'));
});

test('key capture maps physical keys independently of keyboard language and rejects modifier-only presses', () => {
  const key = (code: string, modifiers = {}) =>
    ({ code, ctrlKey: false, altKey: false, shiftKey: false, metaKey: false, ...modifiers }) as KeyboardEvent;
  assert.equal(shortcutFromKey(key('KeyQ', { ctrlKey: true, altKey: true })), 'Control+Alt+Q');
  assert.equal(shortcutFromKey(key('Digit4', { shiftKey: true })), 'Shift+4');
  assert.equal(shortcutFromKey(key('F24', { metaKey: true })), 'Super+F24');
  assert.equal(shortcutFromKey(key('Space')), 'Space');
  assert.equal(shortcutFromKey(key('ArrowUp')), 'Up');
  assert.equal(shortcutFromKey(key('ShiftLeft')), undefined);
  assert.equal(shortcutFromKey(key('F25')), undefined);
});
