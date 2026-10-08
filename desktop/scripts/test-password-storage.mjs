import { build } from 'esbuild';
import { execFile } from 'node:child_process';
import { createRequire } from 'node:module';
import { promisify } from 'node:util';
import { mkdtemp, mkdir, writeFile, copyFile, rm } from 'node:fs/promises';
import { join, dirname } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';
import { randomBytes } from 'node:crypto';
import { keyringEnvironment } from './keyring-proof.mjs';

const require = createRequire(import.meta.url);
const scripts = dirname(fileURLToPath(import.meta.url));
const root = await mkdtemp(join(tmpdir(), 'gul-vault-proof-'));
const expected = {
  write: ['ready', 'saved', true],
  locked: ['locked', 'locked', false],
  retry: ['locked', 'unavailable', false],
  read: ['ready', 'saved', true],
  corrupt: ['ready', 'unreadable', false],
  missing: ['missing', 'unavailable', false],
  different: ['ready', 'unreadable', false],
};
try {
  if (process.platform !== 'linux') throw Error('GUL_VAULT_PROOF_REQUIRES_LINUX');
  const application = join(root, 'app');
  await mkdir(application, { mode: 0o700 });
  await writeFile(
    join(application, 'package.json'),
    JSON.stringify({ name: 'gul-password-store-fixture', version: '0.0.0', main: 'index.cjs' }),
  );
  await build({
    entryPoints: [join(scripts, 'password-store-probe.ts')],
    bundle: true,
    platform: 'node',
    format: 'cjs',
    target: 'node24',
    external: ['electron'],
    outfile: join(application, 'index.cjs'),
  });
  const file = join(root, 'servers.json');
  const original = join(root, 'original-servers.json');
  const secret = randomBytes(32).toString('hex');
  const run = async (mode) => {
    const data = join(root, `${mode}-data`);
    const config = join(root, `${mode}-config`);
    await mkdir(join(data, 'keyrings'), { recursive: true, mode: 0o700 });
    await mkdir(join(data, 'runtime'), { mode: 0o700 });
    await mkdir(config, { mode: 0o700 });
    const env = {
      ...keyringEnvironment(process.env, config, data, secret),
      GUL_VAULT_FIXTURE_FILE: file,
      GUL_VAULT_FIXTURE_SECRET: secret,
      GUL_VAULT_FIXTURE_ORIGINAL: original,
      GUL_VAULT_FIXTURE_KEYRING_PASSWORD: randomBytes(32).toString('hex'),
      GUL_VAULT_FIXTURE_PROFILE: join(root, `${mode}-profile`),
      GUL_VAULT_FIXTURE_HELPER: join(
        dirname(scripts),
        'resources',
        'password-store',
        `linux-${process.arch}`,
        'gul-password-store',
      ),
    };
    const result = await promisify(execFile)(
      'dbus-run-session',
      [
        '--',
        'xvfb-run',
        '-a',
        'sh',
        join(scripts, 'password-store-session.sh'),
        require('electron'),
        application,
        mode,
      ],
      { env, timeout: 60_000, maxBuffer: 128 * 1024 },
    );
    const phases = mode === 'write' ? ['write', 'locked', 'retry', 'read', 'corrupt'] : [mode];
    const lines = result.stdout.split(/\r?\n/u).filter((line) => line.startsWith('GUL_VAULT_PROOF '));
    if (lines.length !== phases.length) throw Error('GUL_VAULT_PROOF_FAILED');
    for (let index = 0; index < phases.length; ++index) {
      const row = JSON.parse(lines[index].slice('GUL_VAULT_PROOF '.length));
      const phase = phases[index];
      const [native, status, correct] = expected[phase];
      if (
        row.phase !== phase ||
        row.native !== native ||
        row.status !== status ||
        row.correct !== correct ||
        row.cipher !== true ||
        row.plaintext !== false ||
        row.unchanged !== true ||
        (phase === 'retry' && (!row.sticky || !row.restartRequired)) ||
        (native === 'ready' && row.backend !== 'gnome_libsecret')
      )
        throw Error('GUL_VAULT_PROOF_FAILED');
    }
  };
  // Preserve a valid encrypted copy before corrupting the test document.
  // The write process writes the original copy itself through this isolated path.
  await run('write');
  await copyFile(original, file);
  await run('missing');
  await run('different');
  console.info(
    'GNOME vault: no-flag protected save, locked metadata, sticky cache, fresh-process recovery, corrupt/different-key and missing-default preservation passed.',
  );
} catch {
  console.error('GUL_VAULT_PROOF_FAILED');
  process.exitCode = 1;
} finally {
  await rm(root, { recursive: true, force: true });
}
