import test from 'node:test';
import assert from 'node:assert/strict';
import { DEFAULT_SETTINGS, GUL_RELAY_ADDRESS, normalizeSettings } from './settings.ts';
import { VAD_HANGOVER_CHOICES, VAD_OPEN_MAX, VAD_OPEN_MIN } from './gate.ts';

// The snapshot crosses a bridge: what arrives is whatever the running Go
// build sends, and nothing at all when the call fails. The store has to end
// up with a usable set of settings either way.

const snapshot = {
  address: 'murmur.example.test:64738',
  username: 'gul',
  captureId: 'aa11',
  playbackId: 'bb22',
  gateMode: 'ptt',
  vadOpen: 0.75,
  hangoverMs: 500,
  pttKey: 'KeyF',
  globalPtt: false,
};

test('a complete snapshot is taken as it is', () => {
  assert.deepEqual(normalizeSettings(snapshot), snapshot);
});

test('a snapshot that never arrived reads as the defaults', () => {
  for (const raw of [undefined, null, 'settings', 42, []]) {
    assert.deepEqual(normalizeSettings(raw), DEFAULT_SETTINGS);
  }
});

// The password lives in the connect form for one attempt; nothing that is
// persisted may carry it, and nothing may smuggle it back in either.
test('only the known settings survive normalization', () => {
  const got = normalizeSettings({ ...snapshot, password: 'hunter2', theme: 'dark' });

  assert.deepEqual(Object.keys(got).sort(), Object.keys(DEFAULT_SETTINGS).sort());
  assert.equal(JSON.stringify(got).includes('hunter2'), false);
});

test('a mode this build does not know falls back to voice activation', () => {
  for (const gateMode of ['shout', '', 'VAD', undefined]) {
    assert.equal(normalizeSettings({ ...snapshot, gateMode }).gateMode, 'vad');
  }
  assert.equal(normalizeSettings({ ...snapshot, gateMode: 'ptt' }).gateMode, 'ptt');
});

test('a threshold outside the offered range folds into it', () => {
  assert.equal(normalizeSettings({ ...snapshot, vadOpen: 0.05 }).vadOpen, VAD_OPEN_MIN);
  assert.equal(normalizeSettings({ ...snapshot, vadOpen: 1 }).vadOpen, VAD_OPEN_MAX);
  assert.equal(normalizeSettings({ ...snapshot, vadOpen: 'loud' }).vadOpen, DEFAULT_SETTINGS.vadOpen);
});

// A hand-edited config.json may hold a tail this UI does not offer, and a
// select with no matching option shows an empty field.
test('a tail the UI does not offer snaps to the nearest one', () => {
  const got = normalizeSettings({ ...snapshot, hangoverMs: 450 }).hangoverMs;

  assert.equal(VAD_HANGOVER_CHOICES.includes(got as never), true);
  assert.equal(got, 500);
});

test('a missing key falls back to the default binding', () => {
  for (const pttKey of ['', undefined, 7]) {
    assert.equal(normalizeSettings({ ...snapshot, pttKey }).pttKey, DEFAULT_SETTINGS.pttKey);
  }
});

// The global hotkey lands with its own wiring; until then nothing but a
// literal true may switch it on.
test('the global push-to-talk flag is off unless it is exactly true', () => {
  for (const globalPtt of ['true', 1, undefined, null]) {
    assert.equal(normalizeSettings({ ...snapshot, globalPtt }).globalPtt, false);
  }
  assert.equal(normalizeSettings({ ...snapshot, globalPtt: true }).globalPtt, true);
});

// The offered relay is the one address the app suggests on its own; anything
// but wss:// would be rejected by the Go endpoint parser before it dialed.
test('the offered relay is a WSS address', () => {
  assert.match(GUL_RELAY_ADDRESS, /^wss:\/\//);
});
