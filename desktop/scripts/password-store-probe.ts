import { app, safeStorage } from 'electron';
import { readFile, writeFile, copyFile } from 'node:fs/promises';
import { SavedServerStore } from '../src/main/servers.ts';
import { PasswordStorage } from '../src/main/password-storage.ts';

const phase = process.argv.find((value) => value.startsWith('--gul-vault-phase='))?.slice(18);
void app
  .whenReady()
  .then(async () => {
    const {
      GUL_VAULT_FIXTURE_FILE: file,
      GUL_VAULT_FIXTURE_SECRET: secret,
      GUL_VAULT_FIXTURE_KEYRING_PASSWORD: keyringPassword,
      GUL_VAULT_FIXTURE_HELPER: executable,
      GUL_VAULT_FIXTURE_ORIGINAL: original,
    } = process.env;
    if (process.platform !== 'linux' || !file || !secret || !keyringPassword || !executable || !original)
      throw Error('GUL_VAULT_PROOF_FAILED');
    const storage = new PasswordStorage({
      platform: 'linux',
      executable,
      applicationName: app.getName(),
      safeStorage,
    });
    const input = {
      address:
        'livekit+vless://server.test?security=reality&flow=none&type=tcp&sni=example.test&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&sid=abcd',
      username: 'Fixture',
      password: secret,
    };
    const store = new SavedServerStore({
      file,
      safeStorage,
      platform: 'linux',
      passwordStorage: () => storage.getSnapshot(),
    });
    await store.load();
    if (phase === 'corrupt') {
      const document = JSON.parse(await readFile(file, 'utf8'));
      const cipher = Buffer.from(document.servers[0].encryptedPassword, 'base64');
      document.servers[0].encryptedPassword = cipher.subarray(0, 3).toString('base64');
      await writeFile(file, JSON.stringify(document), { mode: 0o600 });
    }
    const current =
      phase === 'corrupt'
        ? new SavedServerStore({
            file,
            safeStorage,
            platform: 'linux',
            passwordStorage: () => storage.getSnapshot(),
          })
        : store;
    await current.load();
    const before = phase === 'write' ? '' : await readFile(file, 'utf8');
    const health = await storage.status();
    if (phase === 'write') {
      await current.remember(input);
      await copyFile(file, original);
    }
    const initialStatus = current.list()[0]?.passwordStatus;
    let sticky = false;
    if (phase === 'retry') {
      // Deliberately reproduce alpha4's eager sync lookup; only the private fixture dismisses its prompt.
      if (safeStorage.isEncryptionAvailable()) throw Error('GUL_VAULT_EXPECTED_LOCK');
      // The private session's supervisor models an OS-side unlock while this Electron stays alive.
      await writeFile(`${file}.await-unlock`, '', { mode: 0o600 });
      // Metadata verifies the real keyring became unlocked before querying the old cryptography instance.
      for (let attempt = 0; attempt < 80 && (await storage.status()).state !== 'ready'; ++attempt)
        await new Promise((resolve) => setTimeout(resolve, 50));
      sticky = storage.getSnapshot().state === 'ready' && !safeStorage.isEncryptionAvailable();
    }
    const resolved = current.resolve(input.address);
    const bytes = await readFile(file, 'utf8');
    console.info(
      'GUL_VAULT_PROOF ' +
        JSON.stringify({
          phase,
          native: health.state,
          backend: health.state === 'ready' ? safeStorage.getSelectedStorageBackend() : 'not-initialized',
          initialStatus,
          status: current.list()[0]?.passwordStatus,
          sticky,
          restartRequired: storage.getSnapshot().restartRequired,
          correct: resolved.kind === 'ready' && resolved.input.password === secret,
          cipher: typeof JSON.parse(bytes).servers[0]?.encryptedPassword === 'string',
          plaintext: bytes.includes(secret),
          unchanged: phase === 'write' || before === bytes,
        }),
    );
    await storage.close();
    app.exit(0);
  })
  .catch(() => {
    console.info('GUL_VAULT_PROOF_FAILED');
    app.exit(1);
  });
