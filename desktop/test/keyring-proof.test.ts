import assert from 'node:assert/strict';
import test from 'node:test';
import { keyringProof, keyringEnvironment } from '../scripts/keyring-proof.mjs';

const output = (phase: 'write' | 'read', change: Record<string, unknown> = {}) =>
  'GUL_KEYRING_PROOF ' +
  JSON.stringify({
    phase,
    backend: 'gnome_libsecret',
    protected: true,
    persisted: phase === 'write' ? true : null,
    hasPassword: true,
    remember: true,
    passwordStatus: 'saved',
    correct: true,
    ciphertextOnDisk: true,
    plaintextOnDisk: false,
    ...change,
  });

test('native keyring proof requires actual protected provider, durable ciphertext and each fresh-process stage', () => {
  assert.deepEqual(
    keyringProof(`unrelated engine warning\n${output('write')}\n${output('read')}`, ['write', 'read']),
    ['write', 'read'],
  );
  assert.deepEqual(keyringProof(output('read'), ['read']), ['read']);
  for (const value of [
    output('read'),
    output('write', { backend: 'basic_text' }),
    output('write', { protected: false }),
    output('write', { persisted: false }),
    output('write', { correct: false }),
    output('write', { plaintextOnDisk: true }),
    output('write', { ciphertextOnDisk: false }),
    output('write', { hasPassword: false }),
    output('write', { remember: false }),
    output('write', { passwordStatus: 'locked' }),
    output('write', { unknown: 'private-fixture-data' }),
    'GUL_KEYRING_PROOF not-json',
    'x'.repeat(70_000),
  ])
    assert.throws(() => keyringProof(value, ['write']), { message: 'GUL_KEYRING_PROOF_FAILED' });
});

test('keyring test isolates XDG configuration/data without repurposing HOME or CODEX_HOME', () => {
  const original = Object.freeze({
    HOME: '/real-home',
    CODEX_HOME: '/codex-home',
    XDG_CONFIG_HOME: '/real-config',
    XDG_DATA_HOME: '/real-data',
    XDG_RUNTIME_DIR: '/real-runtime',
    GNOME_KEYRING_CONTROL: '/real-control',
    GNOME_KEYRING_PID: '1234',
  });
  const env = keyringEnvironment(original, '/fixture-config', '/fixture-data', 'synthetic-private-value');
  assert.equal(env.HOME, original.HOME);
  assert.equal(env.CODEX_HOME, original.CODEX_HOME);
  assert.equal(env.XDG_CONFIG_HOME, '/fixture-config');
  assert.equal(env.XDG_DATA_HOME, '/fixture-data');
  assert.equal(env.XDG_CURRENT_DESKTOP, 'GNOME');
  assert.equal(env.XDG_RUNTIME_DIR, '/fixture-data/runtime');
  assert.equal(env.GNOME_KEYRING_CONTROL, undefined);
  assert.equal(env.GNOME_KEYRING_PID, undefined);
  assert.equal(original.XDG_DATA_HOME, '/real-data');
});
