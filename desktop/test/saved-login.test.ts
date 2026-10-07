import assert from 'node:assert/strict';
import test from 'node:test';
import type { ServerList } from '../src/shared/contracts.ts';
import {
  selectedSavedServer,
  savedPasswordMessage,
  passwordSaveNotice,
} from '../src/renderer/saved-login.ts';

const saved: ServerList = {
  storage: 'protected',
  lastSave: null,
  servers: [
    {
      address: 'livekit+vless://one.test',
      username: 'Tester',
      lastUsed: 1,
      hasPassword: true,
      rememberPassword: true,
      passwordStatus: 'saved',
    },
  ],
};

test('remembered consent and password availability belong to the exact selected profile', () => {
  assert.equal(selectedSavedServer(saved, ' livekit+vless://one.test ')?.rememberPassword, true);
  assert.equal(selectedSavedServer(saved, 'livekit+vless://other.test'), undefined);
  assert.equal(savedPasswordMessage(saved.servers[0]), '');
  assert.equal(savedPasswordMessage(undefined), '');
});

test('locked and failed credentials explain manual entry without returning passwords', () => {
  assert.match(
    savedPasswordMessage({ ...saved.servers[0], hasPassword: false, passwordStatus: 'locked' }),
    /хранилищ/iu,
  );
  assert.match(
    savedPasswordMessage({ ...saved.servers[0], hasPassword: false, passwordStatus: 'save-failed' }),
    /не удалось сохранить/iu,
  );
  assert.equal(
    savedPasswordMessage({ ...saved.servers[0], hasPassword: false, passwordStatus: 'missing' }),
    '',
  );
});

test('save notices are fixed text for the selected server and never retain private error details', () => {
  for (const status of ['unavailable', 'encrypt-failed', 'write-failed'] as const) {
    const list = { ...saved, lastSave: { address: saved.servers[0].address, status, persisted: false } };
    assert.notEqual(passwordSaveNotice(list, saved.servers[0].address), '');
    assert.equal(passwordSaveNotice(list, 'livekit+vless://other.test'), '');
    assert.equal(passwordSaveNotice(list, saved.servers[0].address).includes('one.test'), false);
  }
  assert.equal(passwordSaveNotice(saved, saved.servers[0].address), '');
  for (const status of ['saved', 'not-requested'] as const)
    assert.equal(
      passwordSaveNotice(
        { ...saved, lastSave: { address: saved.servers[0].address, status, persisted: true } },
        saved.servers[0].address,
      ),
      '',
    );
});
