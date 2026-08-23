import test from 'node:test';
import assert from 'node:assert/strict';
import {
  GUL_RELAY_ADDRESS,
  commitLastConnection,
  loadLastConnection,
  rememberAttempt,
} from './lastConnection.ts';

/** The module reads and writes exactly two Storage methods, so the stub is
    two methods plus a switch for the webview that refuses both. */
function installStorage(entries: Map<string, string>, broken = false): void {
  const storage = {
    getItem(key: string): string | null {
      if (broken) throw new Error('storage is disabled');
      return entries.get(key) ?? null;
    },
    setItem(key: string, value: string): void {
      if (broken) throw new Error('storage is disabled');
      entries.set(key, value);
    },
  };
  Object.defineProperty(globalThis, 'localStorage', { value: storage, configurable: true });
}

/** The single record the module wrote, whatever it decided to call it. */
function onlyRecord(entries: Map<string, string>): Record<string, unknown> {
  assert.equal(entries.size, 1, 'expected exactly one stored record');
  const [raw] = [...entries.values()];
  return JSON.parse(raw) as Record<string, unknown>;
}

test('an attempt is stored only once the connection is established', () => {
  const entries = new Map<string, string>();
  installStorage(entries);

  rememberAttempt({ address: 'wss://murmur.example.test/mumble', username: 'gul' });
  assert.equal(entries.size, 0, 'an attempt that may still fail was written');

  commitLastConnection();
  assert.deepEqual(loadLastConnection(), {
    address: 'wss://murmur.example.test/mumble',
    username: 'gul',
  });
});

// The password lives in the form for one attempt. Nothing here may carry it.
test('the stored record holds the address and the nickname and nothing else', () => {
  const entries = new Map<string, string>();
  installStorage(entries);

  rememberAttempt({ address: 'murmur.example.test:64738', username: 'gul' });
  commitLastConnection();

  assert.deepEqual(Object.keys(onlyRecord(entries)).sort(), ['address', 'username']);
});

test('surrounding whitespace is not part of what is remembered', () => {
  const entries = new Map<string, string>();
  installStorage(entries);

  rememberAttempt({ address: '  murmur.example.test:64738  ', username: '  gul  ' });
  commitLastConnection();

  assert.deepEqual(loadLastConnection(), {
    address: 'murmur.example.test:64738',
    username: 'gul',
  });
});

test('an incomplete attempt is never stored', () => {
  const entries = new Map<string, string>();
  installStorage(entries);

  rememberAttempt({ address: 'murmur.example.test:64738', username: '   ' });
  commitLastConnection();

  assert.equal(entries.size, 0);
  assert.deepEqual(loadLastConnection(), { address: '', username: '' });
});

// A reconnect emits 'connected' again; the attempt it belonged to is long gone.
test('committing without a pending attempt keeps the stored record', () => {
  const entries = new Map<string, string>();
  installStorage(entries);

  rememberAttempt({ address: 'murmur.example.test:64738', username: 'gul' });
  commitLastConnection();
  commitLastConnection();

  assert.deepEqual(loadLastConnection(), {
    address: 'murmur.example.test:64738',
    username: 'gul',
  });
});

test('a damaged record reads as nothing remembered', () => {
  installStorage(new Map([['gul.lastConnection', 'not json at all']]));

  assert.deepEqual(loadLastConnection(), { address: '', username: '' });
});

test('fields of the wrong type read as nothing remembered', () => {
  installStorage(new Map([['gul.lastConnection', JSON.stringify({ address: 42, username: null })]]));

  assert.deepEqual(loadLastConnection(), { address: '', username: '' });
});

test('a webview with storage disabled costs convenience, not the session', () => {
  installStorage(new Map(), true);

  assert.deepEqual(loadLastConnection(), { address: '', username: '' });
  rememberAttempt({ address: 'murmur.example.test:64738', username: 'gul' });
  assert.doesNotThrow(commitLastConnection);
});

// The offered relay is the one address the app suggests on its own; anything
// but wss:// would be rejected by the Go endpoint parser before it dialed.
test('the offered relay is a WSS address', () => {
  assert.match(GUL_RELAY_ADDRESS, /^wss:\/\//);
});
