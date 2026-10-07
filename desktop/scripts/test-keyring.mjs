import { build } from 'esbuild';
import { createRequire } from 'node:module';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { randomBytes } from 'node:crypto';
import { keyringProof, keyringEnvironment } from './keyring-proof.mjs';

const require = createRequire(import.meta.url);
const scripts = dirname(fileURLToPath(import.meta.url));
const root = await mkdtemp(join(tmpdir(), 'gul-keyring-proof-'));
try {
  if (process.platform !== 'linux') throw Error('GUL_KEYRING_PROOF_REQUIRES_LINUX');
  const application = join(root, 'app');
  const keyringConfig = join(root, 'keyring-config');
  const keyringData = join(root, 'keyring-data');
  await mkdir(application, { mode: 0o700 });
  await mkdir(keyringConfig, { mode: 0o700 });
  await mkdir(join(keyringData, 'keyrings'), { recursive: true, mode: 0o700 });
  await mkdir(join(keyringData, 'runtime'), { mode: 0o700 });
  await writeFile(
    join(application, 'package.json'),
    JSON.stringify({ name: 'gul-keyring-fixture', version: '0.0.0', main: 'index.cjs' }),
  );
  await build({
    entryPoints: [join(scripts, 'keyring-probe.ts')],
    bundle: true,
    platform: 'node',
    format: 'cjs',
    target: 'node24',
    external: ['electron'],
    outfile: join(application, 'index.cjs'),
  });
  const secret = randomBytes(32).toString('hex');
  const env = keyringEnvironment(process.env, keyringConfig, keyringData, secret);
  const run = async (phase) => {
    const result = await promisify(execFile)(
      'dbus-run-session',
      [
        '--',
        'xvfb-run',
        '-a',
        'sh',
        join(scripts, 'keyring-session.sh'),
        require('electron'),
        application,
        phase,
      ],
      { env, timeout: 60_000, maxBuffer: 128 * 1024 },
    );
    return keyringProof(result.stdout, phase === 'write' ? ['write', 'read'] : ['read']);
  };
  await run('write');
  await run('read');
  console.info('GNOME Keyring: encrypted write, fresh Electron read and fresh keyring/session read passed.');
} catch {
  console.error('GUL_KEYRING_PROOF_FAILED');
  process.exitCode = 1;
} finally {
  await rm(root, { recursive: true, force: true });
}
