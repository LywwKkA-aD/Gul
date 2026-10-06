import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, writeFileSync, chmodSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { loadRemoteConfig } from './config.ts';

test('remote media checks read a private HTTPS config without exposing rejected secrets', () => {
  const dir = mkdtempSync(join(tmpdir(), 'gul-remote-config-'));
  const file = join(dir, '.env');
  const password = 'private-test-password';
  try {
    writeFileSync(file, `GUL_REMOTE_URL=https://GUL.example:443/\nGUL_REMOTE_PASSWORD=${password}\n`, { mode: 0o600 });
    assert.deepEqual(loadRemoteConfig(file), { url: 'https://gul.example', password });
    for (const url of ['http://gul.example', 'https://user:private-test-password@gul.example', 'https://gul.example/path', 'https://gul.example/?token=private-test-password']) {
      writeFileSync(file, `GUL_REMOTE_URL=${url}\nGUL_REMOTE_PASSWORD=${password}\n`);
      assert.throws(() => loadRemoteConfig(file), (error: Error) => !error.message.includes(password));
    }
    writeFileSync(file, `GUL_REMOTE_URL=https://gul.example\nGUL_REMOTE_PASSWORD=${password}\n`);
    chmodSync(file, 0o644);
    assert.throws(() => loadRemoteConfig(file), { message: 'Remote E2E env file must be private (mode 0600)' });
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
