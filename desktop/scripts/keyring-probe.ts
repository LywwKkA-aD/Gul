import { app, safeStorage } from 'electron';
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { SavedServerStore } from '../src/main/servers.ts';

const phase = process.argv.includes('--gul-keyring-phase=write') ? 'write' : 'read';
void app
  .whenReady()
  .then(async () => {
    const secret = process.env.GUL_KEYRING_FIXTURE_SECRET;
    if (process.platform !== 'linux' || !secret) throw Error('GUL_KEYRING_PROOF_FAILED');
    const file = join(app.getPath('userData'), 'servers.json');
    const input = {
      address:
        'livekit+vless://server.test?security=reality&flow=none&type=tcp&sni=example.test&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&sid=abcd',
      username: 'Fixture',
      password: secret,
    };
    const store = new SavedServerStore({ file, safeStorage, platform: 'linux' });
    await store.load();
    const saved = phase === 'write' ? await store.remember(input) : null;
    const resolved = store.resolve(input.address);
    const bytes = await readFile(file, 'utf8');
    const row = store.list()[0];
    console.info(
      'GUL_KEYRING_PROOF ' +
        JSON.stringify({
          phase,
          backend: safeStorage.getSelectedStorageBackend(),
          protected: store.storageStatus() === 'protected',
          persisted: saved?.persisted ?? null,
          hasPassword: row?.hasPassword ?? false,
          remember: row?.rememberPassword ?? false,
          passwordStatus: row?.passwordStatus ?? 'missing',
          correct: resolved.kind === 'ready' && resolved.input.password === secret,
          ciphertextOnDisk: typeof JSON.parse(bytes).servers[0]?.encryptedPassword === 'string',
          plaintextOnDisk: bytes.includes(secret),
        }),
    );
    app.exit(0);
  })
  .catch(() => {
    console.info('GUL_KEYRING_PROOF_FAILED');
    app.exit(1);
  });
