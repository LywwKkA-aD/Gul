import assert from 'node:assert/strict';
import test from 'node:test';
import { defaultVoiceSettings } from '../src/renderer/media/voice-gate.ts';
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
    voice: defaultVoiceSettings,
    soundNotifications: false,
    hotkeyMode: 'toggle',
    screenQuality: '720p30',
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
      voice: defaultVoiceSettings,
      soundNotifications: false,
      hotkeyMode: 'toggle',
      screenQuality: '720p30',
    });
  }
  storage(null, true);
  assert.equal(readPreferences().toggleEnabled, false);
  assert.equal(readSavedString('gul.username'), '');
  assert.doesNotThrow(() => savePreferences(readPreferences()));
  assert.doesNotThrow(() => saveConnection('public-profile', 'local-name'));
});
test('voice preferences validate settings and drop unknown secret fields', () => {
  const written = storage(
    JSON.stringify({
      voice: { mode: 'vad', thresholdDb: -50, inputGain: 1.5, password: 'fixture-secret' },
      soundNotifications: true,
      hotkeyMode: 'hold',
    }),
  );
  const preferences = readPreferences();
  assert.equal(preferences.voice.mode, 'vad');
  assert.equal(preferences.voice.inputGain, 1.5);
  assert.equal(preferences.soundNotifications, true);
  assert.equal(preferences.hotkeyMode, 'hold');
  savePreferences(preferences);
  assert.equal(written.get('gul.preferences')?.includes('fixture-secret'), false);
  storage(JSON.stringify({ voice: { inputGain: 100 } }));
  assert.equal(readPreferences().voice.inputGain, 1);
});

test('screen quality defaults safely and persists only supported presets', () => {
  for (const quality of ['720p30', '720p60', '1080p30', '1080p60']) {
    const written = storage(JSON.stringify({ screenQuality: quality }));
    const preferences = readPreferences();
    assert.equal(preferences.screenQuality, quality);
    savePreferences(preferences);
    assert.equal(JSON.parse(written.get('gul.preferences')!).screenQuality, quality);
  }
  for (const quality of [undefined, null, '4k120', '1080p60 ', {}, 60]) {
    storage(JSON.stringify({ screenQuality: quality }));
    assert.equal(readPreferences().screenQuality, '720p30');
  }
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
